package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Decode only the same structured operations shown by the management Web.
// Unknown fields (prompts, reasoning, amounts, raw errors, etc.) are skipped.
type publicDecisionRecord struct {
	CycleNumber int            `json:"cycle_number"`
	Timestamp   time.Time      `json:"timestamp"`
	Success     bool           `json:"success"`
	Decisions   []publicAction `json:"decisions"`
}

type publicLogMetadata struct {
	CycleNumber int       `json:"cycle_number"`
	Timestamp   time.Time `json:"timestamp"`
}

// No second persistent index: reuse the management log listing/opening policy.
// Cache directory metadata for a minute and at most 64 decoded public records.
type publicLogsCache struct {
	mu       sync.Mutex
	root     string
	traderID string
	loadedAt time.Time
	entries  []LogEntry
	details  map[string]publicDecisionRecord
}

func newPublicLogsCache(root, traderID string) *publicLogsCache {
	return &publicLogsCache{root: root, traderID: traderID, details: make(map[string]publicDecisionRecord)}
}

func publicActionName(action string) string {
	switch action {
	case "open_long", "open_short", "close_long", "close_short", "partial_close",
		"update_stop_loss", "update_take_profit", "hold", "wait":
		return action
	default:
		return "unknown" // Never echo arbitrary upstream text as an operation.
	}
}

func readPublicDecisionLog(root, traderID string, entry LogEntry) (publicDecisionRecord, error) {
	file, err := openDecisionLogFile(root, traderID, entry.Filename)
	if err != nil {
		return publicDecisionRecord{}, err
	}
	defer file.Close()
	var record publicDecisionRecord
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&record); err != nil {
		return publicDecisionRecord{}, err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return publicDecisionRecord{}, errors.New("invalid decision log")
	}
	if record.CycleNumber != entry.Cycle || record.Timestamp.IsZero() {
		return publicDecisionRecord{}, errors.New("invalid decision metadata")
	}
	record.Timestamp = record.Timestamp.UTC()
	if record.Decisions == nil {
		record.Decisions = []publicAction{}
	}
	for i := range record.Decisions {
		record.Decisions[i].Action = publicActionName(record.Decisions[i].Action)
	}
	return record, nil
}

func (c *publicLogsCache) loadLocked() error {
	if !c.loadedAt.IsZero() && time.Since(c.loadedAt) < publicRefreshInterval {
		return nil
	}
	entries, err := listDecisionLogFiles(c.root, c.traderID)
	if err != nil {
		return err
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Cycle > entries[j].Cycle })
	c.entries, c.loadedAt = entries, time.Now().UTC()
	// Also invalidate details on refresh: --fresh/archive or file replacement
	// must not keep an earlier run's record alive through a reused cycle number.
	c.details = make(map[string]publicDecisionRecord)
	return nil
}

func (c *publicLogsCache) page(limit, before int) ([]LogEntry, *int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return nil, nil, err
	}
	entries := make([]LogEntry, 0, limit)
	for _, entry := range c.entries {
		if before > 0 && entry.Cycle >= before {
			continue
		}
		if len(entries) == limit {
			next := entries[len(entries)-1].Cycle
			return entries, &next, nil
		}
		entries = append(entries, entry)
	}
	return entries, nil, nil
}

func (c *publicLogsCache) detail(cycle int) (publicDecisionRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadLocked(); err != nil {
		return publicDecisionRecord{}, err
	}
	for _, entry := range c.entries {
		if entry.Cycle != cycle {
			continue
		}
		if record, ok := c.details[entry.Filename]; ok {
			return record, nil
		}
		record, err := readPublicDecisionLog(c.root, c.traderID, entry)
		if err != nil {
			return publicDecisionRecord{}, err
		}
		if len(c.details) >= 64 {
			c.details = make(map[string]publicDecisionRecord)
		}
		c.details[entry.Filename] = record
		return record, nil
	}
	return publicDecisionRecord{}, fs.ErrNotExist
}

func (s *PublicServer) publicLogCache(c *gin.Context) (*publicLogsCache, bool) {
	// Publication entries and their log cache pointers are immutable after setup.
	entry, ok := s.entries[c.Param("slug")]
	if !ok || entry.logs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return nil, false
	}
	return entry.logs, true
}

func publicLogPageQuery(c *gin.Context, defaultLimit int) (limit, before int, ok bool) {
	if !publicQuery(c, "limit", "before_cycle") {
		return 0, 0, false
	}
	limit = defaultLimit
	for key, values := range c.Request.URL.Query() {
		n, err := strconv.Atoi(values[0])
		if err != nil || n < 1 || (key == "limit" && n > 100) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_log_range"})
			return 0, 0, false
		}
		if key == "limit" {
			limit = n
		} else {
			before = n
		}
	}
	return limit, before, true
}

func publicLogError(c *gin.Context, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.Header("Retry-After", "60")
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "logs_unavailable"})
}

func (s *PublicServer) handlePublicLogList(c *gin.Context) {
	limit, before, ok := publicLogPageQuery(c, 100)
	if !ok {
		return
	}
	cache, ok := s.publicLogCache(c)
	if !ok {
		return
	}
	entries, next, err := cache.page(limit, before)
	if err != nil {
		publicLogError(c, err)
		return
	}
	items := make([]publicLogMetadata, 0, len(entries))
	for _, entry := range entries {
		items = append(items, publicLogMetadata{CycleNumber: entry.Cycle, Timestamp: entry.Timestamp.UTC()})
	}
	c.JSON(http.StatusOK, gin.H{"slug": c.Param("slug"), "logs": items, "next_before_cycle": next})
}

func (s *PublicServer) handlePublicLogDetail(c *gin.Context) {
	if !publicQuery(c) {
		return
	}
	cycle, err := strconv.Atoi(c.Param("cycle"))
	if err != nil || cycle < 1 || strconv.Itoa(cycle) != c.Param("cycle") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_cycle"})
		return
	}
	cache, ok := s.publicLogCache(c)
	if !ok {
		return
	}
	record, err := cache.detail(cycle)
	if err != nil {
		publicLogError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"slug": c.Param("slug"), "log": record})
}

func (s *PublicServer) handlePublicDecisions(c *gin.Context) {
	limit, before, ok := publicLogPageQuery(c, 5)
	if !ok {
		return
	}
	cache, ok := s.publicLogCache(c)
	if !ok {
		return
	}
	entries, next, err := cache.page(limit, before)
	if err != nil {
		publicLogError(c, err)
		return
	}
	records := make([]publicDecisionRecord, 0, len(entries))
	for _, entry := range entries {
		record, err := cache.detail(entry.Cycle)
		if err != nil {
			publicLogError(c, err)
			return
		}
		records = append(records, record)
	}
	c.JSON(http.StatusOK, gin.H{"slug": c.Param("slug"), "decisions": records, "next_before_cycle": next})
}

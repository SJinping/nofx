package api

import (
	"nofx/logger"
	"sync"
	"time"
)

const (
	defaultEquityHistoryLimit = 2000
	maxEquityHistoryLimit     = 20000
)

type EquityPoint struct {
	Timestamp        string               `json:"timestamp"`
	TotalEquity      float64              `json:"total_equity"`
	AvailableBalance float64              `json:"available_balance"`
	TotalPnL         float64              `json:"total_pnl"`
	TotalPnLPct      float64              `json:"total_pnl_pct"`
	PositionCount    int                  `json:"position_count"`
	MarginUsedPct    float64              `json:"margin_used_pct"`
	CycleNumber      int                  `json:"cycle_number"`
	SourceFile       string               `json:"source_file"`
	Actions          []logger.ChartAction `json:"actions,omitempty"`
}
type EquityHistoryQuery struct {
	Limit          int
	FromCycle      int
	ToCycle        int
	IncludeActions bool
}
type equityHistoryEntry struct {
	points         []EquityPoint
	lastLoadedAt   time.Time
	initialBalance float64
	query          EquityHistoryQuery
}
type EquityHistoryCache struct {
	mu         sync.Mutex
	entries    map[string]*equityHistoryEntry
	maxHistory int
	ttl        time.Duration
}
type equityIndexFetcher func(limit, from, to int, actions bool) ([]logger.ChartRecord, error)

func NewEquityHistoryCache(maxHistory int, ttl time.Duration) *EquityHistoryCache {
	if maxHistory <= 0 || maxHistory > maxEquityHistoryLimit {
		maxHistory = maxEquityHistoryLimit
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &EquityHistoryCache{entries: make(map[string]*equityHistoryEntry), maxHistory: maxHistory, ttl: ttl}
}

// GetIndexed caches only lightweight data. Range filtering happens at the
// persisted source BEFORE its bounded newest-N selection; no 500-cycle refresh
// window/high-water mark can permanently hide gaps or backfilled points.
func (c *EquityHistoryCache) GetIndexed(traderID string, fetch equityIndexFetcher, initialBalance float64, q EquityHistoryQuery) ([]EquityPoint, error) {
	q.Limit = clampEquityHistoryLimit(q.Limit)
	if q.Limit > c.maxHistory {
		q.Limit = c.maxHistory
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	entry := c.entries[traderID]
	if entry != nil && entry.query == q && entry.initialBalance == initialBalance && now.Sub(entry.lastLoadedAt) < c.ttl {
		return cloneEquityPoints(entry.points), nil
	}
	records, err := fetch(q.Limit, q.FromCycle, q.ToCycle, q.IncludeActions)
	if err != nil {
		return nil, err
	}
	requestedBalance := initialBalance
	if initialBalance <= 0 {
		for _, r := range records {
			if r.Success && r.AccountState.TotalBalance > 0 {
				initialBalance = r.AccountState.TotalBalance
				break
			}
		}
	}
	points := make([]EquityPoint, 0, len(records))
	for _, r := range records {
		if !r.Success || r.AccountState.TotalBalance <= 0 {
			continue
		}
		pct := 0.0
		if initialBalance > 0 {
			pct = r.AccountState.TotalUnrealizedProfit / initialBalance * 100
		}
		p := EquityPoint{Timestamp: r.Timestamp.Format("2006-01-02 15:04:05"), TotalEquity: r.AccountState.TotalBalance, AvailableBalance: r.AccountState.AvailableBalance, TotalPnL: r.AccountState.TotalUnrealizedProfit, TotalPnLPct: pct, PositionCount: r.AccountState.PositionCount, MarginUsedPct: r.AccountState.MarginUsedPct, CycleNumber: r.CycleNumber, SourceFile: r.SourceFile}
		if q.IncludeActions {
			p.Actions = r.Actions
		}
		points = append(points, p)
	}
	// Bound retained query variants/traders as well as individual histories.
	if len(c.entries) >= 8 {
		for key := range c.entries {
			delete(c.entries, key)
			break
		}
	}
	c.entries[traderID] = &equityHistoryEntry{points: points, lastLoadedAt: now, initialBalance: requestedBalance, query: q}
	return cloneEquityPoints(points), nil
}
func clampEquityHistoryLimit(limit int) int {
	if limit <= 0 || limit > maxEquityHistoryLimit {
		return maxEquityHistoryLimit
	}
	return limit
}
func applyEquityHistoryQuery(points []EquityPoint, q EquityHistoryQuery) []EquityPoint {
	out := make([]EquityPoint, 0, len(points))
	for _, p := range points {
		if (q.FromCycle > 0 && p.CycleNumber < q.FromCycle) || (q.ToCycle > 0 && p.CycleNumber > q.ToCycle) {
			continue
		}
		out = append(out, p)
	}
	limit := clampEquityHistoryLimit(q.Limit)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return cloneEquityPoints(out)
}
func cloneEquityPoints(points []EquityPoint) []EquityPoint {
	out := make([]EquityPoint, len(points))
	copy(out, points)
	for i := range out {
		if out[i].Actions != nil {
			out[i].Actions = append([]logger.ChartAction(nil), out[i].Actions...)
		}
	}
	return out
}

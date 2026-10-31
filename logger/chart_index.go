package logger

import (
	"bufio"
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

var ErrChartIndexPending = errors.New("chart history index pending or unavailable; backfill runs outside HTTP requests")
var chartBackfillMu sync.Mutex // Only one historical raw decoder per process.
var chartFilename = regexp.MustCompile(`^decision_[0-9]{8}_[0-9]{6}_cycle[0-9]+\.json$`)

// ChartAction deliberately cannot retain reasoning or errors.
type ChartAction struct {
	Action         string    `json:"action"`
	Symbol         string    `json:"symbol"`
	DecisionSource string    `json:"decision_source,omitempty"`
	Quantity       float64   `json:"quantity"`
	Leverage       int       `json:"leverage"`
	Price          float64   `json:"price"`
	OrderID        int64     `json:"order_id"`
	Timestamp      time.Time `json:"timestamp"`
	Success        bool      `json:"success"`
}

type ChartRecord struct {
	Timestamp    time.Time       `json:"timestamp"`
	CycleNumber  int             `json:"cycle_number"`
	AccountState AccountSnapshot `json:"account_state"`
	Success      bool            `json:"success"`
	SourceFile   string          `json:"source_file"`
	Actions      []ChartAction   `json:"actions,omitempty"`
}

type chartIndex struct {
	mu          sync.Mutex
	dir         string
	initialized bool
	ready       bool
	seen        map[string]bool
}

func chartSourceCycle(name string) int {
	m := cycleNumberRe.FindStringSubmatch(name)
	if len(m) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func chartPointFromDecision(r *DecisionRecord, source string) ChartRecord {
	p := ChartRecord{Timestamp: r.Timestamp, CycleNumber: r.CycleNumber, AccountState: r.AccountState, Success: r.Success, SourceFile: source}
	for _, a := range r.Decisions {
		if a.Success && (a.Action == "open_long" || a.Action == "open_short" || a.Action == "close_long" || a.Action == "close_short" || a.Action == "partial_close") {
			p.Actions = append(p.Actions, ChartAction{Action: a.Action, Symbol: a.Symbol, DecisionSource: a.DecisionSource, Quantity: a.Quantity, Leverage: a.Leverage, Price: a.Price, OrderID: a.OrderID, Timestamp: a.Timestamp, Success: true})
		}
	}
	return p
}

func newChartIndex(dir string) *chartIndex    { return &chartIndex{dir: dir, seen: make(map[string]bool)} }
func (x *chartIndex) path(name string) string { return filepath.Join(x.dir, "chart_index", name) }

// walkChartLines never reads the JSONL into memory. An incomplete last line is
// discarded; reconciliation retries its source file, not a cycle high-water mark.
func walkChartLines(path string, visit func(ChartRecord) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var p ChartRecord
		if json.Unmarshal(line, &p) != nil || !chartFilename.MatchString(p.SourceFile) || p.CycleNumber <= 0 {
			continue
		}
		if err := visit(p); err != nil {
			return err
		}
	}
}

// initializeLocked repairs duplicates/torn tails using an atomic replacement.
func (x *chartIndex) initializeLocked() error {
	if x.initialized {
		return nil
	}
	if err := os.MkdirAll(x.path(""), 0755); err != nil {
		return err
	}
	var meta struct {
		Version int `json:"version"`
	}
	b, _ := os.ReadFile(x.path("meta.json"))
	json.Unmarshal(b, &meta)
	f, err := os.Create(x.path("points.tmp"))
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	enc := json.NewEncoder(f)
	if meta.Version == 1 {
		err = walkChartLines(x.path("points.jsonl"), func(p ChartRecord) error {
			if seen[p.SourceFile] {
				return nil
			}
			if err := enc.Encode(p); err != nil {
				return err
			}
			seen[p.SourceFile] = true
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			f.Close()
			return err
		}
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(x.path("points.tmp"), x.path("points.jsonl")); err != nil {
		return err
	}
	if err = os.WriteFile(x.path("meta.tmp"), []byte(`{"version":1}`), 0644); err != nil {
		return err
	}
	if err = os.Rename(x.path("meta.tmp"), x.path("meta.json")); err != nil {
		return err
	}
	x.seen = seen
	x.initialized = true
	return nil
}

func readChartRaw(dir, name string) (ChartRecord, error) {
	var raw struct {
		Timestamp    time.Time       `json:"timestamp"`
		CycleNumber  int             `json:"cycle_number"`
		AccountState AccountSnapshot `json:"account_state"`
		Success      bool            `json:"success"`
		Decisions    []ChartAction   `json:"decisions"`
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return ChartRecord{}, err
	}
	if !chartFilename.MatchString(name) || !info.Mode().IsRegular() {
		return ChartRecord{}, fmt.Errorf("not a regular decision file: %s", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return ChartRecord{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if err = dec.Decode(&raw); err != nil {
		return ChartRecord{}, err
	}
	var extra interface{}
	if err = dec.Decode(&extra); err != io.EOF {
		return ChartRecord{}, fmt.Errorf("trailing raw JSON: %s", name)
	}
	if raw.CycleNumber <= 0 {
		return ChartRecord{}, fmt.Errorf("invalid cycle: %s", name)
	}
	p := ChartRecord{Timestamp: raw.Timestamp, CycleNumber: raw.CycleNumber, AccountState: raw.AccountState, Success: raw.Success, SourceFile: name}
	for _, a := range raw.Decisions {
		if a.Success && (a.Action == "open_long" || a.Action == "open_short" || a.Action == "close_long" || a.Action == "close_short" || a.Action == "partial_close") {
			p.Actions = append(p.Actions, a)
		}
	}
	return p, nil
}

func (x *chartIndex) appendPoint(p ChartRecord) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.initializeLocked(); err != nil {
		return err
	}
	if x.seen[p.SourceFile] {
		return nil
	}
	f, err := os.OpenFile(x.path("points.jsonl"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		x.ready = false
		return err
	}
	err = json.NewEncoder(f).Encode(p)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		x.ready = false
		return err
	}
	x.seen[p.SourceFile] = true
	return nil
}
func (x *chartIndex) appendRaw(name string) error {
	p, err := readChartRaw(x.dir, name)
	if err != nil {
		return err
	}
	return x.appendPoint(p)
}

func (x *chartIndex) rebuild() error {
	chartBackfillMu.Lock()
	defer chartBackfillMu.Unlock()
	x.mu.Lock()
	x.ready = false
	err := x.initializeLocked()
	x.mu.Unlock()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(x.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !chartFilename.MatchString(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		x.mu.Lock()
		seen := x.seen[e.Name()]
		x.mu.Unlock()
		if seen {
			continue
		}
		p, err := readChartRaw(x.dir, e.Name())
		if err != nil {
			continue
		} // Invalid raw is retried next restart/rebuild.
		if err = x.appendPoint(p); err != nil {
			return err
		}
	}
	x.mu.Lock()
	x.ready = true
	x.mu.Unlock()
	return nil
}

type chartHeap []ChartRecord

func (h chartHeap) Len() int            { return len(h) }
func (h chartHeap) Less(i, j int) bool  { return h[i].CycleNumber < h[j].CycleNumber }
func (h chartHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *chartHeap) Push(v interface{}) { *h = append(*h, v.(ChartRecord)) }
func (h *chartHeap) Pop() interface{}   { a := *h; v := a[len(a)-1]; *h = a[:len(a)-1]; return v }

func (x *chartIndex) query(limit, from, to int, actions bool) ([]ChartRecord, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.ready {
		return nil, ErrChartIndexPending
	}
	if limit <= 0 || limit > 20000 {
		limit = 20000
	}
	h := &chartHeap{}
	cycles := make(map[int]bool)
	err := walkChartLines(x.path("points.jsonl"), func(p ChartRecord) error {
		if !p.Success || p.AccountState.TotalBalance <= 0 || (from > 0 && p.CycleNumber < from) || (to > 0 && p.CycleNumber > to) || cycles[p.CycleNumber] {
			return nil
		}
		if h.Len() >= limit && p.CycleNumber <= (*h)[0].CycleNumber {
			return nil
		}
		if !actions {
			p.Actions = nil
		}
		if h.Len() == limit {
			old := heap.Pop(h).(ChartRecord)
			delete(cycles, old.CycleNumber)
		}
		heap.Push(h, p)
		cycles[p.CycleNumber] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(*h, func(i, j int) bool { return (*h)[i].CycleNumber < (*h)[j].CycleNumber })
	return []ChartRecord(*h), nil
}

func (l *DecisionLogger) GetChartHistory(limit, from, to int, actions bool) ([]ChartRecord, error) {
	if l.chart == nil {
		return nil, ErrChartIndexPending
	}
	return l.chart.query(limit, from, to, actions)
}

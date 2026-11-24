package market

import (
	"math"
	"sort"
	"sync"
	"time"
)

const (
	shortBarMS             = int64(ShortTermBarIntervalMinutes) * 60_000
	historyBars            = 7 * 24 * 60 / ShortTermBarIntervalMinutes
	atrPeriod              = 14
	minReachabilitySamples = 50
)

type ShortTermLevel struct {
	Price     float64 `json:"price"`
	Timeframe string  `json:"timeframe"`
}

type ShortTermTargets struct {
	AsOfMS        int64                   `json:"as_of_ms"`
	HourlyAsOfMS  int64                   `json:"hourly_as_of_ms,omitempty"`
	Supports      []ShortTermLevel        `json:"supports,omitempty"`
	Resistances   []ShortTermLevel        `json:"resistances,omitempty"`
	HistoryStatus string                  `json:"history_status"`
	HistoryBars   int                     `json:"history_bars,omitempty"`
	History       []ShortTermReachability `json:"history,omitempty"`
}

type ShortTermReachability struct {
	Minutes   int     `json:"minutes"`
	Side      string  `json:"side"`
	Samples   int     `json:"samples"`
	MedianATR float64 `json:"median_atr"`
	P90ATR    float64 `json:"p90_atr"`
	distances []float64
}

type ShortTermTargetFrequency struct {
	Minutes  int     `json:"minutes"`
	Samples  int     `json:"samples"`
	Fraction float64 `json:"fraction"`
}

type ShortTermTargetMetrics struct {
	Side               string                     `json:"side"`
	AsOfMS             int64                      `json:"as_of_ms"`
	Entry              float64                    `json:"entry"`
	StopLoss           float64                    `json:"stop_loss"`
	Target             float64                    `json:"target"`
	FirstObstacle      *ShortTermLevel            `json:"first_obstacle,omitempty"`
	FirstObstacleNetRR *float64                   `json:"first_obstacle_net_rr,omitempty"`
	TargetATR          *float64                   `json:"target_atr,omitempty"`
	HistoryStatus      string                     `json:"history_status"`
	Reachability       []ShortTermTargetFrequency `json:"reachability,omitempty"`
}

func newShortTermTargets(bars3m, bars1h []Kline) *ShortTermTargets {
	targets := &ShortTermTargets{AsOfMS: bars3m[len(bars3m)-1].CloseTime, HistoryStatus: "not_requested"}
	if len(bars1h) >= 24 {
		targets.HourlyAsOfMS = bars1h[len(bars1h)-1].CloseTime
	}
	for _, source := range []struct {
		bars      []Kline
		limit     int
		timeframe string
	}{{bars3m, 20, "3m"}, {bars1h, 24, "1h"}} {
		bars := source.bars[max(0, len(source.bars)-source.limit):]
		for i := 2; i+2 < len(bars); i++ {
			pivot := bars[i]
			isHigh, isLow := validPrice(pivot.High), validPrice(pivot.Low)
			for _, offset := range []int{-2, -1, 1, 2} {
				other := bars[i+offset]
				isHigh = isHigh && validPrice(other.High) && pivot.High > other.High
				isLow = isLow && validPrice(other.Low) && pivot.Low < other.Low
			}
			if isHigh {
				targets.Resistances = append(targets.Resistances, ShortTermLevel{pivot.High, source.timeframe})
			}
			if isLow {
				targets.Supports = append(targets.Supports, ShortTermLevel{pivot.Low, source.timeframe})
			}
		}
	}
	return targets
}

// Fresh reports whether both timeframes needed by the scan are current.
func (t *ShortTermTargets) Fresh(now time.Time) bool {
	return t != nil && freshClosedBar(t.AsOfMS, now, 2*shortBarMS) && freshClosedBar(t.HourlyAsOfMS, now, 3_900_000)
}

func freshClosedBar(closeMS int64, now time.Time, maxAgeMS int64) bool {
	age := now.UnixMilli() - closeMS
	return closeMS > 0 && age >= 0 && age <= maxAgeMS
}

func validPrice(x float64) bool { return x > 0 && !math.IsInf(x, 0) }

func (t *ShortTermTargets) FirstObstacle(entry float64, long bool) *ShortTermLevel {
	if t == nil || !validPrice(entry) {
		return nil
	}
	levels, direction := t.Resistances, 1.0
	if !long {
		levels, direction = t.Supports, -1
	}
	var nearest *ShortTermLevel
	for _, level := range levels {
		distance := direction * (level.Price - entry)
		if distance > 0 && (nearest == nil || distance < direction*(nearest.Price-entry)) {
			copy := level
			nearest = &copy
		}
	}
	return nearest
}

func (d *Data) MeasureShortTermTarget(entry, stop, target, cost float64, long bool) *ShortTermTargetMetrics {
	if d == nil || d.ShortTermTargets == nil || !validPrice(entry) || !validPrice(stop) || !validPrice(target) {
		return nil
	}
	t := d.ShortTermTargets
	m := &ShortTermTargetMetrics{
		AsOfMS: t.AsOfMS, Entry: entry, StopLoss: stop, Target: target,
		FirstObstacle: t.FirstObstacle(entry, long), HistoryStatus: t.HistoryStatus,
	}
	direction, side := 1.0, "long"
	if !long {
		direction, side = -1, "short"
	}
	if risk := direction * (entry - stop) / entry; m.FirstObstacle != nil && risk > 0 {
		reward := direction * (m.FirstObstacle.Price - entry) / entry
		rr := (reward - cost) / (risk + cost)
		m.FirstObstacleNetRR = &rr
	}
	if distance := direction * (target - entry); distance > 0 && validPrice(d.IntradayATR14) {
		targetATR := distance / d.IntradayATR14
		m.TargetATR = &targetATR
		for _, h := range t.History {
			if h.Side == side && len(h.distances) > 0 {
				index := sort.SearchFloat64s(h.distances, targetATR)
				m.Reachability = append(m.Reachability, ShortTermTargetFrequency{
					Minutes: h.Minutes, Samples: h.Samples, Fraction: float64(h.Samples-index) / float64(h.Samples),
				})
			}
		}
	}
	return m
}

type shortTermHistoryEntry struct {
	bars       []Kline
	retryAfter time.Time
	lastUsed   time.Time
	loading    bool
	failed     bool
}

// The cache is process-local and bounded. Regular live bars extend the cached
// history; only a cold symbol or a gap schedules a paginated request.
var shortTermHistory = struct {
	sync.Mutex
	entries map[string]*shortTermHistoryEntry
	workers chan struct{}
}{entries: make(map[string]*shortTermHistoryEntry), workers: make(chan struct{}, 2)}

func (d *Data) PrepareShortTermReachability() {
	if d == nil || d.ShortTermTargets == nil || len(d.shortTermBars) == 0 {
		return
	}
	t := d.ShortTermTargets
	t.History = nil
	t.HistoryBars = 0
	if !freshClosedBar(t.AsOfMS, time.Now(), 2*shortBarMS) {
		t.HistoryStatus = "stale"
		return
	}
	bars, status := shortTermHistoryBars(Normalize(d.Symbol), d.shortTermBars)
	t.HistoryBars, t.HistoryStatus = len(bars), status
	if len(bars) < atrPeriod+1+10*minReachabilitySamples {
		return
	}
	t.History = reachabilityDistributions(bars)
	if len(t.History) == 0 {
		return
	}
	t.HistoryStatus = "partial"
	if len(t.History) == 4 && len(bars) >= historyBars+atrPeriod {
		t.HistoryStatus = "ready"
	}
}

func shortTermHistoryBars(symbol string, recent []Kline) ([]Kline, string) {
	base := fapiBaseURL
	key := base + "|" + symbol
	now := time.Now()
	shortTermHistory.Lock()
	defer shortTermHistory.Unlock()
	e := shortTermHistory.entries[key]
	if e == nil {
		if len(shortTermHistory.entries) >= 64 {
			var oldest string
			for k, entry := range shortTermHistory.entries {
				if !entry.loading && (oldest == "" || entry.lastUsed.Before(shortTermHistory.entries[oldest].lastUsed)) {
					oldest = k
				}
			}
			if oldest == "" {
				return nil, "warming"
			}
			delete(shortTermHistory.entries, oldest)
		}
		e = &shortTermHistoryEntry{}
		shortTermHistory.entries[key] = e
	}
	e.lastUsed = now
	e.bars = mergeShortTermBars(e.bars, recent)
	cutoff := recent[len(recent)-1].CloseTime
	end := sort.Search(len(e.bars), func(i int) bool { return e.bars[i].CloseTime > cutoff })
	bars := contiguousShortTermBars(e.bars[:end])
	if len(bars) == 0 || bars[len(bars)-1].CloseTime != cutoff {
		return nil, "unavailable"
	}
	if now.After(e.retryAfter) && !e.loading && len(bars) < historyBars+atrPeriod {
		select {
		case shortTermHistory.workers <- struct{}{}:
			e.loading = true
			go backfillShortTermHistory(e, base, symbol, bars[0].OpenTime)
		default:
		}
	}
	if e.failed && !e.loading {
		return bars, "unavailable"
	}
	return bars, "warming"
}

func backfillShortTermHistory(e *shortTermHistoryEntry, base, symbol string, end int64) {
	var fetched []Kline
	failed := false
	for page := 0; page < 4; page++ {
		bars, err := getKlines(base, symbol, "3m", 1000, end-1)
		if err != nil {
			failed = true
			break
		}
		if len(bars) == 0 || bars[0].OpenTime >= end {
			break
		}
		for _, b := range bars {
			if b.CloseTime < end {
				fetched = append(fetched, b)
			}
		}
		end = bars[0].OpenTime
		if len(bars) < 1000 || len(fetched) >= historyBars+atrPeriod {
			break
		}
	}
	shortTermHistory.Lock()
	defer shortTermHistory.Unlock()
	defer func() { <-shortTermHistory.workers }()
	e.bars = mergeShortTermBars(fetched, e.bars)
	e.loading = false
	e.failed = failed
	e.retryAfter = time.Now().Add(15 * time.Minute)
}

func mergeShortTermBars(older, newer []Kline) []Kline {
	byTime := make(map[int64]Kline, len(older)+len(newer))
	for _, bars := range [][]Kline{older, newer} {
		for _, b := range bars {
			if b.CloseTime == b.OpenTime+shortBarMS-1 && validPrice(b.Low) && validPrice(b.High) &&
				validPrice(b.Close) && b.Low <= b.Close && b.Close <= b.High {
				byTime[b.OpenTime] = b
			}
		}
	}
	bars := make([]Kline, 0, len(byTime))
	for _, b := range byTime {
		bars = append(bars, b)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].OpenTime < bars[j].OpenTime })
	return bars[max(0, len(bars)-historyBars-80):]
}

func contiguousShortTermBars(bars []Kline) []Kline {
	i := len(bars) - 1
	for i > 0 && bars[i].OpenTime-bars[i-1].OpenTime == shortBarMS {
		i--
	}
	return bars[max(0, i):]
}

func reachabilityDistributions(bars []Kline) []ShortTermReachability {
	var result []ShortTermReachability
	for _, window := range []int{10, 15} {
		var up, down []float64
		// Anchor to the last closed candle and step by a full window so
		// outcomes never overlap or extend past the snapshot.
		for i := len(bars) - 1 - window; i >= max(atrPeriod, len(bars)-historyBars); i -= window {
			atr := calculateATR(bars[max(0, i-79):i+1], atrPeriod)
			if !validPrice(atr) {
				continue
			}
			high, low := bars[i].Close, bars[i].Close
			for _, future := range bars[i+1 : i+1+window] {
				high = max(high, future.High)
				low = min(low, future.Low)
			}
			up = append(up, (high-bars[i].Close)/atr)
			down = append(down, (bars[i].Close-low)/atr)
		}
		for _, side := range []struct {
			name   string
			values []float64
		}{{"long", up}, {"short", down}} {
			if len(side.values) < minReachabilitySamples {
				continue
			}
			sort.Float64s(side.values)
			n := len(side.values)
			result = append(result, ShortTermReachability{Minutes: window * ShortTermBarIntervalMinutes,
				Side: side.name, Samples: n, MedianATR: side.values[(n-1)/2], P90ATR: side.values[int(.9*float64(n-1))], distances: side.values})
		}
	}
	return result
}

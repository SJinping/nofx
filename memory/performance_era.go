package memory

import (
	"nofx/decision"
	"nofx/logger"
	"sort"
	"time"
)

const inactivityThreshold = 240 * time.Hour

// PerformanceEra derives the lifetime and decision-making windows from the immutable
// completed ledger and the current exchange snapshot. A continuous exposure interval
// (including overlapping positions) cannot contain an inactivity gap.
func (tm *TradeMemory) PerformanceEra(limit int, now time.Time, positions []decision.PositionInfo) (lifetime, effective *logger.PerformanceAnalysis, reset bool) {
	if tm == nil {
		empty := analyzeTradeHistory(nil, limit)
		return empty, empty, false
	}
	tm.mu.Lock()
	history := append([]*TradeRecord(nil), tm.history...)
	episodes := make(map[string]time.Time, len(tm.episodes))
	for k, ep := range tm.episodes {
		if ep != nil {
			episodes[k] = ep.OpenTime
		}
	}
	tm.mu.Unlock()
	lifetime = analyzeTradeHistory(append([]*TradeRecord(nil), history...), limit)
	type interval struct{ start, end time.Time }
	intervals := make([]interval, 0, len(history)+len(positions))
	activeCount := 0
	for _, tr := range history {
		if tr == nil || tr.Symbol == "" || tr.CloseTime.IsZero() || tr.CloseTime.After(now) {
			continue
		}
		start := tr.OpenTime
		if start.IsZero() || start.After(tr.CloseTime) {
			start = tr.CloseTime
		} // legacy ledger fallback
		intervals = append(intervals, interval{start, tr.CloseTime})
	}
	for _, pos := range positions {
		if pos.Symbol == "" || pos.Quantity <= 0 {
			continue
		}
		activeCount++
		start := episodes[posKey(pos.Symbol, pos.Side)]
		if start.IsZero() || start.After(now) {
			start = time.UnixMilli(pos.UpdateTime)
			if pos.UpdateTime <= 0 || start.After(now) {
				start = now
			} // unknown opening time: do not resurrect stale feedback
		}
		intervals = append(intervals, interval{start, now})
	}
	if len(intervals) == 0 {
		return lifetime, analyzeTradeHistory(nil, limit), false
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	end := intervals[0].end
	var cutoff time.Time
	for _, span := range intervals[1:] {
		if span.start.Sub(end) >= inactivityThreshold {
			cutoff = span.start
		}
		if span.end.After(end) {
			end = span.end
		}
	}
	if activeCount == 0 && now.Sub(end) >= inactivityThreshold {
		return lifetime, analyzeTradeHistory(nil, limit), true
	}
	selected := make([]*TradeRecord, 0, len(history))
	for _, tr := range history {
		if tr != nil && !tr.CloseTime.After(now) && (cutoff.IsZero() || !tr.OpenTime.Before(cutoff) || (tr.OpenTime.IsZero() && !tr.CloseTime.Before(cutoff))) {
			selected = append(selected, tr)
		}
	}
	return lifetime, analyzeTradeHistory(selected, limit), false
}

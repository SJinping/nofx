package market

import (
	"math"
	"testing"
)

func TestShortTermTargetMetrics(t *testing.T) {
	bars := []Kline{{High: 102, Low: 99}, {High: 103, Low: 98}, {High: 104, Low: 97},
		{High: 103, Low: 98}, {High: 102, Low: 99}, {High: 110, Low: 96}}
	data := &Data{IntradayATR14: 2, ShortTermTargets: newShortTermTargets(bars, nil)}
	data.ShortTermTargets.History = []ShortTermReachability{
		{Minutes: 30, Side: "long", Samples: 3, distances: []float64{1, 3, 4}},
	}
	m := data.MeasureShortTermTarget(100, 98, 106, .0018, true)
	if m.FirstObstacle == nil || m.FirstObstacle.Price != 104 || m.FirstObstacle.Timeframe != "3m" {
		t.Fatalf("expected a confirmed 3m resistance, got %+v", m.FirstObstacle)
	}
	if m.FirstObstacleNetRR == nil || math.Abs(*m.FirstObstacleNetRR-(.04-.0018)/(.02+.0018)) > 1e-9 ||
		m.TargetATR == nil || *m.TargetATR != 3 || len(m.Reachability) != 1 || m.Reachability[0].Fraction != 2.0/3 {
		t.Fatalf("unexpected target metrics: %+v", m)
	}
	m = data.MeasureShortTermTarget(100, 102, 95, .0018, false)
	if m.FirstObstacle == nil || m.FirstObstacle.Price != 97 || len(m.Reachability) != 0 {
		t.Fatalf("missing short history must preserve the support and current metrics: %+v", m)
	}
	m = data.MeasureShortTermTarget(105, 103, 109, .0018, true)
	if m.FirstObstacle != nil || m.FirstObstacleNetRR != nil || m.TargetATR == nil {
		t.Fatalf("no confirmed obstacle must still yield an ATR distance: %+v", m)
	}
}

func TestReachabilityUsesCompleteContiguousHistory(t *testing.T) {
	bars := make([]Kline, 900)
	for i := range bars {
		open := int64(i+1) * shortBarMS
		bars[i] = Kline{OpenTime: open, CloseTime: open + shortBarMS - 1, Close: 100, Low: 99, High: 101}
	}
	merged := mergeShortTermBars(bars[:800], bars[750:])
	if len(merged) != 900 {
		t.Fatalf("overlap should be deduplicated, got %d", len(merged))
	}
	got := reachabilityDistributions(merged)
	if len(got) != 4 || got[0].Minutes != 30 || got[2].Minutes != 45 || got[0].MedianATR != .5 {
		t.Fatalf("unexpected reachability distributions: %+v", got)
	}
	merged = append(merged[:850:850], merged[851:]...)
	tail := contiguousShortTermBars(merged)
	if len(tail) != 49 || len(reachabilityDistributions(tail)) != 0 {
		t.Fatal("a candle gap must not manufacture historical reachability samples")
	}
}

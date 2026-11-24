package decision

import (
	"nofx/market"
	"testing"
)

func TestStrategyVFlatScan(t *testing.T) {
	bars := &market.IntradayData{}
	for range 11 {
		bars.Closes = append(bars.Closes, 100)
		bars.Highs = append(bars.Highs, 101)
		bars.Lows = append(bars.Lows, 99)
		bars.Volumes = append(bars.Volumes, 10)
		bars.EMA20Values = append(bars.EMA20Values, 100)
	}
	data := &market.Data{IntradaySeries: bars, MidTermContext: &market.MidTermData{MACDValues: []float64{0, 0}}}
	ctx := &Context{MarketDataMap: map[string]*market.Data{"ETHUSDT": data}}
	if got := evaluateFlatScan(ctx); !got.Skipped || got.Checked != 1 {
		t.Fatalf("range should skip: %+v", got)
	}
	bars.Closes[10], bars.Highs[10], bars.Volumes[10] = 102, 103, 12
	if got := evaluateFlatScan(ctx); got.Skipped || len(got.Matches) != 1 || got.Matches[0] != "ETHUSDT:long" {
		t.Fatalf("volume breakout should call: %+v", got)
	}
	bars.Closes[8], bars.Closes[9], bars.Closes[10] = 101, 100, 99
	bars.Highs[8], bars.Highs[9], bars.Highs[10], bars.Volumes[10] = 103, 102, 101, 5
	data.MidTermContext.MACDValues[1] = -1
	if got := evaluateFlatScan(ctx); got.Skipped || len(got.Matches) != 1 || got.Matches[0] != "ETHUSDT:short" {
		t.Fatalf("trend recovery should call independently of volume: %+v", got)
	}
	data.MidTermContext = nil
	if got := evaluateFlatScan(ctx); got.Skipped || got.Reason != "incomplete_scan_data" {
		t.Fatalf("missing candles should preserve model review: %+v", got)
	}
	ctx.ShortTermWatchlist = []ShortTermWatchItem{{Symbol: "ETHUSDT"}}
	if got := evaluateFlatScan(ctx); got.Skipped || got.Reason != "watchlist" {
		t.Fatalf("watchlist should preserve model review: %+v", got)
	}
	ctx.PositionsUnconfirmed = true
	if got := evaluateFlatScan(ctx); got.Skipped || got.Reason != "positions_unconfirmed" {
		t.Fatalf("unconfirmed positions should preserve model review: %+v", got)
	}
	ctx.Positions = []PositionInfo{{Symbol: "ETHUSDT"}}
	if got := evaluateFlatScan(ctx); got != nil {
		t.Fatalf("position management bypasses the filter: %+v", got)
	}
}

func TestFlatScanSkipDoesNotReportModelUsage(t *testing.T) {
	got := flatScanSkipDecision(evaluateFlatScan(&Context{}))
	if got.LLMUsage != nil || got.LLMCostUSDT != 0 || len(got.Decisions) != 1 || got.Decisions[0].Action != ActionWait {
		t.Fatalf("unexpected skip decision: %+v", got)
	}
}

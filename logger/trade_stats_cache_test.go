package logger

import (
	"testing"
	"time"
)

func testAction(action, symbol string, price, quantity float64) DecisionAction {
	return DecisionAction{Action: action, Symbol: symbol, Price: price, Quantity: quantity, Timestamp: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC), Success: true}
}

func TestTradeStatsCacheIgnoresObservationActions(t *testing.T) {
	cache := &TradeStatsCache{}
	for i, action := range []string{"wait", "hold", "update_stop_loss", "update_take_profit"} {
		cache.ProcessRecords([]*DecisionRecord{{CycleNumber: i + 1, Decisions: []DecisionAction{testAction(action, "BTCUSDT", 0, 0)}}})
	}
	if len(cache.SymbolStats) != 0 {
		t.Fatalf("observation actions created symbol stats: %#v", cache.SymbolStats)
	}
}

func TestTradeStatsCacheIncludesPartialClosePnLInCompletedTrade(t *testing.T) {
	cache := &TradeStatsCache{}
	cache.ProcessRecords([]*DecisionRecord{{CycleNumber: 1, Decisions: []DecisionAction{testAction("open_long", "HYPEUSDT", 100, 10)}}, {CycleNumber: 2, Decisions: []DecisionAction{testAction("partial_close", "HYPEUSDT", 110, 5)}}, {CycleNumber: 3, Decisions: []DecisionAction{testAction("close_long", "HYPEUSDT", 105, 5)}}})
	stats := cache.SymbolStats["HYPEUSDT"]
	if stats == nil || stats.RealizedPnL != 75 || stats.TotalTrades != 1 || stats.WinCount != 1 || stats.LossCount != 0 {
		t.Fatalf("stats = %+v, want realized=75 and one winning trade", stats)
	}
}

func TestTradeStatsCacheCountsBreakevenTrade(t *testing.T) {
	cache := &TradeStatsCache{}
	cache.ProcessRecords([]*DecisionRecord{{CycleNumber: 1, Decisions: []DecisionAction{testAction("open_long", "SOLUSDT", 100, 1)}}, {CycleNumber: 2, Decisions: []DecisionAction{testAction("close_long", "SOLUSDT", 100, 1)}}})
	stats := cache.SymbolStats["SOLUSDT"]
	if stats == nil || stats.TotalTrades != 1 || stats.WinCount != 0 || stats.LossCount != 0 {
		t.Fatalf("stats = %+v, want one breakeven trade", stats)
	}
}

func TestTradeStatsCacheCountsFullSizePartialCloseAsCompleted(t *testing.T) {
	cache := &TradeStatsCache{}
	cache.ProcessRecords([]*DecisionRecord{{CycleNumber: 1, Decisions: []DecisionAction{testAction("open_short", "ETHUSDT", 100, 2)}}, {CycleNumber: 2, Decisions: []DecisionAction{testAction("partial_close", "ETHUSDT", 90, 2)}}})
	stats := cache.SymbolStats["ETHUSDT"]
	if stats == nil || stats.RealizedPnL != 20 || stats.TotalTrades != 1 || stats.WinCount != 1 {
		t.Fatalf("stats = %+v, want one winning trade with PnL 20", stats)
	}
}

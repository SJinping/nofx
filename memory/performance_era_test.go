package memory

import (
	"nofx/decision"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPerformanceEraInactivityAndRecovery(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := completedTrade("BTC", "long", -4, now.Add(-240*time.Hour))
	tm := &TradeMemory{history: []*TradeRecord{old}}
	dir := t.TempDir()
	tm.tradesPath = filepath.Join(dir, "trades.jsonl")
	ledger := []byte("canonical ledger unchanged")
	if err := os.WriteFile(tm.tradesPath, ledger, 0600); err != nil {
		t.Fatal(err)
	}
	lifetime, effective, reset := tm.PerformanceEra(100, now, nil)
	if !reset || lifetime.TotalTrades != 1 || effective.TotalTrades != 0 || effective.CurrentLosingStreak != 0 || len(effective.RecentTrades) != 0 {
		t.Fatalf("flat reset: %v %+v %+v", reset, lifetime, effective)
	}
	if got, err := os.ReadFile(tm.tradesPath); err != nil || string(got) != string(ledger) {
		t.Fatalf("ledger changed: %q %v", got, err)
	}
	_, effective, reset = tm.PerformanceEra(100, now.Add(-time.Second), nil)
	if reset || effective.CurrentLosingStreak != 1 {
		t.Fatalf("before boundary: %v %+v", reset, effective)
	}
	newTrade := completedTrade("ETH", "short", -2, now.Add(2*time.Hour))
	tm.history = append([]*TradeRecord{newTrade}, tm.history...)
	_, effective, reset = tm.PerformanceEra(100, now.Add(3*time.Hour), nil)
	if reset || effective.TotalTrades != 1 || effective.CurrentLosingStreak != 1 {
		t.Fatalf("new era: %v %+v", reset, effective)
	}
}

func TestPerformanceEraCompletedOverlapAndLegacyFallback(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := completedTrade("BTC", "long", -4, now.Add(-300*time.Hour))
	longHeld := completedTrade("ETH", "long", -2, now.Add(-time.Hour))
	longHeld.OpenTime = old.CloseTime.Add(time.Hour) // close-to-close >10 days, actual inactivity only 1h
	tm := &TradeMemory{history: []*TradeRecord{longHeld, old}}
	_, effective, _ := tm.PerformanceEra(100, now, nil)
	if effective.TotalTrades != 2 || effective.CurrentLosingStreak != 2 {
		t.Fatalf("false reset across long-held trade: %+v", effective)
	}
	longHeld.OpenTime = time.Time{} // legacy close-only ledger falls back to close-to-close
	_, effective, _ = tm.PerformanceEra(100, now, nil)
	if effective.TotalTrades != 1 || effective.CurrentLosingStreak != 1 {
		t.Fatalf("legacy fallback: %+v", effective)
	}
}

func TestPerformanceEraOpenGapAndOverlappingExposure(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := completedTrade("BTC", "long", -4, now.Add(-300*time.Hour))
	tm := &TradeMemory{history: []*TradeRecord{old}, episodes: map[string]*TradeEpisode{
		"ETH_long": {OpenTime: now.Add(-time.Hour), Symbol: "ETH", Side: "long"},
	}}
	_, effective, reset := tm.PerformanceEra(100, now, []decision.PositionInfo{{Symbol: "ETH", Side: "long", Quantity: 1}})
	if reset || effective.TotalTrades != 0 {
		t.Fatalf("open after idle: %v %+v", reset, effective)
	}
	tm.episodes["ETH_long"].OpenTime = now.Add(-400 * time.Hour)
	_, effective, reset = tm.PerformanceEra(100, now, []decision.PositionInfo{{Symbol: "ETH", Side: "long", Quantity: 1}})
	if reset || effective.TotalTrades != 1 {
		t.Fatalf("overlap: %v %+v", reset, effective)
	}
	tm.episodes = nil
	newTrade := completedTrade("ETH", "long", 2, now.Add(-time.Hour))
	newTrade.OpenTime = now.Add(-40 * time.Hour)
	tm.history = append([]*TradeRecord{newTrade}, tm.history...)
	_, effective, _ = tm.PerformanceEra(100, now, nil)
	if effective.TotalTrades != 1 {
		t.Fatalf("long-held trade boundary: %+v", effective)
	}
}

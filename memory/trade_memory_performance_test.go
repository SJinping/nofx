package memory

import (
	"nofx/decision"
	"path/filepath"
	"testing"
	"time"
)

func completedTrade(symbol, side string, pnl float64, closeTime time.Time) *TradeRecord {
	return &TradeRecord{
		TradeID:    symbol + "_" + side + "_" + closeTime.Format("150405"),
		Symbol:     symbol,
		Side:       side,
		OpenTime:   closeTime.Add(-time.Hour),
		CloseTime:  closeTime,
		DurationS:  3600,
		EntryPrice: 100,
		ExitPrice:  100 + pnl,
		Quantity:   1,
		PnL:        pnl,
		PnLPct:     pnl,
	}
}

func TestAnalyzeCompletedTradesIncludesExchangeReconciledExits(t *testing.T) {
	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	lit := completedTrade("LITUSDT", "long", -14.34, base.Add(3*time.Hour))
	lit.ExitReason = "exchange_conditional_stop_loss"
	tm := &TradeMemory{history: []*TradeRecord{
		lit,
		completedTrade("BTCUSDT", "short", -3.47, base.Add(2*time.Hour)),
		completedTrade("HYPEUSDT", "long", 3.05, base.Add(time.Hour)),
	}}

	performance := tm.AnalyzeCompletedTrades(2)
	if performance.TotalTrades != 3 {
		t.Fatalf("total trades = %d, want 3 completed ledger trades", performance.TotalTrades)
	}
	if performance.WinningTrades != 1 || performance.LosingTrades != 2 {
		t.Fatalf("wins/losses = %d/%d, want 1/2", performance.WinningTrades, performance.LosingTrades)
	}
	if performance.CurrentLosingStreak != 2 || performance.MaxLosingStreak != 2 {
		t.Fatalf("loss streaks = %d/%d, want 2/2", performance.CurrentLosingStreak, performance.MaxLosingStreak)
	}
	if len(performance.RecentTrades) != 2 {
		t.Fatalf("recent trades len = %d, want limit 2", len(performance.RecentTrades))
	}
	if performance.RecentTrades[0].Symbol != "LITUSDT" || performance.RecentTrades[0].CloseSource != "exchange_conditional_stop_loss" {
		t.Fatalf("latest trade = %+v, want exchange-reconciled LIT close", performance.RecentTrades[0])
	}
}

func TestCompletedSymbolStatsUsesCompletedLedgerInsteadOfDecisionLots(t *testing.T) {
	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	tm := &TradeMemory{history: []*TradeRecord{
		completedTrade("LITUSDT", "long", -14.34, base.Add(2*time.Hour)),
		completedTrade("LITUSDT", "long", 2, base.Add(time.Hour)),
		completedTrade("BTCUSDT", "short", -3, base),
	}}

	stats := tm.CompletedSymbolStats()
	lit := stats["LITUSDT"]
	if lit == nil {
		t.Fatal("LITUSDT missing from completed-ledger symbol stats")
	}
	if lit.TotalTrades != 2 || lit.WinCount != 1 || lit.LossCount != 1 {
		t.Fatalf("LIT stats = %+v, want 2 completed trades with 1 win/1 loss", lit)
	}
	if lit.RealizedPnL != -12.34 {
		t.Fatalf("LIT realized PnL = %.2f, want -12.34", lit.RealizedPnL)
	}
	if lit.OpenLongCount != 0 || lit.OpenShortCount != 0 {
		t.Fatalf("completed-ledger stats must not retain open lots: %+v", lit)
	}
}

func TestPartialCloseIsIncludedWhenEpisodeCompletes(t *testing.T) {
	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	tm := &TradeMemory{episodes: map[string]*TradeEpisode{
		"BTCUSDT_long": {
			TradeID: "btc-long",
			Symbol:  "BTCUSDT", Side: "long", OpenTime: base,
			EntryPrice: 100, Quantity: 10,
		},
	}, history: []*TradeRecord{}, traderID: "test", baseDir: dir, tradesPath: filepath.Join(dir, "trades.jsonl"), episodesPath: filepath.Join(dir, "open_episodes.json")}
	partial := &decision.Decision{Symbol: "BTCUSDT", Action: decision.ActionPartialClose}
	if err := tm.OnPartialCloseSuccess(partial, "long", 4, 110); err != nil {
		t.Fatalf("record partial close: %v", err)
	}
	// Simulate a restart: only the persisted episode checkpoint is carried over.
	var restored map[string]*TradeEpisode
	if err := readJSONFile(tm.episodesPath, &restored); err != nil {
		t.Fatalf("load persisted episode: %v", err)
	}
	tm = &TradeMemory{episodes: restored, history: []*TradeRecord{}, traderID: "test", baseDir: dir, tradesPath: filepath.Join(dir, "trades.jsonl"), episodesPath: filepath.Join(dir, "open_episodes.json")}
	close := &decision.Decision{Symbol: "BTCUSDT", Action: decision.ActionCloseLong}
	if _, err := tm.OnCloseSuccess(nil, close, 90, "llm"); err != nil {
		t.Fatalf("record final close: %v", err)
	}
	if duplicate, err := tm.OnCloseSuccess(nil, close, 90, "duplicate"); err != nil || duplicate != nil {
		t.Fatalf("duplicate close result = %v, %v; want no extra record", duplicate, err)
	}

	performance := tm.AnalyzeCompletedTrades(10)
	if performance.TotalTrades != 1 || len(performance.RecentTrades) != 1 {
		t.Fatalf("completed performance = %+v, want one trade", performance)
	}
	// (110-100)*4 + (90-100)*6 = -20; without the partial ledger this was -100.
	if got := performance.RecentTrades[0].PnL; got != -20 {
		t.Fatalf("completed PnL = %.2f, want -20.00 including partial close", got)
	}
}

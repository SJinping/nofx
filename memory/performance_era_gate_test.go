package memory

import (
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"testing"
	"time"
)

func TestGateDoesNotReusePreResetSimilarTrades(t *testing.T) {
	now := time.Now()
	tm := &TradeMemory{history: []*TradeRecord{}, baseDir: t.TempDir()}
	for i := 0; i < 4; i++ {
		tr := completedTrade("BTCUSDT", "long", -1, now.Add(time.Duration(-i)*time.Hour))
		tr.SignalVector = []float64{1}
		tm.history = append(tm.history, tr)
	}
	ctx := &decision.Context{Performance: &logger.PerformanceAnalysis{TotalTrades: 0}, MarketDataMap: map[string]*market.Data{"BTCUSDT": {}}}
	// The empty effective era must not expose old similar trades even if vector retrieval succeeds.
	matches := tm.effectiveSimilar([]float64{1}, "long", ctx)
	if len(matches) != 0 {
		t.Fatalf("old matches leaked into reset era: %+v", matches)
	}
}

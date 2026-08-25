package trader

import (
	"nofx/decision"
	"nofx/logger"
	"testing"
	"time"
)

func testPerformance(sharpe float64, trades, losses, streak int) *logger.PerformanceAnalysis {
	return &logger.PerformanceAnalysis{
		SharpeRatio:         sharpe,
		TotalTrades:         trades,
		LosingTrades:        losses,
		CurrentLosingStreak: streak,
	}
}

func TestPerformanceCooldownExpiresWithoutEndlessRetrigger(t *testing.T) {
	at := &AutoTrader{}
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	bad := testPerformance(-0.80, 5, 5, 5)

	st := at.updatePerformanceCooldown(bad, start, 1, 10)
	if !st.Active || st.ReleaseAt != start.Add(60*time.Minute) {
		t.Fatalf("initial cooldown = %+v", st)
	}

	st = at.updatePerformanceCooldown(bad, start.Add(50*time.Minute), 6, 10)
	if !st.Active || st.WaitMinutes != 50 || st.WaitCycles != 5 {
		t.Fatalf("active cooldown = %+v", st)
	}

	st = at.updatePerformanceCooldown(bad, start.Add(61*time.Minute), 7, 10)
	if st.Active || st.WaitMinutes != 61 || st.WaitCycles != 6 {
		t.Fatalf("cooldown should expire = %+v", st)
	}

	st = at.updatePerformanceCooldown(bad, start.Add(121*time.Minute), 13, 10)
	if st.Active {
		t.Fatalf("unchanged bad performance must not retrigger = %+v", st)
	}
}

func TestPerformanceCooldownRearmsAfterPerformanceChange(t *testing.T) {
	at := &AutoTrader{}
	start := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	bad := testPerformance(-0.80, 5, 5, 5)

	at.updatePerformanceCooldown(bad, start, 1, 10)
	at.updatePerformanceCooldown(bad, start.Add(61*time.Minute), 7, 10)

	changedBad := testPerformance(-0.90, 6, 6, 6)
	st := at.updatePerformanceCooldown(changedBad, start.Add(62*time.Minute), 8, 10)
	if !st.Active || st.TriggerCycle != 8 {
		t.Fatalf("changed bad performance should rearm = %+v", st)
	}

	st = at.updatePerformanceCooldown(testPerformance(0.10, 6, 6, 0), start.Add(63*time.Minute), 9, 10)
	if st.Active || st.LastTriggerReason != "" {
		t.Fatalf("healthy performance should clear trigger signature = %+v", st)
	}
}

func TestPerformanceCooldownRequiresSevereSharpeAndFiveLossStreak(t *testing.T) {
	at := &AutoTrader{}
	now := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)

	if st := at.updatePerformanceCooldown(testPerformance(-0.62, 5, 5, 5), now, 1, 10); st.Active {
		t.Fatalf("moderately negative Sharpe should remain cautious rather than start cooldown: %+v", st)
	}
	if st := at.updatePerformanceCooldown(testPerformance(-0.80, 5, 5, 4), now, 1, 10); st.Active {
		t.Fatalf("fewer than five consecutive losses should remain cautious rather than start cooldown: %+v", st)
	}
	if st := at.updatePerformanceCooldown(testPerformance(-0.80, 5, 5, 5), now, 1, 10); !st.Active {
		t.Fatalf("severe Sharpe and five-loss streak should start cooldown: %+v", st)
	}
}

func TestShouldSkipLLMForPerformanceCooldownWhileActive(t *testing.T) {
	now := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	active := decision.PerformanceCooldownState{Active: true, ReleaseAt: now.Add(time.Hour)}

	if !shouldSkipLLMForPerformanceCooldown(active, now) {
		t.Fatal("active cooldown must skip the LLM and all LLM-directed trades")
	}
	if shouldSkipLLMForPerformanceCooldown(decision.PerformanceCooldownState{Active: true, ReleaseAt: now}, now) {
		t.Fatal("expired cooldown must not skip the LLM")
	}
}

package logger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogDecisionWritesChartAndDerivativeFailureIsNonfatal(t *testing.T) {
	dir := t.TempDir()
	l := NewDecisionLogger(dir)
	waitChart(t, l)
	r := &DecisionRecord{Success: true, AccountState: AccountSnapshot{TotalBalance: 100}, InputPrompt: "secret", Decisions: []DecisionAction{{Action: "open_short", Success: true, Price: 42, Reasoning: "secret"}}}
	if err := l.LogDecision(r); err != nil {
		t.Fatal(err)
	}
	p, err := l.GetChartHistory(5, 0, 0, true)
	if err != nil || len(p) != 1 || len(p[0].Actions) != 1 {
		t.Fatalf("missing new cycle: %+v %v", p, err)
	}
	// A broken derivative destination must not fail the authoritative save.
	os.RemoveAll(filepath.Join(dir, "chart_index"))
	os.WriteFile(filepath.Join(dir, "chart_index"), []byte("blocked"), 0600)
	if err := l.LogDecision(r); err != nil {
		t.Fatalf("derivative prevented log: %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "decision_*.json")); len(files) != 2 {
		t.Fatalf("full logs missing: %v", files)
	}
}
func waitChart(t *testing.T, l *DecisionLogger) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := l.GetChartHistory(1, 0, 0, false); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("startup backfill did not finish")
}

func TestStartupStatsReplaysCyclesInNumericOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{1, 2, 9, 10, 11} {
		chartRaw(t, dir, n)
	}
	l := NewDecisionLogger(dir)
	waitChart(t, l)
	if l.statsCache.GetLastProcessedCycle() != 11 {
		t.Fatalf("wrong final cycle %d", l.statsCache.GetLastProcessedCycle())
	}
	stats := l.statsCache.GetSymbolStats()
	if len(stats) != 1 || stats[0].OpenLongCount != 5 {
		t.Fatalf("stats skipped numerically later cycles: %+v", stats)
	}
}

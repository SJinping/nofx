package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func chartRaw(t *testing.T, dir string, n int) string {
	t.Helper()
	name := fmt.Sprintf("decision_20260101_000000_cycle%d.json", n)
	r := DecisionRecord{Timestamp: time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC), CycleNumber: n, Success: true, InputPrompt: "SECRET_PROMPT", CoTTrace: "SECRET_COT", AccountState: AccountSnapshot{TotalBalance: 100}, Decisions: []DecisionAction{{Action: "open_long", Symbol: "BTC", Success: true, Price: 99, Quantity: 2, Leverage: 3, Reasoning: "SECRET_REASON"}, {Action: "close_short", Success: false}, {Action: "wait", Success: true}}}
	b, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestChartIndexPersistentRecoveryAndRange(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{1, 3, 7, 10} {
		chartRaw(t, dir, n)
	}
	chartRaw(t, dir, 2)
	os.Rename(filepath.Join(dir, "decision_20260101_000000_cycle2.json"), filepath.Join(dir, "other_cycle2.json"))
	os.Symlink(filepath.Join(dir, "decision_20260101_000000_cycle1.json"), filepath.Join(dir, "decision_20260101_000000_cycle8.json"))
	idx := newChartIndex(dir)
	if _, err := idx.query(10, 0, 0, true); err == nil {
		t.Fatal("unbuilt index must be pending")
	}
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	p, err := idx.query(2, 0, 7, true)
	if err != nil || len(p) != 2 || p[0].CycleNumber != 3 || p[1].CycleNumber != 7 {
		t.Fatalf("range before limit: %+v %v", p, err)
	}
	if len(p[0].Actions) != 1 || p[0].Actions[0].Price != 99 {
		t.Fatalf("actions: %+v", p)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "chart_index", "points.jsonl"))
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "reasoning") {
		t.Fatal("sensitive fields indexed")
	}
	f, _ := os.OpenFile(filepath.Join(dir, "chart_index", "points.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	f.Write(b)
	f.WriteString("{torn")
	f.Close()
	chartRaw(t, dir, 5)
	idx = newChartIndex(dir)
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	p, err = idx.query(20000, 0, 0, false)
	if err != nil || len(p) != 5 {
		t.Fatalf("restart gap/duplicates/tail: %+v %v", p, err)
	}
	for _, v := range p {
		if len(v.Actions) != 0 {
			t.Fatal("actions must be opt in")
		}
	}
	os.WriteFile(filepath.Join(dir, "chart_index", "meta.json"), []byte(`{"version":999}`), 0600)
	idx = newChartIndex(dir)
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	p, _ = idx.query(20000, 0, 0, true)
	if len(p) != 5 {
		t.Fatal("version rebuild lost points")
	}
}

func TestChartIndexDoesNotKeepArchivedCycleAfterFreshStart(t *testing.T) {
	dir := t.TempDir()
	chartRaw(t, dir, 1)
	idx := newChartIndex(dir)
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveLogs(dir); err != nil {
		t.Fatal(err)
	}
	idx = newChartIndex(dir)
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	points, err := idx.query(10, 0, 0, false)
	if err != nil || len(points) != 0 {
		t.Fatalf("archived history returned after fresh start: %+v %v", points, err)
	}
	chartRaw(t, dir, 1)
	idx = newChartIndex(dir)
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	points, err = idx.query(10, 0, 0, false)
	if err != nil || len(points) != 1 {
		t.Fatalf("reused cycle not reindexed: %+v %v", points, err)
	}
}

func TestChartIndexFailedRecoveryCanRetryWithoutLosingSeenSources(t *testing.T) {
	dir := t.TempDir()
	chartRaw(t, dir, 1)
	first := newChartIndex(dir)
	if err := first.rebuild(); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "chart_index", "meta.tmp"), 0700)
	idx := newChartIndex(dir)
	if err := idx.rebuild(); err == nil {
		t.Fatal("expected metadata write failure")
	}
	os.Remove(filepath.Join(dir, "chart_index", "meta.tmp"))
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	p, err := idx.query(2, 0, 0, false)
	if err != nil || len(p) != 1 {
		t.Fatalf("retry lost source: %+v %v", p, err)
	}
	os.Remove(filepath.Join(dir, "chart_index", "meta.json"))
	idx = newChartIndex(dir)
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	p, _ = idx.query(2, 0, 0, false)
	if len(p) != 1 {
		t.Fatal("missing metadata failed to rebuild")
	}
}

func TestChartIndexConcurrentBackfillAndInvalidRepair(t *testing.T) {
	dir := t.TempDir()
	for n := 1; n <= 100; n++ {
		chartRaw(t, dir, n)
	}
	bad := chartRaw(t, dir, 101)
	os.WriteFile(filepath.Join(dir, bad), []byte(`{broken`), 0600)
	idx := newChartIndex(dir)
	done := make(chan error, 1)
	go func() { done <- idx.rebuild() }()
	for n := 102; n <= 110; n++ {
		name := chartRaw(t, dir, n)
		if err := idx.appendRaw(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	p, err := idx.query(20000, 0, 0, true)
	if err != nil || len(p) != 109 {
		t.Fatalf("concurrent gap: %d %v", len(p), err)
	}
	chartRaw(t, dir, 101)
	if err := idx.rebuild(); err != nil {
		t.Fatal(err)
	}
	p, _ = idx.query(20000, 0, 0, true)
	if len(p) != 110 {
		t.Fatalf("invalid raw not recoverable: %d", len(p))
	}
}

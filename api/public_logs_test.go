package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicChartMatchesWebWindowWithoutSampling(t *testing.T) {
	s, source := publicFixture(t)
	source.raw.InitialBalance = 5000 // Account return must not change the curve baseline.
	source.raw.History = nil
	for i := 1; i <= 2500; i++ {
		source.raw.History = append(source.raw.History, publicEquitySample{
			CycleNumber: i, Timestamp: source.raw.AsOf.Add(time.Duration(i-2500) * time.Second),
			Equity:  1234.56 + float64(i)*0.12345,
			Actions: []publicAction{{Symbol: "ETHUSDT", Action: "open_short", Success: true}},
		})
	}
	s.refresh(context.Background())
	for _, tc := range []struct {
		query string
		count int
	}{
		{"", 2000}, {"?limit=1", 1}, {"?limit=1001", 1001}, {"?limit=2500", 2500}, {"?limit=20000", 2500},
	} {
		w := publicRequest(s, "GET", "/api/public/v1/strategies/strategy-a/returns"+tc.query)
		var response publicReturnsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 {
			t.Fatalf("%s: %d %s %v", tc.query, w.Code, w.Body.String(), err)
		}
		if len(response.Points) != tc.count || response.Points[0].ReturnPct != 0 {
			t.Fatalf("wrong window: %s %+v", tc.query, response)
		}
		first := len(source.raw.History) - tc.count
		basis := source.raw.History[first].Equity
		for i, point := range response.Points {
			expected := source.raw.History[first+i]
			// Same calculation as EquityChart.tsx: (equity - first equity) / first equity.
			want := math.Round((expected.Equity-basis)/basis*100*100) / 100
			if point.CycleNumber != expected.CycleNumber || point.ReturnPct != want || len(point.Actions) != 1 {
				t.Fatalf("cycle skipped, action lost, or Web percentage mismatch: %+v want %.2f", point, want)
			}
		}
	}
	// Request-specific rebasing must never mutate the shared cached ratios or points.
	w := publicRequest(s, "GET", "/api/public/v1/strategies/strategy-a/returns?limit=2500")
	var full publicReturnsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &full)
	if full.Points[0].CycleNumber != 1 || full.Points[0].ReturnPct != 0 {
		t.Fatal("request mutated cached window")
	}
}

func publicLogsFixture(t *testing.T) (*PublicServer, string) {
	t.Helper()
	s, _ := publicFixture(t)
	root := t.TempDir()
	dir := filepath.Join(root, "private-trader")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s.entries["strategy-a"].logs = newPublicLogsCache(root, "private-trader")
	for cycle := 1; cycle <= 7; cycle++ {
		name := fmt.Sprintf("decision_20261102_120000_cycle%d.json", cycle)
		raw := fmt.Sprintf(`{
          "cycle_number":%d,"timestamp":"2026-11-02T12:00:00+08:00","success":true,
          "input_prompt":"SECRET_PROMPT 123456","cot_trace":"SECRET_THINKING 123456",
          "decision_json":"SECRET_RAW_JSON","execution_log":["SECRET_EXECUTION"],
          "error_message":"SECRET_ERROR","account_state":{"total_balance":123456},
          "candidate_coins":["BTCUSDT","ETHUSDT"],
          "decisions":[
            {"symbol":"BTCUSDT","action":"wait","success":true,"reasoning":"SECRET_REASON"},
            {"symbol":"ETHUSDT","action":"hold","success":true},
            {"symbol":"ETHUSDT","action":"update_stop_loss","success":false,"error":"SECRET_ERROR amount=123456","quantity":123456,"price":123456},
            {"symbol":"BTCUSDT","action":"partial_close","success":true,"order_id":123456},
            {"symbol":"BTCUSDT","action":"open_long","success":false},
            {"symbol":"BTCUSDT","action":"update_take_profit","success":true}
          ]}`, cycle)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return s, dir
}

func TestPublicLogsReuseOriginalFilesAndExposeOnlyOperations(t *testing.T) {
	s, dir := publicLogsFixture(t)
	// These routes work independently of an unavailable account/curve snapshot.
	for _, route := range []string{"/logs", "/logs/7", "/decisions"} {
		w := publicRequest(s, "GET", "/api/public/v1/strategies/strategy-a"+route)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		for _, forbidden := range []string{"SECRET", "123456", "private-trader", "filename", "reasoning", "cot_trace", "input_prompt", "quantity", "price", "order_id", "error_message", "candidate_coins"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatalf("%s leaked %s: %s", route, forbidden, w.Body.String())
			}
		}
		obj := decodePublicObject(t, w)
		switch route {
		case "/logs":
			assertPublicKeys(t, obj, "slug", "logs", "next_before_cycle")
			items := obj["logs"].([]interface{})
			if len(items) != 7 || items[0].(map[string]interface{})["cycle_number"] != float64(7) {
				t.Fatal("wrong log ordering")
			}
			for _, item := range items {
				assertPublicKeys(t, item.(map[string]interface{}), "cycle_number", "timestamp")
			}
		case "/logs/7":
			assertPublicKeys(t, obj, "slug", "log")
			assertPublicDecision(t, obj["log"].(map[string]interface{}))
		case "/decisions":
			assertPublicKeys(t, obj, "slug", "decisions", "next_before_cycle")
			items := obj["decisions"].([]interface{})
			if len(items) != 5 || obj["next_before_cycle"] != float64(3) {
				t.Fatal("latest decisions must default to five cycles")
			}
			for _, item := range items {
				assertPublicDecision(t, item.(map[string]interface{}))
			}
		}
	}
	// A repeated detail read reuses sanitized memory, not the raw private JSON.
	if err := os.WriteFile(filepath.Join(dir, "decision_20261102_120000_cycle7.json"), []byte(`invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if w := publicRequest(s, "GET", "/api/public/v1/strategies/strategy-a/logs/7"); w.Code != 200 {
		t.Fatal("detail cache was not reused")
	}
	cache := s.entries["strategy-a"].logs
	cache.loadedAt = time.Now().Add(-2 * publicRefreshInterval)
	if w := publicRequest(s, "GET", "/api/public/v1/strategies/strategy-a/logs/7"); w.Code != 503 || strings.Contains(w.Body.String(), "invalid") {
		t.Fatal("expired cache must not hide invalid source or expose decode errors")
	}
}

func assertPublicDecision(t *testing.T, record map[string]interface{}) {
	t.Helper()
	assertPublicKeys(t, record, "cycle_number", "timestamp", "success", "decisions")
	actions := record["decisions"].([]interface{})
	if len(actions) != 6 {
		t.Fatal("recorded operations were dropped")
	}
	for _, action := range actions {
		assertPublicKeys(t, action.(map[string]interface{}), "symbol", "action", "success")
	}
	if actions[0].(map[string]interface{})["action"] != "wait" || actions[2].(map[string]interface{})["success"] != false {
		t.Fatal("wait/failure semantics changed")
	}
}

func TestPublicLogsPaginationValidationAndFilesystemPolicy(t *testing.T) {
	s, dir := publicLogsFixture(t)
	base := "/api/public/v1/strategies/strategy-a"
	w := publicRequest(s, "GET", base+"/logs?limit=2&before_cycle=5")
	obj := decodePublicObject(t, w)
	if w.Code != 200 || obj["next_before_cycle"] != float64(3) || obj["logs"].([]interface{})[0].(map[string]interface{})["cycle_number"] != float64(4) {
		t.Fatalf("bad pagination: %s", w.Body.String())
	}
	for _, route := range []string{"/logs?filename=config.json", "/logs?limit=101", "/decisions?before_cycle=0", "/decisions?limit=1&limit=2", "/logs/01", "/logs/not-a-cycle", "/logs/7?include_actions=true"} {
		if w := publicRequest(s, "GET", base+route); w.Code != 400 {
			t.Errorf("accepted %s: %d", route, w.Code)
		}
	}
	for _, route := range []string{"/logs/999", "/logs/7/extra"} {
		if w := publicRequest(s, "GET", base+route); w.Code != 404 {
			t.Errorf("unknown detail: %s %d", route, w.Code)
		}
	}
	if w := publicRequest(s, "GET", "/api/public/v1/strategies/unpublished/logs"); w.Code != 404 {
		t.Fatal("unpublished logs exposed")
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"cot_trace":"SECRET"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "decision_20261102_130000_cycle8.json")); err != nil {
		t.Fatal(err)
	}
	cache := s.entries["strategy-a"].logs
	cache.loadedAt = time.Time{}
	if w := publicRequest(s, "GET", base+"/logs/8"); w.Code != 404 {
		t.Fatal("symlink log exposed")
	}
	if w := publicRequest(s, "POST", base+"/logs/7"); w.Code != 405 {
		t.Fatal("log route is writable")
	}
}

func TestPublicLogConcurrentReads(t *testing.T) {
	s, _ := publicLogsFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if w := publicRequest(s, "GET", "/api/public/v1/strategies/strategy-a/decisions"); w.Code != 200 {
					t.Errorf("concurrent log read: %d", w.Code)
				}
			}
		}()
	}
	wg.Wait()
}

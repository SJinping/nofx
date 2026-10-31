package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLogHandlersRejectNonDecisionFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := filepath.Join("decision_logs", "test_trader")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"decision_20261031_120000_cycle1.json", "trade_stats.json", "meta.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"cycle_number":1}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	s := &Server{}
	r := gin.New()
	r.GET("/list", s.handleLogList)
	r.GET("/detail", s.handleLogDetail)
	request := func(url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w
	}
	w := request("/list?trader_id=test_trader")
	var entries []LogEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(entries) != 1 || entries[0].Cycle != 1 {
		t.Fatalf("non-decision files exposed: status=%d body=%s", w.Code, w.Body.String())
	}
	for _, url := range []string{
		"/detail?trader_id=test_trader&filename=trade_stats.json",
		"/list?trader_id=..",
		"/detail?trader_id=..&filename=decision_20261031_120000_cycle1.json",
	} {
		if w := request(url); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want400 got%d body=%s", url, w.Code, w.Body.String())
		}
	}
	if w := request("/detail?trader_id=test_trader&filename=decision_20261031_120000_cycle1.json"); w.Code != http.StatusOK {
		t.Fatalf("valid log rejected: %d %s", w.Code, w.Body.String())
	}
	if w := request("/detail?trader_id=test_trader&filename=decision_20261031_120000_cycle2.json"); w.Code != http.StatusNotFound {
		t.Fatalf("missing log status=%d", w.Code)
	}
}

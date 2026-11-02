package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"nofx/config"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type fakePublicSource struct {
	mu    sync.Mutex
	raw   publicRawSnapshot
	err   error
	calls int
}

func (f *fakePublicSource) Read() (publicRawSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.raw, f.err
}

func publicFixture(t *testing.T) (*PublicServer, *fakePublicSource) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	f := &fakePublicSource{raw: publicRawSnapshot{
		AsOf: now, InitialBalance: 10000, Equity: 11234,
		History: []publicEquitySample{
			{Timestamp: now.Add(-3 * time.Hour), Equity: 10000},
			{Timestamp: now.Add(-2 * time.Hour), Equity: 12000},
			{Timestamp: now.Add(-time.Hour), Equity: 9000},
			{Timestamp: now, Equity: 11234},
		},
		Positions: []publicPosition{{Symbol: "BTCUSDT", Side: "long", PriceChangePct: 2.34}},
	}}
	cfg := config.PublicAPIConfig{Enabled: true, Port: 8082, Strategies: []config.PublicStrategyConfig{
		{Slug: "strategy-a", Name: "Strategy A", TraderID: "PRIVATE_TRADER_ID"},
	}}
	s := newPublicServer(cfg, map[string]publicDataSource{"strategy-a": f})
	s.now = func() time.Time { return now }
	return s, f
}

func publicRequest(s *PublicServer, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func decodePublicObject(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", w.Body.String(), err)
	}
	return result
}

func assertPublicKeys(t *testing.T, obj map[string]interface{}, want ...string) {
	t.Helper()
	got := make([]string, 0, len(obj))
	for key := range obj {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public schema changed: got %v want %v", got, want)
	}
}

var publicSummaryKeys = []string{
	"slug", "name", "state", "stale", "as_of", "return_pct", "sampled_max_drawdown_pct",
	"history_from", "history_to", "history_limit_reached",
}

func TestPublicRoutesUseStrictSchemasAndNeverFetchOnRequest(t *testing.T) {
	s, f := publicFixture(t)
	s.refresh(context.Background())
	base := "/api/public/v1/strategies"
	for _, route := range []string{base, base + "/strategy-a/summary", base + "/strategy-a/returns", base + "/strategy-a/positions"} {
		w := publicRequest(s, http.MethodGet, route)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body.String())
		}
		for _, secret := range []string{"PRIVATE_TRADER_ID", "10000", "11234", "12000", "9000", "quantity", "order_id", "source_file", "reasoning", "input_prompt", "cot_trace"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("%s leaked %q: %s", route, secret, w.Body.String())
			}
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unsafe public headers: %v", w.Header())
		}
		obj := decodePublicObject(t, w)
		switch route {
		case base:
			assertPublicKeys(t, obj, "strategies")
			items := obj["strategies"].([]interface{})
			if len(items) != 1 {
				t.Fatal("unexpected published strategies")
			}
			assertPublicKeys(t, items[0].(map[string]interface{}), publicSummaryKeys...)
		case base + "/strategy-a/summary":
			assertPublicKeys(t, obj, publicSummaryKeys...)
			if obj["return_pct"] != 12.34 || obj["sampled_max_drawdown_pct"] != float64(25) {
				t.Fatalf("wrong normalized metrics: %v", obj)
			}
		case base + "/strategy-a/returns":
			assertPublicKeys(t, obj, append(append([]string{}, publicSummaryKeys...), "points")...)
			for _, point := range obj["points"].([]interface{}) {
				assertPublicKeys(t, point.(map[string]interface{}), "timestamp", "return_pct")
			}
		case base + "/strategy-a/positions":
			assertPublicKeys(t, obj, "slug", "state", "stale", "as_of", "positions")
			for _, position := range obj["positions"].([]interface{}) {
				assertPublicKeys(t, position.(map[string]interface{}), "symbol", "side", "price_change_pct")
			}
		}
	}
	if f.calls != 1 {
		t.Fatalf("HTTP requests triggered %d source reads", f.calls)
	}
}

func TestPublicListenerHasNoManagementRoutesOrWriteMethods(t *testing.T) {
	s, f := publicFixture(t)
	for _, path := range []string{
		"/api/account", "/api/positions", "/api/competition", "/api/traders", "/api/status", "/api/statistics",
		"/api/decisions", "/api/decisions/latest", "/api/equity-history", "/api/performance",
		"/api/exchange/orders", "/api/traded-symbols", "/api/logs/detail", "/api/config",
		"/api/public/v1/strategies/PRIVATE_TRADER_ID/summary", "/api/public/v1/strategies/unpublished/returns",
	} {
		if w := publicRequest(s, http.MethodGet, path); w.Code != http.StatusNotFound {
			t.Errorf("private/unknown route %s returned %d", path, w.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodHead} {
		for _, path := range []string{"/api/close-all-positions", "/api/system/pause", "/api/config", "/api/public/v1/strategies", "/api/public/v1/strategies/strategy-a/summary"} {
			if w := publicRequest(s, method, path); w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
				t.Errorf("write method %s %s: %d", method, path, w.Code)
			}
		}
	}
	if f.calls != 0 {
		t.Fatal("routing touched private data")
	}
}

func TestPublicUnavailableAndStaleNeverPretendToBeZeroReturns(t *testing.T) {
	s, f := publicFixture(t)
	path := "/api/public/v1/strategies/strategy-a/summary"
	w := publicRequest(s, http.MethodGet, path)
	obj := decodePublicObject(t, w)
	if w.Code != http.StatusServiceUnavailable || obj["return_pct"] != nil || obj["as_of"] != nil || obj["state"] != "unavailable" {
		t.Fatalf("missing data must be unavailable: %d %v", w.Code, obj)
	}
	f.err = errors.New("SECRET_KEY amount=5000 private upstream details")
	s.refresh(context.Background())
	w = publicRequest(s, http.MethodGet, path)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal("upstream failure exposed or treated as ready")
	}
	f.err = nil
	s.refresh(context.Background())
	f.err = errors.New("upstream unavailable")
	s.refresh(context.Background())
	w = publicRequest(s, http.MethodGet, path)
	obj = decodePublicObject(t, w)
	if w.Code != http.StatusOK || obj["state"] != "stale" || obj["stale"] != true || obj["return_pct"] != 12.34 {
		t.Fatalf("last good snapshot not retained/marked stale: %v", obj)
	}
	f.err = nil
	s.refresh(context.Background())
	if obj = decodePublicObject(t, publicRequest(s, http.MethodGet, path)); obj["stale"] != false {
		t.Fatal("successful refresh did not recover")
	}
	s.now = func() time.Time { return f.raw.AsOf.Add(publicStaleAfter + time.Second) }
	if obj = decodePublicObject(t, publicRequest(s, http.MethodGet, path)); obj["stale"] != true {
		t.Fatal("aged snapshot not marked stale")
	}
}

func TestPublicQueryBoundsAndInternalOptionsAreRejected(t *testing.T) {
	s, _ := publicFixture(t)
	s.refresh(context.Background())
	base := "/api/public/v1/strategies/strategy-a/returns"
	for _, query := range []string{
		"limit=0", "limit=1", "limit=-1", "limit=1001", "limit=all", "limit=", "limit=2&limit=3",
		"trader_id=PRIVATE_TRADER_ID", "include_actions=true", "from_cycle=1", "filename=../config.json", "foo=%zz", "limit=2;foo=1",
	} {
		if w := publicRequest(s, http.MethodGet, base+"?"+query); w.Code != http.StatusBadRequest {
			t.Errorf("accepted unsupported query %s: %d", query, w.Code)
		}
	}
	w := publicRequest(s, http.MethodGet, base+"?limit=2")
	var response publicReturnsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(response.Points) != 2 || response.Points[0].ReturnPct != 0 || response.Points[1].ReturnPct != 12.34 || *response.SampledMaxDrawdownPct != 25 {
		t.Fatalf("sampling lost window or changed drawdown: %+v", response)
	}
	for _, route := range []string{"", "/strategy-a/summary", "/strategy-a/positions"} {
		w := publicRequest(s, http.MethodGet, "/api/public/v1/strategies"+route+"?limit=2")
		if w.Code != http.StatusBadRequest {
			t.Fatal("list/summary/positions silently accepted unsupported options")
		}
	}
}

func TestPublicNormalizationIsScaleInvariantAndValidatesInputs(t *testing.T) {
	s, f := publicFixture(t)
	cfg := s.entries["strategy-a"].config
	a, err := sanitizePublicSnapshot(cfg, f.raw)
	if err != nil {
		t.Fatal(err)
	}
	scaled := f.raw
	scaled.Equity *= 17
	scaled.InitialBalance *= 17
	scaled.History = append([]publicEquitySample{}, f.raw.History...)
	for i := range scaled.History {
		scaled.History[i].Equity *= 17
	}
	b, err := sanitizePublicSnapshot(cfg, scaled)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("percentage output leaked capital scale: %+v %+v %v", a, b, err)
	}
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		raw := f.raw
		raw.InitialBalance = bad
		if _, err := sanitizePublicSnapshot(cfg, raw); err == nil {
			t.Errorf("invalid basis accepted: %v", bad)
		}
	}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		raw := f.raw
		raw.Equity = bad
		if _, err := sanitizePublicSnapshot(cfg, raw); err == nil {
			t.Errorf("invalid equity accepted: %v", bad)
		}
	}
	raw := f.raw
	raw.Equity = 0
	if zero, err := sanitizePublicSnapshot(cfg, raw); err != nil || *zero.Summary.ReturnPct != -100 {
		t.Fatal("zero equity is a real -100% return, not missing data")
	}
	raw.History, raw.Positions = nil, nil
	empty, err := sanitizePublicSnapshot(cfg, raw)
	if err != nil || empty.Summary.SampledMaxDrawdownPct != nil || empty.Points == nil || empty.Positions == nil {
		t.Fatal("missing history must use null drawdown and empty arrays")
	}
	raw = f.raw
	raw.History = []publicEquitySample{{Timestamp: raw.AsOf, Equity: math.NaN()}}
	if _, err := sanitizePublicSnapshot(cfg, raw); err == nil {
		t.Fatal("invalid history accepted")
	}
	raw = f.raw
	raw.Positions = []publicPosition{{Symbol: "BTCUSDT", Side: "long", PriceChangePct: math.Inf(1)}}
	if _, err := sanitizePublicSnapshot(cfg, raw); err == nil {
		t.Fatal("invalid position accepted")
	}
}

func TestPublicStrategiesAreIsolatedAndCacheIsConcurrent(t *testing.T) {
	s, f := publicFixture(t)
	other := &fakePublicSource{raw: f.raw}
	other.raw.Equity = 8000
	s.order = append(s.order, "strategy-b")
	s.entries["strategy-b"] = &publicEntry{config: config.PublicStrategyConfig{Slug: "strategy-b", Name: "B", TraderID: "private-b"}, source: other}
	s.refresh(context.Background())
	obj := decodePublicObject(t, publicRequest(s, http.MethodGet, "/api/public/v1/strategies/strategy-b/summary"))
	if obj["return_pct"] != float64(-20) || obj["slug"] != "strategy-b" {
		t.Fatal("strategy cache mixed data")
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if i == 0 {
					s.refresh(context.Background())
				} else if w := publicRequest(s, http.MethodGet, fmt.Sprintf("/api/public/v1/strategies/strategy-%s/returns?limit=2", []string{"a", "b"}[i%2])); w.Code != http.StatusOK {
					t.Errorf("concurrent read failed: %d", w.Code)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestManagementConstructorDefaultsToLoopback(t *testing.T) {
	if s := NewServer(nil, 8080); s.listenHost != "127.0.0.1" {
		t.Fatal("management API must not default to wildcard binding")
	}
}

type panickingPublicSource struct{}

func (panickingPublicSource) Read() (publicRawSnapshot, error) {
	panic("SECRET_UPSTREAM_PAYLOAD")
}

func TestPublicCollectorContainsUpstreamPanics(t *testing.T) {
	s, _ := publicFixture(t)
	s.entries["strategy-a"].source = panickingPublicSource{}
	s.refresh(context.Background())
	w := publicRequest(s, http.MethodGet, "/api/public/v1/strategies/strategy-a/summary")
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("upstream panic escaped collection: %d %s", w.Code, w.Body.String())
	}
}

func TestPublicStartRejectsOccupiedPortBeforeCollecting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s, source := publicFixture(t)
	s.port = listener.Addr().(*net.TCPAddr).Port
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("occupied port must fail synchronously")
	}
	if source.calls != 0 {
		t.Fatal("collector started despite listener failure")
	}
}

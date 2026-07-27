package market

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClosedKlinesAtExcludesFormingBar(t *testing.T) {
	klines := []Kline{
		{OpenTime: 1, CloseTime: 100, Close: 10},
		{OpenTime: 101, CloseTime: 200, Close: 11},
		{OpenTime: 201, CloseTime: 300, Close: 12},
	}

	got := closedKlinesAt(klines, 250)
	if len(got) != 2 {
		t.Fatalf("expected 2 closed bars, got %d", len(got))
	}
	if got[1].Close != 11 {
		t.Fatalf("expected latest closed price 11, got %.2f", got[1].Close)
	}
}

func TestClosedKlinesAtIncludesBarAtCloseTime(t *testing.T) {
	klines := []Kline{
		{OpenTime: 1, CloseTime: 100, Close: 10},
		{OpenTime: 101, CloseTime: 200, Close: 11},
	}

	got := closedKlinesAt(klines, 200)
	if len(got) != 2 {
		t.Fatalf("expected bar to be closed at CloseTime, got %d bars", len(got))
	}
}

func TestClosedKlinesAtDoesNotMutateInput(t *testing.T) {
	klines := []Kline{
		{OpenTime: 1, CloseTime: 100, Close: 10},
		{OpenTime: 101, CloseTime: 300, Close: 12},
	}

	got := closedKlinesAt(klines, 200)
	got[0].Close = 99
	if klines[0].Close != 10 {
		t.Fatalf("input was mutated: %.2f", klines[0].Close)
	}
}

func TestGetRealtimePriceUsesTickerEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("symbol"); got != "BTCUSDT" {
			t.Fatalf("unexpected symbol query: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","price":"100.125"}`))
	}))
	defer server.Close()

	oldBaseURL := fapiBaseURL
	oldClient := httpClient
	fapiBaseURL = server.URL
	httpClient = server.Client()
	defer func() {
		fapiBaseURL = oldBaseURL
		httpClient = oldClient
	}()

	price, err := GetRealtimePrice("BTCUSDT")
	if err != nil {
		t.Fatalf("GetRealtimePrice returned error: %v", err)
	}
	if price != 100.125 {
		t.Fatalf("expected 100.125, got %.6f", price)
	}
}

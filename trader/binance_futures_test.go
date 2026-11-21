package trader

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/adshao/go-binance/v2/futures"
)

func TestFormatPriceToTickSize(t *testing.T) {
	tests := []struct {
		name      string
		price     float64
		tickSize  float64
		precision int
		want      string
	}{
		{name: "eth two decimals", price: 1928.9135, tickSize: 0.01, precision: 2, want: "1928.91"},
		{name: "integer tick", price: 65321.49, tickSize: 0.1, precision: 1, want: "65321.5"},
		{name: "non decimal-power tick", price: 10.37, tickSize: 0.25, precision: 2, want: "10.25"},
		{name: "zero precision", price: 123.6, tickSize: 1, precision: 0, want: "124"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatPriceToTickSize(tt.price, tt.tickSize, tt.precision)
			if err != nil {
				t.Fatalf("formatPriceToTickSize returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("formatPriceToTickSize() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatPriceToTickSizeRejectsInvalidInputs(t *testing.T) {
	if _, err := formatPriceToTickSize(0, 0.01, 2); err == nil {
		t.Fatal("expected error for non-positive price")
	}
	if _, err := formatPriceToTickSize(1, 0, 2); err == nil {
		t.Fatal("expected error for non-positive tickSize")
	}
}

func TestBinanceOpeningPriceCheckRunsAfterPreparation(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject=%v", reject), func(t *testing.T) {
			var steps []string
			var mu sync.Mutex
			record := func(step string) {
				mu.Lock()
				defer mu.Unlock()
				steps = append(steps, step)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/fapi/v1/allOpenOrders":
					fmt.Fprint(w, `{}`)
				case "/fapi/v1/openAlgoOrders", "/fapi/v2/positionRisk":
					fmt.Fprint(w, `[]`)
				case "/fapi/v1/marginType":
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"code":-4046,"msg":"No need to change margin type."}`)
				case "/fapi/v1/leverage":
					record("leverage")
					fmt.Fprint(w, `{"symbol":"ETHUSDT","leverage":3}`)
				case "/fapi/v1/exchangeInfo":
					record("precision")
					fmt.Fprint(w, `{"symbols":[{"symbol":"ETHUSDT","filters":[{"filterType":"LOT_SIZE","stepSize":"0.001"}]}]}`)
				case "/fapi/v2/ticker/price":
					record("quote")
					fmt.Fprint(w, `{"symbol":"ETHUSDT","price":"1600"}`)
				case "/fapi/v1/order":
					record("order")
					if r.FormValue("side") != "SELL" || r.FormValue("positionSide") != "SHORT" || r.FormValue("quantity") != "0.031" {
						t.Errorf("unexpected order parameters: %v", r.Form)
					}
					fmt.Fprint(w, `{"orderId":123,"status":"FILLED","symbol":"ETHUSDT"}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			client := futures.NewClient("test", "test")
			client.BaseURL = server.URL
			trader := &FuturesTrader{client: client}
			blocked := errors.New("setup invalidated")
			_, err := trader.openPositionWithPriceCheck("ETHUSDT", "short", 0, 3, func(price float64) (float64, error) {
				record("check")
				if reject {
					return 0, blocked
				}
				return 50 / price, nil
			})

			want := []string{"leverage", "precision", "quote", "check"}
			if reject {
				if !errors.Is(err, blocked) {
					t.Fatalf("expected the price check to block the order, got %v", err)
				}
			} else {
				want = append(want, "order")
				if err != nil {
					t.Fatalf("expected the order to pass the check, got %v", err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(steps, want) {
				t.Errorf("execution steps %v, want %v", steps, want)
			}
		})
	}
}

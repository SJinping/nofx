package api

import (
	"errors"
	"testing"
)

func resetExchangeInvalidSymbolCacheForTest() {
	exchangeInvalidSymbolCache.Lock()
	defer exchangeInvalidSymbolCache.Unlock()
	exchangeInvalidSymbolCache.symbols = make(map[string]struct{})
}

func TestExchangeInvalidSymbolCacheNormalizesSymbol(t *testing.T) {
	resetExchangeInvalidSymbolCacheForTest()
	markInvalidExchangeSymbol(" ondusdt ")

	if !isCachedInvalidExchangeSymbol("ONDUSDT") {
		t.Fatalf("expected uppercase symbol to be cached")
	}
	if !isCachedInvalidExchangeSymbol("ondusdt") {
		t.Fatalf("expected lowercase symbol lookup to be normalized")
	}
	if isCachedInvalidExchangeSymbol("ONDOUSDT") {
		t.Fatalf("expected different valid-looking symbol not to be cached")
	}
}

func TestIsExchangeInvalidSymbolError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "binance invalid symbol",
			err:  errors.New("<APIError> code=-1121, msg=Invalid symbol."),
			want: true,
		},
		{
			name: "timestamp error",
			err:  errors.New("<APIError> code=-1021, msg=Timestamp for this request is outside of the recvWindow."),
			want: false,
		},
		{
			name: "generic invalid symbol without binance code",
			err:  errors.New("invalid symbol"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExchangeInvalidSymbolError(tc.err); got != tc.want {
				t.Fatalf("isExchangeInvalidSymbolError() = %v, want %v", got, tc.want)
			}
		})
	}
}

package pool

import "testing"

func TestNormalizeSymbolDoesNotCreateSyntheticUSDTForNonUSDTQuotes(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantNorm  string
		wantValid bool
	}{
		{name: "bare symbol gets USDT suffix", raw: "AAVE", wantNorm: "AAVEUSDT", wantValid: true},
		{name: "USDT contract remains valid", raw: "aaveusdt", wantNorm: "AAVEUSDT", wantValid: true},
		{name: "USDC contract is not converted to synthetic USDCUSDT", raw: "AAVEUSDC", wantNorm: "AAVEUSDC", wantValid: false},
		{name: "USD contract is filtered instead of converted", raw: "BTCUSD", wantNorm: "BTCUSD", wantValid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeSymbol(tt.raw)
			if got != tt.wantNorm {
				t.Fatalf("normalizeSymbol(%q) = %q, want %q", tt.raw, got, tt.wantNorm)
			}
			if valid := isValidSymbol(got); valid != tt.wantValid {
				t.Fatalf("isValidSymbol(%q) = %v, want %v", got, valid, tt.wantValid)
			}
		})
	}
}

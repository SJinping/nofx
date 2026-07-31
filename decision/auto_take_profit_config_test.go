package decision

import "testing"

func TestAutoTakeProfitConfigForSymbolUsesMajorForBTCETH(t *testing.T) {
	cfg := AutoTakeProfitConfig{
		Stage0Threshold:    10,
		Stage0ClosePct:     35,
		Stage1Threshold:    15,
		Stage1ClosePct:     35,
		FullCloseThreshold: 22,
		CooldownMinutes:    30,
		Major: &AutoTakeProfitConfig{
			Stage0Threshold:    5,
			Stage1Threshold:    9,
			FullCloseThreshold: 15,
		},
	}

	for _, symbol := range []string{"BTCUSDT", "ETHUSDT", "btcusdt"} {
		got := autoTakeProfitConfigForSymbol(symbol, cfg)
		if got.Stage0Threshold != 5 || got.Stage1Threshold != 9 || got.FullCloseThreshold != 15 {
			t.Fatalf("%s did not use major thresholds: %+v", symbol, got)
		}
		if got.Stage0ClosePct != 35 || got.Stage1ClosePct != 35 || got.CooldownMinutes != 30 {
			t.Fatalf("%s did not inherit unspecified top-level fields: %+v", symbol, got)
		}
		if got.Major != nil {
			t.Fatalf("selected per-symbol config should not carry nested Major: %+v", got.Major)
		}
	}
}

func TestAutoTakeProfitConfigForSymbolKeepsDefaultForAlts(t *testing.T) {
	cfg := AutoTakeProfitConfig{
		Stage0Threshold:    10,
		Stage0ClosePct:     35,
		Stage1Threshold:    15,
		Stage1ClosePct:     35,
		FullCloseThreshold: 22,
		CooldownMinutes:    30,
		Major: &AutoTakeProfitConfig{
			Stage0Threshold:    5,
			Stage1Threshold:    9,
			FullCloseThreshold: 15,
		},
	}

	got := autoTakeProfitConfigForSymbol("SOLUSDT", cfg)
	if got.Stage0Threshold != 10 || got.Stage1Threshold != 15 || got.FullCloseThreshold != 22 {
		t.Fatalf("alt symbol should keep top-level thresholds: %+v", got)
	}
}

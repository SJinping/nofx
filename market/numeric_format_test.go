package market

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestFormatPriceRoundTripsAcrossPriceScales(t *testing.T) {
	for _, price := range []float64{0, 0.004596, 0.05889, 0.000000004596, 100000.00000001, math.SmallestNonzeroFloat64} {
		text := FormatPrice(price)
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil || parsed != price || strings.ContainsAny(text, "eE") {
			t.Errorf("price %g lost precision: %q, parsed=%g, err=%v", price, text, parsed, err)
		}
	}
	if got := FormatPrice(math.Copysign(0, -1)); got != "0" {
		t.Fatalf("negative zero must be normalized, got %q", got)
	}
}

func TestFormatIndicatorPreservesSmallMagnitudeAndSign(t *testing.T) {
	for _, value := range []float64{-0.000026572904996348574, 0.000023383190061185797, -1.23456789e-15, 1.23456789e8} {
		text := FormatIndicator(value)
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil || parsed == 0 || math.Signbit(parsed) != math.Signbit(value) || math.Abs((parsed-value)/value) > 5e-8 {
			t.Errorf("indicator %g lost magnitude/sign: %q", value, text)
		}
	}
}

func TestMarketFormatPreservesRecordedSmallCoinSignals(t *testing.T) {
	// Values from cycle10634: the old formatter made PENGU's price zero and
	// turned DOGE's ten different prices into a constant 0.059 series.
	pengu := &Data{
		Symbol: "PENGUUSDT", CurrentPrice: 0.004596,
		CurrentEMA20: 0.004652917009700261, CurrentMACD: -0.000026572904996348574, CurrentRSI7: 30.052,
		IntradaySeries: &IntradayData{
			MidPrices: []float64{0.004655, 0.004674, 0.004675, 0.004649, 0.004649, 0.004657, 0.004643, 0.004622, 0.004577, 0.004596},
			Opens:     []float64{0.004655}, Highs: []float64{0.004675}, Lows: []float64{0.004577}, Closes: []float64{0.004596},
			EMA20Values: []float64{0.004685646818553804, 0.004652917009700261},
			MACDValues:  []float64{0.000010605162314672456, -0.000026572904996348574},
			RSI7Values:  []float64{30.052},
		},
		LongerTermContext: &LongerTermData{EMA20: 0.0048, EMA50: 0.0051, ATR14: 0.000173010199151355, RecentLow: 0.004567, RecentHigh: 0.005899},
		MidTermContext:    &MidTermData{EMA20: 0.0048, EMA50: 0.0051, ATR14: 0.000023383190061185797, RecentLow: 0.004567, RecentHigh: 0.005899},
		NofxHeatmap: &NofxHeatmap{
			LargeBids: []NofxHeatmapOrder{{Price: 0.004567, Volume: 1000}},
			LargeAsks: []NofxHeatmapOrder{{Price: 0.005899, Volume: 1000}},
		},
	}
	before, _ := json.Marshal(pengu)
	prompt := Format(pengu)
	for _, want := range []string{
		"current_price = 0.004596,", "current_ema20 = 0.004652917,", "current_macd = -2.6572905e-05,",
		"Mid prices: [0.004655, 0.004674, 0.004675, 0.004649, 0.004649, 0.004657, 0.004643, 0.004622, 0.004577, 0.004596]",
		"Open: [0.004655]", "High: [0.004675]", "Low: [0.004577]", "Close: [0.004596]",
		"EMA indicators (20‑period): [0.0046856468, 0.004652917]",
		"MACD indicators: [1.0605162e-05, -2.6572905e-05]", "RSI indicators (7‑Period): [30.052]",
		"14‑Period ATR: 0.0001730102", "14‑Period ATR: 2.338319e-05",
		"Recent High (4h): 0.005899 | Recent Low (4h): 0.004567",
		"Recent High (1h): 0.005899 | Recent Low (1h): 0.004567",
		"Support walls: 0.004567(1000U)", "Resistance walls: 0.005899(1000U)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("small-coin prompt missing %q", want)
		}
	}
	after, _ := json.Marshal(pengu)
	if string(before) != string(after) {
		t.Fatal("formatting changed the underlying market data")
	}
	doge := &Data{Symbol: "DOGEUSDT", CurrentPrice: 0.05889, IntradaySeries: &IntradayData{
		MidPrices: []float64{0.05944, 0.05936, 0.05941, 0.05924, 0.05927, 0.05935, 0.05926, 0.05901, 0.05887, 0.05889},
	}}
	dogePrompt := Format(doge)
	if !strings.Contains(dogePrompt, "current_price = 0.05889,") || !strings.Contains(dogePrompt, "Mid prices: [0.05944, 0.05936, 0.05941, 0.05924, 0.05927, 0.05935, 0.05926, 0.05901, 0.05887, 0.05889]") {
		t.Fatal("recorded DOGE price movement was lost")
	}
}

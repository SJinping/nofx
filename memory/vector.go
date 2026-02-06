package memory

import (
	"math"
	"nofx/market"
	"strings"
)

// BuildSignalVector creates a lightweight numeric representation of a coin's current signal.
// The vector is intentionally small and stable to keep storage and retrieval cheap.
//
// Vector layout (len=10):
// 0 price_change_1h (scaled)
// 1 price_change_4h (scaled)
// 2 current_rsi7 (0..1)
// 3 current_macd (tanh)
// 4 price_vs_ema20 (pct, tanh)
// 5 normalized_volatility (clamped)
// 6 funding_rate (tanh)
// 7 oi_ratio (latest/avg - 1, tanh)
// 8 market_structure (-1/0/1)
// 9 symbol_is_major (1 for BTC/ETH else 0)
func BuildSignalVector(symbol string, md *market.Data) []float64 {
	if md == nil {
		return nil
	}

	price := md.CurrentPrice
	ema := md.CurrentEMA20
	priceVsEMA := 0.0
	if price > 0 {
		priceVsEMA = (price - ema) / price // ~pct
	}

	oiRatio := 0.0
	if md.OpenInterest != nil && md.OpenInterest.Average > 0 {
		oiRatio = (md.OpenInterest.Latest / md.OpenInterest.Average) - 1.0
	}

	structure := 0.0
	if md.LongerTermContext != nil {
		switch strings.ToLower(strings.TrimSpace(md.LongerTermContext.MarketStructure)) {
		case "uptrend":
			structure = 1
		case "downtrend":
			structure = -1
		default:
			structure = 0
		}
	}

	isMajor := 0.0
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "BTCUSDT" || s == "ETHUSDT" {
		isMajor = 1
	}

	// scaling helpers
	scalePct := func(v float64) float64 {
		// map typical +/-20% into roughly [-1,1] via tanh
		return math.Tanh(v / 10.0)
	}
	scaleRSI := func(v float64) float64 {
		if v <= 0 {
			return 0
		}
		if v >= 100 {
			return 1
		}
		return v / 100.0
	}
	scaleTanh := func(v float64) float64 { return math.Tanh(v) }
	clamp01 := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}

	vol := md.VolatilityPct
	// volatilityPct is ATR14/price. Clamp to [0, 0.05] and normalize to [0,1].
	volNorm := 0.0
	if vol > 0 {
		volNorm = clamp01(vol / 0.05)
	}

	return []float64{
		scalePct(md.PriceChange1h),
		scalePct(md.PriceChange4h),
		scaleRSI(md.CurrentRSI7),
		scaleTanh(md.CurrentMACD),
		scaleTanh(priceVsEMA * 10.0),
		volNorm,
		scaleTanh(md.FundingRate * 100.0),
		scaleTanh(oiRatio),
		structure,
		isMajor,
	}
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	dot := 0.0
	na := 0.0
	nb := 0.0
	for i := 0; i < len(a); i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na <= 0 || nb <= 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}


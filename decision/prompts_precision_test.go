package decision

import (
	"nofx/logger"
	"nofx/market"
	"strings"
	"testing"
)

func tinyPricePromptContext() *Context {
	data := &market.Data{
		Symbol: "PENGUUSDT", CurrentPrice: 0.000004596, CurrentEMA20: 0.0000046529170097,
		CurrentMACD: -0.000000026572904996, IntradayATR14: 0.000000023383190061,
		VolatilityPct: 0.00000001234567,
		IntradaySeries: &market.IntradayData{
			MidPrices:   []float64{0.000004655, 0.000004674, 0.000004577, 0.000004596},
			Closes:      []float64{0.000004655, 0.000004674, 0.000004577, 0.000004596},
			EMA20Values: []float64{0.0000046856, 0.0000046529}, MACDValues: []float64{-0.00000002, -0.00000002657},
		},
		LongerTermContext: &market.LongerTermData{Support: 0.000004567, Resistance: 0.000005899, ATR14: 0.0000001730102},
	}
	return &Context{
		Account: AccountInfo{TotalEquity: 1000, AvailableBalance: 900, PositionCount: 1}, ScanIntervalMin: 10,
		MarketDataMap:  map[string]*market.Data{"PENGUUSDT": data},
		CandidateCoins: []CandidateCoin{{Symbol: "PENGUUSDT"}},
		Positions: []PositionInfo{{Symbol: "PENGUUSDT", Side: "short", Quantity: 100000,
			EntryPrice: 0.000004723, MarkPrice: 0.000004596, LiquidationPrice: 0.000006123,
			EntryStopLoss: 0.000005966, EntryTakeProfit: 0.00000355, EntryReasoning: "Original entry thesis", EntryConfidence: 80}},
		AutoState: &AutoDecisionState{TP: map[string]*AutoTPState{"PENGUUSDT_short": {
			InitialStop: 0.000005966, CurrentStop: 0.0000049, BestPrice: 0.000004577,
		}}},
		ShortTermWatchlist: []ShortTermWatchItem{{Symbol: "PENGUUSDT", TriggerPrice: 0.000004567,
			InvalidationPrice: 0.000005899, SuggestedStopLoss: 0.000005966, SuggestedTakeProfit: 0.00000355}},
	}
}

func TestAllStrategiesPreserveTinyPricesInFinalUserPrompt(t *testing.T) {
	for _, strategy := range []PromptStrategy{StrategyA{}, StrategyB{}, StrategyV{}} {
		t.Run(strategy.Name(), func(t *testing.T) {
			ctx := tinyPricePromptContext()
			prompt := strategy.BuildUserPrompt(ctx)
			for _, want := range []string{
				"入场价0.000004723 当前价0.000004596", "强平价0.000006123",
				"止损 0.000005966", "目标 0.00000355", "current_price = 0.000004596",
				"4h_support_resistance = [0.000004567, 0.000005899]",
				"intraday_atr14 (3m) = 2.338319e-08", "normalized_volatility (ATR14/price) = 1.234567e-08",
				"Mid prices: [0.000004655, 0.000004674, 0.000004577, 0.000004596]",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("final user prompt missing %q", want)
				}
			}
			if ctx.MarketDataMap["PENGUUSDT"].CurrentPrice != 0.000004596 || ctx.Positions[0].MarkPrice != 0.000004596 {
				t.Fatal("prompt formatting mutated execution prices")
			}
		})
	}
}

func TestRiskWatchlistAndTradeHistoryKeepSmallPriceLevels(t *testing.T) {
	ctx := tinyPricePromptContext()
	if risk := formatPositionRiskState(ctx, ctx.Positions[0]); !strings.Contains(risk, "protected_stop=0.0000049（") {
		t.Fatalf("protected stop lost precision: %s", risk)
	}
	watch := formatShortTermWatchlist(ctx)
	if !strings.Contains(watch, "trigger=0.000004567 invalidation=0.000005899 suggested_sl=0.000005966 suggested_tp=0.00000355") {
		t.Fatalf("watchlist prices lost precision: %s", watch)
	}
	history := formatRecentTrades([]logger.TradeOutcome{{Symbol: "PENGUUSDT", Side: "short", OpenPrice: 0.000004723, ClosePrice: 0.000004596}})
	if !strings.Contains(history, "开0.000004723→平0.000004596") {
		t.Fatalf("trade history prices lost precision: %s", history)
	}
	for _, detailed := range []bool{false, true} {
		data := ctx.MarketDataMap["PENGUUSDT"]
		snapshot := formatShortTermMarketData(data, 10, detailed)
		for _, want := range []string{"current_price = 0.000004596", "range=[0.000004577, 0.000004674]", "intraday_atr14=2.338319e-08"} {
			if !strings.Contains(snapshot, want) {
				t.Errorf("StrategyV detailed=%t missing %q", detailed, want)
			}
		}
	}
}

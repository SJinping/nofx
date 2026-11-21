package decision

import (
	"strings"
	"testing"

	"nofx/market"
)

func testAdaptiveContext() *Context {
	return &Context{
		Account: AccountInfo{TotalEquity: 1000, AvailableBalance: 1000},
		MarketDataMap: map[string]*market.Data{
			"BTCUSDT": {Symbol: "BTCUSDT", CurrentPrice: 100, IntradayATR14: 1},
		},
		BTCETHLeverage:  5,
		AltcoinLeverage: 5,
		StopLossDistance: StopLossDistanceConfig{
			MajorMinPct:  0.002,
			MajorATRMult: 1.0,
			MajorVolMult: 0.0,
			AltMinPct:    0.004,
			AltATRMult:   1.0,
			AltVolMult:   0.0,
		},
		AssumedTakerFeeRate: 0.0004,
		AssumedSlippageRate: 0.0005,
	}
}

func TestValidateDecisionAdjustsTooCloseLongStopAndShrinksPosition(t *testing.T) {
	d := Decision{Symbol: "BTCUSDT", Action: ActionOpenLong, Leverage: 3, PositionSizeUSD: 200, StopLoss: 99.5, TakeProfit: 104, Reasoning: "test"}
	if err := coreValidateDecision(&d, testAdaptiveContext()); err != nil {
		t.Fatalf("coreValidateDecision returned error: %v", err)
	}
	if d.StopLoss != 99 {
		t.Fatalf("expected adjusted stop loss 99, got %.8f", d.StopLoss)
	}
	if d.PositionSizeUSD != 100 {
		t.Fatalf("expected adjusted position size 100, got %.8f", d.PositionSizeUSD)
	}
	if !strings.Contains(d.Reasoning, "止损距离自适应调整") {
		t.Fatalf("expected adjustment note in reasoning, got %q", d.Reasoning)
	}
}

func TestValidateDecisionRejectsAdjustedPositionBelow50USDT(t *testing.T) {
	d := Decision{Symbol: "BTCUSDT", Action: ActionOpenLong, Leverage: 3, PositionSizeUSD: 80, StopLoss: 99.5, TakeProfit: 104, Reasoning: "test"}
	err := coreValidateDecision(&d, testAdaptiveContext())
	if err == nil || !strings.Contains(err.Error(), "调整后仓位") || !strings.Contains(err.Error(), "必须≥50.00") {
		t.Fatalf("expected adjusted-position-size rejection, got %v", err)
	}
}

func TestMinRiskRewardIsScopedToEachTraderContext(t *testing.T) {
	// 调整后止损 99、止盈 104、3x：净RR ≈ 3.24
	newDecision := func() Decision {
		return Decision{Symbol: "BTCUSDT", Action: ActionOpenLong, Leverage: 3, PositionSizeUSD: 200, StopLoss: 99.5, TakeProfit: 104, Reasoning: "test"}
	}

	strictCtx := testAdaptiveContext()
	strictCtx.MinRiskReward = 3.5
	looseCtx := testAdaptiveContext()
	looseCtx.MinRiskReward = 1.5

	strict := newDecision()
	if err := coreValidateDecision(&strict, strictCtx); err == nil || !strings.Contains(err.Error(), "必须≥3.50:1") {
		t.Fatalf("expected strict trader to reject at 3.50, got %v", err)
	}
	loose := newDecision()
	if err := coreValidateDecision(&loose, looseCtx); err != nil {
		t.Fatalf("loose trader should accept the same decision: %v", err)
	}
	unset := newDecision()
	if err := coreValidateDecision(&unset, testAdaptiveContext()); err != nil {
		t.Fatalf("unset threshold should fall back to default %.2f: %v", DefaultMinRiskReward, err)
	}
}

func TestValidateDecisionRejectsWhenAdjustedRRIsTooLow(t *testing.T) {
	d := Decision{Symbol: "BTCUSDT", Action: ActionOpenLong, Leverage: 3, PositionSizeUSD: 200, StopLoss: 99.5, TakeProfit: 102, Reasoning: "test"}
	err := coreValidateDecision(&d, testAdaptiveContext())
	if err == nil || !strings.Contains(err.Error(), "净风险回报比过低") {
		t.Fatalf("expected RR rejection after stop adjustment, got %v", err)
	}
}

func TestConfiguredMinimumIsSharedByPromptAndOpeningChecks(t *testing.T) {
	ctx := testAdaptiveContext()
	smallOpen := Decision{Symbol: "BTCUSDT", Action: ActionOpenLong, Leverage: 3, PositionSizeUSD: 40, StopLoss: 99, TakeProfit: 104}
	if err := coreValidateDecision(&smallOpen, ctx); err == nil || !strings.Contains(err.Error(), "50.00") {
		t.Fatalf("expected the default minimum to reject a 40 USDT open, got %v", err)
	}

	ctx.MinPositionSizeUSD = 35
	for _, strategy := range []PromptStrategy{StrategyA{}, StrategyB{}, StrategyV{}} {
		if !strings.Contains(strategy.BuildSystemPrompt(ctx), "position_size_usd 必须至少为 35 USDT") {
			t.Errorf("missing configured minimum in Strategy%s prompt", strategy.Name())
		}
	}
	if err := coreValidateDecision(&smallOpen, ctx); err != nil {
		t.Fatalf("configured minimum rejected a 40 USDT open: %v", err)
	}

	adjustedOpen := smallOpen
	adjustedOpen.PositionSizeUSD, adjustedOpen.StopLoss = 80, 99.5
	if err := coreValidateDecision(&adjustedOpen, ctx); err != nil || adjustedOpen.PositionSizeUSD != 40 {
		t.Fatalf("expected a valid 40 USDT position after adjustment, got %.2f, %v", adjustedOpen.PositionSizeUSD, err)
	}
}

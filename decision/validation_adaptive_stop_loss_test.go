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
	oldRR := minRiskReward
	minRiskReward = 2.2
	defer func() { minRiskReward = oldRR }()

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

func TestValidateDecisionRejectsWhenAdjustedRRIsTooLow(t *testing.T) {
	oldRR := minRiskReward
	minRiskReward = 2.2
	defer func() { minRiskReward = oldRR }()

	d := Decision{Symbol: "BTCUSDT", Action: ActionOpenLong, Leverage: 3, PositionSizeUSD: 200, StopLoss: 99.5, TakeProfit: 102, Reasoning: "test"}
	err := coreValidateDecision(&d, testAdaptiveContext())
	if err == nil || !strings.Contains(err.Error(), "净风险回报比过低") {
		t.Fatalf("expected RR rejection after stop adjustment, got %v", err)
	}
}

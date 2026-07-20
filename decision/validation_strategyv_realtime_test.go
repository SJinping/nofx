package decision

import (
	"errors"
	"nofx/market"
	"strings"
	"testing"
)

func shortTermExecutionTestContext() *Context {
	return &Context{
		AssumedTakerFeeRate: 0.0004,
		AssumedSlippageRate: 0.0005,
		MarketDataMap: map[string]*market.Data{
			"BTCUSDT": {
				CurrentPrice:  100,
				IntradayATR14: 1,
			},
		},
	}
}

func TestValidateShortTermExecutionPriceAcceptsFreshEntry(t *testing.T) {
	ctx := shortTermExecutionTestContext()
	d := &Decision{
		Symbol:     "BTCUSDT",
		Action:     ActionOpenLong,
		Leverage:   1,
		StopLoss:   99,
		TakeProfit: 103.5,
	}

	if err := validateShortTermExecutionPrice(d, ctx, 100.1); err != nil {
		t.Fatalf("expected fresh entry to pass, got %v", err)
	}
}

func TestValidateShortTermExecutionPriceRejectsChasing(t *testing.T) {
	ctx := shortTermExecutionTestContext()
	d := &Decision{
		Symbol:     "BTCUSDT",
		Action:     ActionOpenLong,
		Leverage:   1,
		StopLoss:   99,
		TakeProfit: 104,
	}

	err := validateShortTermExecutionPrice(d, ctx, 100.6)
	if err == nil || !strings.Contains(err.Error(), "拒绝追价") {
		t.Fatalf("expected chasing rejection, got %v", err)
	}
}

func TestValidateShortTermExecutionPriceRejectsStaleSnapshot(t *testing.T) {
	ctx := shortTermExecutionTestContext()
	d := &Decision{
		Symbol:     "BTCUSDT",
		Action:     ActionOpenShort,
		Leverage:   1,
		StopLoss:   103,
		TakeProfit: 96,
	}

	err := validateShortTermExecutionPrice(d, ctx, 101.2)
	if err == nil || !strings.Contains(err.Error(), "快照已过期") {
		t.Fatalf("expected stale snapshot rejection, got %v", err)
	}
}

func TestRealtimeGuardConvertsOnlyUnsafeOpenToWait(t *testing.T) {
	oldFetcher := shortTermRealtimePriceFetcher
	shortTermRealtimePriceFetcher = func(symbol string) (float64, error) {
		if symbol == "BTCUSDT" {
			return 100.6, nil
		}
		return 0, errors.New("unexpected symbol")
	}
	defer func() { shortTermRealtimePriceFetcher = oldFetcher }()

	ctx := shortTermExecutionTestContext()
	decisions := []Decision{
		{Symbol: "ETHUSDT", Action: ActionCloseLong, Reasoning: "risk exit"},
		{Symbol: "BTCUSDT", Action: ActionOpenLong, Leverage: 1, PositionSizeUSD: 100, StopLoss: 99, TakeProfit: 104, Confidence: 80, RiskUSD: 1, Reasoning: "setup_type=breakout_momentum"},
	}

	errs := guardShortTermDecisionsAtRealtimePrice(decisions, ctx)
	if len(errs) != 1 {
		t.Fatalf("expected one guard error, got %d", len(errs))
	}
	if decisions[0].Action != ActionCloseLong {
		t.Fatalf("risk-reduction action was changed: %s", decisions[0].Action)
	}
	if decisions[1].Action != ActionWait {
		t.Fatalf("unsafe open was not converted to wait: %s", decisions[1].Action)
	}
	if decisions[1].PositionSizeUSD != 0 || decisions[1].StopLoss != 0 || decisions[1].TakeProfit != 0 {
		t.Fatal("converted wait retained executable open parameters")
	}
	if !strings.Contains(decisions[1].Reasoning, "execution_price_guard") {
		t.Fatalf("guard reason not preserved: %s", decisions[1].Reasoning)
	}
}

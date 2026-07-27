package decision

import (
	"strings"
	"testing"
	"time"

	"nofx/market"
)

func testStrategyVRiskContext(strategy PromptStrategy) *Context {
	return &Context{
		Account: AccountInfo{TotalEquity: 1000, AvailableBalance: 1000, MarginUsedPct: 10},
		MarketDataMap: map[string]*market.Data{
			"SOLUSDT": {
				Symbol:        "SOLUSDT",
				CurrentPrice:  100,
				IntradayATR14: 1,
				OpenInterest:  &market.OIData{Latest: 200000},
				IntradaySeries: &market.IntradayData{
					MidPrices: []float64{98, 99, 100},
				},
			},
		},
		BTCETHLeverage:                   5,
		AltcoinLeverage:                  5,
		AltcoinMaxPositionEquityMultiple: 2.0,
		MinOIValueMillions:               15,
		AssumedTakerFeeRate:              0,
		AssumedSlippageRate:              0,
		PromptStrategy:                   strategy,
	}
}

func validStrategyVOpenDecision() Decision {
	return Decision{
		Symbol:          "SOLUSDT",
		Action:          ActionOpenLong,
		Leverage:        1,
		PositionSizeUSD: 700,
		StopLoss:        99,
		TakeProfit:      104,
		Confidence:      80,
		RiskUSD:         8,
		Reasoning:       "setup_type=breakout_momentum; why_now=放量突破; time_stop_minutes=30; invalidation_condition=跌回突破位",
	}
}

func TestStrategyVRiskCapDoesNotAffectStrategyA(t *testing.T) {
	oldRR := minRiskReward
	minRiskReward = 2.2
	defer func() { minRiskReward = oldRR }()

	d := validStrategyVOpenDecision()
	d.PositionSizeUSD = 800

	ctxA := testStrategyVRiskContext(StrategyA{})
	if err := validateDecisions([]Decision{d}, ctxA); err != nil {
		t.Fatalf("StrategyA should not use StrategyV short-term notional cap, got %v", err)
	}

	ctxV := testStrategyVRiskContext(StrategyV{})
	ctxV.AltcoinMaxPositionEquityMultiple = 0.75
	err := validateDecisions([]Decision{d}, ctxV)
	if err == nil || !strings.Contains(err.Error(), "山寨币单币种仓位价值不能超过750") {
		t.Fatalf("expected StrategyV notional cap rejection, got %v", err)
	}
}

func TestStrategyVRejectsLowConfidenceOpen(t *testing.T) {
	d := validStrategyVOpenDecision()
	d.Confidence = 69
	err := validateDecisions([]Decision{d}, testStrategyVRiskContext(StrategyV{}))
	if err == nil || !strings.Contains(err.Error(), "StrategyV短线开仓信心度不足") {
		t.Fatalf("expected StrategyV confidence rejection, got %v", err)
	}
}

func TestStrategyVAutoStopBypassesMinHoldValidation(t *testing.T) {
	ctx := testStrategyVRiskContext(StrategyV{})
	ctx.MinHoldMinutes = 60
	ctx.Positions = []PositionInfo{{
		Symbol:           "SOLUSDT",
		Side:             "long",
		MarkPrice:        100,
		EntryPrice:       103,
		UnrealizedPnLPct: -3.0,
		UpdateTime:       time.Now().Add(-5 * time.Minute).UnixMilli(),
	}}

	decisions := GenerateShortTermAutoDecisions(ctx)
	if len(decisions) != 1 || decisions[0].Action != ActionCloseLong || decisions[0].DecisionSource != "auto_stop_loss" {
		t.Fatalf("expected one StrategyV auto close_long stop, got %#v", decisions)
	}
	if err := validateDecisions(decisions, ctx); err != nil {
		t.Fatalf("StrategyV auto stop should bypass min-hold validation, got %v", err)
	}
}

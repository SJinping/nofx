package decision

import (
	"encoding/json"
	"math"
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
	d := validStrategyVOpenDecision()
	d.PositionSizeUSD = 800

	ctxA := testStrategyVRiskContext(StrategyA{})
	decisionsA := []Decision{d}
	validateDecisions(decisionsA, ctxA)
	if decisionsA[0].ValidationError != "" {
		t.Fatalf("StrategyA rejected: %v", decisionsA[0].ValidationError)
	}

	ctxV := testStrategyVRiskContext(StrategyV{})
	ctxV.AltcoinMaxPositionEquityMultiple = 0.75
	decisionsV := []Decision{d}
	validateDecisions(decisionsV, ctxV)
	if !strings.Contains(decisionsV[0].ValidationError, "山寨币单币种仓位价值不能超过750") {
		t.Fatalf("expected rejection")
	}
}

func TestStrategyVRejectsLowConfidenceOpen(t *testing.T) {
	d := validStrategyVOpenDecision()
	d.Confidence = 69
	decisions := []Decision{d}
	validateDecisions(decisions, testStrategyVRiskContext(StrategyV{}))
	if !strings.Contains(decisions[0].ValidationError, "StrategyV短线开仓信心度不足") {
		t.Fatalf("expected rejection")
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
	validateDecisions(decisions, ctx)
	if decisions[0].ValidationError != "" {
		t.Fatalf("auto stop rejected: %v", decisions[0].ValidationError)
	}
}

func TestStrategyVRepricesNetRiskAtExecution(t *testing.T) {
	ctx := testStrategyVRiskContext(StrategyV{})
	ctx.AssumedTakerFeeRate, ctx.AssumedSlippageRate = .0005, .0004
	d := validStrategyVOpenDecision()
	d.PositionSizeUSD, d.RiskUSD = 840, 0
	if err := validateShortTermOpenRisk(&d, ctx); err != nil || math.Abs(d.RiskUSD-9.912) > 1e-8 {
		t.Fatalf("snapshot risk=%f err=%v", d.RiskUSD, err)
	}
	if err := ValidateShortTermExecutionPrice(&d, ctx, 100.05); err == nil || !strings.Contains(err.Error(), "单笔风险过大") {
		t.Fatalf("expected live risk > 10 USDT despite acceptable RR: %v", err)
	}
}

func TestStrategyVBreakoutReferenceExcludesTrigger(t *testing.T) {
	s := &market.IntradayData{Highs: []float64{101, 102, 110}, Lows: []float64{99, 100, 101}, Closes: []float64{100, 101, 109}}
	got := formatShortTermSetupSnapshot(&market.Data{IntradaySeries: s})
	for _, want := range []string{"range=[99, 110]", "pre_trigger_range=[99, 102]", "trigger_close=109"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
}

func TestStrategyVExampleUsesConfiguredCostsAndThreshold(t *testing.T) {
	out := shortTermDecisionExamples(5, .0005, .0004, 2.1)
	blocks := strings.Split(out, "```json\n")
	if len(blocks) != 3 {
		t.Fatalf("expected two independent examples: %s", out)
	}
	for i, action := range []string{ActionOpenLong, ActionWait} {
		var ds []Decision
		err := json.Unmarshal([]byte(strings.Split(blocks[i+1], "\n```")[0]), &ds)
		if err != nil || len(ds) != 1 || ds[0].Action != action {
			t.Fatalf("example %d: %v %v", i, ds, err)
		}
		if i == 0 && (math.Abs(ds[0].RiskUSD-.58) > 1e-8 || !strings.Contains(ds[0].Reasoning, "calculated_net_rr=2.793103")) {
			t.Fatalf("incorrect risk or RR: %+v", ds[0])
		}
	}
	if strings.Contains(shortTermDecisionExamples(5, .0005, .0004, 3), "open_long") {
		t.Fatal("below-threshold example recommended opening")
	}
}

func TestStrategyVRejectsStaleShortBeforeOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		price   float64
		trigger float64
		want    string
	}{
		{name: "valid setup", price: 100, trigger: 100.05},
		{name: "stop too close", price: 100.15, want: "实时止损距离过近"},
		{name: "breakout invalidated", price: 100.06, trigger: 100.05, want: "实时突破失效"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testStrategyVRiskContext(StrategyV{})
			d := validStrategyVOpenDecision()
			d.Action, d.StopLoss, d.TakeProfit, d.TriggerPrice = ActionOpenShort, 100.5, 98, tc.trigger
			err := ValidateShortTermExecutionPrice(&d, ctx, tc.price)
			if tc.want == "" && err != nil {
				t.Fatalf("valid open rejected: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

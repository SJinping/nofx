package decision

import (
	"strings"
	"testing"
	"time"
)

func TestStrategyVSystemPromptUsesOwnBTCDirectionAndCodeThresholds(t *testing.T) {
	prompt := (StrategyV{}).BuildSystemPrompt(&Context{BTCETHLeverage: 5, AltcoinLeverage: 5})

	for _, want := range []string{
		"StrategyV 市场环境与方向约束",
		"正常/绩效重置 ≥ 70；谨慎模式 ≥ 75；防守模式 ≥ 85",
		"开仓后 8 分钟内",
		"BTC/ETH 净ROI ≥3% 减仓30%",
		"## other（其他可验证 setup，谨慎使用）",
		"当前为实盘",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("StrategyV system prompt missing %q", want)
		}
	}
	for _, unwanted := range []string{"恐贪指数使用指南", "confidence ≥68", "paper 探索阶段"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("StrategyV live system prompt must not contain %q", unwanted)
		}
	}

	paperPrompt := (StrategyV{}).BuildSystemPrompt(&Context{PaperTradingMode: true})
	if !strings.Contains(paperPrompt, "paper 探索阶段") {
		t.Fatalf("StrategyV paper system prompt should keep exploration guidance")
	}
}

func TestStrategyAKeepsSharedBTCDirectionPrompt(t *testing.T) {
	prompt := (StrategyA{}).BuildSystemPrompt(&Context{})
	if !strings.Contains(prompt, "恐贪指数使用指南") || strings.Contains(prompt, "StrategyV 市场环境与方向约束") {
		t.Fatalf("StrategyA must keep the shared BTC direction prompt")
	}
}

func TestStrategyVUserPromptInjectsCurrentConfidenceThreshold(t *testing.T) {
	ctx := testStrategyVRiskContext(StrategyV{})
	prompt := (StrategyV{}).BuildUserPrompt(ctx)
	if !strings.Contains(prompt, "confidence ≥ 70（正常模式）；setup_type=other ≥ 75") {
		t.Fatalf("StrategyV user prompt missing current confidence threshold")
	}
}

func TestStrategyVUsesOwnMinHoldInsteadOfGlobal(t *testing.T) {
	ctx := testStrategyVRiskContext(StrategyV{})
	ctx.MinHoldMinutes = 60
	position := PositionInfo{Symbol: "SOLUSDT", Side: "long", EntryPrice: 100, MarkPrice: 100.2}
	close := Decision{Symbol: "SOLUSDT", Action: ActionCloseLong, Reasoning: "breakout failed"}

	position.UpdateTime = time.Now().Add(-5 * time.Minute).UnixMilli()
	ctx.Positions = []PositionInfo{position}
	if err := ExtraValidateShortTerm(&close, ctx); err == nil || !strings.Contains(err.Error(), "最低要求 8 分钟") {
		t.Fatalf("expected StrategyV 8-minute min hold rejection, got %v", err)
	}

	position.UpdateTime = time.Now().Add(-10 * time.Minute).UnixMilli()
	ctx.Positions = []PositionInfo{position}
	if err := ExtraValidateShortTerm(&close, ctx); err != nil {
		t.Fatalf("StrategyV close after 10 minutes should ignore global 60-minute hold: %v", err)
	}
}

func TestStrategyVOtherSetupRequiresHigherBarAndSmallerRisk(t *testing.T) {
	ctx := testStrategyVRiskContext(StrategyV{})
	base := validStrategyVOpenDecision()
	base.Reasoning = "setup_type=other; setup_logic=资金费率极端后挤空回补; why_now=放量收回; time_stop_minutes=30; invalidation_condition=跌破挤仓起点"
	base.RiskUSD = 5

	ok := base
	if err := validateShortTermOpenRisk(&ok, ctx); err != nil {
		t.Fatalf("valid other setup rejected: %v", err)
	}

	lowConfidence := base
	lowConfidence.Confidence = 72
	if err := validateShortTermOpenRisk(&lowConfidence, ctx); err == nil || !strings.Contains(err.Error(), ">= 75") {
		t.Fatalf("expected other-setup confidence rejection, got %v", err)
	}

	missingLogic := base
	missingLogic.Reasoning = "setup_type=other; why_now=放量收回; time_stop_minutes=30; invalidation_condition=跌破挤仓起点"
	if err := validateShortTermOpenRisk(&missingLogic, ctx); err == nil || !strings.Contains(err.Error(), "setup_logic") {
		t.Fatalf("expected missing setup_logic rejection, got %v", err)
	}

	tooRisky := base
	tooRisky.RiskUSD = 8
	if err := validateShortTermOpenRisk(&tooRisky, ctx); err == nil || !strings.Contains(err.Error(), "单笔风险过大") {
		t.Fatalf("expected other-setup risk rejection, got %v", err)
	}
}

func TestStrategyVAutoTakeProfitProfileFitsShortTermStops(t *testing.T) {
	ctx := testStrategyVRiskContext(StrategyV{})
	ctx.AutoTakeProfit = DefaultAutoTakeProfitConfig()
	GenerateShortTermAutoDecisions(ctx)

	major := ResolveAutoTakeProfitConfig("BTCUSDT", ctx.AutoTakeProfit)
	alt := ResolveAutoTakeProfitConfig("SOLUSDT", ctx.AutoTakeProfit)
	if major.Stage0Threshold != 3 || alt.Stage0Threshold != 5 {
		t.Fatalf("StrategyV auto TP profile not applied: major=%v alt=%v", major.Stage0Threshold, alt.Stage0Threshold)
	}
	if major.TrailingDistancePct >= shortTermMajorMaxStopPct*100 || alt.TrailingDistancePct >= shortTermAltMaxStopPct*100 {
		t.Fatalf("StrategyV trailing distance must be tighter than max initial stop: major=%.2f alt=%.2f", major.TrailingDistancePct, alt.TrailingDistancePct)
	}
}

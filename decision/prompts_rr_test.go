package decision

import (
	"nofx/logger"
	"strings"
	"testing"
	"time"
)

func TestStrategyASystemPromptInjectsRuntimeNetRiskRewardRules(t *testing.T) {
	oldMinRiskReward := minRiskReward
	minRiskReward = 2.35
	defer func() { minRiskReward = oldMinRiskReward }()

	prompt := (StrategyA{}).BuildSystemPrompt(&Context{
		BTCETHLeverage:      5,
		AltcoinLeverage:     3,
		AssumedTakerFeeRate: 0.0006,
		AssumedSlippageRate: 0.0008,
		ScanIntervalMin:     10,
	})

	for _, want := range []string{
		"taker_fee_rate = 0.000600 (0.060%)",
		"slippage_rate = 0.000800 (0.080%)",
		"min_net_rr = 2.35:1",
		"round_trip_cost_roi_pct = 2 * (taker_fee_rate + slippage_rate) * leverage * 100",
		"net_risk_roi_pct = raw_risk_pct * leverage + round_trip_cost_roi_pct",
		"net_reward_roi_pct = raw_reward_pct * leverage - round_trip_cost_roi_pct",
		"net_rr = net_reward_roi_pct / net_risk_roi_pct",
		"仅当 net_reward_roi_pct > 0 且 net_rr >= min_net_rr 时才可输出 open_long 或 open_short",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing injected net RR rule %q", want)
		}
	}
}

func TestStrategyASystemPromptUsesConfiguredZeroCostAssumptions(t *testing.T) {
	prompt := (StrategyA{}).BuildSystemPrompt(&Context{
		AssumedTakerFeeRate: 0,
		AssumedSlippageRate: 0,
	})

	for _, want := range []string{
		"taker_fee_rate = 0.000000 (0.000%)",
		"slippage_rate = 0.000000 (0.000%)",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt did not preserve configured cost assumption %q", want)
		}
	}
}

func TestPerformancePromptDoesNotInstructLLMAboutCooldownTradingBan(t *testing.T) {
	now := time.Now()
	prompt := getPerformance(&Context{
		Performance: &logger.PerformanceAnalysis{
			TotalTrades:         12,
			WinRate:             42,
			SharpeRatio:         -0.80,
			CurrentLosingStreak: 5,
		},
		PerformanceCooldown: PerformanceCooldownState{
			Active:    true,
			ReleaseAt: now.Add(time.Hour),
		},
	})

	if strings.Contains(prompt, "绩效冷却") || strings.Contains(prompt, "禁止新开仓") {
		t.Fatalf("performance prompt must not include cooldown execution instructions: %q", prompt)
	}
	if !strings.Contains(prompt, "夏普比率-0.80") || !strings.Contains(prompt, "当前连亏: 5 笔") {
		t.Fatalf("performance prompt should retain performance facts for cautious trading: %q", prompt)
	}
}

func TestStrategyASystemPromptFramesModeratePoorPerformanceAsCautious(t *testing.T) {
	prompt := (StrategyA{}).BuildSystemPrompt(&Context{})

	if !strings.Contains(prompt, "仅为谨慎交易参考，不构成停止交易或固定观望周期") {
		t.Fatalf("system prompt must explicitly frame moderate poor performance as cautious guidance: %q", prompt)
	}
}

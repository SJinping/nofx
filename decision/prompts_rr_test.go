package decision

import (
	"strings"
	"testing"
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

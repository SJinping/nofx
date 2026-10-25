package decision

import (
	"strings"
	"testing"
)

func TestStrategyARequestsExpectedProfitPathInOpeningReasoning(t *testing.T) {
	prompt := (StrategyA{}).BuildSystemPrompt(&Context{})
	for _, want := range []string{
		"开仓 reasoning 必须简要说明",
		"预期获利过程",
		"减弱/失效条件",
		"重新评估",
		"不要为了满足净RR而虚设远端目标",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("opening guidance missing %q", want)
		}
	}
}

func TestStrategyAPositionChecklistAssessesRiskInsteadOfPresumingHold(t *testing.T) {
	prompt := (StrategyA{}).BuildSystemPrompt(&Context{})
	for _, want := range []string{
		"现在应承担多少风险",
		"入场逻辑未失效，不等于继续完整持仓仍有优势",
		"当前支持证据",
		"扣除手续费、滑点及资金费",
		"参考4小时数据不代表必须按4小时趋势仓管理",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("risk assessment guidance missing %q", want)
		}
	}
	for _, obsolete := range []string{
		"平仓前必须逐条回答", "在考虑平仓前", "彻底推翻",
		"不要只用3分钟级别的RSI/MACD短期波动全平4小时级别仓位",
	} {
		if strings.Contains(prompt, obsolete) {
			t.Errorf("prompt retains exit-biased instruction %q", obsolete)
		}
	}
	// Flexible management must retain existing execution safeguards.
	for _, safeguard := range []string{"不能把已有保护止损向亏损方向放宽", "0.25R", "15分钟", "禁止把 100% 写成 partial_close"} {
		if !strings.Contains(prompt, safeguard) {
			t.Errorf("prompt lost execution safeguard %q", safeguard)
		}
	}
}

func TestStrategyAUserPromptPreservesOriginalThesisWithoutMandatoryHold(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		t.Run(side, func(t *testing.T) {
			original := "原始论据：支撑/阻力承接；预期获利过程=局部反弹或回落。"
			ctx := &Context{Positions: []PositionInfo{{
				Symbol: "BTCUSDT", Side: side, EntryReasoning: original,
				EntryConfidence: 78, EntryStopLoss: 80000, EntryTakeProfit: 84000,
			}}}
			prompt := (StrategyA{}).BuildUserPrompt(ctx)
			if strings.Count(prompt, original) != 1 {
				t.Fatal("original entry thesis must be preserved exactly once")
			}
			for _, want := range []string{"信心度 78", "止损 80000.0000", "目标 84000.0000", "不要求继续完整持仓", "预期获利过程", "当前支持证据"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("user prompt missing %q", want)
				}
			}
			if strings.Contains(prompt, "如果入场逻辑未变且止损未到，应继续持有") {
				t.Fatal("user prompt still mandates hold for unchanged thesis")
			}
			if ctx.Positions[0].EntryReasoning != original {
				t.Fatal("prompt generation must not rewrite the original thesis")
			}
		})
	}
}

func TestEntryThesisDoesNotInventMissingOriginalPlan(t *testing.T) {
	for _, reasoning := range []string{"", "(unknown entry)"} {
		if got := formatEntryThesis(PositionInfo{EntryReasoning: reasoning}); got != "" {
			t.Fatalf("missing thesis must stay absent, got %q", got)
		}
	}
}

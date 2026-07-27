package decision

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptShortTermClosedSeparatesScanAndBarIntervals(t *testing.T) {
	prompt := buildSystemPromptShortTermClosed(1000, 5, 5, 6)

	for _, want := range []string{
		"决策扫描周期**：系统每 6 分钟",
		"执行K线周期**：固定为 3 分钟",
		"最近 20 根已闭合 3m K线执行结构",
		"覆盖约60分钟",
		"intraday_atr14 (3m)",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("StrategyV system prompt missing %q", want)
		}
	}

	for _, forbidden := range []string{
		"最近 20 根 6m K线",
		"最近 20 根 6m 区间",
		"6m 价格回踩",
		"intraday_atr14 (6m)",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("StrategyV system prompt still mixes scan and bar intervals: %q", forbidden)
		}
	}
}

func TestBuildUserPromptShortTermClosedDescribesClosedWindow(t *testing.T) {
	ctx := &Context{
		CurrentTime:     "2026-07-20 12:00:00",
		ScanIntervalMin: 6,
	}
	prompt := buildUserPromptShortTermClosed(ctx)

	for _, want := range []string{
		"扫描周期6分钟",
		"K线周期3分钟",
		"最近20根均已闭合",
		"覆盖约60分钟",
		"最近 20 根已闭合 3m K线（约60分钟）",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("StrategyV user prompt missing %q", want)
		}
	}
	if strings.Contains(prompt, "最近 20 根 6m K线") {
		t.Fatal("StrategyV user prompt labels fixed 3m data as 6m bars")
	}
}

func TestStrategyVPromptAdapterDoesNotChangeStrategyAOrB(t *testing.T) {
	ctx := &Context{ScanIntervalMin: 6}
	for name, prompt := range map[string]string{
		"A": (StrategyA{}).BuildSystemPrompt(ctx),
		"B": (StrategyB{}).BuildSystemPrompt(ctx),
	} {
		if strings.Contains(prompt, "StrategyV 数据时间尺度") {
			t.Fatalf("Strategy%s unexpectedly contains StrategyV-only prompt text", name)
		}
	}
}

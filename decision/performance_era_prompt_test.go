package decision

import (
	"nofx/logger"
	"strings"
	"testing"
)

func TestPerformanceEraPromptAndSharedStrategyGuidance(t *testing.T) {
	for _, build := range []struct {
		name string
		fn   func() string
	}{
		{"A", func() string { return buildSystemPrompt(0, 5, 5, 2, 3, 0.0004, 0.0005) }},
		{"B", func() string { return buildSystemPromptB(0, 5, 5, 2, 3) }},
		{"V", func() string { return buildSystemPromptShortTerm(0, 5, 5, 2, 3) }},
	} {
		text := build.fn()
		if !strings.Contains(text, "绩效重置") || !strings.Contains(text, "谨慎模式") || !strings.Contains(text, "防守模式") || strings.Contains(text, "连续观望至少6个周期") {
			t.Errorf("%s guidance: %s", build.name, text)
		}
	}
	ctx := &Context{Performance: &logger.PerformanceAnalysis{TotalTrades: 0}, LifetimePerformance: &logger.PerformanceAnalysis{TotalTrades: 3, WinningTrades: 1}, PerformanceReset: true}
	result := getPerformance(ctx)
	if !strings.Contains(result, "绩效重置") || !strings.Contains(result, "夏普比率未定义") || !strings.Contains(result, "历史累计") || strings.Contains(result, "防守模式") {
		t.Fatal(result)
	}
	ctx.Performance = &logger.PerformanceAnalysis{TotalTrades: 2, WinningTrades: 1, WinRate: 0.5}
	ctx.PerformanceReset = false
	if !strings.Contains(getPerformance(ctx), "胜率50.0%") {
		t.Fatal(getPerformance(ctx))
	}
}

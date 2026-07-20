package decision

import (
	"fmt"
	"nofx/market"
	"strings"
)

const shortTermPromptBarCount = market.DefaultShortTermOutputPoints

// buildSystemPromptShortTermClosed keeps StrategyV's existing trading rules but
// fixes its time-scale contract: scan cadence controls how often the LLM runs,
// while all short-term setup confirmation uses fixed, closed 3m bars.
// StrategyA and StrategyB continue to use their existing prompt builders.
func buildSystemPromptShortTermClosed(accountEquity float64, btcEthLeverage, altcoinLeverage, scanIntervalMin int) string {
	if scanIntervalMin <= 0 {
		scanIntervalMin = market.ShortTermBarIntervalMinutes
	}

	prompt := buildSystemPromptShortTerm(accountEquity, btcEthLeverage, altcoinLeverage, scanIntervalMin)
	barIntervalMin := market.ShortTermBarIntervalMinutes
	coverageMin := shortTermPromptBarCount * barIntervalMin

	// Correct only the StrategyV text generated above. The underlying prompt
	// functions for StrategyA/B are not modified or wrapped.
	prompt = strings.ReplaceAll(
		prompt,
		fmt.Sprintf("最近 20 根 %dm K线执行结构", scanIntervalMin),
		fmt.Sprintf("最近 %d 根已闭合 %dm K线执行结构（覆盖约%d分钟）", shortTermPromptBarCount, barIntervalMin, coverageMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		fmt.Sprintf("%dm 价格回踩", scanIntervalMin),
		fmt.Sprintf("%dm 已闭合K线价格回踩", barIntervalMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		fmt.Sprintf("最近 20 根 %dm 区间", scanIntervalMin),
		fmt.Sprintf("最近 %d 根已闭合 %dm 区间", shortTermPromptBarCount, barIntervalMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"最近 20 根 3m 区间",
		fmt.Sprintf("最近 %d 根已闭合 %dm 区间", shortTermPromptBarCount, barIntervalMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"最近 3m range high/low",
		fmt.Sprintf("最近已闭合 %dm range high/low", barIntervalMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		fmt.Sprintf("intraday_atr14 (%dm)", scanIntervalMin),
		fmt.Sprintf("intraday_atr14 (%dm)", barIntervalMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"20根3m区间上沿",
		fmt.Sprintf("%d根已闭合%dm区间上沿", shortTermPromptBarCount, barIntervalMin),
	)
	prompt = strings.ReplaceAll(prompt, "价格在3m区间中部", "价格在已闭合3m区间中部")

	timeScaleContract := fmt.Sprintf(`# ⏲️ StrategyV 数据时间尺度（最高优先级）

- **决策扫描周期**：系统每 %d 分钟调用你一次。
- **执行K线周期**：固定为 %d 分钟，与扫描周期相互独立。
- **短线观察窗口**：最近 %d 根已闭合 %d 分钟K线，覆盖约 %d 分钟。
- **确认规则**：突破、回踩、假突破、衰竭、成交量和技术指标只能使用已闭合K线确认。
- 当前消息中的 current_price 是本轮实时价格快照，只用于判断可执行性、追价风险和净RR；系统会在执行前重新获取实时价格并再次校验。
- 不得把“每 %d 分钟扫描一次”理解为使用 %d 分钟K线。

`, scanIntervalMin, barIntervalMin, shortTermPromptBarCount, barIntervalMin, coverageMin, scanIntervalMin, scanIntervalMin)

	return timeScaleContract + prompt
}

// buildUserPromptShortTermClosed applies the same StrategyV-only time-scale
// contract to dynamic market data labels. The data itself is supplied by
// market.GetClosedWithOptions; this function only makes that provenance explicit
// to the LLM and fixes the inherited ATR label.
func buildUserPromptShortTermClosed(ctx *Context) string {
	prompt := buildUserPromptShortTerm(ctx)
	barIntervalMin := market.ShortTermBarIntervalMinutes
	scanIntervalMin := ctx.ScanIntervalMin
	if scanIntervalMin <= 0 {
		scanIntervalMin = barIntervalMin
	}
	coverageMin := shortTermPromptBarCount * barIntervalMin

	prompt = strings.ReplaceAll(
		prompt,
		"最近 20 根 3m K线（约60分钟）",
		fmt.Sprintf("最近 %d 根已闭合 %dm K线（约%d分钟）", shortTermPromptBarCount, barIntervalMin, coverageMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"完整3m OHLCV",
		fmt.Sprintf("完整已闭合%dm OHLCV", barIntervalMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		fmt.Sprintf("intraday_atr14 (%dm)", scanIntervalMin),
		fmt.Sprintf("intraday_atr14 (%dm)", barIntervalMin),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"短线原始市场数据（用于判断突破/回踩/假突破/衰竭）",
		fmt.Sprintf("短线原始市场数据（最近已闭合%dm K线，用于判断突破/回踩/假突破/衰竭）", barIntervalMin),
	)

	header := fmt.Sprintf(
		"**StrategyV时间尺度**: 扫描周期%d分钟 | K线周期%d分钟 | 最近%d根均已闭合，覆盖约%d分钟 | current_price为本轮实时价格快照，执行前会二次复核\n\n",
		scanIntervalMin, barIntervalMin, shortTermPromptBarCount, coverageMin,
	)
	return header + prompt
}

package decision

// The same performance-era policy applies to every prompt strategy. Lifetime
// figures are accounting only, not an input to this policy.
func performanceGuidancePrompt() string {
	return "有效绩效只使用当前交易阶段（不使用历史累计盈亏或重置前交易）判断：\n" +
		"- 绩效重置/无样本：Sharpe 未定义，按正常模式寻找合格的新 setup；不得因旧连亏进入谨慎或防守模式。\n" +
		"- 正常模式：有效样本没有负 Sharpe 或连亏警报，遵循策略原有的开仓质量和净 RR 标准。\n" +
		"- 谨慎模式：有效 Sharpe < 0 或当前连亏 1~2 笔，减少试错，优先清晰 setup。\n" +
		"- 防守模式：有效 Sharpe < -0.5 或当前连亏 >=3 笔，缩小试错频率，只做高置信且净 RR 清晰的 setup。\n" +
		"- 绩效反馈仅为谨慎交易参考，不构成停止交易或固定观望周期；风控仍按代码执行。\n\n"
}

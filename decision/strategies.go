package decision

// StrategyA 默认策略：复用现有 buildSystemPrompt / buildUserPrompt
type StrategyA struct{}

func (StrategyA) Name() string { return "A" }

func (StrategyA) BuildSystemPrompt(ctx *Context) string {
	return buildSystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.ScanIntervalMin)
}

func (StrategyA) BuildUserPrompt(ctx *Context) string {
	return buildUserPrompt(ctx)
}

func (StrategyA) GenerateAutoDecisions(ctx *Context) []Decision {
	return GenerateAutoDecisions(ctx)
}

func (StrategyA) ExtraValidate(d *Decision, ctx *Context) error {
	return ExtraValidate(d, ctx)
}

// StrategyB 示例策略：展示如何定制一部分提示词逻辑
type StrategyB struct{}

func (StrategyB) Name() string { return "B" }

func (StrategyB) BuildSystemPrompt(ctx *Context) string {
	return buildSystemPromptB(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.ScanIntervalMin)
}

func (StrategyB) BuildUserPrompt(ctx *Context) string {
	return buildUserPromptB(ctx)
}

func (StrategyB) GenerateAutoDecisions(ctx *Context) []Decision {
	return GenerateAutoDecisions(ctx)
}

// ExtraValidate 示例：B策略额外要求开仓时confidence >= 75
func (StrategyB) ExtraValidate(d *Decision, ctx *Context) error {
	return ExtraValidate(d, ctx)
}

// StrategyV 短期/波动交易策略：专注日内波动捕捉
type StrategyV struct{}

func (StrategyV) Name() string { return "V" }

func (StrategyV) BuildSystemPrompt(ctx *Context) string {
	return buildSystemPromptShortTermClosed(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.ScanIntervalMin)
}

func (StrategyV) BuildUserPrompt(ctx *Context) string {
	return buildUserPromptShortTermClosed(ctx)
}

func (StrategyV) GenerateAutoDecisions(ctx *Context) []Decision {
	return GenerateShortTermAutoDecisions(ctx)
}

func (StrategyV) ExtraValidate(d *Decision, ctx *Context) error {
	return ExtraValidateShortTerm(d, ctx)
}

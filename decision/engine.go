package decision

import (
	"fmt"
	"log"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"nofx/stats"
	"time"
)

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
func GetFullDecision(ctx *Context) (*FullDecision, error) {
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		errType := stats.ClassifyMarketDataError(err.Error())
		recordError(errType, err.Error(), "")
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 数据录制，方便离线回测
	if ctx.EnableRecording {
		if err := saveContextToFile(ctx, ctx.TraderID); err != nil {
			// 录制失败不影响主流程，仅打印日志
			log.Printf("⚠️  数据录制失败: %v", err)
		}
	}
	// =====================

	// 2. 使用策略接口构建 System/User Prompt
	strategy := ctx.PromptStrategy
	if strategy == nil {
		strategy = StrategyA{}        // 默认策略
		ctx.PromptStrategy = strategy // 策略写回ctx
	}

	// 3. 执行策略级自动规则，相当于策略的硬约束条件，先于LLM策略执行，例如自动止盈止损策略
	// TODO: 注意autoDecisions要兼容validateDecisions中的验证条件
	autoDecisions := strategy.GenerateAutoDecisions(ctx)
	if len(autoDecisions) > 0 {
		if err := validateDecisions(autoDecisions, ctx); err != nil {
			errType := stats.ClassifyDecisionValidateError(err.Error())
			recordError(errType, err.Error(), "")
			return nil, fmt.Errorf("自动决策验证失败: %w", err)
		}

		return &FullDecision{
			UserPrompt: "(auto-strategy)",
			CoTTrace:   fmt.Sprintf("策略%s触发自动规则，无需AI。", strategy.Name()),
			Decisions:  autoDecisions,
			Timestamp:  time.Now(),
		}, nil
	}

	// 4. 否则构建System/User Prompt，让LLM决策
	systemPrompt := strategy.BuildSystemPrompt(ctx)
	userPrompt := strategy.BuildUserPrompt(ctx)
	// 在用户提示前加上策略标识，便于日志对比
	userPrompt = fmt.Sprintf("**策略版本**: %s\n\n%s", strategy.Name(), userPrompt)

	// 5. 调用AI API（使用 system + user prompt）
	aiResponse, err := mcp.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		errType := stats.ClassifyLLMError(err.Error())
		recordError(errType, err.Error(), "")
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 6. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx)
	if err != nil {
		errType := stats.ClassifyDecisionParseError(err.Error())
		recordError(errType, err.Error(), "")
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.UserPrompt = userPrompt // 保存输入prompt
	return decision, nil
}

// GetFullDecisionFromText 用于回测，从文本中获取完整决策
func GetFullDecisionFromText(ctx *Context, systemPrompt, userPrompt string) (*FullDecision, error) {

	// 1. 为所有币种获取市场数据
	// 不需要

	// 2. 使用策略接口构建 System/User Prompt
	strategy := ctx.PromptStrategy
	if strategy == nil {
		strategy = StrategyA{}        // 默认策略
		ctx.PromptStrategy = strategy // 策略写回ctx
	}

	// 3. 执行策略级自动规则，相当于策略的硬约束条件，先于LLM策略执行，例如自动止盈止损策略
	// TODO: 注意autoDecisions要兼容validateDecisions中的验证条件
	autoDecisions := strategy.GenerateAutoDecisions(ctx)
	if len(autoDecisions) > 0 {
		if err := validateDecisions(autoDecisions, ctx); err != nil {
			errType := stats.ClassifyDecisionValidateError(err.Error())
			recordError(errType, err.Error(), "")
			return nil, fmt.Errorf("自动决策验证失败: %w", err)
		}

		return &FullDecision{
			UserPrompt: "(auto-strategy)",
			CoTTrace:   fmt.Sprintf("策略%s触发自动规则，无需AI。", strategy.Name()),
			Decisions:  autoDecisions,
			Timestamp:  time.Now(),
		}, nil
	}

	// 4. 否则构建System/User Prompt，让LLM决策
	// 不需要，由参数传入

	// 5. 调用AI API（使用 system + user prompt）
	aiResponse, err := mcp.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		errType := stats.ClassifyLLMError(err.Error())
		recordError(errType, err.Error(), "")
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 6. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx)
	if err != nil {
		errType := stats.ClassifyDecisionParseError(err.Error())
		recordError(errType, err.Error(), "")
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.UserPrompt = userPrompt // 保存输入prompt
	return decision, nil
}

// fetchMarketDataForContext 为上下文中的所有币种获取市场数据和OI数据
func fetchMarketDataForContext(ctx *Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*OITopData)

	// 收集所有需要获取数据的币种
	symbolSet := make(map[string]bool)

	// 1. 优先获取持仓币种的数据（这是必须的）
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	// 2. 候选币种数量根据账户状态动态调整
	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	// 获取市场数据
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for symbol := range symbolSet {
		// ✅ 统一使用轻量市场数据（默认10根3m序列），避免 prompt 体积膨胀与 token 成本上升
		data, err := market.Get(symbol)
		if err != nil {
			// 单个币种失败不影响整体，记录错误
			log.Printf("⚠️  获取 %s 市场数据失败: %v", symbol, err)
			continue
		}

		// ⚠️ 流动性过滤：持仓价值低于15M USD的币种不做（多空都不做）
		// 持仓价值 = 持仓量 × 当前价格
		// 但现有持仓必须保留（需要决策是否平仓）
		isExistingPosition := positionSymbols[symbol]
		if !isExistingPosition && data.OpenInterest != nil && data.CurrentPrice > 0 {
			// 计算持仓价值（USD）= 持仓量 × 当前价格
			oiValue := data.OpenInterest.Latest * data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000 // 转换为百万美元单位
			if oiValueInMillions < 15 {
				log.Printf("⚠️  %s 持仓价值过低(%.2fM USD < 15M)，跳过此币种 [持仓量:%.0f × 价格:%.4f]",
					symbol, oiValueInMillions, data.OpenInterest.Latest, data.CurrentPrice)
				continue
			}
		}

		ctx.MarketDataMap[symbol] = data
	}

	// NOFX Data: best-effort 批量获取 AI300 + 挂单热力图
	nofxSymbols := make([]string, 0, len(ctx.MarketDataMap))
	for sym := range ctx.MarketDataMap {
		nofxSymbols = append(nofxSymbols, sym)
	}
	ai300Map, heatmapMap := market.BatchFetchNofxData(nofxSymbols)
	for sym, data := range ctx.MarketDataMap {
		if sig, ok := ai300Map[sym]; ok {
			data.NofxAI300 = sig
		}
		if hm, ok := heatmapMap[sym]; ok {
			data.NofxHeatmap = hm
		}
	}

	// 加载OI Top数据（不影响主流程）
	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			// 标准化符号匹配
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

// calculateMaxCandidates 根据账户状态计算需要分析的候选币种数量
func calculateMaxCandidates(ctx *Context) int {
	// 直接返回候选池的全部币种数量
	// 因为候选池已经在 auto_trader.go 中筛选过了
	// 固定分析前20个评分最高的币种（来自AI500）
	return len(ctx.CandidateCoins)
}

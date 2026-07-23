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

	if shouldSkipLLMNoMarketData(ctx) {
		return buildNoMarketDataSkipDecision(ctx, strategy.Name()), nil
	}

	// 4. 否则构建System/User Prompt，让LLM决策
	systemPrompt := strategy.BuildSystemPrompt(ctx)
	userPrompt := strategy.BuildUserPrompt(ctx)
	// 在用户提示前加上策略标识，便于日志对比
	userPrompt = fmt.Sprintf("**策略版本**: %s\n\n%s", strategy.Name(), userPrompt)

	// 5. 调用AI API（使用 system + user prompt），获取完整结果含 token 用量
	callResult, err := callAIDetailed(ctx, systemPrompt, userPrompt)
	if err != nil {
		errType := stats.ClassifyLLMError(err.Error())
		recordError(errType, err.Error(), "")
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 6. 解析AI响应
	decision, err := parseFullDecisionResponse(callResult.Content, ctx)
	if err != nil {
		errType := stats.ClassifyDecisionParseError(err.Error())
		recordError(errType, err.Error(), "")
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.UserPrompt = userPrompt
	decision.LLMCostUSDT = mcp.CalcCostUSDT(callResult.Model, callResult.Usage)
	decision.LLMUsage = &callResult.Usage

	// StrategyV only: re-fetch a near-real-time price after the LLM response.
	// Unsafe opens are downgraded to wait, preserving valid risk-reduction
	// decisions in the same batch. StrategyA/B return through the unchanged path.
	if isStrategyV(ctx) {
		for _, guardErr := range guardShortTermDecisionsAtRealtimePrice(decision.Decisions, ctx) {
			errType := stats.ClassifyDecisionValidateError(guardErr.Error())
			recordError(errType, guardErr.Error(), "")
		}
	}

	return decision, nil
}

func shouldSkipLLMNoMarketData(ctx *Context) bool {
	if ctx == nil {
		return false
	}
	if len(ctx.Positions) > 0 {
		return false
	}
	if len(ctx.CandidateCoins) > 0 {
		return false
	}
	if len(ctx.MarketDataMap) > 0 {
		return false
	}
	if isStrategyV(ctx) && len(ctx.ShortTermWatchlist) > 0 {
		return false
	}
	return true
}

func buildNoMarketDataSkipDecision(ctx *Context, strategyName string) *FullDecision {
	watchlistCount := 0
	if ctx != nil {
		watchlistCount = len(ctx.ShortTermWatchlist)
	}
	trace := fmt.Sprintf("system_skip:no_market_data\nstrategy=%s\nreason=empty_context\npositions=0\ncandidate_coins=0\nmarket_data_symbols=0\nwatchlist=%d\naction=wait\nllm_called=false", strategyName, watchlistCount)
	return &FullDecision{
		UserPrompt: "(system_skip:no_market_data)",
		CoTTrace:   trace,
		Decisions: []Decision{
			{
				Action:         ActionWait,
				Reasoning:      fmt.Sprintf("system_skip:no_market_data; empty_context; positions=0 candidate_coins=0 market_data_symbols=0 watchlist=%d; no_trade; wait next cycle", watchlistCount),
				DecisionSource: "system_skip_no_market_data",
			},
		},
		Timestamp: time.Now(),
	}
}

func isStrategyV(ctx *Context) bool {
	if ctx == nil || ctx.PromptStrategy == nil {
		return false
	}
	return ctx.PromptStrategy.Name() == "V"
}

func selectShortTermHeavySymbols(ctx *Context, symbolSet map[string]bool, maxCandidateHeavy int) map[string]bool {
	heavy := make(map[string]bool)
	if ctx == nil {
		return heavy
	}

	// 持仓标的必须给完整短线数据，用于判断持仓是否仍然有效。
	for _, pos := range ctx.Positions {
		if symbolSet[pos.Symbol] {
			heavy[pos.Symbol] = true
		}
	}

	// Watchlist 标的是上一轮待确认 setup，必须给完整短线数据。
	for _, item := range ctx.ShortTermWatchlist {
		if symbolSet[item.Symbol] {
			heavy[item.Symbol] = true
		}
	}

	// BTC/ETH 是短线市场环境的核心锚点；若本轮已抓取，则给完整上下文。
	for _, sym := range []string{"BTCUSDT", "ETHUSDT"} {
		if symbolSet[sym] {
			heavy[sym] = true
		}
	}

	// 只对前 N 个候选使用重数据，控制 token 体积和额外 1h K线 API 调用。
	if maxCandidateHeavy <= 0 {
		maxCandidateHeavy = 5
	}
	count := 0
	for _, coin := range ctx.CandidateCoins {
		if count >= maxCandidateHeavy {
			break
		}
		if !symbolSet[coin.Symbol] {
			continue
		}
		heavy[coin.Symbol] = true
		count++
	}

	return heavy
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
	aiResponse, err := callAI(ctx, systemPrompt, userPrompt)
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

// callAI 优先使用 ctx 中的 AI 客户端实例，否则回退到全局默认
func callAI(ctx *Context, systemPrompt, userPrompt string) (string, error) {
	if ctx.AI != nil {
		return ctx.AI.CallWithMessages(systemPrompt, userPrompt)
	}
	return mcp.CallWithMessagesGlobal(systemPrompt, userPrompt)
}

// callAIDetailed 同 callAI，但返回完整结果（含 token 用量）
func callAIDetailed(ctx *Context, systemPrompt, userPrompt string) (*mcp.CallResult, error) {
	if ctx.AI != nil {
		return ctx.AI.CallWithMessagesDetailed(systemPrompt, userPrompt)
	}
	return mcp.CallWithMessagesDetailedGlobal(systemPrompt, userPrompt)
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

	// StrategyV watchlist 标的强制进入本轮数据集，避免待确认 setup 因候选池排序变化丢失。
	if isStrategyV(ctx) {
		for _, item := range ctx.ShortTermWatchlist {
			if item.Symbol != "" {
				symbolSet[item.Symbol] = true
			}
		}
	}

	// 获取市场数据
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	shortTermMode := isStrategyV(ctx)
	shortTermHeavySymbols := selectShortTermHeavySymbols(ctx, symbolSet, 5)

	for symbol := range symbolSet {
		var data *market.Data
		var err error
		if shortTermMode {
			opt := market.FetchOptions{
				IntradayOutputPoints: 20,
			}
			// StrategyV 使用已闭合K线计算指标；重点标的额外提供完整 3m OHLCV + 1h 结构上下文。
			// 非重点标的仍只扩展到 20 个 3m 指标点，避免 prompt 和 API 调用膨胀。
			if shortTermHeavySymbols[symbol] {
				opt.IncludeIntradayOHLCV = true
				opt.IncludeMidTermContext = true
			}
			data, err = market.GetClosedWithOptions(symbol, opt)
		} else {
			// ✅ 非 StrategyV 继续使用轻量市场数据（默认10根3m序列），避免影响正在运行的 StrategyA/B。
			data, err = market.Get(symbol)
		}
		if err != nil {
			// 单个币种失败不影响整体，记录错误
			log.Printf("⚠️  获取 %s 市场数据失败: %v", symbol, err)
			continue
		}

		// ⚠️ 流动性过滤：持仓价值低于门槛的币种不做（多空都不做）
		// 持仓价值 = 持仓量 × 当前价格
		// 但现有持仓必须保留（需要决策是否平仓）
		isExistingPosition := positionSymbols[symbol]
		if !isExistingPosition && data.OpenInterest != nil && data.CurrentPrice > 0 {
			oiValue := data.OpenInterest.Latest * data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000
			minOI := ctx.MinOIValueMillions
			if minOI > 0 && oiValueInMillions < minOI {
				log.Printf("⚠️  %s 持仓价值过低(%.2fM USD < %.0fM)，跳过此币种 [持仓量:%.0f × 价格:%.4f]",
					symbol, oiValueInMillions, minOI, data.OpenInterest.Latest, data.CurrentPrice)
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

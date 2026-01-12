package decision

import (
	"fmt"
	"log"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"nofx/stats"
	"sync"
	"time"
)

// ===== OI采样缓存（用于构造“每3分钟一次”的OI变化序列）=====
// 注意：
// - Binance 官方 OI history 最小粒度通常为 5m/15m，而本项目扫描周期为 3 分钟
// - 这里采用“每周期采样 Latest OI”形成序列，适合做 prompt 的“OI变化/是否撤退”辅助判断
// - key 维度包含 traderID，避免多实例互相污染
var oiSeriesMu sync.Mutex
var oiSeriesByTrader = map[string]map[string][]float64{} // traderID -> symbol -> values

func appendOISample(traderID, symbol string, latestOI float64, maxLen int) []float64 {
	if maxLen <= 1 {
		maxLen = 20
	}
	oiSeriesMu.Lock()
	defer oiSeriesMu.Unlock()

	if traderID == "" {
		traderID = "default"
	}
	if _, ok := oiSeriesByTrader[traderID]; !ok {
		oiSeriesByTrader[traderID] = make(map[string][]float64)
	}
	seq := oiSeriesByTrader[traderID][symbol]
	seq = append(seq, latestOI)
	if len(seq) > maxLen {
		seq = seq[len(seq)-maxLen:]
	}
	oiSeriesByTrader[traderID][symbol] = seq
	// 返回副本，避免外部误改
	out := make([]float64, len(seq))
	copy(out, seq)
	return out
}

func calcDeltas(values []float64) []float64 {
	if len(values) < 2 {
		return nil
	}
	d := make([]float64, 0, len(values)-1)
	for i := 1; i < len(values); i++ {
		d = append(d, values[i]-values[i-1])
	}
	return d
}

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

	// ===== Top5重数据：只对Top5候选 + 所有持仓输出更长序列/3m OHLCV/1h结构/OI变化，避免prompt膨胀 =====
	// 注意：仅在 StrategyB/StrategyV 启用（策略A保持轻量，避免无意扩大prompt）
	heavyTopN := 0
	if ctx.PromptStrategy != nil {
		name := ctx.PromptStrategy.Name()
		if name == "B" || name == "V" {
			heavyTopN = 5
		}
	}

	heavySymbols := make(map[string]bool)
	if heavyTopN > 0 {
		// 1) 所有持仓币种都加入重数据（数量通常很少，但对风控很重要）
		for _, pos := range ctx.Positions {
			heavySymbols[pos.Symbol] = true
		}

		// 2) 候选TopN（按候选列表顺序；候选列表本身由池子排序输出）
		for i, coin := range ctx.CandidateCoins {
			if i >= heavyTopN {
				break
			}
			heavySymbols[coin.Symbol] = true
		}
	}

	// 获取市场数据
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for symbol := range symbolSet {
		var (
			data *market.Data
			err  error
		)

		// TopN候选 + 持仓：重数据（20根3m序列 + OHLCV + 1h结构）
		if heavySymbols[symbol] {
			data, err = market.GetWithOptions(symbol, market.FetchOptions{
				IntradayOutputPoints:  20,
				IncludeIntradayOHLCV:  true,
				IncludeMidTermContext: true,
			})
		} else {
			// 其他币：保持轻量（默认10根3m序列，避免prompt膨胀）
			data, err = market.Get(symbol)
		}
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

		// 注入OI采样序列（仅重数据币种）
		if heavySymbols[symbol] && data != nil && data.OpenInterest != nil && data.OpenInterest.Latest > 0 {
			seq := appendOISample(ctx.TraderID, symbol, data.OpenInterest.Latest, 20)
			if data.IntradaySeries != nil {
				data.IntradaySeries.OIValues = seq
				data.IntradaySeries.OIDeltaValues = calcDeltas(seq)
			}
		}

		ctx.MarketDataMap[symbol] = data
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

package decision

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"strings"
	"time"
)

const (
	shortTermMinConfidenceBase       = 70
	shortTermMinConfidenceCautious   = 75
	shortTermMinConfidenceDefensive  = 85
	shortTermMaxMarginForNewPosition = 70.0
	shortTermMaxRiskPctPerTrade      = 1.0
	shortTermMajorMaxNotionalEquity  = 2.0
	shortTermDefaultMinOIValueM      = 50.0
	shortTermHardStopLossPct         = -2.5
	shortTermTimeStopMinutes         = 90.0
	shortTermMaxChaseMoveATR         = 0.5
	shortTermMaxAbsoluteMoveATR      = 1.0
	shortTermMinChaseMovePct         = 0.001
	shortTermMinAbsoluteMovePct      = 0.002
)

// GenerateShortTermAutoDecisions 是 StrategyV 专用的自动风控层。
// 它先复用原有自动风控/自动止盈；只有原系统未触发时，才追加短线策略特有的
// 更紧止损、时间止损和高保证金降风险规则。该函数只由 StrategyV 调用，不改变 A/B。
func GenerateShortTermAutoDecisions(ctx *Context) []Decision {
	if decisions := GenerateAutoDecisions(ctx); len(decisions) > 0 {
		return decisions
	}
	if ctx == nil || len(ctx.Positions) == 0 {
		return nil
	}

	var decisions []Decision
	for _, pos := range ctx.Positions {
		closeAction := ActionCloseLong
		if strings.ToLower(pos.Side) == "short" {
			closeAction = ActionCloseShort
		}

		if pos.UnrealizedPnLPct <= shortTermHardStopLossPct {
			decisions = append(decisions, Decision{
				Symbol:         pos.Symbol,
				Action:         closeAction,
				Reasoning:      fmt.Sprintf("StrategyV短线硬止损：浮亏 %.2f%% <= %.2f%%，短线实验优先控制回撤，全部平仓", pos.UnrealizedPnLPct, shortTermHardStopLossPct),
				Confidence:     100,
				DecisionSource: "auto_stop_loss",
			})
			continue
		}

		if shortTermHoldingMinutes(pos) >= shortTermTimeStopMinutes && pos.UnrealizedPnLPct <= 0 {
			decisions = append(decisions, Decision{
				Symbol:         pos.Symbol,
				Action:         closeAction,
				Reasoning:      fmt.Sprintf("StrategyV时间止损：持仓 %.0f 分钟仍未产生正收益(%.2f%%)，短线setup未兑现，全部平仓", shortTermHoldingMinutes(pos), pos.UnrealizedPnLPct),
				Confidence:     100,
				DecisionSource: "auto_stop_loss",
			})
			continue
		}

		if ctx.Account.MarginUsedPct >= 75 && pos.UnrealizedPnLPct < -1.0 {
			decisions = append(decisions, Decision{
				Symbol:          pos.Symbol,
				Action:          ActionPartialClose,
				ClosePercentage: 50,
				Reasoning:       fmt.Sprintf("StrategyV高保证金降风险：保证金 %.1f%% 且该仓浮亏 %.2f%%，先减仓50%%降低短线回撤", ctx.Account.MarginUsedPct, pos.UnrealizedPnLPct),
				Confidence:      100,
				DecisionSource:  "auto_stop_loss",
			})
		}
	}

	return decisions
}

// ExtraValidateShortTerm 是 StrategyV 的策略级风控校验。
// 只挂在 StrategyV.ExtraValidate 上，不写入全局配置、不调用 SetMinRiskReward，因而不会影响其他策略。
func ExtraValidateShortTerm(d *Decision, ctx *Context) error {
	if d == nil || ctx == nil {
		return nil
	}

	// 自动风控/自动止盈是系统硬规则，不再被 LLM 最低持仓时间等约束反向拦截。
	if d.DecisionSource == "auto_stop_loss" || d.DecisionSource == "auto_take_profit" {
		return nil
	}

	if err := validateBTCDirectionConstraint(d, ctx); err != nil {
		return err
	}
	if err := validateMinHoldingTime(d, ctx); err != nil {
		return err
	}
	return validateShortTermOpenRisk(d, ctx)
}

func validateShortTermOpenRisk(d *Decision, ctx *Context) error {
	if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
		return nil
	}

	minConfidence := shortTermRequiredConfidence(ctx)
	if d.Confidence < minConfidence {
		return fmt.Errorf("StrategyV短线开仓信心度不足：当前 %d，最低要求 %d（根据近期表现/连亏动态调整）", d.Confidence, minConfidence)
	}

	if ctx.Account.MarginUsedPct >= shortTermMaxMarginForNewPosition {
		return fmt.Errorf("StrategyV短线禁止新增仓位：当前保证金使用率 %.1f%% >= %.1f%%", ctx.Account.MarginUsedPct, shortTermMaxMarginForNewPosition)
	}

	equity := ctx.Account.TotalEquity
	if equity > 0 {
		altcoinMaxPositionEquityMultiple := 2.0
		if ctx.AltcoinMaxPositionEquityMultiple > 0 {
			altcoinMaxPositionEquityMultiple = ctx.AltcoinMaxPositionEquityMultiple
		}
		isMajor := isShortTermMajorSymbol(d.Symbol)
		maxNotional := equity * altcoinMaxPositionEquityMultiple
		if isMajor {
			maxNotional = equity * shortTermMajorMaxNotionalEquity
		}
		if d.PositionSizeUSD > maxNotional {
			return fmt.Errorf("StrategyV短线单笔名义仓位过大：%.2f USDT > %.2f USDT（%s短线仓位上限）", d.PositionSizeUSD, maxNotional, d.Symbol)
		}

		maxRiskUSD := equity * shortTermMaxRiskPctPerTrade / 100.0
		if d.RiskUSD > 0 && d.RiskUSD > maxRiskUSD {
			return fmt.Errorf("StrategyV短线单笔风险过大：risk_usd %.2f > %.2f（净值 %.2f 的 %.2f%%）", d.RiskUSD, maxRiskUSD, equity, shortTermMaxRiskPctPerTrade)
		}
	}

	md := ctx.MarketDataMap[d.Symbol]
	if md == nil {
		return fmt.Errorf("StrategyV短线开仓缺少 %s 市场数据，禁止开仓", d.Symbol)
	}
	if md.IntradayATR14 <= 0 || md.IntradaySeries == nil || len(md.IntradaySeries.MidPrices) == 0 {
		return fmt.Errorf("StrategyV短线开仓缺少有效3m波动/序列数据，禁止开仓：%s", d.Symbol)
	}

	if !isShortTermMajorSymbol(d.Symbol) && md.OpenInterest != nil && md.CurrentPrice > 0 {
		minOIM := ctx.MinOIValueMillions
		if minOIM <= 0 {
			minOIM = shortTermDefaultMinOIValueM
		}
		oiValueM := md.OpenInterest.Latest * md.CurrentPrice / 1_000_000
		if oiValueM > 0 && oiValueM < minOIM {
			return fmt.Errorf("StrategyV短线流动性不足：%s OI名义价值 %.2fM < %.2fM，禁止开仓", d.Symbol, oiValueM, minOIM)
		}
	}

	if !shortTermReasoningHasSetup(d.Reasoning) {
		return fmt.Errorf("StrategyV短线开仓reasoning必须说明setup_type/why_now/time_stop/invalidation等短线要素，当前reasoning过于粗略")
	}

	return nil
}

func shortTermRequiredConfidence(ctx *Context) int {
	minConfidence := shortTermMinConfidenceBase
	perf, ok := ctx.Performance.(*logger.PerformanceAnalysis)
	if !ok || perf == nil {
		return minConfidence
	}
	if perf.CurrentLosingStreak >= 3 || perf.SharpeRatio < -0.5 {
		return shortTermMinConfidenceDefensive
	}
	if perf.CurrentLosingStreak >= 1 || perf.SharpeRatio < 0 {
		return shortTermMinConfidenceCautious
	}
	return minConfidence
}

func shortTermHoldingMinutes(pos PositionInfo) float64 {
	if pos.UpdateTime <= 0 {
		return 0
	}
	holdMs := time.Now().UnixMilli() - pos.UpdateTime
	if holdMs < 0 {
		return 0
	}
	return float64(holdMs) / 60000.0
}

func isShortTermMajorSymbol(symbol string) bool {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	return sym == "BTCUSDT" || sym == "ETHUSDT"
}

func shortTermReasoningHasSetup(reasoning string) bool {
	r := strings.ToLower(reasoning)
	if strings.TrimSpace(r) == "" {
		return false
	}
	setupTerms := []string{"setup_type", "trend_pullback", "breakout_momentum", "range_reversal", "exhaustion_reversal", "failed_breakout"}
	hasSetup := false
	for _, term := range setupTerms {
		if strings.Contains(r, term) {
			hasSetup = true
			break
		}
	}
	return hasSetup && strings.Contains(r, "why_now") && (strings.Contains(r, "time_stop") || strings.Contains(r, "time stop"))
}

// guardShortTermDecisionsAtRealtimePrice is called only by the online StrategyV
// decision path. Invalid or unverifiable opens are converted to wait so valid
// risk-reduction decisions in the same batch can still proceed.
func guardShortTermDecisionsAtRealtimePrice(decisions []Decision, ctx *Context) []error {
	var guardErrors []error
	for i := range decisions {
		d := &decisions[i]
		if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
			continue
		}

		livePrice, err := market.GetRealtimePrice(d.Symbol)
		if err == nil {
			err = validateShortTermExecutionPrice(d, ctx, livePrice)
		}
		if err != nil {
			guardErr := fmt.Errorf("决策 #%d %s: %w", i+1, d.Symbol, err)
			guardErrors = append(guardErrors, guardErr)
			convertShortTermOpenToWait(d, guardErr.Error())
			continue
		}

		if md := ctx.MarketDataMap[d.Symbol]; md != nil {
			md.CurrentPrice = livePrice
		}
	}
	return guardErrors
}

func convertShortTermOpenToWait(d *Decision, guardReason string) {
	if d == nil {
		return
	}
	originalAction := d.Action
	originalReasoning := strings.TrimSpace(d.Reasoning)
	d.Action = ActionWait
	d.Leverage = 0
	d.OriginalLeverage = 0
	d.PositionSizeUSD = 0
	d.StopLoss = 0
	d.TakeProfit = 0
	d.NewStopLoss = 0
	d.NewTakeProfit = 0
	d.ClosePercentage = 0
	d.Confidence = 0
	d.RiskUSD = 0
	d.Reasoning = fmt.Sprintf("execution_price_guard: %s; original_action=%s", guardReason, originalAction)
	if originalReasoning != "" {
		d.Reasoning += "; original_reasoning=" + originalReasoning
	}
}

func validateShortTermExecutionPrice(d *Decision, ctx *Context, livePrice float64) error {
	if d == nil || ctx == nil {
		return fmt.Errorf("StrategyV执行前价格校验缺少决策或上下文")
	}
	if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
		return nil
	}
	if livePrice <= 0 {
		return fmt.Errorf("StrategyV执行前实时价格无效: %.8f", livePrice)
	}

	md := ctx.MarketDataMap[d.Symbol]
	if md == nil || md.CurrentPrice <= 0 || md.IntradayATR14 <= 0 {
		return fmt.Errorf("StrategyV执行前价格校验缺少有效快照价格或3m ATR: %s", d.Symbol)
	}

	snapshotPrice := md.CurrentPrice
	atr := md.IntradayATR14
	absoluteMove := math.Abs(livePrice - snapshotPrice)
	maxAbsoluteMove := math.Max(shortTermMaxAbsoluteMoveATR*atr, shortTermMinAbsoluteMovePct*snapshotPrice)
	if absoluteMove > maxAbsoluteMove {
		return fmt.Errorf("StrategyV行情快照已过期：分析价 %.8f -> 实时价 %.8f，偏移 %.8f > %.8f（max(%.1f×3m ATR, %.2f%%)），需要重新分析", snapshotPrice, livePrice, absoluteMove, maxAbsoluteMove, shortTermMaxAbsoluteMoveATR, shortTermMinAbsoluteMovePct*100)
	}

	chaseMove := 0.0
	if d.Action == ActionOpenLong {
		chaseMove = livePrice - snapshotPrice
	} else {
		chaseMove = snapshotPrice - livePrice
	}
	maxChaseMove := math.Max(shortTermMaxChaseMoveATR*atr, shortTermMinChaseMovePct*snapshotPrice)
	if chaseMove > maxChaseMove {
		return fmt.Errorf("StrategyV拒绝追价：分析价 %.8f -> 实时价 %.8f，追价偏移 %.8f > %.8f（max(%.1f×3m ATR, %.2f%%)）", snapshotPrice, livePrice, chaseMove, maxChaseMove, shortTermMaxChaseMoveATR, shortTermMinChaseMovePct*100)
	}

	var riskPercent, rewardPercent float64
	if d.Action == ActionOpenLong {
		if d.StopLoss >= livePrice || d.TakeProfit <= livePrice {
			return fmt.Errorf("StrategyV实时价格已超出有效多单区间：stop_loss %.8f < live %.8f < take_profit %.8f 不成立", d.StopLoss, livePrice, d.TakeProfit)
		}
		riskPercent = (livePrice - d.StopLoss) / livePrice * 100
		rewardPercent = (d.TakeProfit - livePrice) / livePrice * 100
	} else {
		if d.TakeProfit >= livePrice || d.StopLoss <= livePrice {
			return fmt.Errorf("StrategyV实时价格已超出有效空单区间：take_profit %.8f < live %.8f < stop_loss %.8f 不成立", d.TakeProfit, livePrice, d.StopLoss)
		}
		riskPercent = (d.StopLoss - livePrice) / livePrice * 100
		rewardPercent = (livePrice - d.TakeProfit) / livePrice * 100
	}
	if riskPercent <= 0 || rewardPercent <= 0 {
		return fmt.Errorf("StrategyV实时价格下无法计算有效RR：risk=%.4f%% reward=%.4f%%", riskPercent, rewardPercent)
	}

	taker := 0.0004
	slippage := 0.0005
	if ctx.AssumedTakerFeeRate >= 0 {
		taker = ctx.AssumedTakerFeeRate
	}
	if ctx.AssumedSlippageRate >= 0 {
		slippage = ctx.AssumedSlippageRate
	}
	leverage := float64(d.Leverage)
	if leverage < 1 {
		leverage = 1
	}
	roundTripCostROIPct := 2.0 * (taker + slippage) * leverage * 100.0
	netRisk := riskPercent*leverage + roundTripCostROIPct
	netReward := rewardPercent*leverage - roundTripCostROIPct
	if netReward <= 0 {
		return fmt.Errorf("StrategyV实时价格下扣除成本后预期收益<=0：净收益 %.4f%%，成本 %.4f%%", netReward, roundTripCostROIPct)
	}

	netRR := netReward / netRisk
	if netRR < minRiskReward {
		return fmt.Errorf("StrategyV实时价格下净RR降至 %.2f:1，低于最低 %.2f:1（live=%.8f, SL=%.8f, TP=%.8f, cost=%.4f%%）", netRR, minRiskReward, livePrice, d.StopLoss, d.TakeProfit, roundTripCostROIPct)
	}
	return nil
}

package decision

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"regexp"
	"strconv"
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

	shortTermMajorMinStopPct       = 0.0012
	shortTermMajorMinStopATR       = 0.30
	shortTermAltMinStopPct         = 0.0020
	shortTermAltMinStopATR         = 0.40
	shortTermMajorMaxStopPct       = 0.0060
	shortTermMajorMaxStopATR       = 1.50
	shortTermAltMaxStopPct         = 0.0100
	shortTermAltMaxStopATR         = 1.80
	shortTermMaxStopAdjustmentRate = 1.33

	// StrategyV 独立于全局 min_hold_minutes：短线需要在突破失败时尽早退出，只拦截开仓后几根K线内的噪声平仓。
	shortTermMinHoldMinutes = 8

	shortTermOtherSetupConfidenceBonus = 5
	shortTermOtherSetupMaxRiskPct      = 0.5
)

var (
	shortTermStopAdjustmentPattern = regexp.MustCompile(`距离 ([0-9]+(?:\.[0-9]+)?)→([0-9]+(?:\.[0-9]+)?)`)
	shortTermSetupTypePattern      = regexp.MustCompile(`setup_type\s*[=:：]\s*([a-z_]+)`)
)

// ShortTermAutoTakeProfitConfig 是 StrategyV 专用的自动止盈/trailing 档位。
// 阈值为净ROI%（价格变动%×杠杆-往返成本%），按 V 的 0.12%–1.0% 短线止损尺度设计；
// trailing 距离必须小于 V 的最大初始止损，避免 TP2 后回吐超过初始风险。
func ShortTermAutoTakeProfitConfig() AutoTakeProfitConfig {
	return AutoTakeProfitConfig{
		Stage0Threshold:      5.0,
		Stage0ClosePct:       30.0,
		Stage1Threshold:      8.0,
		Stage1ClosePct:       30.0,
		FullCloseThreshold:   12.0,
		CooldownMinutes:      6,
		BreakevenEnabled:     true,
		BreakevenFloorUSDT:   0,
		TrailingEnabled:      true,
		TrailingDistancePct:  0.6,
		TrailingMinUpdatePct: 0.10,
		Major: &AutoTakeProfitConfig{
			Stage0Threshold:      3.0,
			Stage0ClosePct:       30.0,
			Stage1Threshold:      5.0,
			Stage1ClosePct:       30.0,
			FullCloseThreshold:   8.0,
			CooldownMinutes:      6,
			BreakevenEnabled:     true,
			BreakevenFloorUSDT:   0,
			TrailingEnabled:      true,
			TrailingDistancePct:  0.35,
			TrailingMinUpdatePct: 0.05,
		},
	}
}

// GenerateShortTermAutoDecisions 是 StrategyV 专用的自动风控层。
// 它先复用原有自动风控/自动止盈；只有原系统未触发时，才追加短线策略特有的
// 更紧止损、时间止损和高保证金降风险规则。该函数只由 StrategyV 调用，不改变 A/B。
func GenerateShortTermAutoDecisions(ctx *Context) []Decision {
	applyShortTermStopLossDistanceConfig(ctx)
	if ctx != nil {
		ctx.AutoTakeProfit = ShortTermAutoTakeProfitConfig()
	}

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

// applyShortTermStopLossDistanceConfig installs a StrategyV-only stop floor.
// It deliberately excludes the 4h volatility floor because StrategyV trades a
// 5-90 minute horizon. StrategyA/B keep their existing configuration untouched.
func applyShortTermStopLossDistanceConfig(ctx *Context) {
	if ctx == nil {
		return
	}
	ctx.StopLossDistance = shortTermStopLossDistanceConfig()
}

func shortTermStopLossDistanceConfig() StopLossDistanceConfig {
	return StopLossDistanceConfig{
		MajorMinPct:  shortTermMajorMinStopPct,
		MajorATRMult: shortTermMajorMinStopATR,
		MajorVolMult: 0,
		AltMinPct:    shortTermAltMinStopPct,
		AltATRMult:   shortTermAltMinStopATR,
		AltVolMult:   0,
	}
}

// ExtraValidateShortTerm 是 StrategyV 的策略级风控校验。
// 只挂在 StrategyV.ExtraValidate 上，不写入全局配置，因而不会影响其他策略。
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
	if err := validateMinHoldingTimeWith(d, ctx, shortTermMinHoldMinutes); err != nil {
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

	}

	md := ctx.MarketDataMap[d.Symbol]
	if md == nil {
		return fmt.Errorf("StrategyV短线开仓缺少 %s 市场数据，禁止开仓", d.Symbol)
	}
	if md.IntradayATR14 <= 0 || md.IntradaySeries == nil || len(md.IntradaySeries.MidPrices) == 0 {
		return fmt.Errorf("StrategyV短线开仓缺少有效3m波动/序列数据，禁止开仓：%s", d.Symbol)
	}

	if err := validateShortTermStopGeometry(d, md); err != nil {
		return err
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

	if shortTermSetupType(d) == "other" {
		if err := validateShortTermOtherSetup(d, minConfidence); err != nil {
			return err
		}
	}

	return checkShortTermRiskBudget(d, ctx, md.CurrentPrice)
}

// shortTermSetupType 优先取结构化字段，其次从 reasoning 的 setup_type=xxx 解析。
func shortTermSetupType(d *Decision) string {
	if d == nil {
		return ""
	}
	if s := strings.ToLower(strings.TrimSpace(d.SetupType)); s != "" {
		return s
	}
	if m := shortTermSetupTypePattern.FindStringSubmatch(strings.ToLower(d.Reasoning)); len(m) == 2 {
		return m[1]
	}
	return ""
}

func validateShortTermOtherSetup(d *Decision, minConfidence int) error {
	required := minConfidence + shortTermOtherSetupConfidenceBonus
	if d.Confidence < required {
		return fmt.Errorf("StrategyV setup_type=other 需信心度 >= %d（当前 %d）", required, d.Confidence)
	}
	r := strings.ToLower(d.Reasoning)
	if !strings.Contains(r, "setup_logic") || !strings.Contains(r, "invalidation") {
		return fmt.Errorf("StrategyV setup_type=other 必须在 reasoning 中写明 setup_logic 与 invalidation_condition")
	}

	return nil
}

func validateShortTermStopGeometry(d *Decision, md *market.Data) error {
	if d == nil || md == nil || md.CurrentPrice <= 0 || md.IntradayATR14 <= 0 {
		return nil
	}

	entryPrice := md.CurrentPrice
	stopDist := 0.0
	if d.Action == ActionOpenLong {
		stopDist = entryPrice - d.StopLoss
	} else {
		stopDist = d.StopLoss - entryPrice
	}
	if stopDist <= 0 {
		return fmt.Errorf("StrategyV止损与入场价不匹配：entry=%.4f stop=%.4f", entryPrice, d.StopLoss)
	}

	maxPct := shortTermAltMaxStopPct
	maxATR := shortTermAltMaxStopATR
	if isShortTermMajorSymbol(d.Symbol) {
		maxPct = shortTermMajorMaxStopPct
		maxATR = shortTermMajorMaxStopATR
	}
	maxStopDist := math.Max(entryPrice*maxPct, md.IntradayATR14*maxATR)
	if stopDist > maxStopDist {
		return fmt.Errorf("StrategyV短线止损过宽：distance=%.4f > max=%.4f（max(%.2f%%, %.2f×3m ATR)），拒绝把短线setup改造成更长周期交易", stopDist, maxStopDist, maxPct*100, maxATR)
	}

	matches := shortTermStopAdjustmentPattern.FindStringSubmatch(d.Reasoning)
	if len(matches) == 3 {
		originalDist, err1 := strconv.ParseFloat(matches[1], 64)
		adjustedDist, err2 := strconv.ParseFloat(matches[2], 64)
		if err1 == nil && err2 == nil && originalDist > 0 {
			adjustmentRate := adjustedDist / originalDist
			if adjustmentRate > shortTermMaxStopAdjustmentRate+1e-9 {
				return fmt.Errorf("StrategyV止损需要拉宽 %.2f 倍，超过允许上限 %.2f 倍；结构失效位过近，应等待更好入场而不是改写setup", adjustmentRate, shortTermMaxStopAdjustmentRate)
			}
		}
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

func shortTermConfidenceModeLabel(minConfidence int) string {
	switch minConfidence {
	case shortTermMinConfidenceDefensive:
		return "防守模式"
	case shortTermMinConfidenceCautious:
		return "谨慎模式"
	default:
		return "正常模式"
	}
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
	setupTerms := []string{"setup_type", "trend_pullback", "breakout_momentum", "range_reversal", "exhaustion_reversal", "failed_breakout", "other"}
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
		if d.ValidationError != "" || (d.Action != ActionOpenLong && d.Action != ActionOpenShort) {
			continue
		}

		livePrice, err := market.GetRealtimePrice(d.Symbol)
		if err == nil {
			d.RealtimeTargetMetrics = MeasureShortTermTarget(d, ctx, livePrice)
			err = ValidateShortTermExecutionPrice(d, ctx, livePrice)
		}
		if err != nil {
			guardErr := fmt.Errorf("决策 #%d %s: %w", i+1, d.Symbol, err)
			guardErrors = append(guardErrors, guardErr)
			convertShortTermOpenToWait(d, guardErr.Error())
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

// ValidateShortTermExecutionPrice checks a StrategyV entry against its original market snapshot.
func ValidateShortTermExecutionPrice(d *Decision, ctx *Context, livePrice float64) error {
	if d == nil || ctx == nil {
		return fmt.Errorf("StrategyV执行前价格校验缺少决策或上下文")
	}
	if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
		return nil
	}
	if !(livePrice > 0) || math.IsInf(livePrice, 0) {
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
	stopConfig := shortTermStopLossDistanceConfig()
	minStopDist := stopLossMinDistance(d.Symbol, livePrice, md, &stopConfig)
	stopDist := math.Abs(livePrice - d.StopLoss)
	if stopDist+livePrice*1e-12 < minStopDist {
		return fmt.Errorf("StrategyV实时止损距离过近：%s 实时价 %.8f 止损 %.8f 距离 %.8f < 最小距离 %.8f", d.Symbol, livePrice, d.StopLoss, stopDist, minStopDist)
	}

	invalidationPrice := d.InvalidationPrice
	if invalidationPrice > 0 && ((d.Action == ActionOpenLong && livePrice <= invalidationPrice) ||
		(d.Action == ActionOpenShort && livePrice >= invalidationPrice)) {
		return fmt.Errorf("StrategyV实时入场条件失效：%s 实时价 %.8f 已收回关键价位 %.8f", d.Symbol, livePrice, invalidationPrice)
	}
	if shortTermSetupType(d) == "breakout_momentum" && d.TriggerPrice > 0 &&
		((d.Action == ActionOpenLong && livePrice <= d.TriggerPrice) || (d.Action == ActionOpenShort && livePrice >= d.TriggerPrice)) {
		return fmt.Errorf("StrategyV实时突破失效：%s 实时价 %.8f 已收回突破位 %.8f", d.Symbol, livePrice, d.TriggerPrice)
	}

	leverage := float64(d.Leverage)
	if leverage < 1 {
		leverage = 1
	}
	roundTripCostROIPct := shortTermRoundTripCost(ctx) * leverage * 100.0
	netRisk := riskPercent*leverage + roundTripCostROIPct
	netReward := rewardPercent*leverage - roundTripCostROIPct
	if netReward <= 0 {
		return fmt.Errorf("StrategyV实时价格下扣除成本后预期收益<=0：净收益 %.4f%%，成本 %.4f%%", netReward, roundTripCostROIPct)
	}

	netRR := netReward / netRisk
	if minRR := minRiskRewardFor(ctx); netRR < minRR {
		return fmt.Errorf("StrategyV实时价格下净RR降至 %.2f:1，低于最低 %.2f:1（live=%.8f, SL=%.8f, TP=%.8f, cost=%.4f%%）", netRR, minRR, livePrice, d.StopLoss, d.TakeProfit, roundTripCostROIPct)
	}
	return checkShortTermRiskBudget(d, ctx, livePrice)
}

func shortTermRoundTripCost(ctx *Context) float64 {
	taker := defaultAssumedTakerFeeRate
	slippage := defaultAssumedSlippageRate
	if ctx.AssumedTakerFeeRate >= 0 {
		taker = ctx.AssumedTakerFeeRate
	}
	if ctx.AssumedSlippageRate >= 0 {
		slippage = ctx.AssumedSlippageRate
	}
	return 2 * (taker + slippage)
}

// Nominal size already includes leverage. Include fees and slippage on both legs.
func checkShortTermRiskBudget(d *Decision, ctx *Context, entry float64) error {
	if !(ctx.Account.TotalEquity > 0 && entry > 0 && d.StopLoss > 0 && d.PositionSizeUSD > 0) {
		return fmt.Errorf("StrategyV缺少有效净值、仓位或价格，无法核验单笔风险")
	}
	limitPct := shortTermMaxRiskPctPerTrade
	if shortTermSetupType(d) == "other" {
		limitPct = shortTermOtherSetupMaxRiskPct
	}
	risk := d.PositionSizeUSD * (math.Abs(entry-d.StopLoss)/entry + shortTermRoundTripCost(ctx))
	limit := ctx.Account.TotalEquity * limitPct / 100
	if !(risk <= limit) {
		return fmt.Errorf("StrategyV单笔风险过大：按入场价和成本复算 %.4f > %.4f USDT（净值的 %.2f%%）", risk, limit, limitPct)
	}
	d.RiskUSD = risk
	return nil
}

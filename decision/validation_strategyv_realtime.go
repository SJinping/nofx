package decision

import (
	"fmt"
	"math"
	"nofx/market"
	"strings"
)

const (
	// Reject entries that moved materially after the market snapshot used by the
	// LLM. Directional movement has a tighter threshold to prevent chasing; a
	// larger absolute threshold catches both favorable and adverse regime changes.
	shortTermMaxChaseMoveATR    = 0.5
	shortTermMaxAbsoluteMoveATR = 1.0
	shortTermMinChaseMovePct    = 0.001 // 0.10% floor for very quiet markets
	shortTermMinAbsoluteMovePct = 0.002 // 0.20% floor for very quiet markets
)

// Kept as a variable so the batch guard can be tested without network access.
// Production always uses Binance's live ticker through the market package.
var shortTermRealtimePriceFetcher = market.GetRealtimePrice

// guardShortTermDecisionsAtRealtimePrice is called only by the online
// StrategyV decision path, after JSON parsing and normal validation, and before
// the decision is returned to the execution layer. Invalid or unverifiable open
// decisions are converted to wait, so they fail closed without blocking valid
// close/partial-close/risk-management decisions in the same batch.
func guardShortTermDecisionsAtRealtimePrice(decisions []Decision, ctx *Context) []error {
	var guardErrors []error
	for i := range decisions {
		d := &decisions[i]
		if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
			continue
		}

		livePrice, err := shortTermRealtimePriceFetcher(d.Symbol)
		if err == nil {
			err = validateShortTermExecutionPrice(d, ctx, livePrice)
		}
		if err != nil {
			guardErr := fmt.Errorf("决策 #%d %s: %w", i+1, d.Symbol, err)
			guardErrors = append(guardErrors, guardErr)
			convertShortTermOpenToWait(d, guardErr.Error())
			continue
		}

		// Keep downstream TradeMemory/OpenGuard checks aligned with the price that
		// just passed the execution-time validation.
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

// validateShortTermExecutionPrice is pure apart from reading ctx and can be
// unit-tested with a supplied live price.
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
		return fmt.Errorf(
			"StrategyV行情快照已过期：分析价 %.8f -> 实时价 %.8f，偏移 %.8f > %.8f（max(%.1f×3m ATR, %.2f%%)），需要重新分析",
			snapshotPrice, livePrice, absoluteMove, maxAbsoluteMove,
			shortTermMaxAbsoluteMoveATR, shortTermMinAbsoluteMovePct*100,
		)
	}

	chaseMove := 0.0
	if d.Action == ActionOpenLong {
		chaseMove = livePrice - snapshotPrice
	} else {
		chaseMove = snapshotPrice - livePrice
	}
	maxChaseMove := math.Max(shortTermMaxChaseMoveATR*atr, shortTermMinChaseMovePct*snapshotPrice)
	if chaseMove > maxChaseMove {
		return fmt.Errorf(
			"StrategyV拒绝追价：分析价 %.8f -> 实时价 %.8f，追价偏移 %.8f > %.8f（max(%.1f×3m ATR, %.2f%%)）",
			snapshotPrice, livePrice, chaseMove, maxChaseMove,
			shortTermMaxChaseMoveATR, shortTermMinChaseMovePct*100,
		)
	}

	var riskPercent, rewardPercent float64
	if d.Action == ActionOpenLong {
		if d.StopLoss >= livePrice || d.TakeProfit <= livePrice {
			return fmt.Errorf(
				"StrategyV实时价格已超出有效多单区间：stop_loss %.8f < live %.8f < take_profit %.8f 不成立",
				d.StopLoss, livePrice, d.TakeProfit,
			)
		}
		riskPercent = (livePrice - d.StopLoss) / livePrice * 100
		rewardPercent = (d.TakeProfit - livePrice) / livePrice * 100
	} else {
		if d.TakeProfit >= livePrice || d.StopLoss <= livePrice {
			return fmt.Errorf(
				"StrategyV实时价格已超出有效空单区间：take_profit %.8f < live %.8f < stop_loss %.8f 不成立",
				d.TakeProfit, livePrice, d.StopLoss,
			)
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
		return fmt.Errorf(
			"StrategyV实时价格下净RR降至 %.2f:1，低于最低 %.2f:1（live=%.8f, SL=%.8f, TP=%.8f, cost=%.4f%%）",
			netRR, minRiskReward, livePrice, d.StopLoss, d.TakeProfit, roundTripCostROIPct,
		)
	}
	return nil
}

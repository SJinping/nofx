package decision

import "strings"

const (
	defaultAssumedTakerFeeRate = 0.0004
	defaultAssumedSlippageRate = 0.0005
)

// NetRiskRewardEstimate describes a code-calculated, cost-adjusted RR estimate.
// Percent fields are in margin ROI terms after leverage where noted.
type NetRiskRewardEstimate struct {
	EntryPrice          float64
	EntryPriceSource    string // market_data / fallback
	RawRiskPct          float64
	RawRewardPct        float64
	RoundTripCostROIPct float64
	NetRiskPct          float64
	NetRewardPct        float64
	NetRiskRewardRatio  float64
	AssumedTakerFeeRate float64
	AssumedSlippageRate float64
	Leverage            float64
	Valid               bool
	InvalidReason       string
}

// EstimateDecisionNetRiskReward calculates the net risk/reward ratio for an open decision
// from structured decision fields and current runtime context. It intentionally does not
// trust any RR number the LLM may have written in reasoning.
func EstimateDecisionNetRiskReward(ctx *Context, dec *Decision) NetRiskRewardEstimate {
	if dec == nil {
		return NetRiskRewardEstimate{InvalidReason: "nil_decision"}
	}
	return EstimateNetRiskReward(ctx, dec.Symbol, dec.Action, dec.StopLoss, dec.TakeProfit, dec.Leverage)
}

// EstimateNetRiskReward calculates cost-adjusted net RR using the current market price
// when available, otherwise a deterministic SL/TP fallback entry price.
func EstimateNetRiskReward(ctx *Context, symbol, action string, stopLoss, takeProfit float64, leverage int) NetRiskRewardEstimate {
	est := NetRiskRewardEstimate{}
	if stopLoss <= 0 || takeProfit <= 0 {
		est.InvalidReason = "invalid_stop_loss_or_take_profit"
		return est
	}

	entryPrice, entrySource := estimateEntryPrice(ctx, symbol, action, stopLoss, takeProfit)
	if entryPrice <= 0 {
		est.InvalidReason = "invalid_entry_price"
		return est
	}
	est.EntryPrice = entryPrice
	est.EntryPriceSource = entrySource

	var riskPercent, rewardPercent float64
	if action == ActionOpenLong {
		riskPercent = (entryPrice - stopLoss) / entryPrice * 100
		rewardPercent = (takeProfit - entryPrice) / entryPrice * 100
	} else {
		riskPercent = (stopLoss - entryPrice) / entryPrice * 100
		rewardPercent = (entryPrice - takeProfit) / entryPrice * 100
	}
	est.RawRiskPct = riskPercent
	est.RawRewardPct = rewardPercent
	if riskPercent <= 0 || rewardPercent <= 0 {
		est.InvalidReason = "invalid_risk_or_reward"
		return est
	}

	taker := defaultAssumedTakerFeeRate
	slippage := defaultAssumedSlippageRate
	if ctx != nil {
		if ctx.AssumedTakerFeeRate >= 0 {
			taker = ctx.AssumedTakerFeeRate
		}
		if ctx.AssumedSlippageRate >= 0 {
			slippage = ctx.AssumedSlippageRate
		}
	}
	L := float64(leverage)
	if L <= 0 {
		L = 1
	}
	est.AssumedTakerFeeRate = taker
	est.AssumedSlippageRate = slippage
	est.Leverage = L
	est.RoundTripCostROIPct = 2.0 * (taker + slippage) * L * 100.0
	est.NetRiskPct = riskPercent*L + est.RoundTripCostROIPct
	est.NetRewardPct = rewardPercent*L - est.RoundTripCostROIPct
	if est.NetRiskPct <= 0 || est.NetRewardPct <= 0 {
		est.InvalidReason = "non_positive_net_risk_or_reward"
		return est
	}
	est.NetRiskRewardRatio = est.NetRewardPct / est.NetRiskPct
	est.Valid = true
	return est
}

func estimateEntryPrice(ctx *Context, symbol, action string, stopLoss, takeProfit float64) (float64, string) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if ctx != nil && ctx.MarketDataMap != nil {
		if md := ctx.MarketDataMap[sym]; md != nil && md.CurrentPrice > 0 {
			return md.CurrentPrice, "market_data"
		}
	}
	if action == ActionOpenLong {
		return stopLoss + (takeProfit-stopLoss)*0.2, "fallback"
	}
	return stopLoss - (stopLoss-takeProfit)*0.2, "fallback"
}

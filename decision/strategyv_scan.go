package decision

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"sort"
	"time"
)

func evaluateFlatScan(ctx *Context) *logger.StrategyVFlatScan {
	if len(ctx.Positions) > 0 || ctx.DisableStrategyVFlatScanFilter {
		return nil
	}
	if ctx.PositionsUnconfirmed {
		return &logger.StrategyVFlatScan{Reason: "positions_unconfirmed"}
	}
	result := &logger.StrategyVFlatScan{Reason: "signal"}
	incomplete := ctx.flatScanDataIncomplete
	for symbol, data := range ctx.MarketDataMap {
		long, short, usable := shortTermScanSignals(data)
		if !usable {
			incomplete = incomplete || isShortTermHeavyData(data)
			continue
		}
		result.Checked++
		if long {
			result.Matches = append(result.Matches, symbol+":long")
		}
		if short {
			result.Matches = append(result.Matches, symbol+":short")
		}
	}
	sort.Strings(result.Matches)
	switch {
	case len(result.Matches) > 0:
	case len(ctx.ShortTermWatchlist) > 0:
		// Existing plans can depend on a custom trigger or invalidation that
		// these two generic patterns do not express.
		result.Reason = "watchlist"
	case len(ctx.MarketDataMap) > 0 && (incomplete || result.Checked == 0):
		result.Reason = "incomplete_scan_data"
	default:
		result.Skipped = true
		result.Reason = "no_signal"
		if len(ctx.MarketDataMap) == 0 {
			result.Reason = "no_market_data"
		}
	}
	return result
}

func shortTermScanSignals(data *market.Data) (long, short, usable bool) {
	if data == nil || data.IntradaySeries == nil || data.MidTermContext == nil {
		return false, false, false
	}
	if targets := data.ShortTermTargets; targets != nil && !targets.Fresh(time.Now()) {
		return false, false, false
	}
	b := data.IntradaySeries
	n := len(b.Closes)
	macd := data.MidTermContext.MACDValues
	if n < 11 || len(b.Highs) != n || len(b.Lows) != n || len(b.Volumes) != n ||
		len(b.EMA20Values) != n || len(macd) < 2 {
		return false, false, false
	}
	high, low, volume := 0.0, math.Inf(1), 0.0
	for i := n - 11; i < n; i++ {
		if !(b.Lows[i] > 0 && b.Lows[i] <= b.Closes[i] && b.Closes[i] <= b.Highs[i] && b.Volumes[i] >= 0) ||
			math.IsInf(b.Highs[i], 0) || math.IsInf(b.Volumes[i], 0) {
			return false, false, false
		}
		if i == n-1 {
			break
		}
		high = math.Max(high, b.Highs[i])
		low = math.Min(low, b.Lows[i])
		volume += b.Volumes[i]
	}
	k := n - 1
	if !(high >= low && low > 0 && b.Closes[k] > 0 && b.EMA20Values[k] > 0 && volume > 0) {
		return false, false, false
	}
	loud := b.Volumes[k] >= 1.2*(volume/10)
	up := macd[len(macd)-1] > macd[len(macd)-2] &&
		b.Lows[k-2] < b.Lows[k-1] && b.Lows[k-1] < b.Lows[k] &&
		b.Closes[k-2] < b.Closes[k-1] && b.Closes[k-1] < b.Closes[k] &&
		b.Closes[k] > b.EMA20Values[k]
	down := macd[len(macd)-1] < macd[len(macd)-2] &&
		b.Highs[k-2] > b.Highs[k-1] && b.Highs[k-1] > b.Highs[k] &&
		b.Closes[k-2] > b.Closes[k-1] && b.Closes[k-1] > b.Closes[k] &&
		b.Closes[k] < b.EMA20Values[k]
	return loud && b.Closes[k] > high || up, loud && b.Closes[k] < low || down, true
}

func flatScanSkipDecision(scan *logger.StrategyVFlatScan) *FullDecision {
	const source = "system_skip_strategy_v_flat_scan"
	reason := fmt.Sprintf("%s: %s, checked_symbols=%d, llm_called=false", source, scan.Reason, scan.Checked)
	return &FullDecision{
		UserPrompt: "(system_skip:strategy_v_flat_scan)",
		CoTTrace:   reason,
		Decisions:  []Decision{{Action: ActionWait, DecisionSource: source, Reasoning: reason}},
		Timestamp:  time.Now(),
	}
}

func MeasureShortTermTarget(d *Decision, ctx *Context, entry float64) *market.ShortTermTargetMetrics {
	if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
		return nil
	}
	return ctx.MarketDataMap[d.Symbol].MeasureShortTermTarget(entry, d.StopLoss, d.TakeProfit, shortTermRoundTripCost(ctx), d.Action == ActionOpenLong)
}

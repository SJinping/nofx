package decision

import "nofx/stats"

// Action 交易动作常量
const (
	ActionOpenLong         = "open_long"
	ActionOpenShort        = "open_short"
	ActionCloseLong        = "close_long"
	ActionCloseShort       = "close_short"
	ActionHold             = "hold"
	ActionWait             = "wait"
	ActionUpdateStopLoss   = "update_stop_loss"
	ActionUpdateTakeProfit = "update_take_profit"
	ActionPartialClose     = "partial_close"
)

// ValidActions 所有合法的交易动作
var ValidActions = map[string]bool{
	ActionOpenLong:         true,
	ActionOpenShort:        true,
	ActionCloseLong:        true,
	ActionCloseShort:       true,
	ActionHold:             true,
	ActionWait:             true,
	ActionUpdateStopLoss:   true,
	ActionUpdateTakeProfit: true,
	ActionPartialClose:     true,
}

// ActionsNeedingSymbol 需要提供symbol的交易动作
var ActionsNeedingSymbol = map[string]bool{
	ActionOpenLong:         true,
	ActionOpenShort:        true,
	ActionCloseLong:        true,
	ActionCloseShort:       true,
	ActionUpdateStopLoss:   true,
	ActionUpdateTakeProfit: true,
	ActionPartialClose:     true,
}

// 当前使用的错误统计实例（由外部设置）
var currentErrorStats *stats.ErrorStats
var currentCycleNum int

// SetErrorStats 设置当前使用的错误统计实例
func SetErrorStats(es *stats.ErrorStats, cycleNum int) {
	currentErrorStats = es
	currentCycleNum = cycleNum
}

// recordError 记录错误到统计
func recordError(errType stats.ErrorType, message string, symbol string) {
	if currentErrorStats != nil {
		currentErrorStats.RecordError(errType, message, symbol, currentCycleNum)
	}
}

// DefaultMinRiskReward 是 trader 未配置 min_risk_reward 时使用的最小净风险回报比。
const DefaultMinRiskReward = 2.2

var minPositionSizeUSD float64 = 20.0 // 最小持仓，防止手续费磨损

// minRiskRewardFor 返回当前 trader 的最小净风险回报比。
// 门槛随 Context 注入而不是存为包级变量，避免同一进程内多个 trader 互相覆盖。
func minRiskRewardFor(ctx *Context) float64 {
	if ctx != nil && ctx.MinRiskReward > 0 {
		return ctx.MinRiskReward
	}
	return DefaultMinRiskReward
}


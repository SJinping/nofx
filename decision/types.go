package decision

import (
	"nofx/market"
	"time"
)

// AICaller 抽象 AI 调用接口，解耦 decision 与 mcp 的直接依赖。
// *mcp.Client 实现此接口。
type AICaller interface {
	CallWithMessages(systemPrompt, userPrompt string) (string, error)
}

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"` // 持仓更新时间戳（毫秒）

	// 入场论据（从 TradeMemory 注入，用于 prompt 构建，不序列化到日志）
	EntryReasoning  string  `json:"-"`
	EntryStopLoss   float64 `json:"-"`
	EntryTakeProfit float64 `json:"-"`
	EntryConfidence int     `json:"-"`
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户净值
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	MarginUsed       float64 `json:"margin_used"`       // 已用保证金
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	PositionCount    int     `json:"position_count"`    // 持仓数量
}

// CandidateCoin 候选币种（来自币种池）
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // 来源: "ai500" 和/或 "oi_top"
}

// OITopData 持仓量增长Top数据（用于AI决策参考）
type OITopData struct {
	Rank              int     // OI Top排名
	OIDeltaPercent    float64 // 持仓量变化百分比（1小时）
	OIDeltaValue      float64 // 持仓量变化价值
	PriceDeltaPercent float64 // 价格变化百分比
	NetLong           float64 // 净多仓
	NetShort          float64 // 净空仓
}

// StopLossDistanceConfig 止损最小距离配置（从 config 层传入）
// 最终 minDist = max(minPct * price, atrMult * ATR14, volMult * price * volatilityPct)
type StopLossDistanceConfig struct {
	MajorMinPct  float64 // BTC/ETH 百分比底线（小数形式，如 0.0015 = 0.15%）
	MajorATRMult float64 // BTC/ETH ATR14倍数
	MajorVolMult float64 // BTC/ETH 波动率倍数
	AltMinPct    float64 // 山寨币百分比底线（小数形式，如 0.0035 = 0.35%）
	AltATRMult   float64 // 山寨币 ATR14倍数
	AltVolMult   float64 // 山寨币波动率倍数
}

// DefaultStopLossDistanceConfig 返回默认的止损最小距离配置（已放宽）
func DefaultStopLossDistanceConfig() StopLossDistanceConfig {
	return StopLossDistanceConfig{
		MajorMinPct:  0.0015, // 0.15%（原0.25%）
		MajorATRMult: 0.3,    // 原0.5
		MajorVolMult: 0.3,    // 原0.5
		AltMinPct:    0.0035, // 0.35%（原0.60%）
		AltATRMult:   0.6,    // 原1.0
		AltVolMult:   0.5,    // 原0.7
	}
}

// AutoTakeProfitConfig 自动止盈配置
// 净ROI = 价格变动% × 杠杆 - round-trip成本%
type AutoTakeProfitConfig struct {
	Stage0Threshold    float64 `json:"stage0_threshold"`     // Stage0 触发净ROI%（默认1.0）
	Stage0ClosePct     float64 `json:"stage0_close_pct"`     // Stage0 平仓比例%（默认50）
	Stage1Threshold    float64 `json:"stage1_threshold"`     // Stage1 触发净ROI%（默认2.0）
	Stage1ClosePct     float64 `json:"stage1_close_pct"`     // Stage1 平仓比例%（默认30）
	FullCloseThreshold float64 `json:"full_close_threshold"` // 全部平仓净ROI%（默认4.0）
	CooldownMinutes    int     `json:"cooldown_minutes"`     // 同一持仓两次止盈间隔（默认15分钟）
}

// DefaultAutoTakeProfitConfig 返回默认的自动止盈配置
func DefaultAutoTakeProfitConfig() AutoTakeProfitConfig {
	return AutoTakeProfitConfig{
		Stage0Threshold:    1.0,
		Stage0ClosePct:     50.0,
		Stage1Threshold:    2.0,
		Stage1ClosePct:     30.0,
		FullCloseThreshold: 4.0,
		CooldownMinutes:    30,
	}
}

// Context 交易上下文（传递给AI的完整信息）
type Context struct {
	CurrentTime     string                  `json:"current_time"`
	RuntimeMinutes  int                     `json:"runtime_minutes"`
	CallCount       int                     `json:"call_count"`
	Account         AccountInfo             `json:"account"`
	Positions       []PositionInfo          `json:"positions"`
	CandidateCoins  []CandidateCoin         `json:"candidate_coins"`
	MarketDataMap   map[string]*market.Data `json:"-"` // 不序列化，但内部使用
	OITopDataMap    map[string]*OITopData   `json:"-"` // OI Top数据映射
	Performance     interface{}             `json:"-"` // 历史表现分析（logger.PerformanceAnalysis）
	BTCETHLeverage  int                     `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage int                     `json:"-"` // 山寨币杠杆倍数（从配置读取）
	PromptStrategy  PromptStrategy          `json:"-"` // 可插拔策略实现（为空时默认StrategyA）
	AutoState       *AutoDecisionState      `json:"-"` // 自动决策跨周期状态（由trader层注入）

	// 成本假设（用于风控/自动止盈/校验，不传给LLM）
	AssumedTakerFeeRate float64 `json:"-"` // 例如 0.0004
	AssumedSlippageRate float64 `json:"-"` // 例如 0.0005

	// 止损最小距离配置（从config层传入，零值时使用默认）
	StopLossDistance StopLossDistanceConfig `json:"-"`

	// 自动止盈配置（从config层传入，零值时使用默认）
	AutoTakeProfit AutoTakeProfitConfig `json:"-"`

	// 录制回放专用字段
	EnableRecording bool   `json:"-"` // 是否开启录制
	TraderID        string `json:"-"` // TraderID (用于区分目录)

	// AI 客户端（每个 trader 独立实例，为 nil 时使用全局默认）
	AI AICaller `json:"-"`
}

// AutoDecisionState 自动决策跨周期状态（用于TP档位只触发一次、冷却等）
// 注意：该状态由 trader 层持有并注入 Context，decision 层只读取/使用。
type AutoDecisionState struct {
	TP map[string]*AutoTPState `json:"-"` // key: symbol_side
}

// AutoTPState 单个持仓的自动止盈状态
type AutoTPState struct {
	Stage            int     `json:"-"` // 0=未触发, 1=已触发TP1, 2=已触发TP2
	LastActionTimeMs int64   `json:"-"` // 上次自动止盈动作时间（毫秒）
	BaselineEntry    float64 `json:"-"` // 基准入场价（用于检测加仓/均价变化）
	BaselineQty      float64 `json:"-"` // 基准数量（用于检测加仓）
}

// Decision AI的交易决策
type Decision struct {
	Symbol          string  `json:"symbol"`
	Action          string  `json:"action"` // 支持 "update_stop_loss", "partial_close" 等
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`

	NewStopLoss     float64 `json:"new_stop_loss,omitempty"`    // 用于 update_stop_loss
	NewTakeProfit   float64 `json:"new_take_profit,omitempty"`  // 用于 update_take_profit
	ClosePercentage float64 `json:"close_percentage,omitempty"` // 用于 partial_close (0-100)

	Confidence int     `json:"confidence,omitempty"`
	RiskUSD    float64 `json:"risk_usd,omitempty"`
	Reasoning  string  `json:"reasoning"`

	// 决策来源标记: "llm" = LLM生成, "auto_stop_loss" = 自动止损, "auto_take_profit" = 自动止盈
	DecisionSource string `json:"decision_source,omitempty"`
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	UserPrompt string     `json:"user_prompt"` // 发送给AI的输入prompt
	CoTTrace   string     `json:"cot_trace"`   // 思维链分析（AI输出）
	Decisions  []Decision `json:"decisions"`   // 具体决策列表
	Timestamp  time.Time  `json:"timestamp"`
}

// PromptStrategy 可插拔策略接口
type PromptStrategy interface {
	Name() string
	BuildSystemPrompt(ctx *Context) string
	BuildUserPrompt(ctx *Context) string

	// LLM前的硬约束条件或自动决策
	GenerateAutoDecisions(ctx *Context) []Decision

	// LLM后的硬约束条件
	ExtraValidate(d *Decision, ctx *Context) error
}

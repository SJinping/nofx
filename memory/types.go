package memory

import "time"

// TradeEpisode represents an in-progress position lifecycle (open -> flat).
// Keyed by symbol_side (e.g. BTCUSDT_long) within a single trader.
type TradeEpisode struct {
	TradeID   string    `json:"trade_id"`
	TraderID  string    `json:"trader_id"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"` // long/short
	OpenTime  time.Time `json:"open_time"`
	CloseTime time.Time `json:"close_time,omitempty"`

	EntryPrice      float64 `json:"entry_price"`
	ExitPrice       float64 `json:"exit_price,omitempty"`
	Quantity        float64 `json:"quantity"`
	Leverage        int     `json:"leverage"`
	PositionSizeUSD float64 `json:"position_size_usd"`

	StopLoss   float64 `json:"stop_loss,omitempty"`
	TakeProfit float64 `json:"take_profit,omitempty"`

	EntryReasoning string `json:"entry_reasoning,omitempty"`
	EntryConfidence int   `json:"entry_confidence,omitempty"`

	// SignalVector is the lightweight numeric representation of the entry signal.
	// It is used for similarity search.
	SignalVector []float64 `json:"signal_vector,omitempty"`

	// Rolling metrics maintained during holding.
	Rolling RollingMetrics `json:"rolling"`
}

type RollingMetrics struct {
	Observations int `json:"observations"`

	MaxPriceSeen float64 `json:"max_price_seen"`
	MinPriceSeen float64 `json:"min_price_seen"`

	// MFE/MAE in percent terms relative to EntryPrice.
	MaxFavorablePct float64 `json:"max_favorable_pct"`
	MaxAdversePct   float64 `json:"max_adverse_pct"`

	TimeInProfitSeconds float64   `json:"time_in_profit_seconds"`
	LastUpdateTime      time.Time `json:"last_update_time,omitempty"`
	LastMarkPrice       float64   `json:"last_mark_price,omitempty"`
}

// TradeRecord is the persisted completed trade.
// It is stored as JSONL lines under trade_memory/<traderID>/trades.jsonl.
type TradeRecord struct {
	TradeID  string `json:"trade_id"`
	TraderID string `json:"trader_id"`
	Symbol   string `json:"symbol"`
	Side     string `json:"side"` // long/short

	OpenTime  time.Time `json:"open_time"`
	CloseTime time.Time `json:"close_time"`
	DurationS int64     `json:"duration_s"`

	EntryPrice      float64 `json:"entry_price"`
	ExitPrice       float64 `json:"exit_price"`
	Quantity        float64 `json:"quantity"`
	Leverage        int     `json:"leverage"`
	PositionSizeUSD float64 `json:"position_size_usd"`

	PnL    float64 `json:"pnl_usd"`
	PnLPct float64 `json:"pnl_pct"`

	MaxFavorablePct float64 `json:"max_favorable_pct"`
	MaxAdversePct   float64 `json:"max_adverse_pct"`

	StopLoss   float64 `json:"stop_loss,omitempty"`
	TakeProfit float64 `json:"take_profit,omitempty"`

	ExitReason string `json:"exit_reason,omitempty"` // ai_close/manual_close/...

	EntryReasoning  string `json:"entry_reasoning,omitempty"`
	ExitReasoning   string `json:"exit_reasoning,omitempty"`
	EntryConfidence int    `json:"entry_confidence,omitempty"`

	SignalVector []float64 `json:"signal_vector,omitempty"`
}

// TradeAnalysis is produced by the post-trade review agent and stored separately
// under trade_memory/<traderID>/analyses/<trade_id>.json.
type TradeAnalysis struct {
	TradeID string `json:"trade_id"`

	TradeGrade   string   `json:"trade_grade"`
	EntryQuality string   `json:"entry_quality"`
	ExitQuality  string   `json:"exit_quality"`
	PatternTags  []string `json:"pattern_tags"`
	MarketRegime string   `json:"market_regime"`
	Lessons      string   `json:"lessons"`

	SimilarScenarioAdvice string `json:"similar_scenario_advice,omitempty"`
}

type SimilarMatch struct {
	Score float64     `json:"score"`
	Trade *TradeRecord `json:"trade"`
	// Analysis may be nil if not available.
	Analysis *TradeAnalysis `json:"analysis,omitempty"`
}

type GateDecision string

const (
	GateApprove GateDecision = "approve"
	GateReject  GateDecision = "reject"
	GateModify  GateDecision = "modify"
)

type GateResult struct {
	Decision GateDecision `json:"decision"`
	// SizeMultiplier applies to Decision.PositionSizeUSD.
	SizeMultiplier float64 `json:"size_multiplier,omitempty"`
	Reason         string  `json:"reason,omitempty"`

	// Similar matches used for this decision (for logging / OpenGuard context).
	Similar []SimilarMatch `json:"similar,omitempty"`
}


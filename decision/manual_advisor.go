package decision

import (
	"fmt"
	"strings"
)

// ManualAdvisorIntent captures what the user is asking the LLM to do. The
// advisor is deliberately separated from the autonomous strategy loop: it is
// decision support for a human-confirmed manual entry.
type ManualAdvisorIntent string

const (
	ManualAdvisorIntentAnalyzeSymbol ManualAdvisorIntent = "analyze_symbol"
	ManualAdvisorIntentEvaluateLong  ManualAdvisorIntent = "evaluate_long"
	ManualAdvisorIntentEvaluateShort ManualAdvisorIntent = "evaluate_short"
	ManualAdvisorIntentValidatePlan  ManualAdvisorIntent = "validate_plan"
)

// ManualAdvisorUserPlan is optional. When present, future implementation should
// validate these values with program logic (RR, SL/TP direction, margin) instead
// of trusting LLM arithmetic.
type ManualAdvisorUserPlan struct {
	Side            string  `json:"side,omitempty"`
	EntryPrice      float64 `json:"entry_price,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	Leverage        int     `json:"leverage,omitempty"`
}

// ManualAdvisorPromptInput is the compact, typed boundary between API/trader
// orchestration and prompt construction. Keeping this type in decision avoids
// scattering advisor-specific prompt rules across HTTP handlers.
type ManualAdvisorPromptInput struct {
	Symbol             string
	Question           string
	Intent             ManualAdvisorIntent
	Horizon            string
	AdvisorTraderID    string
	ManagementTraderID string
	UserPlan           *ManualAdvisorUserPlan
	MarketSnapshot     string
	AccountSnapshot    string
	PositionSnapshot   string
}

// ManualAdvisorPromptBundle is intentionally serializable so advisor sessions
// can later be recorded/replayed just like automatic decision logs.
type ManualAdvisorPromptBundle struct {
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
}

// BuildManualAdvisorPrompts creates the v1 advisor prompt skeleton. The actual
// LLM call is wired later; this function defines the product contract now:
// structured advice, no direct execution, and clear management handoff metadata.
func BuildManualAdvisorPrompts(in ManualAdvisorPromptInput) ManualAdvisorPromptBundle {
	intent := strings.TrimSpace(string(in.Intent))
	if intent == "" {
		intent = string(ManualAdvisorIntentAnalyzeSymbol)
	}
	horizon := strings.TrimSpace(in.Horizon)
	if horizon == "" {
		horizon = "short_term"
	}

	system := strings.TrimSpace(`You are the nofx Manual LLM Advisor.

Your job is to evaluate a human-initiated trade idea using nofx market/account/risk context. You do NOT place orders. The user must manually confirm any entry in the UI.

Core rules:
- Treat the user's direction as a hypothesis, not an instruction. Disagree when the idea is weak.
- Return strict JSON plus a short human-readable summary.
- Include recommendation, side, setup_type, entry range, stop_loss, take_profit, leverage, position_size_usd, net_rr, confidence, invalidation_condition, expected_holding_minutes, time_stop_minutes, and main_risks.
- Net RR and risk checks are advisory only until nofx program validation recalculates them.
- If the plan is not executable, use recommendation=no_trade and explain the better trigger to wait for.
- After manual entry, management is delegated to management_trader_id; do not invent a new strategy.`)

	var b strings.Builder
	fmt.Fprintf(&b, "Advisor trader: %s\n", in.AdvisorTraderID)
	fmt.Fprintf(&b, "Management trader after manual entry: %s\n", in.ManagementTraderID)
	fmt.Fprintf(&b, "Symbol: %s\nIntent: %s\nHorizon: %s\n", in.Symbol, intent, horizon)
	fmt.Fprintf(&b, "\nUser question:\n%s\n", strings.TrimSpace(in.Question))

	if in.UserPlan != nil {
		fmt.Fprintf(&b, "\nUser-proposed plan (validate, do not blindly accept):\n")
		fmt.Fprintf(&b, "- side: %s\n- entry_price: %.8f\n- stop_loss: %.8f\n- take_profit: %.8f\n- position_size_usd: %.2f\n- leverage: %d\n",
			in.UserPlan.Side,
			in.UserPlan.EntryPrice,
			in.UserPlan.StopLoss,
			in.UserPlan.TakeProfit,
			in.UserPlan.PositionSizeUSD,
			in.UserPlan.Leverage,
		)
	}

	fmt.Fprintf(&b, "\nAccount snapshot:\n%s\n", emptyFallback(in.AccountSnapshot, "(not available)"))
	fmt.Fprintf(&b, "\nCurrent positions snapshot:\n%s\n", emptyFallback(in.PositionSnapshot, "(no positions or not available)"))
	fmt.Fprintf(&b, "\nMarket snapshot for target symbol:\n%s\n", emptyFallback(in.MarketSnapshot, "(market data unavailable; explain blocker and return no_trade)"))

	fmt.Fprintf(&b, `
Return JSON with this shape:
{
  "symbol": "%s",
  "recommendation": "open_long|open_short|no_trade|wait",
  "stance_on_user_idea": "agree|agree_with_adjustments|disagree|not_applicable",
  "confidence": 0,
  "setup_type": "trend_pullback|breakout_momentum|range_reversal|exhaustion_reversal|failed_breakout|no_trade",
  "entry": {"suggested_entry_price": 0, "acceptable_entry_range": [0, 0], "wait_for_next_candle": false},
  "risk": {"stop_loss": 0, "invalidation_condition": ""},
  "reward": {"take_profit_1": 0, "take_profit_2": 0},
  "sizing": {"leverage": 0, "position_size_usd": 0},
  "rr": {"net_rr": 0, "passes_min_rr": false},
  "expected_holding_minutes": 0,
  "time_stop_minutes": 0,
  "reasoning_summary": "",
  "main_risks": []
}
`, in.Symbol)

	return ManualAdvisorPromptBundle{SystemPrompt: system, UserPrompt: b.String()}
}

func emptyFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

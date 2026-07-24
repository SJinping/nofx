package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"nofx/decision"
	"nofx/manager"
	"nofx/market"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// advisorAnalyzeRequest is the HTTP boundary for the Manual LLM Advisor.
//
// v1 intentionally separates two identities:
//   - AdvisorTraderID: which trader/account/model supplies context and later LLM call.
//   - ManagementTraderID: which existing trader should manage the position if
//     the user manually opens from the returned plan.
//
// If ManagementTraderID is omitted and there is exactly one running manager,
// the handler fills it automatically. If multiple running managers exist, the
// request must provide one so nofx does not guess the wrong strategy/account.
type advisorAnalyzeRequest struct {
	AdvisorTraderID    string                          `json:"advisor_trader_id"`
	ManagementTraderID string                          `json:"management_trader_id"`
	Symbol             string                          `json:"symbol"`
	Question           string                          `json:"question"`
	Intent             decision.ManualAdvisorIntent    `json:"intent"`
	Horizon            string                          `json:"horizon"`
	UserPlan           *decision.ManualAdvisorUserPlan `json:"user_plan,omitempty"`
}

type advisorAnalyzeResponse struct {
	AdvisorID          string                             `json:"advisor_id"`
	Status             string                             `json:"status"`
	Message            string                             `json:"message"`
	Symbol             string                             `json:"symbol"`
	AdvisorTraderID    string                             `json:"advisor_trader_id"`
	ManagementTraderID string                             `json:"management_trader_id,omitempty"`
	CreatedAt          string                             `json:"created_at"`
	MarketSnapshotTime string                             `json:"market_snapshot_time"`
	PromptPreview      decision.ManualAdvisorPromptBundle `json:"prompt_preview"`
	Recommendation     map[string]interface{}             `json:"recommendation"`
	NextTodo           []string                           `json:"next_todo"`
}

// handleAdvisorManagementCandidates returns the strategy/trader assignment state
// needed by the UI before a user opens a manual-advisor position.
func (s *Server) handleAdvisorManagementCandidates(c *gin.Context) {
	c.JSON(http.StatusOK, s.traderManager.GetManualAdvisorManagementCandidates())
}

// handleAdvisorAnalyze scaffolds the future LLM advisor flow without placing
// orders or mutating trading state. It validates trader assignment, fetches the
// target symbol's heavy short-term market data, and builds prompt previews.
//
// The intentionally unfinished part is the actual LLM call + strict JSON parser.
// Keeping the endpoint operational now lets frontend/manual order handoff work
// be developed against a stable shape while preserving safety.
func (s *Server) handleAdvisorAnalyze(c *gin.Context) {
	var req advisorAnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid advisor request: %v", err)})
		return
	}

	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	if !isValidExchangeSymbol(req.Symbol) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing symbol; expected e.g. ETHUSDT"})
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing question"})
		return
	}

	advisorTraderID := strings.TrimSpace(req.AdvisorTraderID)
	if advisorTraderID == "" {
		ids := s.traderManager.GetTraderIDs()
		if len(ids) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no available trader"})
			return
		}
		advisorTraderID = ids[0]
	}

	advisorTrader, err := s.traderManager.GetTrader(advisorTraderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	candidates := s.traderManager.GetManualAdvisorManagementCandidates()
	managementTraderID, err := resolveAdvisorManagementTraderID(req.ManagementTraderID, candidates)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "management_candidates": candidates})
		return
	}

	accountJSON := ""
	if account, err := advisorTrader.GetAccountInfo(); err == nil {
		accountJSON = compactJSON(account)
	}
	positionsJSON := ""
	if positions, err := advisorTrader.GetPositions(); err == nil {
		positionsJSON = compactJSON(positions)
	}

	marketSnapshot := ""
	marketSnapshotTime := time.Now().Format(time.RFC3339)
	md, marketErr := market.GetClosedWithOptions(req.Symbol, market.FetchOptions{
		IntradayOutputPoints:  market.DefaultShortTermOutputPoints,
		IncludeIntradayOHLCV:  true,
		IncludeMidTermContext: true,
	})
	if marketErr != nil {
		marketSnapshot = fmt.Sprintf("market data fetch failed: %v", marketErr)
	} else {
		marketSnapshot = market.Format(md)
	}

	prompt := decision.BuildManualAdvisorPrompts(decision.ManualAdvisorPromptInput{
		Symbol:             req.Symbol,
		Question:           req.Question,
		Intent:             req.Intent,
		Horizon:            req.Horizon,
		AdvisorTraderID:    advisorTraderID,
		ManagementTraderID: managementTraderID,
		UserPlan:           req.UserPlan,
		MarketSnapshot:     marketSnapshot,
		AccountSnapshot:    accountJSON,
		PositionSnapshot:   positionsJSON,
	})

	// Scaffold recommendation: explicit non-execution placeholder. Future work
	// will replace this map with parsed strict JSON from advisorTrader.GetAIClient().
	recommendation := map[string]interface{}{
		"symbol":                req.Symbol,
		"recommendation":        "not_run_yet",
		"decision_source":       "manual_llm_advisor_scaffold",
		"management_trader_id":  managementTraderID,
		"requires_human_action": true,
		"market_data_available": marketErr == nil,
	}

	c.JSON(http.StatusOK, advisorAnalyzeResponse{
		AdvisorID:          fmt.Sprintf("advisor_%d", time.Now().UnixMilli()),
		Status:             "scaffold_only",
		Message:            "advisor framework is wired: assignment + context + prompt preview are ready; LLM call/order handoff are TODO",
		Symbol:             req.Symbol,
		AdvisorTraderID:    advisorTraderID,
		ManagementTraderID: managementTraderID,
		CreatedAt:          time.Now().Format(time.RFC3339),
		MarketSnapshotTime: marketSnapshotTime,
		PromptPreview:      prompt,
		Recommendation:     recommendation,
		NextTodo: []string{
			"call selected trader AI client with prompt bundle",
			"parse strict advisor JSON and recalculate RR with nofx validators",
			"fill manual order form only; do not auto-place orders",
			"persist advisor_session_id + entry_thesis + management_trader_id after manual entry",
		},
	})
}

func resolveAdvisorManagementTraderID(requested string, candidates manager.ManagementCandidatesResponse) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if candidates.DefaultTraderID != "" {
			return candidates.DefaultTraderID, nil
		}
		if candidates.RequiresChoice {
			return "", fmt.Errorf("multiple running traders are available; management_trader_id is required")
		}
		// No running manager. Keep the field empty so the UI/backend can make it
		// explicit that the manual position will not be LLM-managed after entry.
		return "", nil
	}

	for _, candidate := range candidates.Candidates {
		if candidate.TraderID == requested {
			if !candidate.CanManagePositions {
				return "", fmt.Errorf("management_trader_id %q cannot currently manage positions", requested)
			}
			return requested, nil
		}
	}
	return "", fmt.Errorf("management_trader_id %q was not found", requested)
}

func compactJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

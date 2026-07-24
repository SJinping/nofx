package manager

import (
	"sort"
)

// ManagementCandidate describes a trader instance that can own and manage a
// manually opened position after the user confirms an advisor-generated plan.
//
// Important: ownership is at trader-instance level, not just strategy name.
// Two traders may use the same prompt strategy but different accounts/models;
// choosing the wrong instance can make the position invisible or managed with
// the wrong risk/account context.
type ManagementCandidate struct {
	TraderID   string `json:"trader_id"`
	TraderName string `json:"trader_name"`
	AIModel    string `json:"ai_model"`
	Exchange   string `json:"exchange"`
	IsRunning  bool   `json:"is_running"`
	IsPaused   bool   `json:"is_paused"`

	// CanManagePositions is intentionally separate from auto-open capability.
	// A paused/management-only trader may still be a valid future owner once the
	// product supports that mode. The initial resolver only defaults running
	// traders to stay conservative.
	CanManagePositions bool `json:"can_manage_positions"`
}

// ManagementCandidatesResponse is returned to the UI before manual advisor
// analysis/order handoff. DefaultTraderID is populated only when the safe
// default is unambiguous (exactly one running candidate).
type ManagementCandidatesResponse struct {
	Candidates      []ManagementCandidate `json:"candidates"`
	DefaultTraderID string                `json:"default_trader_id,omitempty"`
	RequiresChoice  bool                  `json:"requires_choice"`
	Reason          string                `json:"reason"`
}

// GetManualAdvisorManagementCandidates implements the v1 assignment policy:
//
//   - 0 running candidates: no default; warn that LLM management is unavailable.
//   - 1 running candidate: default to that trader.
//   - >1 running candidates: require the user to choose.
//
// This is read-only and has no effect on live trading loops.
func (tm *TraderManager) GetManualAdvisorManagementCandidates() ManagementCandidatesResponse {
	traders := tm.GetAllTraders()
	candidates := make([]ManagementCandidate, 0, len(traders))
	running := make([]ManagementCandidate, 0, len(traders))

	for _, t := range traders {
		status := t.GetStatus()
		isRunning, _ := status["is_running"].(bool)
		isPaused, _ := status["is_paused"].(bool)
		exchange, _ := status["exchange"].(string)

		candidate := ManagementCandidate{
			TraderID:           t.GetID(),
			TraderName:         t.GetName(),
			AIModel:            t.GetAIModel(),
			Exchange:           exchange,
			IsRunning:          isRunning,
			IsPaused:           isPaused,
			CanManagePositions: isRunning,
		}
		candidates = append(candidates, candidate)
		if candidate.CanManagePositions {
			running = append(running, candidate)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CanManagePositions != candidates[j].CanManagePositions {
			return candidates[i].CanManagePositions
		}
		return candidates[i].TraderID < candidates[j].TraderID
	})

	resp := ManagementCandidatesResponse{Candidates: candidates}
	switch len(running) {
	case 0:
		resp.Reason = "no running trader can manage advisor-created manual positions"
	case 1:
		resp.DefaultTraderID = running[0].TraderID
		resp.Reason = "single running trader selected as default manager"
	default:
		resp.RequiresChoice = true
		resp.Reason = "multiple running traders available; user must choose management_trader_id"
	}
	return resp
}

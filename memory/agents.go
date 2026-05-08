package memory

import (
	"encoding/json"
	"fmt"
	"nofx/mcp"
	"strings"
)

type openGuardResponse struct {
	Action         string  `json:"action"` // approve|reject|modify
	SizeMultiplier float64 `json:"size_multiplier,omitempty"`
	Reason         string  `json:"reason,omitempty"`
}

func callOpenGuardLLM(symbol, side string, currentDecision any, similar []SimilarMatch) (*openGuardResponse, error) {
	// Keep it short and deterministic.
	system := "You are a cautious trading risk reviewer. You MUST output valid JSON only, no markdown, no extra text."
	var sb strings.Builder
	sb.WriteString("Review whether the following OPEN decision is appropriate given similar historical trades.\n")
	sb.WriteString("Return JSON: {\"action\":\"approve|reject|modify\",\"size_multiplier\":number,\"reason\":\"...\"}.\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- If you are unsure, choose modify with size_multiplier between 0.3 and 0.8.\n")
	sb.WriteString("- Never suggest increasing size above 1.0.\n")
	sb.WriteString("- Keep reason <= 200 chars.\n\n")
	sb.WriteString(fmt.Sprintf("symbol=%s side=%s\n\n", symbol, side))

	cd, _ := json.Marshal(currentDecision)
	sb.WriteString("current_decision_json=" + string(cd) + "\n\n")

	// Provide a compact history digest.
	type h struct {
		Score  float64  `json:"score"`
		Symbol string   `json:"symbol"`
		Side   string   `json:"side"`
		PnLPct float64  `json:"pnl_pct"`
		MFE    float64  `json:"mfe_pct"`
		MAE    float64  `json:"mae_pct"`
		Tags   []string `json:"tags,omitempty"`
		Lessons string  `json:"lessons,omitempty"`
	}
	hist := make([]h, 0, len(similar))
	for _, m := range similar {
		if m.Trade == nil {
			continue
		}
		item := h{
			Score:  m.Score,
			Symbol: m.Trade.Symbol,
			Side:   m.Trade.Side,
			PnLPct: m.Trade.PnLPct,
			MFE:    m.Trade.MaxFavorablePct,
			MAE:    m.Trade.MaxAdversePct,
		}
		if m.Analysis != nil {
			item.Tags = m.Analysis.PatternTags
			item.Lessons = m.Analysis.Lessons
		}
		hist = append(hist, item)
	}
	hb, _ := json.Marshal(hist)
	sb.WriteString("similar_trades=" + string(hb) + "\n")

	raw, err := mcp.CallWithMessagesGlobal(system, sb.String())
	if err != nil {
		return nil, err
	}

	// Extract JSON (best-effort).
	txt := strings.TrimSpace(raw)
	start := strings.Index(txt, "{")
	end := strings.LastIndex(txt, "}")
	if start >= 0 && end > start {
		txt = txt[start : end+1]
	}

	var resp openGuardResponse
	if err := json.Unmarshal([]byte(txt), &resp); err != nil {
		return nil, fmt.Errorf("open_guard parse failed: %w, raw=%s", err, raw)
	}
	resp.Action = strings.ToLower(strings.TrimSpace(resp.Action))
	if resp.SizeMultiplier == 0 {
		resp.SizeMultiplier = 1.0
	}
	if resp.SizeMultiplier < 0.1 {
		resp.SizeMultiplier = 0.1
	}
	if resp.SizeMultiplier > 1.0 {
		resp.SizeMultiplier = 1.0
	}
	return &resp, nil
}

type reviewResponse struct {
	TradeGrade            string   `json:"trade_grade"`
	EntryQuality          string   `json:"entry_quality"`
	ExitQuality           string   `json:"exit_quality"`
	PatternTags           []string `json:"pattern_tags"`
	MarketRegime          string   `json:"market_regime"`
	Lessons               string   `json:"lessons"`
	SimilarScenarioAdvice string   `json:"similar_scenario_advice"`
}

func callPostTradeReviewLLM(tr *TradeRecord) (*TradeAnalysis, error) {
	if tr == nil {
		return nil, fmt.Errorf("nil trade record")
	}

	system := "You are a trading post-mortem analyst. Output valid JSON only. No markdown, no extra text."
	user := fmt.Sprintf(
		"Analyze the completed trade and output JSON with fields: trade_grade, entry_quality, exit_quality, pattern_tags(array), market_regime, lessons, similar_scenario_advice.\n"+
			"Constraints: keep each text field <= 240 chars. pattern_tags <= 8 items.\n\n"+
			"trade_json=%s\n",
		func() string {
			b, _ := json.Marshal(tr)
			return string(b)
		}(),
	)

	raw, err := mcp.CallWithMessagesGlobal(system, user)
	if err != nil {
		return nil, err
	}
	txt := strings.TrimSpace(raw)
	start := strings.Index(txt, "{")
	end := strings.LastIndex(txt, "}")
	if start >= 0 && end > start {
		txt = txt[start : end+1]
	}

	var rr reviewResponse
	if err := json.Unmarshal([]byte(txt), &rr); err != nil {
		return nil, fmt.Errorf("post_trade_review parse failed: %w, raw=%s", err, raw)
	}
	return &TradeAnalysis{
		TradeID:               tr.TradeID,
		TradeGrade:            strings.TrimSpace(rr.TradeGrade),
		EntryQuality:          strings.TrimSpace(rr.EntryQuality),
		ExitQuality:           strings.TrimSpace(rr.ExitQuality),
		PatternTags:           rr.PatternTags,
		MarketRegime:          strings.TrimSpace(rr.MarketRegime),
		Lessons:               strings.TrimSpace(rr.Lessons),
		SimilarScenarioAdvice: strings.TrimSpace(rr.SimilarScenarioAdvice),
	}, nil
}


package memory

import (
	"encoding/json"
	"fmt"
	"math"
	"nofx/decision"
	"nofx/logger"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type TradeMemory struct {
	traderID     string
	baseDir      string
	tradesPath   string
	episodesPath string

	mu       sync.Mutex
	episodes map[string]*TradeEpisode // key: symbol_side
	closing  map[string]struct{}      // prevents duplicate finalization per symbol_side
	history  []*TradeRecord           // completed trades (recent first)

	analysisCache map[string]*TradeAnalysis
}

func NewTradeMemory(traderID string) (*TradeMemory, error) {
	base := filepath.Join("trade_memory", traderID)
	if err := ensureDir(base); err != nil {
		return nil, err
	}
	if err := ensureDir(filepath.Join(base, "analyses")); err != nil {
		return nil, err
	}

	tradesPath := filepath.Join(base, "trades.jsonl")
	loaded, err := loadJSONL[TradeRecord](tradesPath, 0)
	if err != nil {
		return nil, err
	}

	h := make([]*TradeRecord, 0, len(loaded))
	for i := range loaded {
		tr := loaded[i]
		// copy
		t := tr
		h = append(h, &t)
	}
	// most recent first
	sort.Slice(h, func(i, j int) bool { return h[i].CloseTime.After(h[j].CloseTime) })

	episodesPath := filepath.Join(base, "open_episodes.json")
	episodes := make(map[string]*TradeEpisode)
	if err := readJSONFile(episodesPath, &episodes); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load open trade episodes: %w", err)
	}
	completedIDs := make(map[string]struct{}, len(h))
	for _, trade := range h {
		if trade != nil && trade.TradeID != "" {
			completedIDs[trade.TradeID] = struct{}{}
		}
	}
	for key, ep := range episodes {
		if ep == nil || ep.Symbol == "" || (ep.Side != "long" && ep.Side != "short") {
			delete(episodes, key)
			continue
		}
		// A crash after appending the completed record but before checkpointing
		// episodes leaves a stale entry. The ledger wins on recovery.
		if _, completed := completedIDs[ep.TradeID]; completed {
			delete(episodes, key)
		}
	}

	tm := &TradeMemory{
		traderID:      traderID,
		baseDir:       base,
		tradesPath:    tradesPath,
		episodesPath:  episodesPath,
		episodes:      episodes,
		closing:       make(map[string]struct{}),
		history:       h,
		analysisCache: make(map[string]*TradeAnalysis),
	}

	// best-effort: warm up analysis cache for recent N trades
	for i := 0; i < len(tm.history) && i < 50; i++ {
		id := tm.history[i].TradeID
		var a TradeAnalysis
		p := analysisFilePath(base, id)
		if err := readJSONFile(p, &a); err == nil {
			tm.analysisCache[id] = &a
		}
	}

	return tm, nil
}

func posKey(symbol, side string) string {
	return strings.ToUpper(strings.TrimSpace(symbol)) + "_" + strings.ToLower(strings.TrimSpace(side))
}

// persistEpisodesLocked atomically checkpoints open lifecycle state. In particular,
// partial-close PnL must survive a process restart before the final exchange exit.
func (tm *TradeMemory) persistEpisodesLocked() error {
	if tm == nil || tm.episodesPath == "" {
		return nil
	}
	if err := ensureDir(filepath.Dir(tm.episodesPath)); err != nil {
		return err
	}
	data, err := json.Marshal(tm.episodes)
	if err != nil {
		return err
	}
	tmp := tm.episodesPath + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, tm.episodesPath); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(tm.episodesPath))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// UpdateEpisodesFromPositions updates rolling metrics for active episodes based on current positions snapshot.
// It also creates placeholder episodes for positions that existed before the system started (best-effort).
func (tm *TradeMemory) UpdateEpisodesFromPositions(traderID string, positions []decision.PositionInfo) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	for _, p := range positions {
		if strings.TrimSpace(p.Symbol) == "" || strings.TrimSpace(p.Side) == "" {
			continue
		}
		k := posKey(p.Symbol, p.Side)
		ep, ok := tm.episodes[k]
		if !ok || ep == nil {
			// placeholder episode
			ep = &TradeEpisode{
				TradeID:         fmt.Sprintf("%s_%s_%d", traderID, k, now.UnixMilli()),
				TraderID:        traderID,
				Symbol:          strings.ToUpper(strings.TrimSpace(p.Symbol)),
				Side:            strings.ToLower(strings.TrimSpace(p.Side)),
				OpenTime:        time.UnixMilli(p.UpdateTime),
				EntryPrice:      p.EntryPrice,
				Quantity:        p.Quantity,
				Leverage:        p.Leverage,
				PositionSizeUSD: p.EntryPrice * p.Quantity,
				SignalVector:    nil,
				Rolling:         RollingMetrics{},
				EntryReasoning:  "(unknown - preexisting position)",
				EntryConfidence: 0,
			}
			tm.episodes[k] = ep
		}

		mark := p.MarkPrice
		if mark <= 0 {
			mark = p.EntryPrice
		}
		tm.updateRollingLocked(ep, mark, now)
	}
	if err := tm.persistEpisodesLocked(); err != nil {
		// Rolling-state persistence is best-effort; it must not interrupt trading.
		return
	}
}

// GetEpisodeSnapshot 返回指定持仓的 TradeEpisode 拷贝（线程安全，只读）。
// 如果不存在返回 nil。
func (tm *TradeMemory) GetEpisodeSnapshot(symbol, side string) *TradeEpisode {
	k := strings.ToUpper(symbol) + "_" + strings.ToLower(side)
	tm.mu.Lock()
	defer tm.mu.Unlock()
	ep, ok := tm.episodes[k]
	if !ok || ep == nil {
		return nil
	}
	copy := *ep
	return &copy
}

func (tm *TradeMemory) updateRollingLocked(ep *TradeEpisode, markPrice float64, now time.Time) {
	if ep == nil || ep.EntryPrice <= 0 || markPrice <= 0 {
		return
	}

	// time-in-profit accumulation
	if !ep.Rolling.LastUpdateTime.IsZero() {
		dt := now.Sub(ep.Rolling.LastUpdateTime).Seconds()
		if dt > 0 && dt < 3600 {
			// compute current pnl pct
			pnlPct := 0.0
			if ep.Side == "long" {
				pnlPct = ((markPrice - ep.EntryPrice) / ep.EntryPrice) * 100
			} else {
				pnlPct = ((ep.EntryPrice - markPrice) / ep.EntryPrice) * 100
			}
			if pnlPct > 0 {
				ep.Rolling.TimeInProfitSeconds += dt
			}
		}
	}

	ep.Rolling.Observations++
	ep.Rolling.LastUpdateTime = now
	ep.Rolling.LastMarkPrice = markPrice

	if ep.Rolling.MaxPriceSeen == 0 || markPrice > ep.Rolling.MaxPriceSeen {
		ep.Rolling.MaxPriceSeen = markPrice
	}
	if ep.Rolling.MinPriceSeen == 0 || markPrice < ep.Rolling.MinPriceSeen {
		ep.Rolling.MinPriceSeen = markPrice
	}

	// MFE/MAE (percent)
	mfe := 0.0
	mae := 0.0
	if ep.Side == "long" {
		mfe = ((ep.Rolling.MaxPriceSeen - ep.EntryPrice) / ep.EntryPrice) * 100
		mae = ((ep.Rolling.MinPriceSeen - ep.EntryPrice) / ep.EntryPrice) * 100
	} else {
		// short: favorable is price down
		mfe = ((ep.EntryPrice - ep.Rolling.MinPriceSeen) / ep.EntryPrice) * 100
		mae = ((ep.EntryPrice - ep.Rolling.MaxPriceSeen) / ep.EntryPrice) * 100
	}
	if mfe > ep.Rolling.MaxFavorablePct {
		ep.Rolling.MaxFavorablePct = mfe
	}
	// adverse is negative for both; store as negative magnitude
	if mae < ep.Rolling.MaxAdversePct {
		ep.Rolling.MaxAdversePct = mae
	}
}

// GateOpenDecision performs historical retrieval + rule gate + optional OpenGuard LLM.
// It returns allow/reject/modify.
func (tm *TradeMemory) GateOpenDecision(ctx *decision.Context, dec *decision.Decision) (*GateResult, error) {
	if ctx == nil || dec == nil {
		return &GateResult{Decision: GateApprove, SizeMultiplier: 1}, nil
	}
	sym := strings.ToUpper(strings.TrimSpace(dec.Symbol))
	if sym == "" {
		return &GateResult{Decision: GateApprove, SizeMultiplier: 1}, nil
	}
	side := "long"
	if dec.Action == decision.ActionOpenShort {
		side = "short"
	}

	var vec []float64
	if ctx.MarketDataMap != nil {
		if md := ctx.MarketDataMap[sym]; md != nil {
			vec = BuildSignalVector(sym, md)
		}
	}
	if len(vec) == 0 {
		return &GateResult{Decision: GateApprove, SizeMultiplier: 1, Reason: "no_market_vector"}, nil
	}

	// Retrieve similar trades (across all symbols) for this side.
	sim := tm.searchSimilarLocked(vec, side, 8)

	// Quick rule gate (no LLM).
	losers := 0
	avg := 0.0
	count := 0
	for _, m := range sim {
		if m.Trade == nil {
			continue
		}
		count++
		avg += m.Trade.PnLPct
		if m.Trade.PnLPct < 0 {
			losers++
		}
	}
	if count > 0 {
		avg /= float64(count)
	}

	// Symbol-specific recent performance
	symLossStreak := tm.symbolRecentLossStreak(sym, side, 3)

	// base result
	res := &GateResult{
		Decision:       GateApprove,
		SizeMultiplier: 1.0,
		Reason:         "",
		Similar:        sim,
	}

	// Hard reject: very consistent losses in similar setups + this symbol already losing repeatedly.
	if count >= 4 && float64(losers)/float64(count) >= 0.75 && avg < -0.3 && symLossStreak >= 2 {
		res.Decision = GateReject
		res.SizeMultiplier = 0
		res.Reason = fmt.Sprintf("rule_gate_reject: similar_loss_rate=%.2f avg_pnl=%.2f%% sym_loss_streak=%d", float64(losers)/float64(count), avg, symLossStreak)
		return res, nil
	}

	// Soft modify: tiered size reduction instead of a flat 0.6 multiplier.
	// Keep serious historical loss signals defensive, but avoid over-shrinking
	// higher-quality setups whose confidence and net RR justify a larger probe.
	needsReview := false
	if (count >= 4 && avg < 0) || symLossStreak >= 2 {
		rr := decision.EstimateDecisionNetRiskReward(ctx, dec)
		netRR := rr.NetRiskRewardRatio
		multiplier, tier := gateSoftSizeMultiplier(avg, count, symLossStreak, dec.Confidence, netRR)
		res.Decision = GateModify
		res.SizeMultiplier = multiplier
		res.Reason = fmt.Sprintf("rule_gate_modify:%s avg_pnl=%.2f%% similar_count=%d sym_loss_streak=%d confidence=%d net_rr=%.2f multiplier=%.2f",
			tier, avg, count, symLossStreak, dec.Confidence, netRR, multiplier)
		needsReview = true
	}

	// Optional OpenGuard LLM for borderline/risky situations.
	// Trigger on: soft modify OR poor overall performance (negative sharpe) OR current losing streak.
	sharpe := 0.0
	losingStreak := 0
	if ctx.Performance != nil {
		if p, ok := ctx.Performance.(*logger.PerformanceAnalysis); ok && p != nil {
			sharpe = p.SharpeRatio
			losingStreak = p.CurrentLosingStreak
		}
	}
	if sharpe < 0 || losingStreak >= 2 {
		needsReview = true
	}

	if needsReview {
		resp, err := callOpenGuardLLM(sym, side, dec, sim)
		if err == nil && resp != nil {
			switch resp.Action {
			case "reject":
				res.Decision = GateReject
				res.SizeMultiplier = 0
				res.Reason = "openguard_reject: " + strings.TrimSpace(resp.Reason)
				return res, nil
			case "modify":
				res.Decision = GateModify
				res.SizeMultiplier = math.Min(res.SizeMultiplier, resp.SizeMultiplier)
				if res.SizeMultiplier <= 0 {
					res.SizeMultiplier = resp.SizeMultiplier
				}
				res.Reason = "openguard_modify: " + strings.TrimSpace(resp.Reason)
				return res, nil
			default:
				// approve
				if res.Decision == GateModify {
					// keep rule modify unless explicitly approved? we'll allow approve to override to 1.0
					res.Decision = GateApprove
					res.SizeMultiplier = 1.0
				}
				res.Reason = "openguard_approve: " + strings.TrimSpace(resp.Reason)
				return res, nil
			}
		}
		// if OpenGuard fails, fall back to rule result
	}

	return res, nil
}

func gateSoftSizeMultiplier(avg float64, count, symLossStreak, confidence int, netRR float64) (float64, string) {
	multiplier := 1.0
	tier := "base"

	// Base reduction tier from historical negative feedback.
	switch {
	case symLossStreak >= 3 || (count >= 4 && avg <= -0.3):
		multiplier = 0.6
		tier = "serious"
	case symLossStreak == 2 || (count >= 4 && avg <= -0.1):
		multiplier = 0.75
		tier = "moderate"
	case count >= 4 && avg < 0:
		multiplier = 0.85
		tier = "mild"
	}

	// Quality uplift: do not override serious same-symbol streaks or deeply
	// negative similar-trade history, but allow strong current setups to avoid
	// the old one-size-fits-all 0.6 shrink.
	if symLossStreak < 3 && avg > -0.3 {
		switch {
		case confidence >= 85 && netRR >= 3.0:
			if multiplier < 0.9 {
				multiplier = 0.9
			}
			tier += "+quality_high"
		case confidence >= 80 && netRR >= 2.5:
			if multiplier < 0.8 {
				multiplier = 0.8
			}
			tier += "+quality_mid"
		}
	}

	return multiplier, tier
}

func (tm *TradeMemory) searchSimilarLocked(vec []float64, side string, topK int) []SimilarMatch {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.searchSimilarNoLock(vec, side, topK)
}

func (tm *TradeMemory) searchSimilarNoLock(vec []float64, side string, topK int) []SimilarMatch {
	if topK <= 0 {
		topK = 5
	}
	side = strings.ToLower(strings.TrimSpace(side))

	type cand struct {
		score float64
		tr    *TradeRecord
	}
	cands := make([]cand, 0, 64)
	for _, tr := range tm.history {
		if tr == nil || len(tr.SignalVector) == 0 {
			continue
		}
		if side != "" && strings.ToLower(tr.Side) != side {
			continue
		}
		s := cosineSimilarity(vec, tr.SignalVector)
		if s <= 0 {
			continue
		}
		cands = append(cands, cand{score: s, tr: tr})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > topK {
		cands = cands[:topK]
	}

	out := make([]SimilarMatch, 0, len(cands))
	for _, c := range cands {
		var a *TradeAnalysis
		if tm.analysisCache != nil {
			if v, ok := tm.analysisCache[c.tr.TradeID]; ok {
				a = v
			}
		}
		// best-effort lazy load if missing
		if a == nil {
			p := analysisFilePath(tm.baseDir, c.tr.TradeID)
			var aa TradeAnalysis
			if err := readJSONFile(p, &aa); err == nil {
				a = &aa
				if tm.analysisCache != nil {
					tm.analysisCache[c.tr.TradeID] = a
				}
			}
		}

		out = append(out, SimilarMatch{
			Score:    c.score,
			Trade:    c.tr,
			Analysis: a,
		})
	}
	return out
}

func (tm *TradeMemory) symbolRecentLossStreak(symbol, side string, lookback int) int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	side = strings.ToLower(strings.TrimSpace(side))
	if lookback <= 0 {
		lookback = 3
	}
	streak := 0
	seen := 0
	for _, tr := range tm.history {
		if tr == nil {
			continue
		}
		if strings.ToUpper(tr.Symbol) != symbol {
			continue
		}
		if side != "" && strings.ToLower(tr.Side) != side {
			continue
		}
		seen++
		if tr.PnLPct < 0 {
			streak++
		} else {
			break
		}
		if seen >= lookback {
			break
		}
	}
	return streak
}

// OnOpenSuccess creates a new TradeEpisode after a successful open.
func (tm *TradeMemory) OnOpenSuccess(ctx *decision.Context, dec *decision.Decision, quantity, entryPrice float64) {
	if ctx == nil || dec == nil {
		return
	}
	sym := strings.ToUpper(strings.TrimSpace(dec.Symbol))
	if sym == "" {
		return
	}
	side := "long"
	if dec.Action == decision.ActionOpenShort {
		side = "short"
	}
	k := posKey(sym, side)

	var vec []float64
	if ctx.MarketDataMap != nil {
		if md := ctx.MarketDataMap[sym]; md != nil {
			vec = BuildSignalVector(sym, md)
		}
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.episodes[k] = &TradeEpisode{
		TradeID:         fmt.Sprintf("%s_%s_%d", tm.traderID, k, time.Now().UnixMilli()),
		TraderID:        tm.traderID,
		Symbol:          sym,
		Side:            side,
		OpenTime:        time.Now(),
		EntryPrice:      entryPrice,
		Quantity:        quantity,
		Leverage:        dec.Leverage,
		PositionSizeUSD: dec.PositionSizeUSD,
		StopLoss:        dec.StopLoss,
		TakeProfit:      dec.TakeProfit,
		EntryReasoning:  strings.TrimSpace(dec.Reasoning),
		EntryConfidence: dec.Confidence,
		SignalVector:    vec,
		Rolling:         RollingMetrics{},
	}
	if err := tm.persistEpisodesLocked(); err != nil {
		// The position is live even if the local checkpoint failed; keep trading and
		// let a later position refresh retry persistence.
		return
	}
}

// OnPartialCloseSuccess accumulates a locally executed partial exit without
// finalizing the episode. The subsequent final close, including an exchange-side
// conditional close detected by reconciliation, records the total lifecycle PnL.
func (tm *TradeMemory) OnPartialCloseSuccess(dec *decision.Decision, side string, quantity, exitPrice float64) error {
	if tm == nil || dec == nil || quantity <= 0 || exitPrice <= 0 {
		return nil
	}
	sym := strings.ToUpper(strings.TrimSpace(dec.Symbol))
	side = strings.ToLower(strings.TrimSpace(side))
	if sym == "" || (side != "long" && side != "short") {
		return nil
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	ep := tm.episodes[posKey(sym, side)]
	if ep == nil || ep.EntryPrice <= 0 || ep.Quantity <= 0 {
		return nil
	}
	remaining := ep.Quantity - ep.ClosedQuantity
	if remaining <= 0 {
		return nil
	}
	closedQty := math.Min(quantity, remaining)
	pnl := (exitPrice - ep.EntryPrice) * closedQty
	if side == "short" {
		pnl = -pnl
	}
	ep.RealizedPnL += pnl
	ep.ClosedQuantity += closedQty
	return tm.persistEpisodesLocked()
}

// OnCloseSuccess finalizes an episode into a persisted TradeRecord and triggers post-trade review agent (async).
func (tm *TradeMemory) OnCloseSuccess(ctx *decision.Context, dec *decision.Decision, exitPrice float64, exitReason string) (*TradeRecord, error) {
	if dec == nil {
		return nil, nil
	}
	sym := strings.ToUpper(strings.TrimSpace(dec.Symbol))
	if sym == "" {
		return nil, nil
	}
	side := "long"
	if dec.Action == decision.ActionCloseShort {
		side = "short"
	}
	k := posKey(sym, side)

	tm.mu.Lock()
	if tm.closing == nil {
		tm.closing = make(map[string]struct{})
	}
	if _, alreadyClosing := tm.closing[k]; alreadyClosing {
		tm.mu.Unlock()
		return nil, nil
	}
	ep := tm.episodes[k]
	if ep == nil {
		// Never synthesize a zero-quantity completed trade: an absent episode means
		// this close was already finalized or was not observed by the local ledger.
		tm.mu.Unlock()
		return nil, nil
	}
	tm.closing[k] = struct{}{}
	tm.mu.Unlock()

	clearClosing := func() {
		tm.mu.Lock()
		delete(tm.closing, k)
		tm.mu.Unlock()
	}

	closeTime := time.Now()
	durationS := int64(0)
	if !ep.OpenTime.IsZero() {
		durationS = int64(closeTime.Sub(ep.OpenTime).Seconds())
		if durationS < 0 {
			durationS = 0
		}
	}

	// PnL computation (USDT) includes any earlier partial exits from this episode.
	// The final close only applies to the quantity still open on the exchange.
	remainingQty := ep.Quantity - ep.ClosedQuantity
	if remainingQty < 0 {
		remainingQty = 0
	}
	pnl := ep.RealizedPnL
	if remainingQty > 0 && ep.EntryPrice > 0 {
		if side == "long" {
			pnl += (exitPrice - ep.EntryPrice) * remainingQty
		} else {
			pnl += (ep.EntryPrice - exitPrice) * remainingQty
		}
	}
	pnlPct := 0.0
	if ep.EntryPrice > 0 && ep.Quantity > 0 {
		pnlPct = pnl / (ep.EntryPrice * ep.Quantity) * 100
	}

	tr := &TradeRecord{
		TradeID:         ep.TradeID,
		TraderID:        tm.traderID,
		Symbol:          sym,
		Side:            side,
		OpenTime:        ep.OpenTime,
		CloseTime:       closeTime,
		DurationS:       durationS,
		EntryPrice:      ep.EntryPrice,
		ExitPrice:       exitPrice,
		Quantity:        ep.Quantity,
		Leverage:        ep.Leverage,
		PositionSizeUSD: ep.PositionSizeUSD,
		PnL:             pnl,
		PnLPct:          pnlPct,
		MaxFavorablePct: ep.Rolling.MaxFavorablePct,
		MaxAdversePct:   ep.Rolling.MaxAdversePct,
		StopLoss:        ep.StopLoss,
		TakeProfit:      ep.TakeProfit,
		ExitReason:      strings.TrimSpace(exitReason),
		EntryReasoning:  ep.EntryReasoning,
		ExitReasoning:   strings.TrimSpace(dec.Reasoning),
		EntryConfidence: ep.EntryConfidence,
		SignalVector:    ep.SignalVector,
	}

	// Persist the trade record before removing the checkpointed open episode.
	if err := appendJSONL(tm.tradesPath, tr); err != nil {
		clearClosing()
		return tr, err
	}

	// Update in-memory history (recent first, keep bounded).
	tm.mu.Lock()
	delete(tm.episodes, k)
	tm.history = append([]*TradeRecord{tr}, tm.history...)
	if len(tm.history) > 5000 {
		tm.history = tm.history[:5000]
	}
	checkpointErr := tm.persistEpisodesLocked()
	delete(tm.closing, k)
	tm.mu.Unlock()
	if checkpointErr != nil {
		// The completed record is durable; surface checkpoint failure so callers can
		// log it rather than silently allowing a later duplicate close attempt.
		return tr, fmt.Errorf("checkpoint closed trade episode: %w", checkpointErr)
	}

	// Async post-trade review + analysis file.
	go func() {
		a, err := callPostTradeReviewLLM(tr)
		if err != nil || a == nil {
			return
		}
		p := analysisFilePath(tm.baseDir, tr.TradeID)
		_ = writeJSONFile(p, a)

		tm.mu.Lock()
		if tm.analysisCache == nil {
			tm.analysisCache = make(map[string]*TradeAnalysis)
		}
		tm.analysisCache[tr.TradeID] = a
		tm.mu.Unlock()
	}()

	return tr, nil
}

// ManualClose records a trade close when the close was triggered externally (e.g., API CloseAllPositions).
// This is best-effort and will not have full decision reasoning.
func (tm *TradeMemory) ManualClose(symbol, side string, exitPrice float64) (*TradeRecord, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	side = strings.ToLower(strings.TrimSpace(side))
	if sym == "" || (side != "long" && side != "short") {
		return nil, nil
	}
	dec := &decision.Decision{
		Symbol: sym,
		Action: func() string {
			if side == "long" {
				return decision.ActionCloseLong
			}
			return decision.ActionCloseShort
		}(),
		Reasoning: "manual_close",
	}
	return tm.OnCloseSuccess(nil, dec, exitPrice, "manual_close")
}

// Health check: ensure base directory exists (for runtime environments that wipe dirs).
func (tm *TradeMemory) EnsureStorage() {
	_ = ensureDir(tm.baseDir)
	_ = ensureDir(filepath.Join(tm.baseDir, "analyses"))
	// ensure trades path can be created
	if _, err := os.Stat(tm.tradesPath); err != nil && os.IsNotExist(err) {
		_ = appendJSONL(tm.tradesPath, map[string]any{
			"_":  "trade_memory_initialized",
			"ts": time.Now().Format(time.RFC3339),
		})
		// remove the init line to keep file clean? keep it; loader will ignore unknown shape.
	}
}

// AnalyzeCompletedTrades calculates strategy feedback from the canonical completed
// trade ledger. Both local closes and reconciled exchange conditional closes are
// persisted there, unlike decision-cycle logs which can omit exchange-side fills.
func (tm *TradeMemory) AnalyzeCompletedTrades(tradeLimit int) *logger.PerformanceAnalysis {
	analysis := &logger.PerformanceAnalysis{
		RecentTrades: []logger.TradeOutcome{},
		SymbolStats:  make(map[string]*logger.SymbolPerformance),
	}
	if tm == nil {
		return analysis
	}

	tm.mu.Lock()
	history := append([]*TradeRecord(nil), tm.history...)
	tm.mu.Unlock()
	if len(history) == 0 {
		return analysis
	}

	// history is stored newest-first; calculate streaks in close-time order.
	sort.SliceStable(history, func(i, j int) bool { return history[i].CloseTime.Before(history[j].CloseTime) })
	returns := make([]float64, 0, len(history))
	currentLosingStreak, maxLosingStreak := 0, 0
	totalWin, totalLoss := 0.0, 0.0

	for _, trade := range history {
		if trade == nil || trade.Symbol == "" || trade.CloseTime.IsZero() {
			continue
		}
		analysis.TotalTrades++
		returns = append(returns, trade.PnLPct)
		if trade.PnL > 0 {
			analysis.WinningTrades++
			totalWin += trade.PnL
			currentLosingStreak = 0
		} else if trade.PnL < 0 {
			analysis.LosingTrades++
			totalLoss += trade.PnL
			currentLosingStreak++
			if currentLosingStreak > maxLosingStreak {
				maxLosingStreak = currentLosingStreak
			}
		}

		stats := analysis.SymbolStats[trade.Symbol]
		if stats == nil {
			stats = &logger.SymbolPerformance{Symbol: trade.Symbol}
			analysis.SymbolStats[trade.Symbol] = stats
		}
		stats.TotalTrades++
		stats.TotalPnL += trade.PnL
		if trade.PnL > 0 {
			stats.WinningTrades++
		} else if trade.PnL < 0 {
			stats.LosingTrades++
		}
	}

	analysis.CurrentLosingStreak = currentLosingStreak
	analysis.MaxLosingStreak = maxLosingStreak
	if analysis.TotalTrades > 0 {
		analysis.WinRate = float64(analysis.WinningTrades) / float64(analysis.TotalTrades)
	}
	if analysis.WinningTrades > 0 {
		analysis.AvgWin = totalWin / float64(analysis.WinningTrades)
	}
	if analysis.LosingTrades > 0 {
		analysis.AvgLoss = totalLoss / float64(analysis.LosingTrades)
	}
	if totalLoss != 0 {
		analysis.ProfitFactor = totalWin / -totalLoss
	}
	analysis.SharpeRatio = completedTradeSharpe(returns)

	bestPnL, worstPnL := math.Inf(-1), math.Inf(1)
	for symbol, stats := range analysis.SymbolStats {
		if stats.TotalTrades == 0 {
			continue
		}
		stats.WinRate = float64(stats.WinningTrades) / float64(stats.TotalTrades) * 100
		stats.AvgPnL = stats.TotalPnL / float64(stats.TotalTrades)
		if stats.TotalPnL > bestPnL {
			bestPnL, analysis.BestSymbol = stats.TotalPnL, symbol
		}
		if stats.TotalPnL < worstPnL {
			worstPnL, analysis.WorstSymbol = stats.TotalPnL, symbol
		}
	}

	for i := len(history) - 1; i >= 0 && (tradeLimit <= 0 || len(analysis.RecentTrades) < tradeLimit); i-- {
		trade := history[i]
		if trade == nil || trade.Symbol == "" || trade.CloseTime.IsZero() {
			continue
		}
		analysis.RecentTrades = append(analysis.RecentTrades, logger.TradeOutcome{
			Symbol:      trade.Symbol,
			Side:        trade.Side,
			OpenPrice:   trade.EntryPrice,
			ClosePrice:  trade.ExitPrice,
			PnL:         trade.PnL,
			PnLPct:      trade.PnLPct,
			Duration:    time.Duration(trade.DurationS * int64(time.Second)).String(),
			OpenTime:    trade.OpenTime,
			CloseTime:   trade.CloseTime,
			WasStopLoss: strings.Contains(trade.ExitReason, "stop_loss"),
			CloseSource: trade.ExitReason,
		})
	}
	return analysis
}

// CompletedSymbolStats derives closed-trade summaries from the canonical ledger.
// It intentionally reports no open lots: live positions are supplied separately by
// the API from the exchange snapshot.
func (tm *TradeMemory) CompletedSymbolStats() map[string]*logger.SymbolTradeStats {
	stats := make(map[string]*logger.SymbolTradeStats)
	if tm == nil {
		return stats
	}
	tm.mu.Lock()
	history := append([]*TradeRecord(nil), tm.history...)
	tm.mu.Unlock()
	for _, trade := range history {
		if trade == nil || trade.Symbol == "" || trade.CloseTime.IsZero() {
			continue
		}
		item := stats[trade.Symbol]
		if item == nil {
			item = &logger.SymbolTradeStats{Symbol: trade.Symbol}
			stats[trade.Symbol] = item
		}
		item.TotalTrades++
		item.RealizedPnL += trade.PnL
		if trade.PnL > 0 {
			item.WinCount++
		} else if trade.PnL < 0 {
			item.LossCount++
		}
		if strings.EqualFold(trade.Side, "short") {
			item.CloseShortCount++
		} else {
			item.CloseLongCount++
		}
		closedAt := trade.CloseTime.Format(time.RFC3339)
		if item.FirstTradeTime == "" || closedAt < item.FirstTradeTime {
			item.FirstTradeTime = closedAt
		}
		if item.LastTradeTime == "" || closedAt > item.LastTradeTime {
			item.LastTradeTime = closedAt
		}
	}
	return stats
}

func completedTradeSharpe(returns []float64) float64 {
	if len(returns) < 2 {
		return 0
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		delta := r - mean
		variance += delta * delta
	}
	stdDev := math.Sqrt(variance / float64(len(returns)-1))
	if stdDev == 0 {
		return 0
	}
	return mean / stdDev
}

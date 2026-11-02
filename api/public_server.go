package api

import (
	"context"
	"errors"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"nofx/config"
	"nofx/manager"
	traderpkg "nofx/trader"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	publicRefreshInterval = time.Minute
	publicStaleAfter      = 3 * time.Minute
	publicMaxHistory      = maxEquityHistoryLimit
)

// Public DTOs are intentionally separate from all management API types. Never
// embed AccountInfo, EquityPoint, ChartAction, decision records or raw maps here.
type publicSummary struct {
	Slug                  string     `json:"slug"`
	Name                  string     `json:"name"`
	State                 string     `json:"state"`
	Stale                 bool       `json:"stale"`
	AsOf                  *time.Time `json:"as_of"`
	ReturnPct             *float64   `json:"return_pct"`
	SampledMaxDrawdownPct *float64   `json:"sampled_max_drawdown_pct"`
	HistoryFrom           *time.Time `json:"history_from"`
	HistoryTo             *time.Time `json:"history_to"`
	HistoryLimitReached   bool       `json:"history_limit_reached"`
}

type publicReturnPoint struct {
	CycleNumber int            `json:"cycle_number"`
	Timestamp   time.Time      `json:"timestamp"`
	ReturnPct   float64        `json:"return_pct"`
	Actions     []publicAction `json:"actions"`
	// Unrounded, dimensionless equity ratio for rebasing each requested window.
	// Never retain absolute account amounts or rebase rounded percentages.
	relativeEquity float64
}

type publicAction struct {
	Symbol  string `json:"symbol"`
	Action  string `json:"action"`
	Success bool   `json:"success"`
}

type publicPosition struct {
	Symbol         string  `json:"symbol"`
	Side           string  `json:"side"`
	PriceChangePct float64 `json:"price_change_pct"`
}

type publicReturnsResponse struct {
	publicSummary
	ReturnBasis string              `json:"return_basis"`
	Points      []publicReturnPoint `json:"points"`
}

type publicPositionsResponse struct {
	Slug      string           `json:"slug"`
	State     string           `json:"state"`
	Stale     bool             `json:"stale"`
	AsOf      *time.Time       `json:"as_of"`
	Positions []publicPosition `json:"positions"`
}

// Raw inputs are transient and never retained in the public cache or serialized.
type publicEquitySample struct {
	CycleNumber int
	Timestamp   time.Time
	Equity      float64
	Actions     []publicAction
}

type publicRawSnapshot struct {
	AsOf                time.Time
	Equity              float64
	InitialBalance      float64
	History             []publicEquitySample
	HistoryLimitReached bool
	Positions           []publicPosition
}

type publicDataSource interface {
	Read() (publicRawSnapshot, error)
}

type publicTraderSource struct{ trader *traderpkg.AutoTrader }

func (s publicTraderSource) Read() (publicRawSnapshot, error) {
	account, err := s.trader.GetAccountInfo()
	if err != nil {
		return publicRawSnapshot{}, err
	}
	equity, equityOK := account["total_equity"].(float64)
	initial, initialOK := account["initial_balance"].(float64)
	if !equityOK || !initialOK {
		return publicRawSnapshot{}, errors.New("account snapshot unavailable")
	}
	// These getters reuse the trader's existing 30-second account/position caches.
	positions, err := s.trader.GetPositions()
	if err != nil {
		return publicRawSnapshot{}, err
	}
	records, err := s.trader.GetDecisionLogger().GetChartHistory(publicMaxHistory, 0, 0, true)
	if err != nil {
		return publicRawSnapshot{}, err
	}
	result := publicRawSnapshot{
		AsOf: time.Now().UTC(), Equity: equity, InitialBalance: initial,
		HistoryLimitReached: len(records) >= publicMaxHistory,
		Positions:           make([]publicPosition, 0, len(positions)),
	}
	for _, r := range records {
		point := publicEquitySample{CycleNumber: r.CycleNumber, Timestamp: r.Timestamp, Equity: r.AccountState.TotalBalance}
		for _, a := range r.Actions {
			point.Actions = append(point.Actions, publicAction{Symbol: a.Symbol, Action: publicActionName(a.Action), Success: a.Success})
		}
		result.History = append(result.History, point)
	}
	for _, p := range positions {
		symbol, symbolOK := p["symbol"].(string)
		side, sideOK := p["side"].(string)
		pct, pctOK := p["unrealized_pnl_pct"].(float64)
		if !symbolOK || !sideOK || !pctOK || symbol == "" || (side != "long" && side != "short") || !finite(pct) {
			return publicRawSnapshot{}, errors.New("position snapshot unavailable")
		}
		result.Positions = append(result.Positions, publicPosition{Symbol: symbol, Side: side, PriceChangePct: roundPublicPct(pct)})
	}
	return result, nil
}

type publicSnapshot struct {
	Summary   publicSummary
	Points    []publicReturnPoint
	Positions []publicPosition
}

type publicEntry struct {
	config   config.PublicStrategyConfig
	source   publicDataSource
	snapshot *publicSnapshot // immutable after assignment, protected by PublicServer.mu
	failed   bool
	logs     *publicLogsCache
}

// PublicServer has its own router and loopback listener. It cannot route requests
// to the management handlers. Market routes only read sanitized in-memory
// snapshots and never call a trader or exchange. Log routes reuse the existing
// secure file reader with a narrow DTO and bounded in-memory caching.
type PublicServer struct {
	router  *gin.Engine
	port    int
	mu      sync.RWMutex
	entries map[string]*publicEntry
	order   []string
	now     func() time.Time
}

func NewPublicServer(tm *manager.TraderManager, cfg config.PublicAPIConfig) (*PublicServer, error) {
	if !cfg.Enabled {
		return nil, errors.New("public API is disabled")
	}
	if tm == nil {
		return nil, errors.New("trader manager unavailable")
	}
	ids := make(map[string]bool)
	for _, t := range tm.GetAllTraders() {
		ids[t.GetID()] = true
	}
	if err := cfg.Validate(ids, 0); err != nil {
		return nil, err
	}
	sources := make(map[string]publicDataSource)
	for _, strategy := range cfg.Strategies {
		t, err := tm.GetTrader(strategy.TraderID)
		if err != nil {
			return nil, errors.New("published trader unavailable")
		}
		sources[strategy.Slug] = publicTraderSource{trader: t}
	}
	return newPublicServer(cfg, sources), nil
}

func newPublicServer(cfg config.PublicAPIConfig, sources map[string]publicDataSource) *PublicServer {
	s := &PublicServer{
		port: cfg.Port, entries: make(map[string]*publicEntry), now: time.Now,
	}
	for _, strategy := range cfg.Strategies {
		s.entries[strategy.Slug] = &publicEntry{
			config: strategy, source: sources[strategy.Slug],
			logs: newPublicLogsCache("./decision_logs", strategy.TraderID),
		}
		s.order = append(s.order, strategy.Slug)
	}
	// A fresh engine, not Server.router or its /api route group. No permissive CORS.
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		if c.Request.Method != http.MethodGet {
			c.Header("Allow", http.MethodGet)
			c.AbortWithStatusJSON(http.StatusMethodNotAllowed, gin.H{"error": "read_only"})
			return
		}
		c.Next()
	})
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/api/public/v1/strategies", s.handleList)
	r.GET("/api/public/v1/strategies/:slug/summary", s.handleSummary)
	r.GET("/api/public/v1/strategies/:slug/returns", s.handleReturns)
	r.GET("/api/public/v1/strategies/:slug/positions", s.handlePositions)
	r.GET("/api/public/v1/strategies/:slug/decisions", s.handlePublicDecisions)
	r.GET("/api/public/v1/strategies/:slug/logs", s.handlePublicLogList)
	r.GET("/api/public/v1/strategies/:slug/logs/:cycle", s.handlePublicLogDetail)
	r.NoRoute(func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "not_found"}) })
	s.router = r
	return s
}

// Start binds synchronously so a port conflict fails before trading is started.
// Cancelling ctx closes the listener without interrupting the trading engine.
func (s *PublicServer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port)))
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: s.router, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("public API listener stopped: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		ticker := time.NewTicker(publicRefreshInterval)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			s.refresh(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	log.Printf("🌐 公开只读API启动在 http://%s", listener.Addr())
	return nil
}

func (s *PublicServer) refresh(ctx context.Context) {
	for _, slug := range s.order {
		if ctx.Err() != nil {
			return
		}
		entry := s.entries[slug]
		snapshot, err := entry.collect()
		s.mu.Lock()
		entry.failed = err != nil
		if err == nil {
			entry.snapshot = snapshot
		}
		s.mu.Unlock()
		// Do not echo upstream errors; they may contain private account details.
		if err != nil {
			log.Printf("public snapshot unavailable for %s", slug)
		}
	}
}

// A malformed upstream snapshot must not let the new background collector crash
// the trading process. Gin's HTTP recovery does not cover this goroutine.
func (e *publicEntry) collect() (snapshot *publicSnapshot, err error) {
	defer func() {
		if recover() != nil {
			snapshot = nil
			err = errors.New("snapshot collection failed")
		}
	}()
	raw, err := e.source.Read()
	if err != nil {
		return nil, err
	}
	return sanitizePublicSnapshot(e.config, raw)
}

func sanitizePublicSnapshot(cfg config.PublicStrategyConfig, raw publicRawSnapshot) (*publicSnapshot, error) {
	if raw.AsOf.IsZero() || !finite(raw.InitialBalance) || raw.InitialBalance <= 0 || !finite(raw.Equity) || raw.Equity < 0 {
		return nil, errors.New("invalid account snapshot")
	}
	returnPct := (raw.Equity/raw.InitialBalance - 1) * 100
	if !finite(returnPct) {
		return nil, errors.New("invalid account return")
	}
	returnPct = roundPublicPct(returnPct)
	asOf := raw.AsOf.UTC()
	result := &publicSnapshot{
		Summary: publicSummary{
			Slug: cfg.Slug, Name: cfg.Name, State: "ready", AsOf: &asOf,
			ReturnPct: &returnPct, HistoryLimitReached: raw.HistoryLimitReached,
		},
		Points:    make([]publicReturnPoint, 0, len(raw.History)),
		Positions: append([]publicPosition{}, raw.Positions...),
	}
	// Sort a copy: callers may share or cache their input history.
	history := append([]publicEquitySample(nil), raw.History...)
	sort.SliceStable(history, func(i, j int) bool { return history[i].CycleNumber < history[j].CycleNumber })
	peak, maxDrawdown := 0.0, 0.0
	basis := 1.0
	if len(history) > 0 {
		basis = history[0].Equity
	}
	for _, p := range history {
		relative := p.Equity / basis
		pct := (relative - 1) * 100
		if p.CycleNumber <= 0 || p.Timestamp.IsZero() || p.Timestamp.After(raw.AsOf) || !finite(p.Equity) || p.Equity <= 0 || !finite(relative) || relative <= 0 || !finite(pct) {
			return nil, errors.New("invalid history sample")
		}
		if p.Equity > peak {
			peak = p.Equity
		}
		if peak > 0 {
			maxDrawdown = math.Max(maxDrawdown, (peak-p.Equity)/peak*100)
		}
		actions := append([]publicAction{}, p.Actions...)
		for i := range actions {
			actions[i].Action = publicActionName(actions[i].Action)
		}
		result.Points = append(result.Points, publicReturnPoint{
			CycleNumber: p.CycleNumber, Timestamp: p.Timestamp.UTC(), ReturnPct: roundPublicPct(pct),
			Actions: actions, relativeEquity: relative,
		})
	}
	for _, p := range result.Positions {
		if p.Symbol == "" || (p.Side != "long" && p.Side != "short") || !finite(p.PriceChangePct) {
			return nil, errors.New("invalid position")
		}
	}
	if len(result.Points) > 0 {
		maxDrawdown = roundPublicPct(maxDrawdown)
		result.Summary.SampledMaxDrawdownPct = &maxDrawdown
		result.Summary.HistoryFrom = &result.Points[0].Timestamp
		result.Summary.HistoryTo = &result.Points[len(result.Points)-1].Timestamp
	}
	return result, nil
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func roundPublicPct(n float64) float64 {
	rounded := math.Round(n*100) / 100
	if !finite(rounded) {
		return n // Preserve a finite number even when multiplication would overflow.
	}
	if rounded == 0 {
		return 0 // Avoid displaying -0.00%.
	}
	return rounded
}

// Returns a copy of the metadata; the cached slices remain immutable.
func (s *PublicServer) read(slug string) (publicSummary, *publicSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[slug]
	if !ok {
		return publicSummary{}, nil, false
	}
	if entry.snapshot == nil {
		return publicSummary{Slug: slug, Name: entry.config.Name, State: "unavailable", Stale: true}, nil, true
	}
	summary := entry.snapshot.Summary
	summary.Stale = entry.failed || s.now().Sub(*summary.AsOf) > publicStaleAfter
	if summary.Stale {
		summary.State = "stale"
	}
	return summary, entry.snapshot, true
}

func (s *PublicServer) handleList(c *gin.Context) {
	if !publicQuery(c) {
		return
	}
	items := make([]publicSummary, 0, len(s.order))
	for _, slug := range s.order {
		summary, _, _ := s.read(slug)
		items = append(items, summary)
	}
	c.JSON(http.StatusOK, gin.H{"strategies": items})
}

func (s *PublicServer) lookup(c *gin.Context) (publicSummary, *publicSnapshot, bool) {
	summary, snapshot, exists := s.read(c.Param("slug"))
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return summary, nil, false
	}
	if snapshot == nil {
		c.Header("Retry-After", "60")
		c.JSON(http.StatusServiceUnavailable, summary)
		return summary, nil, false
	}
	return summary, snapshot, true
}

func (s *PublicServer) handleSummary(c *gin.Context) {
	if !publicQuery(c) {
		return
	}
	if summary, _, ok := s.lookup(c); ok {
		c.JSON(http.StatusOK, summary)
	}
}

func (s *PublicServer) handlePositions(c *gin.Context) {
	if !publicQuery(c) {
		return
	}
	if summary, snapshot, ok := s.lookup(c); ok {
		c.JSON(http.StatusOK, publicPositionsResponse{
			Slug: summary.Slug, State: summary.State, Stale: summary.Stale,
			AsOf: summary.AsOf, Positions: snapshot.Positions,
		})
	}
}

func (s *PublicServer) handleReturns(c *gin.Context) {
	if !publicQuery(c, "limit") {
		return
	}
	limit := defaultEquityHistoryLimit
	if raw, exists := c.GetQuery("limit"); exists {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > publicMaxHistory {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit_must_be_between_1_and_20000"})
			return
		}
		limit = n
	} else if _, exists := c.Request.URL.Query()["limit"]; exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_limit"})
		return
	}
	if summary, snapshot, ok := s.lookup(c); ok {
		c.JSON(http.StatusOK, publicReturnsWindow(summary, snapshot.Points, limit))
	}
}

// Match EquityChart: newest N consecutive cycles, with the first returned
// cycle as the baseline. Never drop intermediate cycles or their trade markers.
func publicReturnsWindow(summary publicSummary, points []publicReturnPoint, limit int) publicReturnsResponse {
	if len(points) > limit {
		points = points[len(points)-limit:]
		summary.HistoryLimitReached = true
	}
	result := make([]publicReturnPoint, len(points))
	summary.ReturnPct, summary.SampledMaxDrawdownPct = nil, nil
	summary.HistoryFrom, summary.HistoryTo = nil, nil
	if len(points) > 0 {
		basis, peak, drawdown := points[0].relativeEquity, 0.0, 0.0
		for i, point := range points {
			result[i] = point
			result[i].ReturnPct = roundPublicPct((point.relativeEquity - basis) / basis * 100)
			peak = math.Max(peak, point.relativeEquity)
			drawdown = math.Max(drawdown, (peak-point.relativeEquity)/peak*100)
		}
		drawdown = roundPublicPct(drawdown)
		summary.ReturnPct = &result[len(result)-1].ReturnPct
		summary.SampledMaxDrawdownPct = &drawdown
		summary.HistoryFrom, summary.HistoryTo = &result[0].Timestamp, &result[len(result)-1].Timestamp
	}
	return publicReturnsResponse{publicSummary: summary, ReturnBasis: "window_start", Points: result}
}

// Reject internal and duplicate query parameters instead of silently accepting
// trader_id, include_actions, file paths, arbitrary ranges, or future options.
func publicQuery(c *gin.Context, allowed ...string) bool {
	// URL.Query silently drops malformed values, so use ParseQuery explicitly.
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_query"})
		return false
	}
	for key, values := range query {
		ok := false
		for _, name := range allowed {
			ok = ok || key == name
		}
		if !ok || len(values) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_query"})
			return false
		}
	}
	return true
}

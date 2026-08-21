package api

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"nofx/manager"
	"nofx/market"
	traderpkg "nofx/trader"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Server HTTP API服务器
type Server struct {
	router        *gin.Engine
	traderManager *manager.TraderManager
	port          int
	equityCache   *EquityHistoryCache
}

// NewServer 创建API服务器
func NewServer(traderManager *manager.TraderManager, port int) *Server {
	// 设置为Release模式（减少日志输出）
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()

	// 启用CORS
	router.Use(corsMiddleware())

	s := &Server{
		router:        router,
		traderManager: traderManager,
		port:          port,
		equityCache:   NewEquityHistoryCache(maxEquityHistoryLimit, 30*time.Second),
	}

	// 设置路由
	s.setupRoutes()

	return s
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}

// LogEntry 日志文件条目
type LogEntry struct {
	Filename  string    `json:"filename"`
	Cycle     int       `json:"cycle"`
	Timestamp time.Time `json:"timestamp"`
	Size      int64     `json:"size"`
	Preview   string    `json:"preview,omitempty"` // 可选：简短预览
}

// SetupRoutes 设置路由
func (s *Server) setupRoutes() {
	// 健康检查
	s.router.GET("/health", s.handleHealth)

	// API路由组
	api := s.router.Group("/api")
	{
		// 竞赛总览
		api.GET("/competition", s.handleCompetition)
		// 市场概览（BTC/ETH/SOL 价格+涨跌+资金费率 + Fear&Greed）
		api.GET("/market-overview", s.handleMarketOverview)

		// Trader列表
		api.GET("/traders", s.handleTraderList)

		// 指定trader的数据（使用query参数 ?trader_id=xxx）
		api.GET("/status", s.handleStatus)
		api.GET("/account", s.handleAccount)
		api.GET("/positions", s.handlePositions)
		api.GET("/decisions", s.handleDecisions)
		api.GET("/decisions/latest", s.handleLatestDecisions)
		api.GET("/statistics", s.handleStatistics)
		api.GET("/equity-history", s.handleEquityHistory)
		api.GET("/performance", s.handlePerformance)
		// 交易币种盈亏汇总（按 symbol 聚合）
		api.GET("/traded-symbols", s.handleTradedSymbols)

		// K线数据（代理Binance fapi）
		api.GET("/klines", s.handleKlines)

		// 交易所数据（订单/聚合统计）
		ex := api.Group("/exchange")
		{
			// 按 symbol 聚合的订单统计（用于详情页“交易币种”）
			ex.GET("/traded-symbols", s.handleExchangeTradedSymbols)
			// 单币种订单明细（用于展开查看）
			ex.GET("/orders", s.handleExchangeOrders)
		}

		// LLM 费用统计
		api.GET("/llm-cost", s.handleLLMCost)

		// 错误统计
		api.GET("/error-stats", s.handleErrorStats)
		api.GET("/error-stats/recent", s.handleRecentErrors)

		// 平仓操作（POST请求）
		api.POST("/close-all-positions", s.handleCloseAllPositions)
		api.POST("/close-positions", s.handleClosePositions)

		// 系统控制
		api.POST("/system/pause", s.handleSystemPause)
		api.GET("/system/auto-resume", s.handleGetAutoResume)
		api.POST("/system/auto-resume", s.handleSetAutoResume)

		// 高峰时段暂停
		api.GET("/peak-hour", s.handleGetPeakHour)
		api.POST("/peak-hour/override", s.handlePeakHourOverride)
		api.POST("/peak-hour/toggle", s.handlePeakHourToggle)

		// 运行时配置（热更新）
		api.GET("/config", s.handleGetConfig)
		api.PUT("/config", s.handleUpdateConfig)

		// 日志浏览接口 (Merged from LogViewer)
		logs := api.Group("/logs")
		{
			logs.GET("/traders", s.handleLogTraderList)
			logs.GET("/list", s.handleLogList)
			logs.GET("/detail", s.handleLogDetail)
		}
	}
}

// ===== Traded Symbols Summary =====

type tradedSymbolStatus string

const (
	tradedSymbolHoldingLong  tradedSymbolStatus = "holding_long"
	tradedSymbolHoldingShort tradedSymbolStatus = "holding_short"
	tradedSymbolClosed       tradedSymbolStatus = "closed"
)

type tradedSymbolCurrentPosition struct {
	EntryPrice float64 `json:"entry_price"`
	MarkPrice  float64 `json:"mark_price"`
	Quantity   float64 `json:"quantity"`
	Leverage   int     `json:"leverage"`
}

type tradedSymbolSummaryItem struct {
	Symbol string             `json:"symbol"`
	Status tradedSymbolStatus `json:"status"`

	TotalPnL      float64 `json:"total_pnl"`
	RealizedPnL   float64 `json:"realized_pnl"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	AvgPnL        float64 `json:"avg_pnl"`

	TotalTrades int     `json:"total_trades"`
	WinRate     float64 `json:"win_rate"`
	WinCount    int     `json:"win_count"`
	LossCount   int     `json:"loss_count"`

	OpenLongCount     int `json:"open_long_count"`
	OpenShortCount    int `json:"open_short_count"`
	CloseLongCount    int `json:"close_long_count"`
	CloseShortCount   int `json:"close_short_count"`
	PartialCloseCount int `json:"partial_close_count"`

	CurrentPosition *tradedSymbolCurrentPosition `json:"current_position,omitempty"`
	FirstTradeTime  string                       `json:"first_trade_time"`
	LastTradeTime   string                       `json:"last_trade_time"`
}

type tradedSymbolsSummary struct {
	TotalSymbols       int     `json:"total_symbols"`
	HoldingCount       int     `json:"holding_count"`
	ClosedCount        int     `json:"closed_count"`
	TotalRealizedPnL   float64 `json:"total_realized_pnl"`
	TotalUnrealizedPnL float64 `json:"total_unrealized_pnl"`
	TotalTrades        int     `json:"total_trades"`
	TotalWinCount      int     `json:"total_win_count"`
	TotalWinRate       float64 `json:"total_win_rate"`
}

type tradedSymbolsResponse struct {
	Symbols []tradedSymbolSummaryItem `json:"symbols"`
	Summary tradedSymbolsSummary      `json:"summary"`
}

// ===== Exchange (Orders-based) =====

type exchangeOrderStats struct {
	Symbol string `json:"symbol"`

	// Orders counters
	TotalOrders  int `json:"total_orders"`
	FilledOrders int `json:"filled_orders"`

	// Qty / Notional
	TotalExecutedQty float64 `json:"total_executed_qty"`
	TotalNotional    float64 `json:"total_notional"`

	// Estimated fees (based on assumed taker fee; order endpoint doesn't return commission)
	EstimatedFee float64 `json:"estimated_fee"`

	// Real income from exchange (via /fapi/v1/income; zero if unavailable)
	RealCommission  float64 `json:"real_commission"`   // 真实手续费（负数=支出）
	RealFundingFee  float64 `json:"real_funding_fee"`  // 真实资金费用（正=收到, 负=支出）
	RealRealizedPnL float64 `json:"real_realized_pnl"` // 交易所已实现盈亏

	// Pairing-based realized PnL (order-based estimate)
	RealizedPnL float64 `json:"realized_pnl"`
	Trades      int     `json:"trades"`
	WinCount    int     `json:"win_count"`
	LossCount   int     `json:"loss_count"`
	WinRate     float64 `json:"win_rate"`
	AvgPnL      float64 `json:"avg_pnl"`

	// Holding time estimate (seconds)
	AvgHoldSeconds float64 `json:"avg_hold_seconds"`

	// Time range covered by executed orders
	FirstTradeTime string `json:"first_trade_time"`
	LastTradeTime  string `json:"last_trade_time"`

	// Remaining open qty after pairing (best-effort)
	OpenQtyRemaining float64 `json:"open_qty_remaining"`
}

type exchangeOrdersResponse struct {
	Symbol    string                  `json:"symbol"`
	StartTime string                  `json:"start_time"`
	EndTime   string                  `json:"end_time"`
	Orders    []traderpkg.OrderRecord `json:"orders"`
	Stats     exchangeOrderStats      `json:"stats"`
}

type exchangeTradedSymbolsSummary struct {
	TotalSymbols      int     `json:"total_symbols"`
	TotalRealizedPnL  float64 `json:"total_realized_pnl"`
	TotalEstimatedFee float64 `json:"total_estimated_fee"`
	TotalTrades       int     `json:"total_trades"`
	TotalCommission   float64 `json:"total_commission"`
	TotalFundingFee   float64 `json:"total_funding_fee"`
	TotalRealRealized float64 `json:"total_real_realized_pnl"`
}

type exchangeTradedSymbolsResponse struct {
	Symbols []exchangeOrderStats         `json:"symbols"`
	Summary exchangeTradedSymbolsSummary `json:"summary"`
}

func isValidExchangeSymbol(symbol string) bool {
	symbol = strings.TrimSpace(strings.ToUpper(symbol))
	if symbol == "" || !strings.HasSuffix(symbol, "USDT") {
		return false
	}
	for _, r := range symbol {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

var exchangeInvalidSymbolCache = struct {
	sync.RWMutex
	symbols map[string]struct{}
}{symbols: make(map[string]struct{})}

func isCachedInvalidExchangeSymbol(symbol string) bool {
	exchangeInvalidSymbolCache.RLock()
	defer exchangeInvalidSymbolCache.RUnlock()
	_, ok := exchangeInvalidSymbolCache.symbols[strings.ToUpper(strings.TrimSpace(symbol))]
	return ok
}

func markInvalidExchangeSymbol(symbol string) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return
	}
	exchangeInvalidSymbolCache.Lock()
	defer exchangeInvalidSymbolCache.Unlock()
	exchangeInvalidSymbolCache.symbols[symbol] = struct{}{}
}

func isExchangeInvalidSymbolError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "code=-1121") && strings.Contains(msg, "invalid symbol")
}

func parseTimeParamToMs(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	// unix milli?
	allDigits := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		return v, nil
	}
	// RFC3339
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

// calcOrderStatsFromOrders 用“订单”数据做 best-effort 复盘统计（不含真实 commission / funding）
func calcOrderStatsFromOrders(symbol string, orders []traderpkg.OrderRecord, assumedTakerFeeRate float64) exchangeOrderStats {
	stats := exchangeOrderStats{Symbol: symbol}
	if assumedTakerFeeRate < 0 {
		assumedTakerFeeRate = 0
	}

	// 过滤：只统计有成交的订单
	execOrders := make([]traderpkg.OrderRecord, 0, len(orders))
	for _, o := range orders {
		stats.TotalOrders++
		if o.ExecutedQty > 0 {
			stats.FilledOrders++
			execOrders = append(execOrders, o)
			price := o.AvgPrice
			if price <= 0 {
				price = o.Price
			}
			notional := price * o.ExecutedQty
			stats.TotalExecutedQty += o.ExecutedQty
			stats.TotalNotional += notional
			stats.EstimatedFee += notional * assumedTakerFeeRate
		}
	}
	if len(execOrders) == 0 {
		return stats
	}

	// sort by created time
	sort.Slice(execOrders, func(i, j int) bool {
		return execOrders[i].CreatedAt.Before(execOrders[j].CreatedAt)
	})
	first := execOrders[0].CreatedAt
	last := execOrders[0].UpdatedAt
	for _, o := range execOrders {
		if o.CreatedAt.Before(first) {
			first = o.CreatedAt
		}
		if o.UpdatedAt.After(last) {
			last = o.UpdatedAt
		}
	}
	stats.FirstTradeTime = first.Format(time.RFC3339)
	stats.LastTradeTime = last.Format(time.RFC3339)

	// FIFO lots per (positionSide)
	type lot struct {
		price float64
		qty   float64
		t     time.Time
	}
	openLots := map[string][]lot{
		"LONG":  {},
		"SHORT": {},
	}
	// helpers for direction
	isOpen := func(posSide, side string) bool {
		// LONG: BUY opens; SHORT: SELL opens
		if posSide == "LONG" && side == "BUY" {
			return true
		}
		if posSide == "SHORT" && side == "SELL" {
			return true
		}
		return false
	}
	isClose := func(posSide, side string) bool {
		// LONG: SELL closes; SHORT: BUY closes
		if posSide == "LONG" && side == "SELL" {
			return true
		}
		if posSide == "SHORT" && side == "BUY" {
			return true
		}
		return false
	}
	pnlPerUnit := func(posSide string, openPrice, closePrice float64) float64 {
		if posSide == "LONG" {
			return closePrice - openPrice
		}
		// SHORT
		return openPrice - closePrice
	}

	var holdSum time.Duration
	var holdN int

	for _, o := range execOrders {
		posSide := strings.ToUpper(o.PositionSide)
		side := strings.ToUpper(o.Side)
		if posSide != "LONG" && posSide != "SHORT" {
			continue
		}
		price := o.AvgPrice
		if price <= 0 {
			price = o.Price
		}
		if price <= 0 || o.ExecutedQty <= 0 {
			continue
		}

		if isOpen(posSide, side) {
			openLots[posSide] = append(openLots[posSide], lot{price: price, qty: o.ExecutedQty, t: o.CreatedAt})
			continue
		}
		if !isClose(posSide, side) {
			continue
		}

		remaining := o.ExecutedQty
		closePnL := 0.0
		matchedAny := false
		for remaining > 0 && len(openLots[posSide]) > 0 {
			lt := openLots[posSide][0]
			use := lt.qty
			if use > remaining {
				use = remaining
			}
			closePnL += pnlPerUnit(posSide, lt.price, price) * use
			matchedAny = true
			holdSum += o.CreatedAt.Sub(lt.t)
			holdN++

			lt.qty -= use
			remaining -= use
			if lt.qty <= 0 {
				openLots[posSide] = openLots[posSide][1:]
			} else {
				openLots[posSide][0] = lt
			}
		}

		if matchedAny {
			stats.Trades++
			stats.RealizedPnL += closePnL
			if closePnL > 0 {
				stats.WinCount++
			} else if closePnL < 0 {
				stats.LossCount++
			}
		}
	}

	// remaining open qty
	for _, ps := range []string{"LONG", "SHORT"} {
		for _, lt := range openLots[ps] {
			stats.OpenQtyRemaining += lt.qty
		}
	}

	if stats.Trades > 0 {
		stats.WinRate = (float64(stats.WinCount) / float64(stats.Trades)) * 100
		stats.AvgPnL = stats.RealizedPnL / float64(stats.Trades)
	}
	if holdN > 0 {
		stats.AvgHoldSeconds = holdSum.Seconds() / float64(holdN)
	}

	return stats
}

// handleTradedSymbols 返回按币种汇总的盈亏详情（已实现 + 当前未实现）。
// 已实现统计来自统一 completed-trade ledger；当前仓位来自交易所快照。
func (s *Server) handleTradedSymbols(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	cachedStats := trader.GetCompletedSymbolStats()

	items := make(map[string]*tradedSymbolSummaryItem)
	for _, cs := range cachedStats {
		if !isValidExchangeSymbol(cs.Symbol) {
			continue
		}
		items[cs.Symbol] = &tradedSymbolSummaryItem{
			Symbol:            cs.Symbol,
			Status:            tradedSymbolClosed,
			TotalTrades:       cs.TotalTrades,
			WinCount:          cs.WinCount,
			LossCount:         cs.LossCount,
			RealizedPnL:       cs.RealizedPnL,
			OpenLongCount:     cs.OpenLongCount,
			OpenShortCount:    cs.OpenShortCount,
			CloseLongCount:    cs.CloseLongCount,
			CloseShortCount:   cs.CloseShortCount,
			PartialCloseCount: cs.PartialCloseCount,
			FirstTradeTime:    cs.FirstTradeTime,
			LastTradeTime:     cs.LastTradeTime,
		}
	}

	ensureItem := func(symbol string) *tradedSymbolSummaryItem {
		if it, ok := items[symbol]; ok {
			return it
		}
		it := &tradedSymbolSummaryItem{
			Symbol: symbol,
			Status: tradedSymbolClosed,
		}
		items[symbol] = it
		return it
	}

	positions, _ := trader.GetPositions()
	posBySymbol := make(map[string]map[string]interface{})

	// 更可靠：调用 /api/positions 用 trader.GetPositions() 返回的是 []map?；本 handler 内直接复用 trader.GetPositions()
	// 但 trader.GetPositions() 的具体类型在 interface 里定义，避免做强依赖，这里做一次 JSON roundtrip 兼容。
	type posDTO struct {
		Symbol        string  `json:"symbol"`
		Side          string  `json:"side"`
		EntryPrice    float64 `json:"entry_price"`
		MarkPrice     float64 `json:"mark_price"`
		Quantity      float64 `json:"quantity"`
		Leverage      int     `json:"leverage"`
		UnrealizedPnL float64 `json:"unrealized_pnl"`
	}

	// 将 positions 转成通用 map，再转 DTO
	rawPosBytes, _ := json.Marshal(positions)
	var posDTOs []posDTO
	_ = json.Unmarshal(rawPosBytes, &posDTOs)
	for _, p := range posDTOs {
		if !isValidExchangeSymbol(p.Symbol) {
			continue
		}
		// 当前持仓中的币种即使没有历史成交，也应在列表中展示。
		ensureItem(p.Symbol)
		posBySymbol[p.Symbol] = map[string]interface{}{
			"side":           p.Side,
			"entry_price":    p.EntryPrice,
			"mark_price":     p.MarkPrice,
			"quantity":       p.Quantity,
			"leverage":       p.Leverage,
			"unrealized_pnl": p.UnrealizedPnL,
		}
	}

	// 汇总 items -> slice + status/unrealized/total/winrate/avg
	var resp tradedSymbolsResponse
	resp.Symbols = make([]tradedSymbolSummaryItem, 0, len(items))

	totalRealized := 0.0
	totalUnrealized := 0.0
	holdingCount := 0
	totalTrades := 0
	totalWinCount := 0

	for _, it := range items {
		if it == nil {
			continue
		}

		// 未实现/状态
		if pos, ok := posBySymbol[it.Symbol]; ok {
			it.UnrealizedPnL, _ = pos["unrealized_pnl"].(float64)
			sideStr, _ := pos["side"].(string)
			if sideStr == "long" {
				it.Status = tradedSymbolHoldingLong
				holdingCount++
			} else if sideStr == "short" {
				it.Status = tradedSymbolHoldingShort
				holdingCount++
			} else {
				it.Status = tradedSymbolClosed
			}

			ep, _ := pos["entry_price"].(float64)
			mp, _ := pos["mark_price"].(float64)
			qty, _ := pos["quantity"].(float64)
			lev, _ := pos["leverage"].(int)
			it.CurrentPosition = &tradedSymbolCurrentPosition{
				EntryPrice: ep,
				MarkPrice:  mp,
				Quantity:   qty,
				Leverage:   lev,
			}
		} else {
			it.Status = tradedSymbolClosed
		}

		it.TotalPnL = it.RealizedPnL + it.UnrealizedPnL
		if it.TotalTrades > 0 {
			it.WinRate = (float64(it.WinCount) / float64(it.TotalTrades)) * 100
			it.AvgPnL = it.RealizedPnL / float64(it.TotalTrades)
		}

		totalRealized += it.RealizedPnL
		totalUnrealized += it.UnrealizedPnL
		totalTrades += it.TotalTrades
		totalWinCount += it.WinCount

		resp.Symbols = append(resp.Symbols, *it)
	}

	// 排序：持仓优先，再按 total_pnl 降序
	sort.Slice(resp.Symbols, func(i, j int) bool {
		aHolding := resp.Symbols[i].Status != tradedSymbolClosed
		bHolding := resp.Symbols[j].Status != tradedSymbolClosed
		if aHolding != bHolding {
			return aHolding
		}
		return resp.Symbols[i].TotalPnL > resp.Symbols[j].TotalPnL
	})

	totalWinRate := 0.0
	if totalTrades > 0 {
		totalWinRate = (float64(totalWinCount) / float64(totalTrades)) * 100
	}

	resp.Summary = tradedSymbolsSummary{
		TotalSymbols:       len(resp.Symbols),
		HoldingCount:       holdingCount,
		ClosedCount:        len(resp.Symbols) - holdingCount,
		TotalRealizedPnL:   totalRealized,
		TotalUnrealizedPnL: totalUnrealized,
		TotalTrades:        totalTrades,
		TotalWinCount:      totalWinCount,
		TotalWinRate:       totalWinRate,
	}

	c.JSON(http.StatusOK, resp)
}

// handleExchangeTradedSymbols 基于交易所“订单历史”做按 symbol 聚合统计（best-effort）
// 注意：订单接口不返回真实手续费/资金费；此处 EstimatedFee 使用 assumed taker fee 估算。
// Query:
// - trader_id: required
// - symbols: required, comma-separated, e.g. BTCUSDT,ETHUSDT
// - start_time/end_time: optional (RFC3339 or unix milli). default: trader start_time -> now
// - limit: optional (default 1000; Binance futures max 1000 per request)
func (s *Server) handleExchangeTradedSymbols(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	at, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	symbolsParam := strings.TrimSpace(c.Query("symbols"))
	if symbolsParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing symbols (comma-separated)"})
		return
	}
	rawSymbols := strings.Split(symbolsParam, ",")
	symbolSet := make(map[string]struct{}, len(rawSymbols))
	symbols := make([]string, 0, len(rawSymbols))
	for _, s0 := range rawSymbols {
		sym := strings.ToUpper(strings.TrimSpace(s0))
		if !isValidExchangeSymbol(sym) {
			continue
		}
		if _, ok := symbolSet[sym]; ok {
			continue
		}
		symbolSet[sym] = struct{}{}
		symbols = append(symbols, sym)
	}
	sort.Strings(symbols)
	if len(symbols) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbols is empty after parsing"})
		return
	}

	limit := 1000
	if lq := strings.TrimSpace(c.Query("limit")); lq != "" {
		if v, e := strconv.Atoi(lq); e == nil && v > 0 {
			limit = v
		}
	}

	startMs, err := parseTimeParamToMs(c.Query("start_time"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid start_time: %v", err)})
		return
	}
	endMs, err := parseTimeParamToMs(c.Query("end_time"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid end_time: %v", err)})
		return
	}
	if endMs == 0 {
		endMs = time.Now().UnixMilli()
	}
	if startMs == 0 {
		// default: earliest known trade time for requested symbols; process start time is only a fallback.
		requested := make(map[string]struct{}, len(symbols))
		for _, sym := range symbols {
			requested[sym] = struct{}{}
		}
		for _, st := range at.GetCompletedSymbolStats() {
			if st == nil {
				continue
			}
			if _, ok := requested[st.Symbol]; !ok {
				continue
			}
			for _, ts := range []string{st.FirstTradeTime, st.LastTradeTime} {
				ms, e := parseTimeParamToMs(ts)
				if e == nil && ms > 0 && (startMs == 0 || ms < startMs) {
					startMs = ms
				}
			}
		}
		if startMs == 0 {
			if st, ok := at.GetStatus()["start_time"].(string); ok && st != "" {
				if ms, e := parseTimeParamToMs(st); e == nil && ms > 0 {
					startMs = ms
				}
			}
		}
	}

	feeRate := at.GetAssumedTakerFeeRate()

	// Best-effort: 拉取所有 symbol 的 income 记录（资金费、手续费、已实现盈亏）
	type symbolIncome struct {
		Commission  float64
		FundingFee  float64
		RealizedPnL float64
	}
	incomeBySymbol := make(map[string]*symbolIncome, len(symbols))
	allIncome, incomeErr := at.GetIncome("", "", startMs, endMs, 1000)
	if incomeErr != nil {
		log.Printf("⚠️ exchange traded-symbols: GetIncome failed (best-effort): trader=%s err=%v", traderID, incomeErr)
	} else {
		for _, rec := range allIncome {
			sym := rec.Symbol
			if sym == "" {
				continue
			}
			si, ok := incomeBySymbol[sym]
			if !ok {
				si = &symbolIncome{}
				incomeBySymbol[sym] = si
			}
			switch rec.IncomeType {
			case "COMMISSION":
				si.Commission += rec.Income
			case "FUNDING_FEE":
				si.FundingFee += rec.Income
			case "REALIZED_PNL":
				si.RealizedPnL += rec.Income
			}
		}
	}

	resp := exchangeTradedSymbolsResponse{
		Symbols: make([]exchangeOrderStats, 0, len(symbols)),
	}

	for _, sym := range symbols {
		if isCachedInvalidExchangeSymbol(sym) {
			continue
		}
		orders, e := at.GetOrders(sym, startMs, endMs, limit)
		if e != nil {
			if isExchangeInvalidSymbolError(e) {
				markInvalidExchangeSymbol(sym)
				log.Printf("⚠️ exchange traded-symbols: skip invalid/delisted symbol and cache it: trader=%s symbol=%s err=%v", traderID, sym, e)
				continue
			}
			log.Printf("⚠️ exchange traded-symbols: ListOrders failed: trader=%s symbol=%s err=%v", traderID, sym, e)
			continue
		}
		st := calcOrderStatsFromOrders(sym, orders, feeRate)

		if si, ok := incomeBySymbol[sym]; ok {
			st.RealCommission = si.Commission
			st.RealFundingFee = si.FundingFee
			st.RealRealizedPnL = si.RealizedPnL
		}

		resp.Symbols = append(resp.Symbols, st)
		resp.Summary.TotalRealizedPnL += st.RealizedPnL
		resp.Summary.TotalEstimatedFee += st.EstimatedFee
		resp.Summary.TotalTrades += st.Trades
		resp.Summary.TotalCommission += st.RealCommission
		resp.Summary.TotalFundingFee += st.RealFundingFee
		resp.Summary.TotalRealRealized += st.RealRealizedPnL
	}

	resp.Summary.TotalSymbols = len(resp.Symbols)

	// 排序：按 realized pnl 降序
	sort.Slice(resp.Symbols, func(i, j int) bool {
		return resp.Symbols[i].RealizedPnL > resp.Symbols[j].RealizedPnL
	})

	c.JSON(http.StatusOK, resp)
}

// handleExchangeOrders 返回单币种订单明细 + 统计
// Query:
// - trader_id, symbol required
// - start_time/end_time optional (RFC3339 or unix milli). default: trader start_time -> now
// - limit optional (default 1000)
func (s *Server) handleExchangeOrders(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	at, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	symbol := strings.ToUpper(strings.TrimSpace(c.Query("symbol")))
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing symbol"})
		return
	}

	limit := 1000
	if lq := strings.TrimSpace(c.Query("limit")); lq != "" {
		if v, e := strconv.Atoi(lq); e == nil && v > 0 {
			limit = v
		}
	}

	startMs, err := parseTimeParamToMs(c.Query("start_time"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid start_time: %v", err)})
		return
	}
	endMs, err := parseTimeParamToMs(c.Query("end_time"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid end_time: %v", err)})
		return
	}
	if endMs == 0 {
		endMs = time.Now().UnixMilli()
	}
	if startMs == 0 {
		if st, ok := at.GetStatus()["start_time"].(string); ok && st != "" {
			if ms, e := parseTimeParamToMs(st); e == nil && ms > 0 {
				startMs = ms
			}
		}
	}

	orders, err := at.GetOrders(symbol, startMs, endMs, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("ListOrders failed: %v", err)})
		return
	}

	// 排序：按 created_at 倒序（最新在前）
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})

	stats := calcOrderStatsFromOrders(symbol, orders, at.GetAssumedTakerFeeRate())

	resp := exchangeOrdersResponse{
		Symbol:    symbol,
		StartTime: time.UnixMilli(startMs).Format(time.RFC3339),
		EndTime:   time.UnixMilli(endMs).Format(time.RFC3339),
		Orders:    orders,
		Stats:     stats,
	}
	c.JSON(http.StatusOK, resp)
}

// handleLogTraderList 获取 Trader 列表 (即 decision_logs 下的子目录)
func (s *Server) handleLogTraderList(c *gin.Context) {
	logsDir := "./decision_logs"
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var traders []string
	for _, entry := range entries {
		if entry.IsDir() {
			// 忽略 case 目录和以 . 开头的目录
			if entry.Name() == "case" || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			traders = append(traders, entry.Name())
		}
	}
	c.JSON(http.StatusOK, traders)
}

// handleLogList 获取指定 Trader 的日志文件列表
func (s *Server) handleLogList(c *gin.Context) {
	traderID := c.Query("trader_id")
	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing trader_id"})
		return
	}

	logsDir := "./decision_logs"
	traderDir := filepath.Join(logsDir, traderID)
	entries, err := os.ReadDir(traderDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "trader logs not found"})
		return
	}

	var logsList []LogEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			info, err := entry.Info()
			if err != nil {
				continue
			}

			// 尝试解析文件名提取 Cycle 和 Timestamp
			// 格式: decision_20251201_201324_cycle62.json
			var cycle int
			var ts time.Time

			// 简单解析逻辑...
			parts := strings.Split(entry.Name(), "_")
			if len(parts) >= 4 {
				// 解析时间
				timeStr := parts[1] + parts[2] // 20251201201324
				if t, err := time.Parse("20060102150405", timeStr); err == nil {
					ts = t
				}
				// 解析 Cycle
				cyclePart := parts[3] // cycle62.json
				cyclePart = strings.TrimSuffix(cyclePart, ".json")
				fmt.Sscanf(cyclePart, "cycle%d", &cycle)
			}

			// 如果解析失败，使用文件修改时间
			if ts.IsZero() {
				ts = info.ModTime()
			}

			logsList = append(logsList, LogEntry{
				Filename:  entry.Name(),
				Cycle:     cycle,
				Timestamp: ts,
				Size:      info.Size(),
			})
		}
	}

	// 按时间倒序排序
	sort.Slice(logsList, func(i, j int) bool {
		return logsList[i].Timestamp.After(logsList[j].Timestamp)
	})

	c.JSON(http.StatusOK, logsList)
}

// handleLogDetail 读取具体日志内容
func (s *Server) handleLogDetail(c *gin.Context) {
	traderID := c.Query("trader_id")
	filename := c.Query("filename")
	if traderID == "" || filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing parameters"})
		return
	}

	// 安全检查: 防止目录遍历攻击
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return
	}

	logsDir := "./decision_logs"
	filePath := filepath.Join(logsDir, traderID, filename)
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	// 直接返回 JSON 内容
	c.Data(http.StatusOK, "application/json", content)
}

// handleHealth 健康检查
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   c.Request.Context().Value("time"),
	})
}

// getTraderFromQuery 从query参数获取trader
func (s *Server) getTraderFromQuery(c *gin.Context) (*manager.TraderManager, string, error) {
	traderID := c.Query("trader_id")
	if traderID == "" {
		// 如果没有指定trader_id，返回第一个trader
		ids := s.traderManager.GetTraderIDs()
		if len(ids) == 0 {
			return nil, "", fmt.Errorf("没有可用的trader")
		}
		traderID = ids[0]
	}
	return s.traderManager, traderID, nil
}

// handleCompetition 竞赛总览（对比所有trader）
func (s *Server) handleCompetition(c *gin.Context) {
	comparison, err := s.traderManager.GetComparisonData()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取对比数据失败: %v", err),
		})
		return
	}
	c.JSON(http.StatusOK, comparison)
}

// handleTraderList trader列表
func (s *Server) handleTraderList(c *gin.Context) {
	traders := s.traderManager.GetAllTraders()
	result := make([]map[string]interface{}, 0, len(traders))

	for _, t := range traders {
		result = append(result, map[string]interface{}{
			"trader_id":   t.GetID(),
			"trader_name": t.GetName(),
			"ai_model":    t.GetAIModel(),
		})
	}

	c.JSON(http.StatusOK, result)
}

// handleStatus 系统状态
func (s *Server) handleStatus(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status := trader.GetStatus()
	c.JSON(http.StatusOK, status)
}

// handleAccount 账户信息
func (s *Server) handleAccount(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	log.Printf("📊 收到账户信息请求 [%s]", trader.GetName())
	account, err := trader.GetAccountInfo()
	if err != nil {
		log.Printf("❌ 获取账户信息失败 [%s]: %v", trader.GetName(), err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取账户信息失败: %v", err),
		})
		return
	}

	log.Printf("✓ 返回账户信息 [%s]: 净值=%.2f, 可用=%.2f, 盈亏=%.2f (%.2f%%)",
		trader.GetName(),
		account["total_equity"],
		account["available_balance"],
		account["total_pnl"],
		account["total_pnl_pct"])
	c.JSON(http.StatusOK, account)
}

// handlePositions 持仓列表
func (s *Server) handlePositions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	positions, err := trader.GetPositions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取持仓列表失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, positions)
}

// handleDecisions 决策日志列表
func (s *Server) handleDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取所有历史决策记录（无限制）
	records, err := trader.GetDecisionLogger().GetLatestRecords(10000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, records)
}

// handleLatestDecisions 最新决策日志（最近5条，最新的在前）
func (s *Server) handleLatestDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	records, err := trader.GetDecisionLogger().GetLatestRecords(5)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	// 反转数组，让最新的在前面（用于列表显示）
	// GetLatestRecords返回的是从旧到新（用于图表），这里需要从新到旧
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	c.JSON(http.StatusOK, records)
}

// handleStatistics 统计信息
func (s *Server) handleStatistics(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	t, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	stats, err := t.GetDecisionLogger().GetStatistics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取统计信息失败: %v", err),
		})
		return
	}

	// 将交易统计和 LLM 费用统计合并返回
	result := gin.H{
		"trade_stats": stats,
		"llm_cost":    t.GetLLMCostSnapshot(),
	}

	c.JSON(http.StatusOK, result)
}

// handleEquityHistory 收益率历史数据
func (s *Server) handleEquityHistory(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	query := parseEquityHistoryQuery(c)

	// 从AutoTrader获取初始余额（用于计算盈亏百分比）
	initialBalance := 0.0
	if status := trader.GetStatus(); status != nil {
		if ib, ok := status["initial_balance"].(float64); ok && ib > 0 {
			initialBalance = ib
		}
	}

	points, err := s.equityCache.Get(traderID, trader.GetDecisionLogger().GetLatestRecords, initialBalance, query.Limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取历史数据失败: %v", err),
		})
		return
	}

	history := applyEquityHistoryQuery(points, query)
	c.JSON(http.StatusOK, history)
}

func parseEquityHistoryQuery(c *gin.Context) EquityHistoryQuery {
	limit := defaultEquityHistoryLimit
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit <= 0 {
		limit = maxEquityHistoryLimit
	}
	if limit > maxEquityHistoryLimit {
		limit = maxEquityHistoryLimit
	}

	fromCycle := 0
	if raw := c.Query("from_cycle"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			fromCycle = parsed
		}
	}

	toCycle := 0
	if raw := c.Query("to_cycle"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			toCycle = parsed
		}
	}

	return EquityHistoryQuery{
		Limit:     limit,
		FromCycle: fromCycle,
		ToCycle:   toCycle,
	}
}

// handlePerformance AI历史表现分析（用于展示AI学习和反思）
func (s *Server) handlePerformance(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// trade_limit limits returned recent completed trades; limit is retained as a
	// backwards-compatible alias now that cycle-count lookback is not meaningful.
	tradeLimit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tradeLimit = n
		}
	}
	if v := c.Query("trade_limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tradeLimit = n
		}
	}

	if tradeLimit < 0 {
		tradeLimit = 0
	}
	if tradeLimit > 500 {
		tradeLimit = 500
	}

	// 已完成交易只能从统一账本读取：交易所条件单平仓不会新增 AI cycle，
	// 因而不能回放有限 decision log 来统计。
	performance := trader.AnalyzeCompletedTrades(tradeLimit)

	c.JSON(http.StatusOK, performance)
}

// handleCloseAllPositions 平掉所有模型的所有持仓
func (s *Server) handleCloseAllPositions(c *gin.Context) {
	log.Println("🔴 API请求：平掉所有模型的所有持仓")

	results := s.traderManager.CloseAllPositionsForAllTraders()

	// 构建响应
	response := gin.H{
		"success": true,
		"message": "平仓操作已执行",
		"results": make(map[string]interface{}),
	}

	hasError := false
	for traderID, err := range results {
		if err != nil {
			hasError = true
			response["results"].(map[string]interface{})[traderID] = gin.H{
				"success": false,
				"error":   err.Error(),
			}
		} else {
			response["results"].(map[string]interface{})[traderID] = gin.H{
				"success": true,
				"message": "平仓成功",
			}
		}
	}

	if hasError {
		response["success"] = false
		response["message"] = "部分trader平仓失败"
		c.JSON(http.StatusPartialContent, response)
	} else {
		c.JSON(http.StatusOK, response)
	}
}

// handleClosePositions 平掉指定trader的所有持仓
func (s *Server) handleClosePositions(c *gin.Context) {
	traderID := c.Query("trader_id")
	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 trader_id 参数",
		})
		return
	}

	log.Printf("🔴 API请求：平掉 %s 的所有持仓", traderID)

	err := s.traderManager.CloseAllPositionsForTrader(traderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "平仓成功",
	})
}

// handleLLMCost LLM 调用费用统计
func (s *Server) handleLLMCost(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	t, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, t.GetLLMCostSnapshot())
}

// handleErrorStats 错误统计
func (s *Server) handleErrorStats(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	errorStats := trader.GetErrorStats()
	if errorStats == nil {
		c.JSON(http.StatusOK, gin.H{
			"message": "错误统计尚未初始化",
		})
		return
	}

	c.JSON(http.StatusOK, errorStats.GetSummary())
}

// handleRecentErrors 最近的错误列表
func (s *Server) handleRecentErrors(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	errorStats := trader.GetErrorStats()
	if errorStats == nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}

	// 默认返回最近50条错误
	limit := 50
	recentErrors := errorStats.GetRecentErrors(limit)
	c.JSON(http.StatusOK, recentErrors)
}

// handleSystemPause 设置系统暂停状态
func (s *Server) handleSystemPause(c *gin.Context) {
	var req struct {
		Paused bool `json:"paused"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}

	s.traderManager.SetAllPaused(req.Paused)

	action := "已恢复"
	if req.Paused {
		action = "已暂停"
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("系统%s交易执行", action),
		"paused":  req.Paused,
	})
}

// handleGetAutoResume 获取接续运行开关状态
func (s *Server) handleGetAutoResume(c *gin.Context) {
	enabled := s.traderManager.GetAutoResume()
	c.JSON(http.StatusOK, gin.H{"auto_resume": enabled})
}

// handleSetAutoResume 设置接续运行开关
func (s *Server) handleSetAutoResume(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}

	if err := s.traderManager.SetAutoResume(req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	status := "已关闭"
	if req.Enabled {
		status = "已开启"
	}
	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"message":     fmt.Sprintf("接续运行%s（下次启动生效）", status),
		"auto_resume": req.Enabled,
	})
}

// handleGetPeakHour 获取指定 trader 的高峰时段暂停状态
func (s *Server) handleGetPeakHour(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status := trader.GetPeakHourStatus()
	status["trader_id"] = traderID
	c.JSON(http.StatusOK, status)
}

// handlePeakHourOverride 切换高峰时段 override（手动恢复/重新暂停）
func (s *Server) handlePeakHourOverride(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		Override bool `json:"override"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}

	trader.SetPeakHourOverride(req.Override)
	status := trader.GetPeakHourStatus()
	status["trader_id"] = traderID
	c.JSON(http.StatusOK, status)
}

// handlePeakHourToggle 开启/关闭高峰时段暂停功能
func (s *Server) handlePeakHourToggle(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}

	trader.SetPeakHourEnabled(req.Enabled)
	status := trader.GetPeakHourStatus()
	status["trader_id"] = traderID
	c.JSON(http.StatusOK, status)
}

// handleGetConfig 获取运行时配置
func (s *Server) handleGetConfig(c *gin.Context) {
	traderID := c.Query("trader_id")
	configs, err := s.traderManager.GetRuntimeConfig(traderID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 如果只有一个 trader 或指定了 trader_id，直接返回配置（不嵌套 map）
	if len(configs) == 1 {
		for id, cfg := range configs {
			c.JSON(http.StatusOK, gin.H{
				"trader_id": id,
				"config":    cfg,
			})
			return
		}
	}

	// 多个 trader：返回 map
	c.JSON(http.StatusOK, configs)
}

// handleUpdateConfig 更新运行时配置
func (s *Server) handleUpdateConfig(c *gin.Context) {
	var patch traderpkg.RuntimeConfigPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("无效的请求体: %v", err)})
		return
	}

	traderID := c.Query("trader_id")
	if err := s.traderManager.UpdateRuntimeConfig(traderID, patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 返回更新后的配置
	configs, _ := s.traderManager.GetRuntimeConfig(traderID)
	c.JSON(http.StatusOK, gin.H{
		"message": "配置已更新，将在下一个交易周期生效",
		"config":  configs,
	})
}

// handleKlines 返回K线数据（代理Binance fapi）
func (s *Server) handleKlines(c *gin.Context) {
	symbol := c.Query("symbol")
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol参数必填"})
		return
	}

	interval := c.DefaultQuery("interval", "15m")
	limitStr := c.DefaultQuery("limit", "200")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 1500 {
		limit = 200
	}

	klines, err := market.GetKlines(symbol, interval, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取K线数据失败: %v", err)})
		return
	}

	type klineJSON struct {
		OpenTime  int64   `json:"open_time"`
		Open      float64 `json:"open"`
		High      float64 `json:"high"`
		Low       float64 `json:"low"`
		Close     float64 `json:"close"`
		Volume    float64 `json:"volume"`
		CloseTime int64   `json:"close_time"`
	}

	result := make([]klineJSON, len(klines))
	for i, k := range klines {
		result[i] = klineJSON{
			OpenTime:  k.OpenTime,
			Open:      k.Open,
			High:      k.High,
			Low:       k.Low,
			Close:     k.Close,
			Volume:    k.Volume,
			CloseTime: k.CloseTime,
		}
	}

	c.JSON(http.StatusOK, result)
}

// ===== Market Overview =====

type marketCoinOverview struct {
	Symbol      string  `json:"symbol"`
	Price       float64 `json:"price"`
	Change24h   float64 `json:"change_24h"` // percent
	FundingRate float64 `json:"funding_rate"`
}

type fearGreedData struct {
	Value int    `json:"value"` // 0-100
	Label string `json:"label"` // "Extreme Fear", "Fear", "Neutral", "Greed", "Extreme Greed"
}

type marketOverviewResponse struct {
	Coins     []marketCoinOverview `json:"coins"`
	FearGreed fearGreedData        `json:"fear_greed"`
	UpdatedAt string               `json:"updated_at"`
}

func (s *Server) handleMarketOverview(c *gin.Context) {
	symbols := []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"}
	baseURL := market.GetFAPIBaseURL()

	coins := make([]marketCoinOverview, 0, len(symbols))
	client := market.NewHTTPClient()

	for _, sym := range symbols {
		co := marketCoinOverview{Symbol: sym}

		// 24h ticker: price + priceChangePercent
		if resp, err := client.Get(fmt.Sprintf("%s/fapi/v1/ticker/24hr?symbol=%s", baseURL, sym)); err == nil {
			defer resp.Body.Close()
			if body, err := ioutil.ReadAll(resp.Body); err == nil {
				var t struct {
					LastPrice          string `json:"lastPrice"`
					PriceChangePercent string `json:"priceChangePercent"`
				}
				if json.Unmarshal(body, &t) == nil {
					co.Price, _ = strconv.ParseFloat(t.LastPrice, 64)
					co.Change24h, _ = strconv.ParseFloat(t.PriceChangePercent, 64)
				}
			}
		}

		// funding rate
		if resp, err := client.Get(fmt.Sprintf("%s/fapi/v1/premiumIndex?symbol=%s", baseURL, sym)); err == nil {
			defer resp.Body.Close()
			if body, err := ioutil.ReadAll(resp.Body); err == nil {
				var f struct {
					LastFundingRate string `json:"lastFundingRate"`
				}
				if json.Unmarshal(body, &f) == nil {
					co.FundingRate, _ = strconv.ParseFloat(f.LastFundingRate, 64)
				}
			}
		}

		coins = append(coins, co)
	}

	// Fear & Greed Index (best-effort, external API)
	fg := fearGreedData{Value: -1, Label: "N/A"}
	if resp, err := client.Get("https://api.alternative.me/fng/?limit=1"); err == nil {
		defer resp.Body.Close()
		if body, err := ioutil.ReadAll(resp.Body); err == nil {
			var result struct {
				Data []struct {
					Value               string `json:"value"`
					ValueClassification string `json:"value_classification"`
				} `json:"data"`
			}
			if json.Unmarshal(body, &result) == nil && len(result.Data) > 0 {
				if v, e := strconv.Atoi(result.Data[0].Value); e == nil {
					fg.Value = v
					fg.Label = result.Data[0].ValueClassification
				}
			}
		}
	}

	c.JSON(http.StatusOK, marketOverviewResponse{
		Coins:     coins,
		FearGreed: fg,
		UpdatedAt: time.Now().Format(time.RFC3339),
	})
}

// Start 启动服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("🌐 API服务器启动在 http://localhost%s", addr)
	log.Printf("📊 API文档:")
	log.Printf("  • GET  /api/competition      - 竞赛总览（对比所有trader）")
	log.Printf("  • GET  /api/traders          - Trader列表")
	log.Printf("  • GET  /api/status?trader_id=xxx     - 指定trader的系统状态")
	log.Printf("  • GET  /api/account?trader_id=xxx    - 指定trader的账户信息")
	log.Printf("  • GET  /api/positions?trader_id=xxx  - 指定trader的持仓列表")
	log.Printf("  • GET  /api/decisions?trader_id=xxx  - 指定trader的决策日志")
	log.Printf("  • GET  /api/decisions/latest?trader_id=xxx - 指定trader的最新决策")
	log.Printf("  • GET  /api/statistics?trader_id=xxx - 指定trader的统计信息")
	log.Printf("  • GET  /api/equity-history?trader_id=xxx - 指定trader的收益率历史数据")
	log.Printf("  • GET  /api/performance?trader_id=xxx - 指定trader的AI学习表现分析")
	log.Printf("  • GET  /api/klines?symbol=BTCUSDT&interval=15m&limit=200 - K线数据")
	log.Printf("  • GET  /api/error-stats?trader_id=xxx - 指定trader的错误统计")
	log.Printf("  • GET  /api/error-stats/recent?trader_id=xxx - 指定trader最近的错误列表")
	log.Printf("  • POST /api/close-all-positions - 平掉所有模型的所有持仓")
	log.Printf("  • POST /api/close-positions?trader_id=xxx - 平掉指定trader的所有持仓")
	log.Printf("  • GET  /api/config?trader_id=xxx - 获取运行时配置")
	log.Printf("  • PUT  /api/config?trader_id=xxx - 更新运行时配置（热更新）")
	log.Printf("  • GET  /health               - 健康检查")
	log.Println()

	return s.router.Run(addr)
}

package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SymbolTradeStats is the persisted, incremental per-symbol trading summary.
type SymbolTradeStats struct {
	Symbol            string  `json:"symbol"`
	TotalTrades       int     `json:"total_trades"`
	WinCount          int     `json:"win_count"`
	LossCount         int     `json:"loss_count"`
	RealizedPnL       float64 `json:"realized_pnl"`
	OpenLongCount     int     `json:"open_long_count"`
	OpenShortCount    int     `json:"open_short_count"`
	CloseLongCount    int     `json:"close_long_count"`
	CloseShortCount   int     `json:"close_short_count"`
	PartialCloseCount int     `json:"partial_close_count"`
	FirstTradeTime    string  `json:"first_trade_time"`
	LastTradeTime     string  `json:"last_trade_time"`
}

type openTradeLot struct {
	Price    float64   `json:"price"`
	Quantity float64   `json:"quantity"`
	Time     time.Time `json:"time"`
}

// TradeStatsCache keeps incrementally computed trade stats so expensive API
// handlers do not need to rescan all decision logs on every request.
type TradeStatsCache struct {
	path               string
	LastProcessedCycle int                          `json:"last_processed_cycle"`
	PeakEquity         float64                      `json:"peak_equity"`
	PeakEquityTime     string                       `json:"peak_equity_time"`
	SymbolStats        map[string]*SymbolTradeStats `json:"symbols"`
	OpenLots           map[string][]openTradeLot    `json:"open_lots"`
}

// NewTradeStatsCache loads the persistent stats cache for a decision log dir.
func NewTradeStatsCache(logDir string) *TradeStatsCache {
	cache := &TradeStatsCache{
		path:        filepath.Join(logDir, "trade_stats.json"),
		SymbolStats: make(map[string]*SymbolTradeStats),
		OpenLots:    make(map[string][]openTradeLot),
	}

	data, err := os.ReadFile(cache.path)
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, cache); err != nil {
		fmt.Printf("⚠ 加载交易统计缓存失败，将重新生成: %v\n", err)
		cache.SymbolStats = make(map[string]*SymbolTradeStats)
		cache.OpenLots = make(map[string][]openTradeLot)
		cache.LastProcessedCycle = 0
		return cache
	}
	cache.path = filepath.Join(logDir, "trade_stats.json")
	if cache.SymbolStats == nil {
		cache.SymbolStats = make(map[string]*SymbolTradeStats)
	}
	if cache.OpenLots == nil {
		cache.OpenLots = make(map[string][]openTradeLot)
	}
	fmt.Printf("📂 已加载交易统计缓存: %d 个币种, 已处理 cycle %d\n", len(cache.SymbolStats), cache.LastProcessedCycle)
	return cache
}

// GetLastProcessedCycle returns the last decision cycle included in the cache.
func (c *TradeStatsCache) GetLastProcessedCycle() int {
	if c == nil {
		return 0
	}
	return c.LastProcessedCycle
}

// GetSymbolStats returns stable, sorted per-symbol statistics.
func (c *TradeStatsCache) GetSymbolStats() []*SymbolTradeStats {
	if c == nil || len(c.SymbolStats) == 0 {
		return []*SymbolTradeStats{}
	}
	out := make([]*SymbolTradeStats, 0, len(c.SymbolStats))
	for _, stat := range c.SymbolStats {
		if stat != nil {
			out = append(out, stat)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RealizedPnL == out[j].RealizedPnL {
			return out[i].Symbol < out[j].Symbol
		}
		return out[i].RealizedPnL > out[j].RealizedPnL
	})
	return out
}

// UpdatePeakEquity updates and returns peak equity and current drawdown percent.
func (c *TradeStatsCache) UpdatePeakEquity(totalEquity float64, timestamp string) (float64, float64) {
	if c == nil || totalEquity <= 0 {
		return 0, 0
	}
	if totalEquity > c.PeakEquity {
		c.PeakEquity = totalEquity
		c.PeakEquityTime = timestamp
	}
	drawdownPct := 0.0
	if c.PeakEquity > 0 && totalEquity < c.PeakEquity {
		drawdownPct = (c.PeakEquity - totalEquity) / c.PeakEquity * 100
	}
	return c.PeakEquity, drawdownPct
}

// ProcessRecords incrementally folds decision records into the cache.
func (c *TradeStatsCache) ProcessRecords(records []*DecisionRecord) {
	if c == nil {
		return
	}
	if c.SymbolStats == nil {
		c.SymbolStats = make(map[string]*SymbolTradeStats)
	}
	if c.OpenLots == nil {
		c.OpenLots = make(map[string][]openTradeLot)
	}

	for _, record := range records {
		if record == nil || record.CycleNumber <= c.LastProcessedCycle {
			continue
		}
		for _, action := range record.Decisions {
			if !action.Success || action.Symbol == "" {
				continue
			}
			c.processAction(record, action)
		}
		if record.CycleNumber > c.LastProcessedCycle {
			c.LastProcessedCycle = record.CycleNumber
		}
	}
}

func (c *TradeStatsCache) processAction(record *DecisionRecord, action DecisionAction) {
	symbol := action.Symbol
	stat := c.ensureSymbolStats(symbol)
	actionTime := action.Timestamp
	if actionTime.IsZero() {
		actionTime = record.Timestamp
	}
	if actionTime.IsZero() {
		actionTime = time.Now()
	}
	updateTradeTime(stat, actionTime)

	switch action.Action {
	case "open_long":
		stat.OpenLongCount++
		c.addOpenLot(symbol, "long", action.Price, action.Quantity, actionTime)
	case "open_short":
		stat.OpenShortCount++
		c.addOpenLot(symbol, "short", action.Price, action.Quantity, actionTime)
	case "close_long":
		stat.CloseLongCount++
		c.closeLots(stat, symbol, "long", action.Price, action.Quantity)
	case "close_short":
		stat.CloseShortCount++
		c.closeLots(stat, symbol, "short", action.Price, action.Quantity)
	case "partial_close":
		stat.PartialCloseCount++
		// partial_close records in historical logs do not always carry side; count
		// them separately and leave realized PnL to explicit close_* actions.
	}
}

func (c *TradeStatsCache) ensureSymbolStats(symbol string) *SymbolTradeStats {
	stat := c.SymbolStats[symbol]
	if stat == nil {
		stat = &SymbolTradeStats{Symbol: symbol}
		c.SymbolStats[symbol] = stat
	}
	return stat
}

func updateTradeTime(stat *SymbolTradeStats, t time.Time) {
	if stat == nil || t.IsZero() {
		return
	}
	formatted := t.Format(time.RFC3339)
	if stat.FirstTradeTime == "" || formatted < stat.FirstTradeTime {
		stat.FirstTradeTime = formatted
	}
	if stat.LastTradeTime == "" || formatted > stat.LastTradeTime {
		stat.LastTradeTime = formatted
	}
}

func (c *TradeStatsCache) addOpenLot(symbol, side string, price, qty float64, t time.Time) {
	if price <= 0 || qty <= 0 {
		return
	}
	key := symbol + "_" + side
	c.OpenLots[key] = append(c.OpenLots[key], openTradeLot{Price: price, Quantity: qty, Time: t})
}

func (c *TradeStatsCache) closeLots(stat *SymbolTradeStats, symbol, side string, closePrice, qty float64) {
	if stat == nil || closePrice <= 0 || qty <= 0 {
		return
	}
	key := symbol + "_" + side
	lots := c.OpenLots[key]
	if len(lots) == 0 {
		return
	}

	remaining := qty
	realized := 0.0
	matched := false
	for remaining > 0 && len(lots) > 0 {
		lot := lots[0]
		useQty := lot.Quantity
		if useQty > remaining {
			useQty = remaining
		}
		if side == "long" {
			realized += (closePrice - lot.Price) * useQty
		} else {
			realized += (lot.Price - closePrice) * useQty
		}
		matched = true
		lot.Quantity -= useQty
		remaining -= useQty
		if lot.Quantity <= 0 {
			lots = lots[1:]
		} else {
			lots[0] = lot
		}
	}
	c.OpenLots[key] = lots
	if !matched {
		return
	}
	stat.TotalTrades++
	stat.RealizedPnL += realized
	if realized > 0 {
		stat.WinCount++
	} else if realized < 0 {
		stat.LossCount++
	}
}

// Save persists the cache atomically enough for this single-process use case.
func (c *TradeStatsCache) Save() error {
	if c == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0644)
}

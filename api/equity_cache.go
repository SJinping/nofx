package api

import (
	"fmt"
	"sync"
	"time"

	"nofx/logger"
)

const (
	defaultEquityHistoryLimit = 2000
	maxEquityHistoryLimit     = 20000
	equityCacheRefreshWindow  = 500
)

// EquityPoint is the lightweight account-equity sample returned to the web UI.
// Keep this intentionally small: do not cache full decision records, prompts, or market data.
type EquityPoint struct {
	Timestamp        string  `json:"timestamp"`
	TotalEquity      float64 `json:"total_equity"`      // 账户净值（wallet + unrealized）
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏（相对初始余额）
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	PositionCount    int     `json:"position_count"`    // 持仓数量
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	CycleNumber      int     `json:"cycle_number"`
}

type EquityHistoryQuery struct {
	Limit     int
	FromCycle int
	ToCycle   int
}

type equityHistoryEntry struct {
	points         []EquityPoint
	lastLoadedAt   time.Time
	initialBalance float64
	loadedLimit    int
}

type EquityHistoryCache struct {
	mu         sync.RWMutex
	entries    map[string]*equityHistoryEntry
	maxHistory int
	ttl        time.Duration
}

type equityRecordFetcher func(limit int) ([]*logger.DecisionRecord, error)

func NewEquityHistoryCache(maxHistory int, ttl time.Duration) *EquityHistoryCache {
	if maxHistory <= 0 {
		maxHistory = maxEquityHistoryLimit
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &EquityHistoryCache{
		entries:    make(map[string]*equityHistoryEntry),
		maxHistory: maxHistory,
		ttl:        ttl,
	}
}

func (c *EquityHistoryCache) Get(traderID string, fetch equityRecordFetcher, initialBalance float64, requestedLimit int) ([]EquityPoint, error) {
	requestedLimit = clampEquityHistoryLimit(requestedLimit)
	now := time.Now()

	c.mu.RLock()
	entry := c.entries[traderID]
	if entry != nil && now.Sub(entry.lastLoadedAt) < c.ttl && equityInitialBalanceMatches(entry.initialBalance, initialBalance) && entry.loadedLimit >= requestedLimit {
		points := cloneEquityPoints(entry.points)
		c.mu.RUnlock()
		return points, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	entry = c.entries[traderID]
	if entry != nil && now.Sub(entry.lastLoadedAt) < c.ttl && equityInitialBalanceMatches(entry.initialBalance, initialBalance) && entry.loadedLimit >= requestedLimit {
		return cloneEquityPoints(entry.points), nil
	}

	fetchLimit := requestedLimit
	canRefreshIncrementally := entry != nil && len(entry.points) > 0 && equityInitialBalanceMatches(entry.initialBalance, initialBalance) && entry.loadedLimit >= requestedLimit
	if canRefreshIncrementally {
		fetchLimit = equityCacheRefreshWindow
		if initialBalance == 0 {
			initialBalance = entry.initialBalance
		}
	}

	records, err := fetch(fetchLimit)
	if err != nil {
		return nil, err
	}

	if initialBalance == 0 && len(records) > 0 {
		initialBalance = records[0].AccountState.TotalBalance
	}

	newPoints, err := buildEquityPoints(records, initialBalance)
	if err != nil {
		return nil, err
	}

	if !canRefreshIncrementally {
		entry = &equityHistoryEntry{
			points:         trimEquityPoints(newPoints, c.maxHistory),
			lastLoadedAt:   now,
			initialBalance: initialBalance,
			loadedLimit:    fetchLimit,
		}
		c.entries[traderID] = entry
		return cloneEquityPoints(entry.points), nil
	}

	lastCycle := 0
	if len(entry.points) > 0 {
		lastCycle = entry.points[len(entry.points)-1].CycleNumber
	}
	for _, point := range newPoints {
		if point.CycleNumber > lastCycle {
			entry.points = append(entry.points, point)
		}
	}
	entry.points = trimEquityPoints(entry.points, c.maxHistory)
	entry.lastLoadedAt = now

	return cloneEquityPoints(entry.points), nil
}

func clampEquityHistoryLimit(limit int) int {
	if limit <= 0 {
		return maxEquityHistoryLimit
	}
	if limit > maxEquityHistoryLimit {
		return maxEquityHistoryLimit
	}
	return limit
}

func equityInitialBalanceMatches(cached float64, requested float64) bool {
	return requested <= 0 || cached == requested
}

func buildEquityPoints(records []*logger.DecisionRecord, initialBalance float64) ([]EquityPoint, error) {
	if initialBalance == 0 && len(records) > 0 {
		initialBalance = records[0].AccountState.TotalBalance
	}
	if initialBalance == 0 {
		return nil, fmt.Errorf("无法获取初始余额")
	}

	history := make([]EquityPoint, 0, len(records))
	for _, record := range records {
		// ⚠️ 过滤掉失败的记录或账户状态异常的记录（避免网络超时导致曲线大幅波动）
		if record == nil || !record.Success || record.AccountState.TotalBalance <= 0 {
			continue
		}

		// TotalBalance字段实际存储的是TotalEquity
		totalEquity := record.AccountState.TotalBalance
		// TotalUnrealizedProfit字段实际存储的是TotalPnL（相对初始余额）
		totalPnL := record.AccountState.TotalUnrealizedProfit

		totalPnLPct := 0.0
		if initialBalance > 0 {
			totalPnLPct = (totalPnL / initialBalance) * 100
		}

		history = append(history, EquityPoint{
			Timestamp:        record.Timestamp.Format("2006-01-02 15:04:05"),
			TotalEquity:      totalEquity,
			AvailableBalance: record.AccountState.AvailableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			PositionCount:    record.AccountState.PositionCount,
			MarginUsedPct:    record.AccountState.MarginUsedPct,
			CycleNumber:      record.CycleNumber,
		})
	}

	return history, nil
}

func applyEquityHistoryQuery(points []EquityPoint, query EquityHistoryQuery) []EquityPoint {
	filtered := points
	if query.FromCycle > 0 || query.ToCycle > 0 {
		filtered = make([]EquityPoint, 0, len(points))
		for _, point := range points {
			if query.FromCycle > 0 && point.CycleNumber < query.FromCycle {
				continue
			}
			if query.ToCycle > 0 && point.CycleNumber > query.ToCycle {
				continue
			}
			filtered = append(filtered, point)
		}
	}

	if query.Limit > 0 && len(filtered) > query.Limit {
		filtered = filtered[len(filtered)-query.Limit:]
	}
	return cloneEquityPoints(filtered)
}

func trimEquityPoints(points []EquityPoint, max int) []EquityPoint {
	if max > 0 && len(points) > max {
		points = points[len(points)-max:]
	}
	return points
}

func cloneEquityPoints(points []EquityPoint) []EquityPoint {
	if len(points) == 0 {
		return []EquityPoint{}
	}
	out := make([]EquityPoint, len(points))
	copy(out, points)
	return out
}

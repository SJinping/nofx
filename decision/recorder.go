package decision

import (
	"encoding/json"
	"fmt"
	"nofx/market"
	"os"
	"time"
)

// RecordedContext 用于录制和回放的完整上下文
type RecordedContext struct {
	// 基础信息
	CurrentTime    string `json:"current_time"`
	RuntimeMinutes int    `json:"runtime_minutes"`
	CallCount      int    `json:"call_count"`

	// 账户和持仓
	Account        AccountInfo     `json:"account"`
	Positions      []PositionInfo  `json:"positions"`
	CandidateCoins []CandidateCoin `json:"candidate_coins"`

	// 🔥 关键：市场数据（不能省略）
	MarketData map[string]*market.Data `json:"market_data"`
	OITopData  map[string]*OITopData   `json:"oi_top_data"`

	// 配置和绩效
	BTCETHLeverage  int                  `json:"btc_eth_leverage"`
	AltcoinLeverage int                  `json:"altcoin_leverage"`
	Performance     *PerformanceSnapshot `json:"performance,omitempty"`
}

// PerformanceSnapshot 绩效快照（避免使用interface{}）
type PerformanceSnapshot struct {
	SharpeRatio float64 `json:"sharpe_ratio"`
	TotalReturn float64 `json:"total_return"`
	MaxDrawdown float64 `json:"max_drawdown"`
}

// 修改保存函数
func saveContextToFile(ctx *Context, traderID string) error {
	// 转换为可序列化的结构
	record := &RecordedContext{
		CurrentTime:     ctx.CurrentTime,
		RuntimeMinutes:  ctx.RuntimeMinutes,
		CallCount:       ctx.CallCount,
		Account:         ctx.Account,
		Positions:       ctx.Positions,
		CandidateCoins:  ctx.CandidateCoins,
		MarketData:      ctx.MarketDataMap, // ✅ 保存市场数据
		OITopData:       ctx.OITopDataMap,  // ✅ 保存OI数据
		BTCETHLeverage:  ctx.BTCETHLeverage,
		AltcoinLeverage: ctx.AltcoinLeverage,
	}

	// 提取Performance（如果有）
	if ctx.Performance != nil {
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			var perfSnap PerformanceSnapshot
			if err := json.Unmarshal(jsonData, &perfSnap); err == nil {
				record.Performance = &perfSnap
			}
		}
	}

	// 序列化和保存
	dir := fmt.Sprintf("data/records/%s", traderID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建录制目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s/%s_cycle%d.json", dir, timestamp, ctx.CallCount)

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}

	return os.WriteFile(filename, data, 0644)
}

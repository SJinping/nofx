package trader

import (
	"log"
	"nofx/decision"
	"sync"
	"time"
)

// RuntimeConfig 运行时可热更新的配置（线程安全）。
// 交易循环每个周期通过 Get() 获取快照，API 层通过 Update() 修改。
type RuntimeConfig struct {
	mu               sync.RWMutex
	btcETHLeverage   int
	altcoinLeverage  int
	stopLossDistance  decision.StopLossDistanceConfig
	autoTakeProfit   decision.AutoTakeProfitConfig
	maxDailyLoss     float64       // 最大日亏损百分比
	maxDrawdown      float64       // 最大回撤百分比
	stopTradingTime  time.Duration // 风控暂停时长
	scanInterval     time.Duration // 扫描间隔
}

// RuntimeConfigSnapshot 运行时配置的只读快照（无锁，安全传递）
type RuntimeConfigSnapshot struct {
	BTCETHLeverage  int                              `json:"btc_eth_leverage"`
	AltcoinLeverage int                              `json:"altcoin_leverage"`
	StopLossDistance decision.StopLossDistanceConfig  `json:"stop_loss_distance"`
	AutoTakeProfit  decision.AutoTakeProfitConfig     `json:"auto_take_profit"`
	MaxDailyLoss    float64                          `json:"max_daily_loss"`
	MaxDrawdown     float64                          `json:"max_drawdown"`
	StopTradingMin  int                              `json:"stop_trading_minutes"`
	ScanIntervalMin int                              `json:"scan_interval_minutes"`
}

// RuntimeConfigPatch 用于部分更新运行时配置（零值表示不修改）
type RuntimeConfigPatch struct {
	BTCETHLeverage   *int                                `json:"btc_eth_leverage,omitempty"`
	AltcoinLeverage  *int                                `json:"altcoin_leverage,omitempty"`
	StopLossDistance *decision.StopLossDistanceConfig     `json:"stop_loss_distance,omitempty"`
	AutoTakeProfit   *decision.AutoTakeProfitConfig       `json:"auto_take_profit,omitempty"`
	MaxDailyLoss     *float64                            `json:"max_daily_loss,omitempty"`
	MaxDrawdown      *float64                            `json:"max_drawdown,omitempty"`
	StopTradingMin   *int                                `json:"stop_trading_minutes,omitempty"`
	ScanIntervalMin  *int                                `json:"scan_interval_minutes,omitempty"`
}

// NewRuntimeConfig 从 AutoTraderConfig 初始化运行时配置
func NewRuntimeConfig(cfg AutoTraderConfig) *RuntimeConfig {
	return &RuntimeConfig{
		btcETHLeverage:  cfg.BTCETHLeverage,
		altcoinLeverage: cfg.AltcoinLeverage,
		stopLossDistance: cfg.StopLossDistance,
		autoTakeProfit:  cfg.AutoTakeProfit,
		maxDailyLoss:    cfg.MaxDailyLoss,
		maxDrawdown:     cfg.MaxDrawdown,
		stopTradingTime: cfg.StopTradingTime,
		scanInterval:    cfg.ScanInterval,
	}
}

// Get 返回当前配置的只读快照
func (rc *RuntimeConfig) Get() RuntimeConfigSnapshot {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return RuntimeConfigSnapshot{
		BTCETHLeverage:  rc.btcETHLeverage,
		AltcoinLeverage: rc.altcoinLeverage,
		StopLossDistance: rc.stopLossDistance,
		AutoTakeProfit:  rc.autoTakeProfit,
		MaxDailyLoss:    rc.maxDailyLoss,
		MaxDrawdown:     rc.maxDrawdown,
		StopTradingMin:  int(rc.stopTradingTime.Minutes()),
		ScanIntervalMin: int(rc.scanInterval.Minutes()),
	}
}

// Update 部分更新运行时配置。返回 scanInterval 是否发生了变化。
// 如果 scanInterval 变了，调用方需要通知交易循环重置 ticker。
func (rc *RuntimeConfig) Update(patch RuntimeConfigPatch) (scanIntervalChanged bool, newInterval time.Duration) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if patch.BTCETHLeverage != nil && *patch.BTCETHLeverage > 0 {
		log.Printf("🔧 运行时配置更新: BTCETHLeverage %d → %d", rc.btcETHLeverage, *patch.BTCETHLeverage)
		rc.btcETHLeverage = *patch.BTCETHLeverage
	}
	if patch.AltcoinLeverage != nil && *patch.AltcoinLeverage > 0 {
		log.Printf("🔧 运行时配置更新: AltcoinLeverage %d → %d", rc.altcoinLeverage, *patch.AltcoinLeverage)
		rc.altcoinLeverage = *patch.AltcoinLeverage
	}
	if patch.StopLossDistance != nil {
		log.Printf("🔧 运行时配置更新: StopLossDistance 已修改")
		rc.stopLossDistance = *patch.StopLossDistance
	}
	if patch.AutoTakeProfit != nil {
		log.Printf("🔧 运行时配置更新: AutoTakeProfit 已修改")
		rc.autoTakeProfit = *patch.AutoTakeProfit
	}
	if patch.MaxDailyLoss != nil && *patch.MaxDailyLoss >= 0 {
		log.Printf("🔧 运行时配置更新: MaxDailyLoss %.2f → %.2f", rc.maxDailyLoss, *patch.MaxDailyLoss)
		rc.maxDailyLoss = *patch.MaxDailyLoss
	}
	if patch.MaxDrawdown != nil && *patch.MaxDrawdown >= 0 {
		log.Printf("🔧 运行时配置更新: MaxDrawdown %.2f → %.2f", rc.maxDrawdown, *patch.MaxDrawdown)
		rc.maxDrawdown = *patch.MaxDrawdown
	}
	if patch.StopTradingMin != nil && *patch.StopTradingMin >= 0 {
		newDur := time.Duration(*patch.StopTradingMin) * time.Minute
		log.Printf("🔧 运行时配置更新: StopTradingTime %v → %v", rc.stopTradingTime, newDur)
		rc.stopTradingTime = newDur
	}
	if patch.ScanIntervalMin != nil && *patch.ScanIntervalMin >= 1 {
		newDur := time.Duration(*patch.ScanIntervalMin) * time.Minute
		if newDur != rc.scanInterval {
			log.Printf("🔧 运行时配置更新: ScanInterval %v → %v", rc.scanInterval, newDur)
			rc.scanInterval = newDur
			return true, newDur
		}
	}

	return false, 0
}

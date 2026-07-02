package trader

import (
	"log"
	"nofx/decision"
	"nofx/mcp"
	"sync"
	"time"
)

// RuntimeConfig 运行时可热更新的配置（线程安全）。
// 交易循环每个周期通过 Get() 获取快照，API 层通过 Update() 修改。
type RuntimeConfig struct {
	mu               sync.RWMutex
	btcETHLeverage   int
	altcoinLeverage  int
	leverageClip     decision.LeverageClipConfig
	marginValidation decision.MarginValidationConfig
	stopLossDistance decision.StopLossDistanceConfig
	autoTakeProfit   decision.AutoTakeProfitConfig
	maxDailyLoss     float64       // 最大日亏损百分比
	maxDrawdown      float64       // 最大回撤百分比
	stopTradingTime  time.Duration // 风控暂停时长
	scanInterval     time.Duration // 扫描间隔
	minHoldMinutes   int           // LLM 平仓最低持仓时间（分钟，0=不限制）
	minOIValueMil    float64       // 候选币种 OI 价值门槛（百万USD）
	aiClient         *mcp.Client   // 该 trader 的 AI 客户端（用于热更新模型名）
}

// PeakHourPauseSnapshot 高峰时段暂停配置的只读快照
type PeakHourPauseSnapshot struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// RuntimeConfigSnapshot 运行时配置的只读快照（无锁，安全传递）
type RuntimeConfigSnapshot struct {
	BTCETHLeverage   int                             `json:"btc_eth_leverage"`
	AltcoinLeverage  int                             `json:"altcoin_leverage"`
	LeverageClip     decision.LeverageClipConfig     `json:"leverage_clip"`
	MarginValidation decision.MarginValidationConfig `json:"margin_validation"`
	StopLossDistance decision.StopLossDistanceConfig `json:"stop_loss_distance"`
	AutoTakeProfit   decision.AutoTakeProfitConfig   `json:"auto_take_profit"`
	MaxDailyLoss     float64                         `json:"max_daily_loss"`
	MaxDrawdown      float64                         `json:"max_drawdown"`
	StopTradingMin   int                             `json:"stop_trading_minutes"`
	ScanIntervalMin  int                             `json:"scan_interval_minutes"`
	MinHoldMinutes   int                             `json:"min_hold_minutes"`
	MinOIValueMil    float64                         `json:"min_oi_value_millions"`
	AIModel          string                          `json:"ai_model"`
	PeakHourPause    PeakHourPauseSnapshot           `json:"peak_hour_pause"`
}

// PeakHourPausePatch 高峰时段暂停配置的部分更新
type PeakHourPausePatch struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Start   *string `json:"start,omitempty"`
	End     *string `json:"end,omitempty"`
}

// RuntimeConfigPatch 用于部分更新运行时配置（零值表示不修改）
type RuntimeConfigPatch struct {
	BTCETHLeverage   *int                             `json:"btc_eth_leverage,omitempty"`
	AltcoinLeverage  *int                             `json:"altcoin_leverage,omitempty"`
	LeverageClip     *decision.LeverageClipConfig     `json:"leverage_clip,omitempty"`
	MarginValidation *decision.MarginValidationConfig `json:"margin_validation,omitempty"`
	StopLossDistance *decision.StopLossDistanceConfig `json:"stop_loss_distance,omitempty"`
	AutoTakeProfit   *decision.AutoTakeProfitConfig   `json:"auto_take_profit,omitempty"`
	MaxDailyLoss     *float64                         `json:"max_daily_loss,omitempty"`
	MaxDrawdown      *float64                         `json:"max_drawdown,omitempty"`
	StopTradingMin   *int                             `json:"stop_trading_minutes,omitempty"`
	ScanIntervalMin  *int                             `json:"scan_interval_minutes,omitempty"`
	MinHoldMinutes   *int                             `json:"min_hold_minutes,omitempty"`
	MinOIValueMil    *float64                         `json:"min_oi_value_millions,omitempty"`
	AIModel          *string                          `json:"ai_model,omitempty"`
	PeakHourPause    *PeakHourPausePatch              `json:"peak_hour_pause,omitempty"`
}

// NewRuntimeConfig 从 AutoTraderConfig 初始化运行时配置
func NewRuntimeConfig(cfg AutoTraderConfig, aiClient *mcp.Client) *RuntimeConfig {
	minOI := cfg.MinOIValueMillions
	if minOI <= 0 {
		minOI = 30 // 默认 30M USD
	}
	return &RuntimeConfig{
		btcETHLeverage:   cfg.BTCETHLeverage,
		altcoinLeverage:  cfg.AltcoinLeverage,
		leverageClip:     cfg.LeverageClip,
		marginValidation: cfg.MarginValidation,
		stopLossDistance: cfg.StopLossDistance,
		autoTakeProfit:   cfg.AutoTakeProfit,
		maxDailyLoss:     cfg.MaxDailyLoss,
		maxDrawdown:      cfg.MaxDrawdown,
		stopTradingTime:  cfg.StopTradingTime,
		scanInterval:     cfg.ScanInterval,
		minHoldMinutes:   cfg.MinHoldMinutes,
		minOIValueMil:    minOI,
		aiClient:         aiClient,
	}
}

// Get 返回当前配置的只读快照
func (rc *RuntimeConfig) Get() RuntimeConfigSnapshot {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	aiModel := ""
	if rc.aiClient != nil {
		aiModel = rc.aiClient.GetModel()
	}

	return RuntimeConfigSnapshot{
		BTCETHLeverage:   rc.btcETHLeverage,
		AltcoinLeverage:  rc.altcoinLeverage,
		LeverageClip:     rc.leverageClip,
		MarginValidation: rc.marginValidation,
		StopLossDistance: rc.stopLossDistance,
		AutoTakeProfit:   rc.autoTakeProfit,
		MaxDailyLoss:     rc.maxDailyLoss,
		MaxDrawdown:      rc.maxDrawdown,
		StopTradingMin:   int(rc.stopTradingTime.Minutes()),
		ScanIntervalMin:  int(rc.scanInterval.Minutes()),
		MinHoldMinutes:   rc.minHoldMinutes,
		MinOIValueMil:    rc.minOIValueMil,
		AIModel:          aiModel,
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
	if patch.LeverageClip != nil {
		log.Printf("🔧 运行时配置更新: LeverageClip 已修改")
		rc.leverageClip = *patch.LeverageClip
	}
	if patch.MarginValidation != nil {
		log.Printf("🔧 运行时配置更新: MarginValidation 已修改")
		rc.marginValidation = *patch.MarginValidation
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
	if patch.MinHoldMinutes != nil && *patch.MinHoldMinutes >= 0 {
		log.Printf("🔧 运行时配置更新: MinHoldMinutes %d → %d", rc.minHoldMinutes, *patch.MinHoldMinutes)
		rc.minHoldMinutes = *patch.MinHoldMinutes
	}
	if patch.MinOIValueMil != nil && *patch.MinOIValueMil >= 0 {
		log.Printf("🔧 运行时配置更新: MinOIValueMillions %.1f → %.1f", rc.minOIValueMil, *patch.MinOIValueMil)
		rc.minOIValueMil = *patch.MinOIValueMil
	}
	if patch.ScanIntervalMin != nil && *patch.ScanIntervalMin >= 1 {
		newDur := time.Duration(*patch.ScanIntervalMin) * time.Minute
		if newDur != rc.scanInterval {
			log.Printf("🔧 运行时配置更新: ScanInterval %v → %v", rc.scanInterval, newDur)
			rc.scanInterval = newDur
			return true, newDur
		}
	}

	if patch.AIModel != nil && *patch.AIModel != "" && rc.aiClient != nil {
		oldModel := rc.aiClient.GetModel()
		if *patch.AIModel != oldModel {
			log.Printf("🔧 运行时配置更新: AI Model %s → %s", oldModel, *patch.AIModel)
			rc.aiClient.SetModel(*patch.AIModel)
		}
	}

	return false, 0
}

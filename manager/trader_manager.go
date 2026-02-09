package manager

import (
	"fmt"
	"log"
	"nofx/config"
	"nofx/decision"
	"nofx/trader"
	"strings"
	"sync"
	"time"
)

// TraderManager 管理多个trader实例
type TraderManager struct {
	traders map[string]*trader.AutoTrader // key: trader ID
	mu      sync.RWMutex
}

// NewTraderManager 创建trader管理器
func NewTraderManager() *TraderManager {
	return &TraderManager{
		traders: make(map[string]*trader.AutoTrader),
	}
}

// AddTrader 添加一个trader
func (tm *TraderManager) AddTrader(cfg config.TraderConfig, coinPoolURL string, maxDailyLoss, maxDrawdown float64, stopTradingMinutes int, leverage config.LeverageConfig, enableRecording bool, binanceTestnet bool) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, exists := tm.traders[cfg.ID]; exists {
		return fmt.Errorf("trader ID '%s' 已存在", cfg.ID)
	}

	// 成本假设默认值（用于风控/校验/自动止盈；不影响实际下单）
	assumedTaker := 0.0004
	assumedSlippage := 0.0005
	// 显式配置优先
	if cfg.AssumedTakerFeeRate != nil {
		assumedTaker = *cfg.AssumedTakerFeeRate
	} else if cfg.PaperTradingMode && cfg.PaperTradingTakerFeeRate != nil {
		// paper 没配置 assumed 时，默认沿用 paper 的费率
		assumedTaker = *cfg.PaperTradingTakerFeeRate
	}
	if cfg.AssumedSlippageRate != nil {
		assumedSlippage = *cfg.AssumedSlippageRate
	} else if cfg.PaperTradingMode && cfg.PaperTradingSlippageRate != nil {
		assumedSlippage = *cfg.PaperTradingSlippageRate
	}

	// 构建AutoTraderConfig
	traderConfig := trader.AutoTraderConfig{
		ID:                       cfg.ID,
		Name:                     cfg.Name,
		AIModel:                  cfg.AIModel,
		Exchange:                 cfg.Exchange,
		BinanceTestnet:           binanceTestnet,
		BinanceAPIKey:            cfg.BinanceAPIKey,
		BinanceSecretKey:         cfg.BinanceSecretKey,
		HyperliquidPrivateKey:    cfg.HyperliquidPrivateKey,
		HyperliquidTestnet:       cfg.HyperliquidTestnet,
		AsterUser:                cfg.AsterUser,
		AsterSigner:              cfg.AsterSigner,
		AsterPrivateKey:          cfg.AsterPrivateKey,
		CoinPoolAPIURL:           coinPoolURL,
		UseQwen:                  cfg.AIModel == "qwen",
		DeepSeekKey:              cfg.DeepSeekKey,
		QwenKey:                  cfg.QwenKey,
		CustomAPIURL:             cfg.CustomAPIURL,
		CustomAPIKey:             cfg.CustomAPIKey,
		CustomModelName:          cfg.CustomModelName,
		ScanInterval:             cfg.GetScanInterval(),
		InitialBalance:           cfg.InitialBalance,
		BTCETHLeverage:           leverage.BTCETHLeverage,  // 使用配置的杠杆倍数
		AltcoinLeverage:          leverage.AltcoinLeverage, // 使用配置的杠杆倍数
		MaxDailyLoss:             maxDailyLoss,
		MaxDrawdown:              maxDrawdown,
		StopTradingTime:          time.Duration(stopTradingMinutes) * time.Minute,
		PaperTradingMode:         cfg.PaperTradingMode,
		PaperTradingTakerFeeRate: cfg.PaperTradingTakerFeeRate,
		PaperTradingSlippageRate: cfg.PaperTradingSlippageRate,
		AssumedTakerFeeRate:      assumedTaker,
		AssumedSlippageRate:      assumedSlippage,
		MinRiskReward:            cfg.MinRiskReward,
		EnableRecording:          enableRecording,
	}

	// 创建trader实例
	at, err := trader.NewAutoTrader(traderConfig)
	if err != nil {
		return fmt.Errorf("创建trader失败: %w", err)
	}

	// 根据配置动态设置 Prompt 策略（默认 A，配置为 "B" 则使用 B）
	switch strings.ToUpper(strings.TrimSpace(cfg.PromptStrategy)) {
	case "B":
		at.SetPromptStrategy(decision.StrategyB{})
	case "V":
		at.SetPromptStrategy(decision.StrategyV{})
	default:
		at.SetPromptStrategy(decision.StrategyA{})
	}

	tm.traders[cfg.ID] = at
	log.Printf("✓ Trader '%s' (%s) 已添加", cfg.Name, cfg.AIModel)
	return nil
}

// GetTrader 获取指定ID的trader
func (tm *TraderManager) GetTrader(id string) (*trader.AutoTrader, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	t, exists := tm.traders[id]
	if !exists {
		return nil, fmt.Errorf("trader ID '%s' 不存在", id)
	}
	return t, nil
}

// GetAllTraders 获取所有trader
func (tm *TraderManager) GetAllTraders() map[string]*trader.AutoTrader {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make(map[string]*trader.AutoTrader)
	for id, t := range tm.traders {
		result[id] = t
	}
	return result
}

// GetTraderIDs 获取所有trader ID列表
func (tm *TraderManager) GetTraderIDs() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	ids := make([]string, 0, len(tm.traders))
	for id := range tm.traders {
		ids = append(ids, id)
	}
	return ids
}

// StartAll 启动所有trader
func (tm *TraderManager) StartAll() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	log.Println("🚀 启动所有Trader...")
	for id, t := range tm.traders {
		go func(traderID string, at *trader.AutoTrader) {
			log.Printf("▶️  启动 %s...", at.GetName())
			if err := at.Run(); err != nil {
				log.Printf("❌ %s 运行错误: %v", at.GetName(), err)
			}
		}(id, t)
	}
}

// StopAll 停止所有trader
func (tm *TraderManager) StopAll() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	log.Println("⏹  停止所有Trader...")
	for _, t := range tm.traders {
		t.Stop()
	}
}

// SetAllPaused 设置所有trader的暂停状态
func (tm *TraderManager) SetAllPaused(paused bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	action := "恢复"
	if paused {
		action = "暂停"
	}
	log.Printf("⏯ %s所有Trader...", action)
	for _, t := range tm.traders {
		t.SetPaused(paused)
	}
}

// GetComparisonData 获取对比数据
func (tm *TraderManager) GetComparisonData() (map[string]interface{}, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	comparison := make(map[string]interface{})
	traders := make([]map[string]interface{}, 0, len(tm.traders))

	for _, t := range tm.traders {
		account, err := t.GetAccountInfo()
		if err != nil {
			continue
		}

		status := t.GetStatus()

		traders = append(traders, map[string]interface{}{
			"trader_id":       t.GetID(),
			"trader_name":     t.GetName(),
			"ai_model":        t.GetAIModel(),
			"total_equity":    account["total_equity"],
			"total_pnl":       account["total_pnl"],
			"total_pnl_pct":   account["total_pnl_pct"],
			"position_count":  account["position_count"],
			"margin_used_pct": account["margin_used_pct"],
			"call_count":      status["call_count"],
			"is_running":      status["is_running"],
			"is_paused":       status["is_paused"],
		})
	}

	comparison["traders"] = traders
	comparison["count"] = len(traders)

	return comparison, nil
}

// CloseAllPositionsForAllTraders 平掉所有trader的所有持仓
func (tm *TraderManager) CloseAllPositionsForAllTraders() map[string]error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	log.Println("🔴 批量平仓：开始平掉所有模型的所有持仓...")

	results := make(map[string]error)

	for id, t := range tm.traders {
		log.Printf("正在处理 %s (%s)...", t.GetName(), id)
		err := t.CloseAllPositions()
		results[id] = err

		if err != nil {
			log.Printf("❌ %s 平仓失败: %v", t.GetName(), err)
		} else {
			log.Printf("✓ %s 平仓成功", t.GetName())
		}
	}

	log.Println("🔴 批量平仓完成")
	return results
}

// CloseAllPositionsForTrader 平掉指定trader的所有持仓
func (tm *TraderManager) CloseAllPositionsForTrader(traderID string) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	trader, exists := tm.traders[traderID]
	if !exists {
		return fmt.Errorf("trader ID '%s' 不存在", traderID)
	}

	return trader.CloseAllPositions()
}

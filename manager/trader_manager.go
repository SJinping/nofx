package manager

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/config"
	"nofx/decision"
	"nofx/trader"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TraderManager 管理多个trader实例
type TraderManager struct {
	traders        map[string]*trader.AutoTrader // key: trader ID
	mu             sync.RWMutex
	configFilePath string // config.json 路径（用于持久化运行时配置变更）
}

// NewTraderManager 创建trader管理器
func NewTraderManager(configFilePath string) *TraderManager {
	return &TraderManager{
		traders:        make(map[string]*trader.AutoTrader),
		configFilePath: configFilePath,
	}
}

// AddTrader 添加一个trader
func (tm *TraderManager) AddTrader(cfg config.TraderConfig, coinPoolURL string, maxDailyLoss, maxDrawdown float64, stopTradingMinutes int, leverage config.LeverageConfig, enableRecording bool, binanceTestnet bool, stopLossDistCfg config.StopLossDistanceConfig, autoTPCfg config.AutoTakeProfitConfig, autoResume bool) error {
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
		StopLossDistance:         convertStopLossDistanceConfig(stopLossDistCfg),
		AutoTakeProfit:           convertAutoTakeProfitConfig(autoTPCfg),
		EnableRecording:          enableRecording,
		AutoResume:               autoResume,
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

// GetRuntimeConfig 获取指定trader的运行时配置（traderID为空时返回第一个trader的配置）
func (tm *TraderManager) GetRuntimeConfig(traderID string) (map[string]trader.RuntimeConfigSnapshot, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make(map[string]trader.RuntimeConfigSnapshot)

	if traderID != "" {
		t, exists := tm.traders[traderID]
		if !exists {
			return nil, fmt.Errorf("trader ID '%s' 不存在", traderID)
		}
		result[traderID] = t.GetRuntimeConfig()
		return result, nil
	}

	// 返回所有 trader 的配置
	for id, t := range tm.traders {
		result[id] = t.GetRuntimeConfig()
	}
	return result, nil
}

// UpdateRuntimeConfig 更新运行时配置（traderID为空时更新所有trader），并持久化到 config.json
func (tm *TraderManager) UpdateRuntimeConfig(traderID string, patch trader.RuntimeConfigPatch) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	// 记录变更前的快照（用于审计日志）
	targetID := traderID
	if targetID == "" {
		targetID = "(all)"
	}
	beforeSnaps := make(map[string]trader.RuntimeConfigSnapshot)
	if traderID != "" {
		if t, exists := tm.traders[traderID]; exists {
			beforeSnaps[traderID] = t.GetRuntimeConfig()
		}
	} else {
		for id, t := range tm.traders {
			beforeSnaps[id] = t.GetRuntimeConfig()
		}
	}

	// 应用变更
	if traderID != "" {
		t, exists := tm.traders[traderID]
		if !exists {
			return fmt.Errorf("trader ID '%s' 不存在", traderID)
		}
		t.UpdateRuntimeConfig(patch)
	} else {
		for _, t := range tm.traders {
			t.UpdateRuntimeConfig(patch)
		}
	}

	// 记录变更后的快照
	afterSnaps := make(map[string]trader.RuntimeConfigSnapshot)
	if traderID != "" {
		if t, exists := tm.traders[traderID]; exists {
			afterSnaps[traderID] = t.GetRuntimeConfig()
		}
	} else {
		for id, t := range tm.traders {
			afterSnaps[id] = t.GetRuntimeConfig()
		}
	}

	// 持久化到 config.json
	persistErr := tm.persistConfigPatch(patch, traderID)
	if persistErr != nil {
		log.Printf("⚠️  运行时配置已生效，但持久化到 %s 失败: %v", tm.configFilePath, persistErr)
	}

	// 写入审计日志
	tm.writeConfigChangeLog(targetID, patch, beforeSnaps, afterSnaps, persistErr)

	return nil
}

// persistConfigPatch 将 patch 中变更的字段写回 config.json（read-modify-write）
func (tm *TraderManager) persistConfigPatch(patch trader.RuntimeConfigPatch, traderID string) error {
	if tm.configFilePath == "" {
		return nil // 无配置文件路径，跳过持久化
	}

	// 1. 读取现有 config.json 为 map（保留所有未知字段）
	data, err := os.ReadFile(tm.configFilePath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 2. 更新全局字段
	if patch.MaxDailyLoss != nil {
		raw["max_daily_loss"] = *patch.MaxDailyLoss
	}
	if patch.MaxDrawdown != nil {
		raw["max_drawdown"] = *patch.MaxDrawdown
	}
	if patch.StopTradingMin != nil {
		raw["stop_trading_minutes"] = *patch.StopTradingMin
	}

	// leverage 是嵌套对象
	if patch.BTCETHLeverage != nil || patch.AltcoinLeverage != nil {
		leverage, _ := raw["leverage"].(map[string]interface{})
		if leverage == nil {
			leverage = make(map[string]interface{})
		}
		if patch.BTCETHLeverage != nil {
			leverage["btc_eth_leverage"] = *patch.BTCETHLeverage
		}
		if patch.AltcoinLeverage != nil {
			leverage["altcoin_leverage"] = *patch.AltcoinLeverage
		}
		raw["leverage"] = leverage
	}

	// stop_loss_distance 是嵌套对象，需要把 decision.StopLossDistanceConfig 转为 config 层的百分比格式
	if patch.StopLossDistance != nil {
		sld := patch.StopLossDistance
		sldMap, _ := raw["stop_loss_distance"].(map[string]interface{})
		if sldMap == nil {
			sldMap = make(map[string]interface{})
		}
		// decision 层用小数（0.0015 = 0.15%），config.json 用百分比值（0.15）
		if sld.MajorMinPct > 0 {
			sldMap["major_min_pct"] = sld.MajorMinPct * 100.0
		}
		if sld.MajorATRMult > 0 {
			sldMap["major_atr_mult"] = sld.MajorATRMult
		}
		if sld.MajorVolMult > 0 {
			sldMap["major_vol_mult"] = sld.MajorVolMult
		}
		if sld.AltMinPct > 0 {
			sldMap["alt_min_pct"] = sld.AltMinPct * 100.0
		}
		if sld.AltATRMult > 0 {
			sldMap["alt_atr_mult"] = sld.AltATRMult
		}
		if sld.AltVolMult > 0 {
			sldMap["alt_vol_mult"] = sld.AltVolMult
		}
		raw["stop_loss_distance"] = sldMap
	}

	// auto_take_profit 是嵌套对象，直接用原值（无需单位转换）
	if patch.AutoTakeProfit != nil {
		atp := patch.AutoTakeProfit
		atpMap, _ := raw["auto_take_profit"].(map[string]interface{})
		if atpMap == nil {
			atpMap = make(map[string]interface{})
		}
		if atp.Stage0Threshold > 0 {
			atpMap["stage0_threshold"] = atp.Stage0Threshold
		}
		if atp.Stage0ClosePct > 0 {
			atpMap["stage0_close_pct"] = atp.Stage0ClosePct
		}
		if atp.Stage1Threshold > 0 {
			atpMap["stage1_threshold"] = atp.Stage1Threshold
		}
		if atp.Stage1ClosePct > 0 {
			atpMap["stage1_close_pct"] = atp.Stage1ClosePct
		}
		if atp.FullCloseThreshold > 0 {
			atpMap["full_close_threshold"] = atp.FullCloseThreshold
		}
		if atp.CooldownMinutes > 0 {
			atpMap["cooldown_minutes"] = atp.CooldownMinutes
		}
		raw["auto_take_profit"] = atpMap
	}

	// scan_interval_minutes 是 per-trader 字段，更新 traders 数组中对应的条目
	if patch.ScanIntervalMin != nil {
		if traders, ok := raw["traders"].([]interface{}); ok {
			for _, t := range traders {
				traderMap, ok := t.(map[string]interface{})
				if !ok {
					continue
				}
				id, _ := traderMap["id"].(string)
				// 如果指定了 traderID 则只更新对应的；否则更新全部
				if traderID == "" || id == traderID {
					traderMap["scan_interval_minutes"] = *patch.ScanIntervalMin
				}
			}
		}
	}

	// 3. 写回 config.json（格式化缩进以保持可读性）
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(tm.configFilePath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	log.Printf("💾 运行时配置变更已持久化到 %s", tm.configFilePath)
	return nil
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

// GetAutoResume 读取 config.json 中的 auto_resume 字段
func (tm *TraderManager) GetAutoResume() bool {
	if tm.configFilePath == "" {
		return false
	}
	data, err := os.ReadFile(tm.configFilePath)
	if err != nil {
		return false
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	if v, ok := raw["auto_resume"].(bool); ok {
		return v
	}
	return false
}

// SetAutoResume 更新 config.json 中的 auto_resume 字段
func (tm *TraderManager) SetAutoResume(enabled bool) error {
	if tm.configFilePath == "" {
		return fmt.Errorf("无配置文件路径")
	}
	data, err := os.ReadFile(tm.configFilePath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}
	val, _ := json.Marshal(enabled)
	raw["auto_resume"] = val
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := os.WriteFile(tm.configFilePath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	log.Printf("💾 auto_resume=%v 已持久化到 %s", enabled, tm.configFilePath)
	return nil
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

// writeConfigChangeLog 将配置变更记录追加写入审计日志文件（config_changes.log）
func (tm *TraderManager) writeConfigChangeLog(
	targetID string,
	patch trader.RuntimeConfigPatch,
	before map[string]trader.RuntimeConfigSnapshot,
	after map[string]trader.RuntimeConfigSnapshot,
	persistErr error,
) {
	// 日志文件：与 config.json 同目录下的 config_changes.log
	logPath := "config_changes.log"
	if tm.configFilePath != "" {
		logPath = filepath.Join(filepath.Dir(tm.configFilePath), "config_changes.log")
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("⚠️  写入配置变更日志失败: %v", err)
		return
	}
	defer f.Close()

	// 构造日志条目
	entry := map[string]interface{}{
		"time":      time.Now().Format("2006-01-02 15:04:05"),
		"target":    targetID,
		"patch":     patch,
		"before":    before,
		"after":     after,
		"persisted": persistErr == nil,
	}
	if persistErr != nil {
		entry["persist_error"] = persistErr.Error()
	}

	line, _ := json.Marshal(entry)
	fmt.Fprintf(f, "%s\n", line)
}

// convertStopLossDistanceConfig 将配置层的百分比值（如0.15表示0.15%）转换为decision层的小数形式（0.0015）
// 零值字段使用默认值
func convertStopLossDistanceConfig(cfg config.StopLossDistanceConfig) decision.StopLossDistanceConfig {
	defaults := decision.DefaultStopLossDistanceConfig()

	if cfg.MajorMinPct > 0 {
		defaults.MajorMinPct = cfg.MajorMinPct / 100.0 // 0.15 → 0.0015
	}
	if cfg.MajorATRMult > 0 {
		defaults.MajorATRMult = cfg.MajorATRMult
	}
	if cfg.MajorVolMult > 0 {
		defaults.MajorVolMult = cfg.MajorVolMult
	}
	if cfg.AltMinPct > 0 {
		defaults.AltMinPct = cfg.AltMinPct / 100.0 // 0.35 → 0.0035
	}
	if cfg.AltATRMult > 0 {
		defaults.AltATRMult = cfg.AltATRMult
	}
	if cfg.AltVolMult > 0 {
		defaults.AltVolMult = cfg.AltVolMult
	}

	return defaults
}

// convertAutoTakeProfitConfig 将配置层的自动止盈参数转换为decision层结构体
// 零值字段使用默认值
func convertAutoTakeProfitConfig(cfg config.AutoTakeProfitConfig) decision.AutoTakeProfitConfig {
	defaults := decision.DefaultAutoTakeProfitConfig()

	if cfg.Stage0Threshold > 0 {
		defaults.Stage0Threshold = cfg.Stage0Threshold
	}
	if cfg.Stage0ClosePct > 0 {
		defaults.Stage0ClosePct = cfg.Stage0ClosePct
	}
	if cfg.Stage1Threshold > 0 {
		defaults.Stage1Threshold = cfg.Stage1Threshold
	}
	if cfg.Stage1ClosePct > 0 {
		defaults.Stage1ClosePct = cfg.Stage1ClosePct
	}
	if cfg.FullCloseThreshold > 0 {
		defaults.FullCloseThreshold = cfg.FullCloseThreshold
	}
	if cfg.CooldownMinutes > 0 {
		defaults.CooldownMinutes = cfg.CooldownMinutes
	}

	return defaults
}

package trader

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/decision"
	"nofx/logger"
	"nofx/memory"
	"nofx/mcp"
	"nofx/pool"
	"nofx/stats"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AutoTraderConfig 自动交易配置（简化版 - AI全权决策）
type AutoTraderConfig struct {
	// Trader标识
	ID      string // Trader唯一标识（用于日志目录等）
	Name    string // Trader显示名称
	AIModel string // AI模型: "qwen" 或 "deepseek"

	// 交易平台选择
	Exchange string // "binance", "hyperliquid" 或 "aster"

	// 币安API配置
	BinanceAPIKey    string
	BinanceSecretKey string
	BinanceTestnet   bool // 是否使用币安合约测试网（USDT-M Futures）

	// Hyperliquid配置
	HyperliquidPrivateKey string
	HyperliquidTestnet    bool

	// Aster配置
	AsterUser       string // Aster主钱包地址
	AsterSigner     string // Aster API钱包地址
	AsterPrivateKey string // Aster API钱包私钥

	CoinPoolAPIURL string

	// AI配置
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// 自定义AI API配置
	CustomAPIURL    string
	CustomAPIKey    string
	CustomModelName string

	// 扫描配置
	ScanInterval time.Duration // 扫描间隔（建议3分钟）

	// 新增：是否开启录制
	EnableRecording bool

	// 账户配置
	InitialBalance float64 // 初始金额（用于计算盈亏，需手动设置）

	// 杠杆配置
	BTCETHLeverage  int // BTC和ETH的杠杆倍数
	AltcoinLeverage int // 山寨币的杠杆倍数

	// 风险控制（仅作为提示，AI可自主决定）
	MaxDailyLoss    float64       // 最大日亏损百分比（提示）
	MaxDrawdown     float64       // 最大回撤百分比（提示）
	StopTradingTime time.Duration // 触发风控后暂停时长
	MinRiskReward   float64       // 最小风险回报比

	// 纸上交易模式
	PaperTradingMode         bool     // 是否启用纸上交易模式（只做策略分析，不实际下单）
	PaperTradingTakerFeeRate *float64 // 纸上交易Taker手续费率（不填=默认；填0=禁用）
	PaperTradingSlippageRate *float64 // 纸上交易滑点比例（不填=默认；填0=禁用）

	// 成本假设（用于风控/校验/自动止盈，不影响实际下单）
	AssumedTakerFeeRate float64
	AssumedSlippageRate float64

	// 止损最小距离配置（零值时使用默认）
	StopLossDistance decision.StopLossDistanceConfig
}

// AutoTrader 自动交易器
type AutoTrader struct {
	id                    string // Trader唯一标识
	name                  string // Trader显示名称
	aiModel               string // AI模型名称
	exchange              string // 交易平台名称
	config                AutoTraderConfig
	trader                Trader                 // 使用Trader接口（支持多平台）
	decisionLogger        *logger.DecisionLogger // 决策日志记录器
	tradeMemory           *memory.TradeMemory    // Trade memory (episodes + history)
	errorStats            *stats.ErrorStats      // 错误统计
	initialBalance        float64
	dailyPnL              float64
	lastResetTime         time.Time
	dailyStartEquity      float64 // 当日开始净值（用于日亏损计算）
	peakEquity            float64 // 历史最高净值（用于回撤计算）
	stopUntil             time.Time
	isRunning             bool
	isPaused              bool                    // 是否暂停（仅停止交易循环，不退出程序）
	startTime             time.Time               // 系统启动时间
	callCount             int                     // AI调用次数
	promptStrategy        decision.PromptStrategy // 当前使用的Prompt策略（默认StrategyA）
	positionFirstSeenTime map[string]int64        // 持仓首次出现时间 (symbol_side -> timestamp毫秒)
	autoDecisionState     decision.AutoDecisionState

	// 运行时可热更新配置（线程安全）
	runtimeCfg     *RuntimeConfig
	scanIntervalCh chan time.Duration // 通知 Run() 循环重置 ticker

	// API缓存（减少币安API调用频率）
	cacheMutex           sync.RWMutex
	accountInfoCache     map[string]interface{}
	accountInfoCacheTime time.Time
	positionsCache       []map[string]interface{}
	positionsCacheTime   time.Time
	ordersCache          map[string][]OrderRecord
	ordersCacheTime      map[string]time.Time
	cacheTTL             time.Duration // 缓存过期时间（默认30秒）
}

// NewAutoTrader 创建自动交易器
func NewAutoTrader(config AutoTraderConfig) (*AutoTrader, error) {
	// 设置默认值
	if config.ID == "" {
		config.ID = "default_trader"
	}
	if config.Name == "" {
		config.Name = "Default Trader"
	}
	if config.AIModel == "" {
		if config.UseQwen {
			config.AIModel = "qwen"
		} else {
			config.AIModel = "deepseek"
		}
	}

	// 初始化AI
	if config.AIModel == "custom" {
		// 使用自定义API
		mcp.SetCustomAPI(config.CustomAPIURL, config.CustomAPIKey, config.CustomModelName)
		log.Printf("🤖 [%s] 使用自定义AI API: %s (模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
	} else if config.UseQwen || config.AIModel == "qwen" {
		// 使用Qwen
		mcp.SetQwenAPIKey(config.QwenKey, "")
		log.Printf("🤖 [%s] 使用阿里云Qwen AI", config.Name)
	} else {
		// 默认使用DeepSeek
		mcp.SetDeepSeekAPIKey(config.DeepSeekKey)
		log.Printf("🤖 [%s] 使用DeepSeek AI", config.Name)
	}

	// 初始化币种池API
	if config.CoinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(config.CoinPoolAPIURL)
	}

	// 设置默认交易平台
	if config.Exchange == "" {
		config.Exchange = "binance"
	}

	// 根据配置创建对应的交易器
	var trader Trader
	var err error

	switch config.Exchange {
	case "binance":
		if config.BinanceTestnet {
			log.Printf("🏦 [%s] 使用币安合约交易 (TESTNET)", config.Name)
		} else {
			log.Printf("🏦 [%s] 使用币安合约交易", config.Name)
		}
		trader = NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey, config.BinanceTestnet)
	case "hyperliquid":
		log.Printf("🏦 [%s] 使用Hyperliquid交易", config.Name)
		trader, err = NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidTestnet)
		if err != nil {
			return nil, fmt.Errorf("初始化Hyperliquid交易器失败: %w", err)
		}
	case "aster":
		log.Printf("🏦 [%s] 使用Aster交易", config.Name)
		trader, err = NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("初始化Aster交易器失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的交易平台: %s", config.Exchange)
	}

	// 如果启用纸上交易模式，包装为纸上交易器
	if config.PaperTradingMode {
		// 构建纸上交易配置，使用默认币安费率
		paperConfig := DefaultPaperTradingConfig()

		// 应用用户自定义配置
		if config.PaperTradingTakerFeeRate != nil {
			paperConfig.TakerFeeRate = *config.PaperTradingTakerFeeRate
			paperConfig.EnableFees = *config.PaperTradingTakerFeeRate > 0
		}
		if config.PaperTradingSlippageRate != nil {
			paperConfig.SlippageRate = *config.PaperTradingSlippageRate
			paperConfig.EnableSlippage = *config.PaperTradingSlippageRate > 0
		}

		feeStatus := "关闭"
		if paperConfig.EnableFees {
			feeStatus = fmt.Sprintf("%.2f%%", paperConfig.TakerFeeRate*100)
		}
		slippageStatus := "关闭"
		if paperConfig.EnableSlippage {
			slippageStatus = fmt.Sprintf("%.2f%%", paperConfig.SlippageRate*100)
		}

		log.Printf("📝 [%s] 启用纸上交易模式 - 只做策略分析，不实际下单 (手续费: %s, 滑点: %s)",
			config.Name, feeStatus, slippageStatus)
		trader = NewPaperTraderWithConfig(trader, config.InitialBalance, config.Name, paperConfig)
	}

	// 验证初始金额配置
	if config.InitialBalance <= 0 {
		return nil, fmt.Errorf("初始金额必须大于0，请在配置中设置InitialBalance")
	}
	// 将配置写入决策层全局约束
	if config.MinRiskReward > 0 {
		decision.SetMinRiskReward(config.MinRiskReward)
	}

	// 初始化决策日志记录器（使用trader ID创建独立目录）
	logDir := fmt.Sprintf("decision_logs/%s", config.ID)
	decisionLogger := logger.NewDecisionLogger(logDir)

	// 初始化错误统计（使用全局实例，按traderID区分）
	errorStats := stats.GetErrorStats(config.ID)

	// 初始化 TradeMemory（best-effort，不影响主流程）
	var tradeMemory *memory.TradeMemory
	if tm, err := memory.NewTradeMemory(config.ID); err != nil {
		log.Printf("⚠️  [%s] 初始化TradeMemory失败: %v", config.Name, err)
	} else {
		tradeMemory = tm
	}

	return &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		config:                config,
		trader:                trader,
		decisionLogger:        decisionLogger,
		tradeMemory:           tradeMemory,
		errorStats:            errorStats,
		initialBalance:        config.InitialBalance,
		lastResetTime:         time.Now(),
		dailyStartEquity:      0,           // 第一次获取到净值后再初始化
		peakEquity:            0,           // 第一次获取到净值后再初始化
		stopUntil:             time.Time{}, // 默认无风控暂停
		isRunning:             false,       // Run 调用后才变为 true
		isPaused:              true,        // ⚠️ 启动时默认处于"暂停"状态，等待前端开关启动
		startTime:             time.Now(),
		callCount:             0,
		positionFirstSeenTime: make(map[string]int64),
		autoDecisionState: decision.AutoDecisionState{
			TP: make(map[string]*decision.AutoTPState),
		},
		// 运行时可热更新配置
		runtimeCfg:     NewRuntimeConfig(config),
		scanIntervalCh: make(chan time.Duration, 1),
		// 初始化API缓存（30秒过期，减少币安API调用）
		accountInfoCache: make(map[string]interface{}),
		positionsCache:   []map[string]interface{}{},
		ordersCache:      make(map[string][]OrderRecord),
		ordersCacheTime:  make(map[string]time.Time),
		cacheTTL:         30 * time.Second,
	}, nil
}

// Run 运行自动交易主循环
func (at *AutoTrader) Run() error {
	at.isRunning = true
	log.Println("🚀 AI驱动自动交易系统启动")
	log.Printf("💰 初始余额: %.2f USDT", at.initialBalance)
	log.Printf("⚙️  扫描间隔: %d分钟", at.runtimeCfg.Get().ScanIntervalMin)
	log.Println("🤖 AI将全权决定杠杆、仓位大小、止损止盈等参数")

	snap := at.runtimeCfg.Get()
	ticker := time.NewTicker(time.Duration(snap.ScanIntervalMin) * time.Minute)
	defer ticker.Stop()

	// 首次立即执行（仅在未暂停时）
	if !at.isPaused {
		if err := at.runCycle(); err != nil {
			log.Printf("❌ 执行失败: %v", err)
		}
	}

	for at.isRunning {
		select {
		case <-ticker.C:
			if at.isPaused {
				continue
			}
			if err := at.runCycle(); err != nil {
				log.Printf("❌ 执行失败: %v", err)
			}
		case newInterval := <-at.scanIntervalCh:
			ticker.Reset(newInterval)
			log.Printf("⚙️  扫描间隔已热更新为 %v", newInterval)
		}
	}

	return nil
}

// Stop 停止自动交易
func (at *AutoTrader) Stop() {
	at.isRunning = false
	log.Println("⏹ 自动交易系统停止")
}

// SetPaused 设置暂停状态
func (at *AutoTrader) SetPaused(paused bool) {
	at.isPaused = paused
	status := "运行"
	if paused {
		status = "暂停"
	}
	log.Printf("⏯ [%s] 交易状态已切换为: %s", at.name, status)
}

// GetRuntimeConfig 返回当前运行时配置快照（供 API 层调用）
func (at *AutoTrader) GetRuntimeConfig() RuntimeConfigSnapshot {
	return at.runtimeCfg.Get()
}

// UpdateRuntimeConfig 部分更新运行时配置（供 API 层调用）
func (at *AutoTrader) UpdateRuntimeConfig(patch RuntimeConfigPatch) {
	changed, newInterval := at.runtimeCfg.Update(patch)
	if changed {
		// 非阻塞发送，避免 Run() 循环未就绪时死锁
		select {
		case at.scanIntervalCh <- newInterval:
		default:
		}
	}
}

// runCycle 运行一个交易周期（使用AI全权决策）
func (at *AutoTrader) runCycle() error {
	at.callCount++

	// 开始新周期的错误统计
	at.errorStats.StartCycle()
	cycleSuccess := true // 标记周期是否成功
	defer func() {
		at.errorStats.EndCycle(cycleSuccess)
	}()

	// 设置当前错误统计实例供 decision 包使用
	decision.SetErrorStats(at.errorStats, at.callCount)

	log.Print("\n" + strings.Repeat("=", 70))
	log.Printf("⏰ %s - AI决策周期 #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	log.Print(strings.Repeat("=", 70))

	// 创建决策记录
	record := &logger.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. 检查是否需要停止交易
	if time.Now().Before(at.stopUntil) {
		remaining := at.stopUntil.Sub(time.Now())
		log.Printf("⏸ 风险控制：暂停交易中，剩余 %.0f 分钟", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("风险控制暂停中，剩余 %.0f 分钟", remaining.Minutes())
		at.decisionLogger.LogDecision(record)
		return nil
	}

	// 2. 重置日盈亏（每天重置）
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		log.Println("📅 日盈亏已重置")
	}

	// 3. 收集交易上下文
	ctx, err := at.buildTradingContext()
	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("构建交易上下文失败: %v", err)
		at.decisionLogger.LogDecision(record)
		cycleSuccess = false
		at.errorStats.RecordError(stats.ErrContextBuild, err.Error(), "", at.callCount)
		return fmt.Errorf("构建交易上下文失败: %w", err)
	}

	// 3.5 ✅ 硬风控：基于净值序列计算日亏损/最大回撤，触发则暂停交易
	// 说明：暂停期间仍会继续循环（用于前端展示/日志），但不会再开新仓。
	if at.shouldPauseForRisk(ctx) {
		remaining := at.stopUntil.Sub(time.Now())
		riskSnap := at.runtimeCfg.Get()
		log.Printf("⏸ 风险控制：触发暂停交易，剩余 %.0f 分钟（max_daily_loss=%.2f%%, max_drawdown=%.2f%%）",
			remaining.Minutes(), riskSnap.MaxDailyLoss, riskSnap.MaxDrawdown)

		record.Success = false
		record.ErrorMessage = fmt.Sprintf("风险控制暂停中，剩余 %.0f 分钟", remaining.Minutes())
		at.decisionLogger.LogDecision(record)
		return nil
	}

	// 保存账户状态快照
	record.AccountState = logger.AccountSnapshot{
		TotalBalance:          ctx.Account.TotalEquity,
		AvailableBalance:      ctx.Account.AvailableBalance,
		TotalUnrealizedProfit: ctx.Account.TotalPnL,
		PositionCount:         ctx.Account.PositionCount,
		MarginUsedPct:         ctx.Account.MarginUsedPct,
	}

	// 保存持仓快照
	for _, pos := range ctx.Positions {
		record.Positions = append(record.Positions, logger.PositionSnapshot{
			Symbol:           pos.Symbol,
			Side:             pos.Side,
			PositionAmt:      pos.Quantity,
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        pos.MarkPrice,
			UnrealizedProfit: pos.UnrealizedPnL,
			Leverage:         float64(pos.Leverage),
			LiquidationPrice: pos.LiquidationPrice,
		})
	}

	// 保存候选币种列表
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	log.Printf("📊 账户净值: %.2f USDT | 可用: %.2f USDT | 持仓: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 4. 调用AI获取完整决策
	log.Println("🤖 正在请求AI分析并决策...")
	engeinDecision, err := decision.GetFullDecision(ctx)

	// 即使有错误，也保存思维链、决策和输入prompt（用于debug）
	if engeinDecision != nil {
		record.InputPrompt = engeinDecision.UserPrompt
		record.CoTTrace = engeinDecision.CoTTrace
		if len(engeinDecision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(engeinDecision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("获取AI决策失败: %v", err)
		cycleSuccess = false

		// 打印AI思维链（即使有错误）
		if engeinDecision != nil && engeinDecision.CoTTrace != "" {
			log.Print("\n" + strings.Repeat("-", 70))
			log.Println("💭 AI思维链分析（错误情况）:")
			log.Println(strings.Repeat("-", 70))
			log.Println(engeinDecision.CoTTrace)
			log.Print(strings.Repeat("-", 70) + "\n")
		}

		at.decisionLogger.LogDecision(record)
		// 错误已在 decision.GetFullDecision 中记录，这里不再重复记录
		return fmt.Errorf("获取AI决策失败: %w", err)
	}

	// 5. 打印AI思维链
	log.Print("\n" + strings.Repeat("-", 70))
	log.Println("💭 AI思维链分析:")
	log.Println(strings.Repeat("-", 70))
	log.Println(engeinDecision.CoTTrace)
	log.Print(strings.Repeat("-", 70) + "\n")

	// 6. 打印AI决策
	log.Printf("📋 AI决策列表 (%d 个):\n", len(engeinDecision.Decisions))
	for i, d := range engeinDecision.Decisions {
		log.Printf("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
		if d.Action == decision.ActionOpenLong || d.Action == decision.ActionOpenShort {
			log.Printf("      杠杆: %dx | 仓位: %.2f USDT | 止损: %.4f | 止盈: %.4f",
				d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
		}
	}
	log.Println()

	// 7. 对决策排序：确保先平仓后开仓（防止仓位叠加超限）
	sortedDecisions := sortDecisionsByPriority(engeinDecision.Decisions)

	log.Println("🔄 执行顺序（已优化）: 先平仓→后开仓")
	for i, d := range sortedDecisions {
		log.Printf("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	log.Println()

	// 执行决策并记录结果
	for _, d := range sortedDecisions {
		actionRecord := logger.DecisionAction{
			Action:         d.Action,
			Symbol:         d.Symbol,
			DecisionSource: d.DecisionSource,
			Quantity:       0,
			Leverage:       d.Leverage,
			Price:          0,
			Timestamp:      time.Now(),
			Success:        false,
		}

		// ✅ 开仓前历史学习：检索 + 规则Gate + 可选OpenGuard
		if at.tradeMemory != nil && (d.Action == decision.ActionOpenLong || d.Action == decision.ActionOpenShort) {
			gate, gateErr := at.tradeMemory.GateOpenDecision(ctx, &d)
			if gateErr != nil {
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("⚠️ %s %s Gate异常: %v", d.Symbol, d.Action, gateErr))
			} else if gate != nil {
				switch gate.Decision {
				case memory.GateReject:
					actionRecord.Error = fmt.Sprintf("rejected_by_gate: %s", gate.Reason)
					record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("⛔ %s %s 被Gate拒绝: %s", d.Symbol, d.Action, gate.Reason))
					record.Decisions = append(record.Decisions, actionRecord)
					continue
				case memory.GateModify:
					if gate.SizeMultiplier > 0 && gate.SizeMultiplier < 1 {
						old := d.PositionSizeUSD
						d.PositionSizeUSD = d.PositionSizeUSD * gate.SizeMultiplier
						record.ExecutionLog = append(record.ExecutionLog,
							fmt.Sprintf("🧠 %s %s Gate缩仓: %.2f -> %.2f (%s)", d.Symbol, d.Action, old, d.PositionSizeUSD, gate.Reason))
					}
				case memory.GateApprove:
					if strings.TrimSpace(gate.Reason) != "" {
						record.ExecutionLog = append(record.ExecutionLog,
							fmt.Sprintf("🧠 %s %s Gate放行: %s", d.Symbol, d.Action, gate.Reason))
					}
				}
			}
		}

		if err := at.executeDecisionWithRecord(ctx, &d, &actionRecord); err != nil {
			log.Printf("❌ 执行决策失败 (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s 失败: %v", d.Symbol, d.Action, err))
			// 记录交易执行错误
			errType := stats.ClassifyTradeError(err.Error())
			at.errorStats.RecordError(errType, err.Error(), d.Symbol, at.callCount)
		} else {
			actionRecord.Success = true
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("✓ %s %s 成功", d.Symbol, d.Action))
			// 成功执行后短暂延迟
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

	// 8. 保存决策记录
	if err := at.decisionLogger.LogDecision(record); err != nil {
		log.Printf("⚠ 保存决策记录失败: %v", err)
	}

	return nil
}

// buildTradingContext 构建交易上下文
func (at *AutoTrader) buildTradingContext() (*decision.Context, error) {
	// 1. 获取账户信息（失败时尝试使用缓存）
	balance, err := at.trader.GetBalance()
	usedCache := false
	if err != nil {
		// 尝试使用缓存数据
		at.cacheMutex.RLock()
		hasCache := at.accountInfoCache != nil
		at.cacheMutex.RUnlock()

		if hasCache {
			log.Printf("⚠️  获取账户余额失败，使用缓存数据继续: %v", err)
			at.cacheMutex.RLock()
			balance = map[string]interface{}{
				"totalWalletBalance":    at.accountInfoCache["wallet_balance"],
				"totalUnrealizedProfit": at.accountInfoCache["unrealized_profit"],
				"availableBalance":      at.accountInfoCache["available_balance"],
			}
			at.cacheMutex.RUnlock()
			usedCache = true
		} else {
			at.errorStats.RecordError(stats.ErrAccountBalance, err.Error(), "", at.callCount)
			return nil, fmt.Errorf("获取账户余额失败: %w", err)
		}
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 如果使用了缓存但数据无效，仍然返回错误
	if usedCache && totalEquity <= 0 {
		return nil, fmt.Errorf("获取账户余额失败且缓存数据无效")
	}

	// 2. 获取持仓信息（失败时尝试使用缓存）
	positions, err := at.trader.GetPositions()
	if err != nil {
		// 尝试使用缓存数据
		at.cacheMutex.RLock()
		hasCache := at.positionsCache != nil
		at.cacheMutex.RUnlock()

		if hasCache {
			log.Printf("⚠️  获取持仓信息失败，使用缓存数据继续: %v", err)
			at.cacheMutex.RLock()
			positions = at.positionsCache
			at.cacheMutex.RUnlock()
		} else {
			at.errorStats.RecordError(stats.ErrAccountPosition, err.Error(), "", at.callCount)
			return nil, fmt.Errorf("获取持仓失败: %w", err)
		}
	}

	var positionInfos []decision.PositionInfo
	totalMarginUsed := 0.0

	// 当前持仓的key集合（用于清理已平仓的记录）
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbolVal, ok := pos["symbol"]
		if !ok {
			continue
		}
		symbol, ok := symbolVal.(string)
		if !ok || symbol == "" {
			continue
		}

		// side 兼容性处理
		var side string
		if sv, ok := pos["side"].(string); ok && sv != "" {
			side = sv
		} else if psv, ok := pos["positionSide"].(string); ok && psv != "" {
			side = strings.ToLower(psv)
		} else {
			continue
		}

		// 数值安全解析（兼容 float64 / string）
		toFloat := func(v interface{}) (float64, bool) {
			switch t := v.(type) {
			case float64:
				return t, true
			case string:
				f, err := strconv.ParseFloat(t, 64)
				if err != nil {
					return 0, false
				}
				return f, true
			default:
				return 0, false
			}
		}

		ep, ok := toFloat(pos["entryPrice"])
		if !ok {
			continue
		}
		mp, ok := toFloat(pos["markPrice"])
		if !ok {
			mp = ep // 兜底
		}
		qty, ok := toFloat(pos["positionAmt"])
		if !ok {
			continue
		}
		if qty < 0 {
			qty = -qty // 空仓数量为负，转为正数
		}
		upnl, _ := toFloat(pos["unRealizedProfit"]) // 失败则按0
		liq, _ := toFloat(pos["liquidationPrice"])  // 失败则按0

		// 计算盈亏百分比
		pnlPct := 0.0
		if side == "long" {
			pnlPct = ((mp - ep) / ep) * 100
		} else {
			pnlPct = ((ep - mp) / ep) * 100
		}

		// 计算占用保证金（估算）
		leverage := 10 // 默认值，实际应该从持仓信息获取
		if levf, ok := toFloat(pos["leverage"]); ok {
			leverage = int(levf)
		}
		marginUsed := (qty * mp) / float64(leverage)
		totalMarginUsed += marginUsed

		// 跟踪持仓首次出现时间
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true
		if _, exists := at.positionFirstSeenTime[posKey]; !exists {
			// 新持仓，记录当前时间
			at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
		}
		updateTime := at.positionFirstSeenTime[posKey]

		// 初始化 / 更新自动止盈状态（用于TP1/TP2只触发一次）
		// 检测明显加仓/均价变化时，重置TP阶段（更偏积极：把加仓视为“新一轮”止盈）
		if at.autoDecisionState.TP == nil {
			at.autoDecisionState.TP = make(map[string]*decision.AutoTPState)
		}
		if st, exists := at.autoDecisionState.TP[posKey]; !exists || st == nil {
			at.autoDecisionState.TP[posKey] = &decision.AutoTPState{
				Stage:            0,
				LastActionTimeMs: 0,
				BaselineEntry:    ep,
				BaselineQty:      qty,
			}
		} else {
			// 加仓/均价变化阈值（经验值）
			entryChanged := false
			if st.BaselineEntry > 0 {
				diff := (ep - st.BaselineEntry) / st.BaselineEntry
				if diff < 0 {
					diff = -diff
				}
				entryChanged = diff > 0.005 // 0.5%
			}
			qtyIncreased := qty > st.BaselineQty*1.25 // +25%
			if entryChanged || qtyIncreased {
				st.Stage = 0
				st.LastActionTimeMs = 0
				st.BaselineEntry = ep
				st.BaselineQty = qty
			}
		}

		positionInfos = append(positionInfos, decision.PositionInfo{
			Symbol:           symbol,
			Side:             side,
			EntryPrice:       ep,
			MarkPrice:        mp,
			Quantity:         qty,
			Leverage:         leverage,
			UnrealizedPnL:    upnl,
			UnrealizedPnLPct: pnlPct,
			LiquidationPrice: liq,
			MarginUsed:       marginUsed,
			UpdateTime:       updateTime,
		})
	}

	// ✅ 更新TradeEpisode滚动指标（每个周期一次，不落EpisodePoint）
	if at.tradeMemory != nil {
		at.tradeMemory.UpdateEpisodesFromPositions(at.id, positionInfos)
	}

	// 清理已平仓的持仓记录
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			delete(at.positionFirstSeenTime, key)
		}
	}

	// 清理已平仓的自动止盈状态
	for key := range at.autoDecisionState.TP {
		if !currentPositionKeys[key] {
			delete(at.autoDecisionState.TP, key)
		}
	}

	// 3. 获取合并的候选币种池（AI500 + OI Top，去重）
	// 无论有没有持仓，都分析相同数量的币种（让AI看到所有好机会）
	// AI会根据保证金使用率和现有持仓情况，自己决定是否要换仓
	const ai500Limit = 20 // AI500取前20个评分最高的币种

	// 获取合并后的币种池（AI500 + OI Top）
	mergedPool, err := pool.GetMergedCoinPool(ai500Limit)
	if err != nil {
		at.errorStats.RecordError(stats.ErrCoinPoolAPI, err.Error(), "", at.callCount)
		return nil, fmt.Errorf("获取合并币种池失败: %w", err)
	}

	// 构建候选币种列表（包含来源信息）
	var candidateCoins []decision.CandidateCoin
	for _, symbol := range mergedPool.AllSymbols {
		sources := mergedPool.SymbolSources[symbol]
		candidateCoins = append(candidateCoins, decision.CandidateCoin{
			Symbol:  symbol,
			Sources: sources, // "ai500" 和/或 "oi_top"
		})
	}

	log.Printf("📋 合并币种池: AI500前%d + OI_Top20 = 总计%d个候选币种",
		ai500Limit, len(candidateCoins))

	// 4. 计算总盈亏
	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. 分析历史表现（最近20个周期）
	performance, err := at.decisionLogger.AnalyzePerformance(20)
	if err != nil {
		log.Printf("⚠️  分析历史表现失败: %v", err)
		// 不影响主流程，继续执行（但设置performance为nil以避免传递错误数据）
		performance = nil
	}

	// 6. 构建上下文（从运行时配置读取可热更新的参数）
	rcSnap := at.runtimeCfg.Get()
	ctx := &decision.Context{
		CurrentTime:     time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes:  int(time.Since(at.startTime).Minutes()),
		CallCount:       at.callCount,
		BTCETHLeverage:  rcSnap.BTCETHLeverage,  // 从运行时配置读取
		AltcoinLeverage: rcSnap.AltcoinLeverage, // 从运行时配置读取
		Account: decision.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:           positionInfos,
		CandidateCoins:      candidateCoins,
		Performance:         performance, // 添加历史表现分析
		AssumedTakerFeeRate: at.config.AssumedTakerFeeRate,
		AssumedSlippageRate: at.config.AssumedSlippageRate,
		StopLossDistance:    rcSnap.StopLossDistance, // 从运行时配置读取
		EnableRecording:     at.config.EnableRecording,
		TraderID:            at.config.ID,
		AutoState:           &at.autoDecisionState,
	}

	// 注入可插拔策略，默认使用 StrategyA
	if at.promptStrategy == nil {
		ctx.PromptStrategy = decision.StrategyA{}
	} else {
		ctx.PromptStrategy = at.promptStrategy
	}

	return ctx, nil
}

// shouldPauseForRisk 基于净值序列进行硬风控判断：
// - max_daily_loss：从“当日开始净值”计算
// - max_drawdown：从“历史峰值净值”计算
func (at *AutoTrader) shouldPauseForRisk(ctx *decision.Context) bool {
	if ctx == nil {
		return false
	}
	// 如果本来就在暂停窗口内，直接返回 true（上层会记录并跳过交易）
	if !at.stopUntil.IsZero() && time.Now().Before(at.stopUntil) {
		return true
	}

	equity := ctx.Account.TotalEquity
	if equity <= 0 {
		return false
	}

	// 初始化当日起始净值与峰值
	if at.dailyStartEquity <= 0 {
		at.dailyStartEquity = equity
	}
	if at.peakEquity <= 0 {
		at.peakEquity = equity
	}

	// 每日重置：以 lastResetTime 为基准（已在 runCycle 中每日 reset dailyPnL）
	// 这里补全 dailyStartEquity 重置逻辑（与 dailyPnL 重置保持一致）
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyStartEquity = equity
		// 同时把峰值也用当前值初始化，避免跨日带来过度收缩
		at.peakEquity = equity
		return false
	}

	// 更新峰值
	if equity > at.peakEquity {
		at.peakEquity = equity
	}

	// 日盈亏（用于状态展示）
	at.dailyPnL = equity - at.dailyStartEquity

	// 计算日亏损百分比（负数表示亏损）
	dailyPnLPct := 0.0
	if at.dailyStartEquity > 0 {
		dailyPnLPct = (equity - at.dailyStartEquity) / at.dailyStartEquity * 100
	}

	// 计算回撤百分比（负数表示回撤）
	drawdownPct := 0.0
	if at.peakEquity > 0 {
		drawdownPct = (equity - at.peakEquity) / at.peakEquity * 100
	}

	// 从运行时配置读取风控参数
	rcSnap := at.runtimeCfg.Get()
	maxDailyLoss := rcSnap.MaxDailyLoss
	maxDrawdown := rcSnap.MaxDrawdown
	stopTradingTime := time.Duration(rcSnap.StopTradingMin) * time.Minute

	// 触发条件：亏损超过阈值（阈值按正数配置）
	trigger := false
	if maxDailyLoss > 0 && dailyPnLPct <= -maxDailyLoss {
		trigger = true
	}
	if maxDrawdown > 0 && drawdownPct <= -maxDrawdown {
		trigger = true
	}
	if !trigger {
		return false
	}

	// 触发暂停窗口
	if stopTradingTime <= 0 {
		// 兜底：默认暂停60分钟
		at.stopUntil = time.Now().Add(60 * time.Minute)
	} else {
		at.stopUntil = time.Now().Add(stopTradingTime)
	}
	return true
}

// executeDecisionWithRecord 执行AI决策并记录详细信息
func (at *AutoTrader) executeDecisionWithRecord(ctx *decision.Context, dec *decision.Decision, actionRecord *logger.DecisionAction) error {
	switch dec.Action {
	case decision.ActionOpenLong:
		return at.executeOpenLongWithRecord(ctx, dec, actionRecord)
	case decision.ActionOpenShort:
		return at.executeOpenShortWithRecord(ctx, dec, actionRecord)
	case decision.ActionCloseLong:
		return at.executeCloseLongWithRecord(ctx, dec, actionRecord)
	case decision.ActionCloseShort:
		return at.executeCloseShortWithRecord(ctx, dec, actionRecord)
	case decision.ActionUpdateStopLoss:
		return at.executeUpdateStopLossWithRecord(dec, actionRecord)
	case decision.ActionUpdateTakeProfit:
		return at.executeUpdateTakeProfitWithRecord(dec, actionRecord)
	case decision.ActionPartialClose:
		return at.executePartialCloseWithRecord(dec, actionRecord)
	case decision.ActionHold, decision.ActionWait:
		// 无需执行，仅记录
		return nil
	default:
		return fmt.Errorf("未知的action: %s", dec.Action)
	}
}

// executeOpenLongWithRecord 执行开多仓并记录详细信息
func (at *AutoTrader) executeOpenLongWithRecord(ctx *decision.Context, decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📈 开多仓: %s", decision.Symbol)

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
				return fmt.Errorf("❌ %s 已有多仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_long 决策", decision.Symbol)
			}
		}
	}

	// 获取当前价格
	currentPrice, err := at.trader.GetMarketPrice(decision.Symbol)
	if err != nil {
		return err
	}

	// 计算数量
	quantity := decision.PositionSizeUSD / currentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = currentPrice

	// 开仓
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// ✅ 创建 TradeEpisode（开仓成功后）
	if at.tradeMemory != nil {
		at.tradeMemory.OnOpenSuccess(ctx, decision, quantity, currentPrice)
	}

	// 设置止损止盈
	if err := at.trader.SetStopLoss(decision.Symbol, "LONG", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "LONG", quantity, decision.TakeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败: %v", err)
	}

	return nil
}

// executeOpenShortWithRecord 执行开空仓并记录详细信息
func (at *AutoTrader) executeOpenShortWithRecord(ctx *decision.Context, decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📉 开空仓: %s", decision.Symbol)

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
				return fmt.Errorf("❌ %s 已有空仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_short 决策", decision.Symbol)
			}
		}
	}

	// 获取当前价格
	currentPrice, err := at.trader.GetMarketPrice(decision.Symbol)
	if err != nil {
		return err
	}

	// 计算数量
	quantity := decision.PositionSizeUSD / currentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = currentPrice

	// 开仓
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// ✅ 创建 TradeEpisode（开仓成功后）
	if at.tradeMemory != nil {
		at.tradeMemory.OnOpenSuccess(ctx, decision, quantity, currentPrice)
	}

	// 设置止损止盈
	if err := at.trader.SetStopLoss(decision.Symbol, "SHORT", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "SHORT", quantity, decision.TakeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败: %v", err)
	}

	return nil
}

// executeCloseLongWithRecord 执行平多仓并记录详细信息
func (at *AutoTrader) executeCloseLongWithRecord(ctx *decision.Context, decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 平多仓: %s", decision.Symbol)

	// 获取当前价格
	currentPrice, err := at.trader.GetMarketPrice(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = currentPrice

	// 平仓
	order, err := at.trader.CloseLong(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 平仓成功")

	// ✅ 写入TradeRecord并触发复盘总结agent（异步）
	if at.tradeMemory != nil {
		reason := "ai_close"
		if strings.TrimSpace(decision.DecisionSource) != "" {
			reason = decision.DecisionSource
		}
		_, _ = at.tradeMemory.OnCloseSuccess(ctx, decision, currentPrice, reason)
	}

	// 自动止盈的全平：清理状态，防止残留
	if decision.DecisionSource == "auto_take_profit" {
		posKey := decision.Symbol + "_long"
		delete(at.autoDecisionState.TP, posKey)
	}
	return nil
}

// executeCloseShortWithRecord 执行平空仓并记录详细信息
func (at *AutoTrader) executeCloseShortWithRecord(ctx *decision.Context, decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 平空仓: %s", decision.Symbol)

	// 获取当前价格
	currentPrice, err := at.trader.GetMarketPrice(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = currentPrice

	// 平仓
	order, err := at.trader.CloseShort(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 平仓成功")

	// ✅ 写入TradeRecord并触发复盘总结agent（异步）
	if at.tradeMemory != nil {
		reason := "ai_close"
		if strings.TrimSpace(decision.DecisionSource) != "" {
			reason = decision.DecisionSource
		}
		_, _ = at.tradeMemory.OnCloseSuccess(ctx, decision, currentPrice, reason)
	}

	// 自动止盈的全平：清理状态，防止残留
	if decision.DecisionSource == "auto_take_profit" {
		posKey := decision.Symbol + "_short"
		delete(at.autoDecisionState.TP, posKey)
	}
	return nil
}

// executeUpdateStopLossWithRecord 执行更新止损并记录详细信息
// 1. 获取目标持仓
// 2. 获取持仓方向和数量
// 3. 获取当前价格用于记录
// 4. 基于当前价格做方向校验，避免明显无效/瞬间触发的止损
// 5. 更新止损
// 6. 记录详细信息
func (at *AutoTrader) executeUpdateStopLossWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🛡️ 更新止损: %s -> %.4f", decision.Symbol, decision.NewStopLoss)

	// 获取当前持仓信息
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	// 查找目标持仓
	var targetPosition map[string]interface{}
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol {
			targetPosition = pos
			break
		}
	}

	if targetPosition == nil {
		return fmt.Errorf("未找到 %s 的持仓", decision.Symbol)
	}

	// 获取持仓方向和数量
	side, _ := targetPosition["side"].(string)
	quantity := 0.0
	if qty, ok := targetPosition["positionAmt"].(float64); ok {
		quantity = qty
		if quantity < 0 {
			quantity = -quantity
		}
	}

	if quantity == 0 {
		return fmt.Errorf("持仓数量为0")
	}

	// 获取当前价格，用于记录与有效性校验
	currentPrice, err := at.trader.GetMarketPrice(decision.Symbol)
	if err != nil {
		return fmt.Errorf("获取市场价格失败: %w", err)
	}
	if currentPrice <= 0 {
		return fmt.Errorf("当前价格无效: %.4f", currentPrice)
	}

	actionRecord.Price = currentPrice
	actionRecord.Quantity = quantity

	switch strings.ToLower(side) {
	case "long":
		// 多仓止损必须在当前价之下，否则会立即触发
		if decision.NewStopLoss >= currentPrice {
			return fmt.Errorf("new_stop_loss(%.4f) 必须低于当前价(%.4f) 才是有效多仓止损", decision.NewStopLoss, currentPrice)
		}
	case "short":
		// 空仓止损必须在当前价之上，否则会立即触发
		if decision.NewStopLoss <= currentPrice {
			return fmt.Errorf("new_stop_loss(%.4f) 必须高于当前价(%.4f) 才是有效空仓止损", decision.NewStopLoss, currentPrice)
		}
	default:
		return fmt.Errorf("未知持仓方向: %s", side)
	}

	// 更新止损
	// TODO: 如果有旧止损单，需要先取消旧止损单
	positionSide := strings.ToUpper(side)
	if err := at.trader.SetStopLoss(decision.Symbol, positionSide, quantity, decision.NewStopLoss); err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	log.Printf("  ✓ 止损更新成功")
	return nil
}

// executeUpdateTakeProfitWithRecord 执行更新止盈并记录详细信息
func (at *AutoTrader) executeUpdateTakeProfitWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🎯 更新止盈: %s -> %.4f", decision.Symbol, decision.NewTakeProfit)

	// 获取当前持仓信息
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	// 查找目标持仓
	var targetPosition map[string]interface{}
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol {
			targetPosition = pos
			break
		}
	}

	if targetPosition == nil {
		return fmt.Errorf("未找到 %s 的持仓", decision.Symbol)
	}

	// 获取持仓方向和数量
	side, _ := targetPosition["side"].(string)
	quantity := 0.0
	if qty, ok := targetPosition["positionAmt"].(float64); ok {
		quantity = qty
		if quantity < 0 {
			quantity = -quantity
		}
	}

	if quantity == 0 {
		return fmt.Errorf("持仓数量为0")
	}

	// 获取当前价格，用于记录与有效性校验
	// 基于当前价格做方向校验，避免明显无效/瞬间触发的止盈
	currentPrice, err := at.trader.GetMarketPrice(decision.Symbol)
	if err != nil {
		return fmt.Errorf("获取市场价格失败: %w", err)
	}
	if currentPrice <= 0 {
		return fmt.Errorf("当前价格无效: %.4f", currentPrice)
	}
	actionRecord.Price = currentPrice
	actionRecord.Quantity = quantity

	switch strings.ToLower(side) {
	case "long":
		// 多仓止盈应该在当前价之上，否则意义不大/可能瞬间触发
		if decision.NewTakeProfit <= currentPrice {
			return fmt.Errorf("new_take_profit(%.4f) 应高于当前价(%.4f) 才是合理多仓止盈", decision.NewTakeProfit, currentPrice)
		}
	case "short":
		// 空仓止盈应该在当前价之下
		if decision.NewTakeProfit >= currentPrice {
			return fmt.Errorf("new_take_profit(%.4f) 应低于当前价(%.4f) 才是合理空仓止盈", decision.NewTakeProfit, currentPrice)
		}
	default:
		return fmt.Errorf("未知持仓方向: %s", side)
	}

	// 更新止盈
	positionSide := strings.ToUpper(side)
	if err := at.trader.SetTakeProfit(decision.Symbol, positionSide, quantity, decision.NewTakeProfit); err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	log.Printf("  ✓ 止盈更新成功")
	return nil
}

// executePartialCloseWithRecord 执行部分平仓并记录详细信息
func (at *AutoTrader) executePartialCloseWithRecord(dec *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📊 部分平仓: %s (%.0f%%)", dec.Symbol, dec.ClosePercentage)

	// 获取当前持仓信息
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	// 查找目标持仓
	var targetPosition map[string]interface{}
	for _, pos := range positions {
		if pos["symbol"] == dec.Symbol {
			targetPosition = pos
			break
		}
	}

	if targetPosition == nil {
		return fmt.Errorf("未找到 %s 的持仓", dec.Symbol)
	}

	// 获取持仓方向和数量
	side, _ := targetPosition["side"].(string)
	quantity := 0.0
	if qty, ok := targetPosition["positionAmt"].(float64); ok {
		quantity = qty
		if quantity < 0 {
			quantity = -quantity
		}
	}

	if quantity == 0 {
		return fmt.Errorf("持仓数量为0")
	}

	// 计算要平仓的数量
	closeQuantity := quantity * (dec.ClosePercentage / 100.0)

	// 获取当前价格
	currentPrice, err := at.trader.GetMarketPrice(dec.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = currentPrice
	actionRecord.Quantity = closeQuantity

	// 执行部分平仓
	var order map[string]interface{}
	if side == "long" {
		order, err = at.trader.CloseLong(dec.Symbol, closeQuantity)
	} else if side == "short" {
		order, err = at.trader.CloseShort(dec.Symbol, closeQuantity)
	} else {
		return fmt.Errorf("未知的持仓方向: %s", side)
	}

	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 部分平仓成功，平仓数量: %.4f (%.0f%%)", closeQuantity, dec.ClosePercentage)

	// 平仓后，如果还有剩余持仓，可能需要更新止损止盈的数量
	remainingQuantity := quantity - closeQuantity
	if remainingQuantity > 0 {
		log.Printf("  ℹ️ 剩余持仓数量: %.4f", remainingQuantity)
	}

	// ✅ 自动止盈状态更新：只有在真实执行成功后才推进TP阶段
	// 由于 partial_close 决策本身不携带 side，这里复用已解析出的 side。
	if dec.DecisionSource == "auto_take_profit" {
		posKey := dec.Symbol + "_" + side
		if at.autoDecisionState.TP == nil {
			at.autoDecisionState.TP = make(map[string]*decision.AutoTPState)
		}
		st, ok := at.autoDecisionState.TP[posKey]
		if !ok || st == nil {
			st = &decision.AutoTPState{}
			at.autoDecisionState.TP[posKey] = st
		}
		// 阶段推进：每次 auto_take_profit 的 partial_close 成功执行就推进一档
		if st.Stage < 0 {
			st.Stage = 0
		}
		st.Stage++
		st.LastActionTimeMs = time.Now().UnixMilli()
	}

	return nil
}

// GetID 获取trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetName 获取trader名称
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel 获取AI模型
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetDecisionLogger 获取决策日志记录器
func (at *AutoTrader) GetDecisionLogger() *logger.DecisionLogger {
	return at.decisionLogger
}

// GetAssumedTakerFeeRate 返回“估算手续费率”（用于订单级别复盘展示的估算，不影响真实下单）
func (at *AutoTrader) GetAssumedTakerFeeRate() float64 {
	return at.config.AssumedTakerFeeRate
}

// GetOrders 获取某币种订单历史（带缓存；用于 API 展示）
func (at *AutoTrader) GetOrders(symbol string, startTimeMs, endTimeMs int64, limit int) ([]OrderRecord, error) {
	// key 维度：symbol + time range + limit
	key := fmt.Sprintf("%s|%d|%d|%d", strings.ToUpper(strings.TrimSpace(symbol)), startTimeMs, endTimeMs, limit)

	at.cacheMutex.RLock()
	if ts, ok := at.ordersCacheTime[key]; ok && time.Since(ts) < at.cacheTTL {
		if cached, ok2 := at.ordersCache[key]; ok2 {
			at.cacheMutex.RUnlock()
			return cached, nil
		}
	}
	at.cacheMutex.RUnlock()

	orders, err := at.trader.ListOrders(symbol, startTimeMs, endTimeMs, limit)
	if err != nil {
		// best-effort：如果有旧缓存就返回旧缓存（避免前端频繁报错闪烁）
		at.cacheMutex.RLock()
		cached, ok := at.ordersCache[key]
		at.cacheMutex.RUnlock()
		if ok {
			return cached, nil
		}
		return nil, err
	}

	at.cacheMutex.Lock()
	at.ordersCache[key] = orders
	at.ordersCacheTime[key] = time.Now()
	at.cacheMutex.Unlock()

	return orders, nil
}

// GetErrorStats 获取错误统计
func (at *AutoTrader) GetErrorStats() *stats.ErrorStats {
	return at.errorStats
}

// SetPromptStrategy 设置Prompt策略（可在运行时切换）
func (at *AutoTrader) SetPromptStrategy(strategy decision.PromptStrategy) {
	at.promptStrategy = strategy
}

// GetStatus 获取系统状态（用于API）
func (at *AutoTrader) GetStatus() map[string]interface{} {
	aiProvider := "DeepSeek"
	if at.config.UseQwen {
		aiProvider = "Qwen"
	}

	result := map[string]interface{}{
		"trader_id":       at.id,
		"trader_name":     at.name,
		"ai_model":        at.aiModel,
		"exchange":        at.exchange,
		"is_running":      at.isRunning,
		"is_paused":       at.isPaused,
		"start_time":      at.startTime.Format(time.RFC3339),
		"runtime_minutes": int(time.Since(at.startTime).Minutes()),
		"call_count":      at.callCount,
		"initial_balance": at.initialBalance,
		"scan_interval":   at.config.ScanInterval.String(),
		"stop_until":      at.stopUntil.Format(time.RFC3339),
		"last_reset_time": at.lastResetTime.Format(time.RFC3339),
		"ai_provider":     aiProvider,
	}

	// 添加错误统计摘要
	if at.errorStats != nil {
		errorSummary := at.errorStats.GetSummary()
		result["error_stats"] = errorSummary
	}

	return result
}

// GetAccountInfo 获取账户信息（用于API，带缓存）
func (at *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
	// 检查缓存是否有效
	at.cacheMutex.RLock()
	if at.accountInfoCache != nil && time.Since(at.accountInfoCacheTime) < at.cacheTTL {
		result := at.accountInfoCache
		at.cacheMutex.RUnlock()
		return result, nil
	}
	at.cacheMutex.RUnlock()

	// 缓存过期，调用真实API
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %w", err)
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 获取持仓计算总保证金（使用带缓存的内部方法）
	positions, err := at.getPositionsWithCache()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	totalMarginUsed := 0.0
	totalUnrealizedPnL := 0.0
	for _, pos := range positions {
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		totalUnrealizedPnL += unrealizedPnl

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed
	}

	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	result := map[string]interface{}{
		// 核心字段
		"total_equity":      totalEquity,           // 账户净值 = wallet + unrealized
		"wallet_balance":    totalWalletBalance,    // 钱包余额（不含未实现盈亏）
		"unrealized_profit": totalUnrealizedProfit, // 未实现盈亏（从API）
		"available_balance": availableBalance,      // 可用余额

		// 盈亏统计
		"total_pnl":            totalPnL,           // 总盈亏 = equity - initial
		"total_pnl_pct":        totalPnLPct,        // 总盈亏百分比
		"total_unrealized_pnl": totalUnrealizedPnL, // 未实现盈亏（从持仓计算）
		"initial_balance":      at.initialBalance,  // 初始余额
		"daily_pnl":            at.dailyPnL,        // 日盈亏

		// 持仓信息
		"position_count":  len(positions),  // 持仓数量
		"margin_used":     totalMarginUsed, // 保证金占用
		"margin_used_pct": marginUsedPct,   // 保证金使用率
	}

	// 更新缓存
	at.cacheMutex.Lock()
	at.accountInfoCache = result
	at.accountInfoCacheTime = time.Now()
	at.cacheMutex.Unlock()

	return result, nil
}

// getPositionsWithCache 获取原始持仓数据（带缓存，内部使用）
func (at *AutoTrader) getPositionsWithCache() ([]map[string]interface{}, error) {
	// 检查缓存是否有效
	at.cacheMutex.RLock()
	if at.positionsCache != nil && time.Since(at.positionsCacheTime) < at.cacheTTL {
		result := at.positionsCache
		at.cacheMutex.RUnlock()
		return result, nil
	}
	at.cacheMutex.RUnlock()

	// 缓存过期，调用真实API
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, err
	}

	// 更新缓存
	at.cacheMutex.Lock()
	at.positionsCache = positions
	at.positionsCacheTime = time.Now()
	at.cacheMutex.Unlock()

	return positions, nil
}

// GetPositions 获取持仓列表（用于API，带缓存）
func (at *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := at.getPositionsWithCache()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		toFloat := func(v interface{}) (float64, bool) {
			switch t := v.(type) {
			case float64:
				return t, true
			case string:
				f, err := strconv.ParseFloat(t, 64)
				if err != nil {
					return 0, false
				}
				return f, true
			default:
				return 0, false
			}
		}

		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		entryPrice, _ := toFloat(pos["entryPrice"])
		markPrice, _ := toFloat(pos["markPrice"])
		if markPrice == 0 {
			markPrice = entryPrice
		}
		quantity, _ := toFloat(pos["positionAmt"])
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl, _ := toFloat(pos["unRealizedProfit"])
		liquidationPrice, _ := toFloat(pos["liquidationPrice"])

		leverage := 10
		if levf, ok := toFloat(pos["leverage"]); ok {
			leverage = int(levf)
		}

		pnlPct := 0.0
		if side == "long" {
			pnlPct = ((markPrice - entryPrice) / entryPrice) * 100
		} else {
			pnlPct = ((entryPrice - markPrice) / entryPrice) * 100
		}

		marginUsed := (quantity * markPrice) / float64(leverage)

		result = append(result, map[string]interface{}{
			"symbol":             symbol,
			"side":               side,
			"entry_price":        entryPrice,
			"mark_price":         markPrice,
			"quantity":           quantity,
			"leverage":           leverage,
			"unrealized_pnl":     unrealizedPnl,
			"unrealized_pnl_pct": pnlPct,
			"liquidation_price":  liquidationPrice,
			"margin_used":        marginUsed,
		})
	}

	return result, nil
}

// CloseAllPositions 平掉所有持仓（手动触发）
func (at *AutoTrader) CloseAllPositions() error {
	log.Printf("🔴 [%s] 手动触发：平掉所有持仓", at.name)

	// 获取当前所有持仓
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	if len(positions) == 0 {
		log.Printf("✓ [%s] 无持仓需要平仓", at.name)
		return nil
	}

	// 逐个平仓
	successCount := 0
	failCount := 0
	var errors []string

	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)

		// best-effort exit price snapshot (before close)
		exitPrice, _ := at.trader.GetMarketPrice(symbol)
		if exitPrice <= 0 {
			// fallback: try markPrice from position object
			if mp, ok := pos["markPrice"].(float64); ok && mp > 0 {
				exitPrice = mp
			}
		}

		var err error
		if side == "long" {
			_, err = at.trader.CloseLong(symbol, 0) // 0 表示全部平仓
		} else if side == "short" {
			_, err = at.trader.CloseShort(symbol, 0)
		}

		if err != nil {
			failCount++
			errMsg := fmt.Sprintf("%s %s平仓失败: %v", symbol, side, err)
			errors = append(errors, errMsg)
			log.Printf("❌ [%s] %s", at.name, errMsg)
		} else {
			successCount++
			log.Printf("✓ [%s] %s %s 平仓成功", at.name, symbol, side)

			// ✅ 手动平仓也写入TradeRecord（best-effort）
			if at.tradeMemory != nil && exitPrice > 0 {
				_, _ = at.tradeMemory.ManualClose(symbol, side, exitPrice)
			}
		}

		// 避免请求过快
		time.Sleep(500 * time.Millisecond)
	}

	log.Printf("📊 [%s] 平仓完成：成功 %d，失败 %d", at.name, successCount, failCount)

	if len(errors) > 0 {
		return fmt.Errorf("部分平仓失败: %s", strings.Join(errors, "; "))
	}

	return nil
}

// sortDecisionsByPriority 对决策排序：先平仓，再开仓，最后hold/wait
// 这样可以避免换仓时仓位叠加超限
func sortDecisionsByPriority(decisions []decision.Decision) []decision.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// 定义优先级
	getActionPriority := func(action string) int {
		switch action {
		case decision.ActionCloseLong, decision.ActionCloseShort, decision.ActionPartialClose:
			return 1 // 最高优先级：先平仓
		case decision.ActionOpenLong, decision.ActionOpenShort:
			return 2 // 次优先级：后开仓
		case decision.ActionHold, decision.ActionWait:
			return 3 // 最低优先级：观望
		default:
			return 999 // 未知动作放最后
		}
	}

	// 复制决策列表
	sorted := make([]decision.Decision, len(decisions))
	copy(sorted, decisions)

	// 按优先级排序
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

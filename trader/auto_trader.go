package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"nofx/decision"
	"nofx/logger"
	"nofx/mcp"
	"nofx/memory"
	"nofx/pool"
	"nofx/stats"
	"os"
	"path/filepath"
	"sort"
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

	// 模型名称（可选，为空则使用默认）
	DeepSeekModel string
	QwenModel     string

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
	BTCETHLeverage                   int     // BTC和ETH的杠杆倍数
	AltcoinLeverage                  int     // 山寨币的杠杆倍数
	AltcoinMaxPositionEquityMultiple float64 // 山寨币单币名义仓位上限：账户净值倍数
	LeverageClip                     decision.LeverageClipConfig

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

	// 保证金预检配置
	MarginValidation decision.MarginValidationConfig

	// 止损最小距离配置（零值时使用默认）
	StopLossDistance decision.StopLossDistanceConfig

	// 自动止盈配置（零值时使用默认）
	AutoTakeProfit decision.AutoTakeProfitConfig

	// 独立仓位风险扫描配置（默认关闭）
	PositionRisk PositionRiskConfig

	// LLM 平仓前的最低持仓时间（分钟，0=不限制）
	MinHoldMinutes int

	// 候选币种 OI 价值过滤门槛（百万USD，默认50M）
	MinOIValueMillions float64

	// 接续运行：启动时自动恢复交易（不需要手动点击开始）
	AutoResume bool

	// 高峰时段暂停配置
	PeakHourPause *PeakHourPauseConfig
}

// PeakHourPauseConfig 高峰时段暂停配置（per-trader）
type PeakHourPauseConfig struct {
	Enabled bool
	Start   string // "HH:MM" 北京时间，默认 "09:00"
	End     string // "HH:MM" 北京时间，默认 "18:00"
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
	isPaused              bool                        // 是否暂停（仅停止交易循环，不退出程序）
	startTime             time.Time                   // 系统启动时间
	callCount             int                         // AI调用次数
	promptStrategy        decision.PromptStrategy     // 当前使用的Prompt策略（默认StrategyA）
	positionFirstSeenTime map[string]int64            // 持仓首次出现时间 (symbol_side -> timestamp毫秒)
	observedPositions     map[string]observedPosition // 上次成功读取到的持仓；用于对账交易所自行触发的条件单
	autoDecisionState     decision.AutoDecisionState
	shortTermWatchlist    map[string]*decision.ShortTermWatchItem // StrategyV 专用轻量 watchlist，per-trader 隔离

	// AI 客户端（每个 trader 独立实例）
	aiClient *mcp.Client

	// LLM 调用费用追踪器
	llmCostTracker *LLMCostTracker

	// 高峰时段暂停
	peakHourEnabled  bool   // 是否启用高峰暂停
	peakHourStart    string // "HH:MM" 北京时间
	peakHourEnd      string // "HH:MM" 北京时间
	peakHourOverride bool   // 用户手动恢复（仅当次高峰期生效）
	lastPeakCheck    bool   // 上一次是否在高峰期（用于检测高峰期切换以重置 override）
	peakMu           sync.RWMutex

	// 运行时可热更新配置（线程安全）
	runtimeCfg        *RuntimeConfig
	scanIntervalCh    chan time.Duration // 通知 Run() 循环重置 ticker
	riskConfigCh      chan PositionRiskConfig
	tradeMu           sync.Mutex // AI 与高频风险循环共用的下单串行锁
	autoStateMu       sync.Mutex // 保护 autoDecisionState；决策上下文使用其快照
	riskLogMu         sync.Mutex
	riskLogPath       string
	exchangeAuditPath string

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

	// 初始化 AI 客户端（每个 trader 独立实例，互不干扰）
	var aiClient *mcp.Client
	if config.AIModel == "custom" {
		aiClient = mcp.NewCustomClient(config.CustomAPIURL, config.CustomAPIKey, config.CustomModelName)
		log.Printf("🤖 [%s] 使用自定义AI API: %s (模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
	} else if config.UseQwen || config.AIModel == "qwen" {
		aiClient = mcp.NewQwenClient(config.QwenKey, "", config.QwenModel)
		modelInfo := config.QwenModel
		if modelInfo == "" {
			modelInfo = "qwen3.5-plus (默认)"
		}
		log.Printf("🤖 [%s] 使用阿里云Qwen AI (模型: %s)", config.Name, modelInfo)
	} else {
		aiClient = mcp.NewDeepSeekClient(config.DeepSeekKey, config.DeepSeekModel)
		modelInfo := config.DeepSeekModel
		if modelInfo == "" {
			modelInfo = "deepseek-reasoner (默认)"
		}
		log.Printf("🤖 [%s] 使用DeepSeek AI (模型: %s)", config.Name, modelInfo)
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

	// 从已有日志恢复周期计数，实现重启接续
	resumedCycle := decisionLogger.GetCycleNumber()

	// 初始化错误统计（使用全局实例，按traderID区分）
	errorStats := stats.GetErrorStats(config.ID)

	// 初始化 TradeMemory（best-effort，不影响主流程）
	var tradeMemory *memory.TradeMemory
	if tm, err := memory.NewTradeMemory(config.ID); err != nil {
		log.Printf("⚠️  [%s] 初始化TradeMemory失败: %v", config.Name, err)
	} else {
		tradeMemory = tm
	}

	// auto_resume=true 时启动即交易，否则等待前端手动开启
	startPaused := !config.AutoResume

	return &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		config:                config,
		trader:                trader,
		aiClient:              aiClient,
		decisionLogger:        decisionLogger,
		tradeMemory:           tradeMemory,
		errorStats:            errorStats,
		initialBalance:        config.InitialBalance,
		lastResetTime:         time.Now(),
		dailyStartEquity:      0,           // 第一次获取到净值后再初始化
		peakEquity:            0,           // 第一次获取到净值后再初始化
		stopUntil:             time.Time{}, // 默认无风控暂停
		isRunning:             false,       // Run 调用后才变为 true
		isPaused:              startPaused,
		startTime:             time.Now(),
		callCount:             resumedCycle,
		positionFirstSeenTime: make(map[string]int64),
		observedPositions:     make(map[string]observedPosition),
		autoDecisionState: decision.AutoDecisionState{
			TP: make(map[string]*decision.AutoTPState),
		},
		// 高峰时段暂停
		peakHourEnabled: config.PeakHourPause != nil && config.PeakHourPause.Enabled,
		peakHourStart:   peakHourDefault(config.PeakHourPause, true),
		peakHourEnd:     peakHourDefault(config.PeakHourPause, false),
		// LLM 调用费用追踪（从 logDir 恢复历史累计）
		llmCostTracker: NewLLMCostTracker(logDir),
		// 运行时可热更新配置
		runtimeCfg:        NewRuntimeConfig(config, aiClient),
		scanIntervalCh:    make(chan time.Duration, 1),
		riskConfigCh:      make(chan PositionRiskConfig, 1),
		riskLogPath:       filepath.Join(logDir, "risk_events.jsonl"),
		exchangeAuditPath: filepath.Join(logDir, "exchange_events.jsonl"),
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
	riskCtx, cancelRisk := context.WithCancel(context.Background())
	defer cancelRisk()
	go at.runPositionRiskLoop(riskCtx)

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

// runPositionRiskLoop 独立于 LLM 主循环扫描已有仓位。配置关闭时 timer channel 为 nil，不产生轮询。
func (at *AutoTrader) runPositionRiskLoop(ctx context.Context) {
	cfg := normalizePositionRiskConfig(at.runtimeCfg.Get().PositionRisk)
	var ticker *time.Ticker
	var tickC <-chan time.Time
	apply := func(next PositionRiskConfig) {
		next = normalizePositionRiskConfig(next)
		if ticker != nil {
			ticker.Stop()
			ticker = nil
			tickC = nil
		}
		cfg = next
		if !cfg.Enabled {
			log.Printf("🛡️ [%s] 独立仓位风险循环已关闭", at.name)
			return
		}
		ticker = time.NewTicker(time.Duration(cfg.ScanIntervalSeconds) * time.Second)
		tickC = ticker.C
		log.Printf("🛡️ [%s] 独立仓位风险循环启动: mode=%s interval=%ds", at.name, cfg.Mode, cfg.ScanIntervalSeconds)
	}
	apply(cfg)
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case next := <-at.riskConfigCh:
			apply(next)
		case <-tickC:
			if err := at.runPositionRiskScan(cfg); err != nil {
				log.Printf("⚠️ [%s] 独立仓位风险扫描失败: %v", at.name, err)
			}
		}
	}
}

func (at *AutoTrader) runPositionRiskScan(cfg PositionRiskConfig) error {
	// 扫描、复核和可能的下单视为一个串行事务，避免读取到 AI 正在修改的中间仓位。
	at.tradeMu.Lock()
	defer at.tradeMu.Unlock()

	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取实时持仓失败: %w", err)
	}
	if len(positions) == 0 {
		return nil
	}

	infos := make([]decision.PositionInfo, 0, len(positions))
	for _, raw := range positions {
		if info, ok := fastPositionInfo(raw); ok {
			infos = append(infos, info)
		}
	}
	if len(infos) == 0 {
		return nil
	}

	rc := at.runtimeCfg.Get()
	riskCtx := &decision.Context{
		Positions:           infos,
		AssumedTakerFeeRate: at.config.AssumedTakerFeeRate,
		AssumedSlippageRate: at.config.AssumedSlippageRate,
		AutoTakeProfit:      rc.AutoTakeProfit,
	}

	// 决策层会更新最佳价格；真实状态只在此锁内暴露，AI 上下文使用深拷贝。
	at.autoStateMu.Lock()
	at.reconcileAutoStateLocked(infos)
	riskCtx.AutoState = &at.autoDecisionState
	actions := decision.GenerateHighFrequencyRiskDecisions(riskCtx)
	riskStateSnapshot := cloneAutoDecisionState(at.autoDecisionState)
	riskCtx.AutoState = &riskStateSnapshot
	at.autoStateMu.Unlock()

	if len(actions) == 0 {
		return nil
	}
	for _, action := range actions {
		if cfg.Mode != "live" {
			log.Printf("🧪 [%s] 风险扫描 shadow: %s %s - %s", at.name, action.Symbol, action.Action, action.Reasoning)
			at.appendRiskEvent(cfg, action, nil, nil)
			continue
		}
		record := &logger.DecisionAction{
			Action: action.Action, Symbol: action.Symbol, DecisionSource: action.DecisionSource,
			Reasoning: action.Reasoning, Timestamp: time.Now(),
		}
		execErr := at.executeDecisionWithRecord(riskCtx, &action, record)
		if execErr != nil {
			log.Printf("❌ [%s] 高频风险动作失败: %s %s: %v", at.name, action.Symbol, action.Action, execErr)
			at.appendRiskEvent(cfg, action, record, execErr)
			continue
		}
		log.Printf("✅ [%s] 高频风险动作成功: %s %s", at.name, action.Symbol, action.Action)
		at.appendRiskEvent(cfg, action, record, nil)
	}
	return nil
}

func (at *AutoTrader) appendRiskEvent(cfg PositionRiskConfig, action decision.Decision, record *logger.DecisionAction, execErr error) {
	if at.riskLogPath == "" {
		return
	}
	event := map[string]interface{}{
		"timestamp":             time.Now().Format(time.RFC3339Nano),
		"trader_id":             at.id,
		"mode":                  cfg.Mode,
		"scan_interval_seconds": cfg.ScanIntervalSeconds,
		"symbol":                action.Symbol,
		"action":                action.Action,
		"source":                action.DecisionSource,
		"reasoning":             action.Reasoning,
		"close_percentage":      action.ClosePercentage,
		"new_stop_loss":         action.NewStopLoss,
		"expected_stage":        action.ExpectedStage,
		"executed":              cfg.Mode == "live",
		"success":               execErr == nil,
	}
	if record != nil {
		event["quantity"] = record.Quantity
		event["price"] = record.Price
		event["order_id"] = record.OrderID
		if record.Error != "" {
			event["warning"] = record.Error
		}
	}
	if execErr != nil {
		event["error"] = execErr.Error()
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	at.riskLogMu.Lock()
	defer at.riskLogMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(at.riskLogPath), 0o755); err != nil {
		log.Printf("⚠️ 创建风险日志目录失败: %v", err)
		return
	}
	f, err := os.OpenFile(at.riskLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("⚠️ 打开风险日志失败: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		log.Printf("⚠️ 写入风险日志失败: %v", err)
	}
}

func fastPositionInfo(raw map[string]interface{}) (decision.PositionInfo, bool) {
	symbol, _ := raw["symbol"].(string)
	side, _ := raw["side"].(string)
	entry := floatFromInterface(raw["entryPrice"])
	mark := floatFromInterface(raw["markPrice"])
	qty := absFloatFromMap(raw, "positionAmt")
	if symbol == "" || (side != "long" && side != "short") || entry <= 0 || mark <= 0 || qty <= 0 {
		return decision.PositionInfo{}, false
	}
	leverage := int(floatFromInterface(raw["leverage"]))
	if leverage <= 0 {
		leverage = 1
	}
	pnlPct := (mark - entry) / entry * 100
	if side == "short" {
		pnlPct = -pnlPct
	}
	return decision.PositionInfo{
		Symbol: symbol, Side: side, EntryPrice: entry, MarkPrice: mark, Quantity: qty,
		Leverage: leverage, UnrealizedPnL: floatFromInterface(raw["unRealizedProfit"]),
		UnrealizedPnLPct: pnlPct, LiquidationPrice: floatFromInterface(raw["liquidationPrice"]),
	}, true
}

// observedPosition is a local, non-persistent reconciliation checkpoint.  It is
// intentionally only populated after a successful exchange position read: an API
// failure must never be mistaken for a position closed by the exchange.
type observedPosition struct {
	Info        decision.PositionInfo
	SeenAt      time.Time
	InitialStop float64
	CurrentStop float64
	TakeProfit  float64
}

type exchangeCloseAuditEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	Symbol         string    `json:"symbol"`
	Side           string    `json:"side"`
	Action         string    `json:"action"`
	DecisionSource string    `json:"decision_source"`
	Reasoning      string    `json:"reasoning"`
	Quantity       float64   `json:"quantity"`
	ExitPrice      float64   `json:"exit_price"`
	OrderID        int64     `json:"order_id,omitempty"`
	ObservedAt     time.Time `json:"observed_at"`
}

// auditExchangeClosedPositions fills the blind spot where Binance executes a
// reduce-only conditional order itself.  Such a fill bypasses executeClose...,
// so it otherwise would be absent from both decision_logs and trades.jsonl.
func (at *AutoTrader) auditExchangeClosedPositions(missing []observedPosition) {
	for _, prev := range missing {
		if prev.Info.Symbol == "" || prev.Info.Side == "" {
			continue
		}
		order, exitPrice := at.findExchangeExitOrder(prev)
		source := classifyExchangeClose(prev, exitPrice)
		if exitPrice <= 0 {
			// A condition can be executed and then disappear from order history API
			// briefly; retain a conservative fallback so the audit is still complete.
			exitPrice = prev.Info.MarkPrice
		}
		action := decision.ActionCloseLong
		if strings.EqualFold(prev.Info.Side, "short") {
			action = decision.ActionCloseShort
		}
		reason := fmt.Sprintf("交易所持仓对账：上次观察到的%s仓已消失，未经过本地平仓执行路径", prev.Info.Side)
		if prev.CurrentStop > 0 || prev.TakeProfit > 0 {
			reason += fmt.Sprintf("；已知保护单 SL=%.8f TP=%.8f", prev.CurrentStop, prev.TakeProfit)
		}
		if order.OrderID != 0 {
			reason += fmt.Sprintf("；匹配交易所 reduce-only 成交订单 #%d", order.OrderID)
		}

		event := exchangeCloseAuditEvent{
			Timestamp: time.Now(), Symbol: prev.Info.Symbol, Side: prev.Info.Side,
			Action: action, DecisionSource: source, Reasoning: reason,
			Quantity: prev.Info.Quantity, ExitPrice: exitPrice, OrderID: order.OrderID,
			ObservedAt: prev.SeenAt,
		}
		at.appendExchangeCloseAudit(event)
		log.Printf("🧾 [%s] 补记交易所平仓: %s %s @ %.8f (%s)", at.name, event.Symbol, event.Action, exitPrice, source)

		if at.tradeMemory != nil && exitPrice > 0 {
			dec := &decision.Decision{Symbol: prev.Info.Symbol, Action: action, Reasoning: reason, DecisionSource: source}
			if _, err := at.tradeMemory.OnCloseSuccess(nil, dec, exitPrice, source); err != nil {
				log.Printf("⚠️ [%s] 补写 trades.jsonl 失败: %v", at.name, err)
			}
		}
	}
}

func (at *AutoTrader) findExchangeExitOrder(prev observedPosition) (OrderRecord, float64) {
	start := prev.SeenAt.Add(-5 * time.Second).UnixMilli()
	orders, err := at.trader.ListOrders(prev.Info.Symbol, start, time.Now().UnixMilli(), 20)
	if err != nil {
		log.Printf("⚠️ [%s] 查询 %s 条件单成交记录失败，使用持仓快照补记: %v", at.name, prev.Info.Symbol, err)
		return OrderRecord{}, 0
	}
	var best OrderRecord
	for _, order := range orders {
		if !order.ReduceOnly || !strings.EqualFold(order.Status, "FILLED") || order.ExecutedQty <= 0 {
			continue
		}
		if order.UpdatedAt.Before(prev.SeenAt.Add(-5 * time.Second)) {
			continue
		}
		if best.OrderID == 0 || order.UpdatedAt.After(best.UpdatedAt) {
			best = order
		}
	}
	if best.OrderID == 0 {
		return best, 0
	}
	if best.AvgPrice > 0 {
		return best, best.AvgPrice
	}
	if best.ExecutedQty > 0 && best.CumQuote > 0 {
		return best, best.CumQuote / best.ExecutedQty
	}
	return best, best.Price
}

func classifyExchangeClose(prev observedPosition, exitPrice float64) string {
	if exitPrice <= 0 {
		return "exchange_position_disappeared"
	}
	// Permit a small execution/slippage band around the trigger.  A match is
	// audit classification only; it never affects live trading behaviour.
	const triggerBand = 0.0035
	if strings.EqualFold(prev.Info.Side, "long") {
		if prev.CurrentStop > 0 && exitPrice <= prev.CurrentStop*(1+triggerBand) {
			return "exchange_conditional_stop_loss"
		}
		if prev.TakeProfit > 0 && exitPrice >= prev.TakeProfit*(1-triggerBand) {
			return "exchange_conditional_take_profit"
		}
	} else {
		if prev.CurrentStop > 0 && exitPrice >= prev.CurrentStop*(1-triggerBand) {
			return "exchange_conditional_stop_loss"
		}
		if prev.TakeProfit > 0 && exitPrice <= prev.TakeProfit*(1+triggerBand) {
			return "exchange_conditional_take_profit"
		}
	}
	return "exchange_conditional_order_triggered"
}

func (at *AutoTrader) appendExchangeCloseAudit(event exchangeCloseAuditEvent) {
	if at.exchangeAuditPath == "" {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	at.riskLogMu.Lock()
	defer at.riskLogMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(at.exchangeAuditPath), 0o755); err != nil {
		log.Printf("⚠️ 创建交易所平仓审计目录失败: %v", err)
		return
	}
	f, err := os.OpenFile(at.exchangeAuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("⚠️ 打开交易所平仓审计日志失败: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		log.Printf("⚠️ 写入交易所平仓审计日志失败: %v", err)
	}
}

func (at *AutoTrader) reconcileAutoStateLocked(infos []decision.PositionInfo) {
	if at.autoDecisionState.TP == nil {
		at.autoDecisionState.TP = make(map[string]*decision.AutoTPState)
	}
	current := make(map[string]bool, len(infos))
	for i := range infos {
		pos := &infos[i]
		key := pos.Symbol + "_" + strings.ToLower(pos.Side)
		current[key] = true
		st := at.autoDecisionState.TP[key]
		if st == nil {
			st = &decision.AutoTPState{
				BaselineEntry: pos.EntryPrice, BaselineQty: pos.Quantity, BestPrice: pos.MarkPrice,
			}
			at.autoDecisionState.TP[key] = st
		}
		pos.EntryStopLoss = st.InitialStop
		pos.EntryTakeProfit = st.CurrentTakeProfit
	}
	for key := range at.autoDecisionState.TP {
		if !current[key] {
			delete(at.autoDecisionState.TP, key)
		}
	}
}

func cloneAutoDecisionState(src decision.AutoDecisionState) decision.AutoDecisionState {
	dst := decision.AutoDecisionState{TP: make(map[string]*decision.AutoTPState, len(src.TP))}
	for key, st := range src.TP {
		if st == nil {
			continue
		}
		copyState := *st
		dst.TP[key] = &copyState
	}
	return dst
}

func (at *AutoTrader) mergeAutoStateObservations(ctx *decision.Context) {
	if ctx == nil || ctx.AutoState == nil {
		return
	}
	sides := make(map[string]string, len(ctx.Positions))
	for _, pos := range ctx.Positions {
		sides[pos.Symbol+"_"+strings.ToLower(pos.Side)] = strings.ToLower(pos.Side)
	}
	at.autoStateMu.Lock()
	defer at.autoStateMu.Unlock()
	for key, observed := range ctx.AutoState.TP {
		if observed == nil {
			continue
		}
		current := at.autoDecisionState.TP[key]
		if current == nil {
			copyState := *observed
			at.autoDecisionState.TP[key] = &copyState
			continue
		}
		side := sides[key]
		if current.BestPrice <= 0 || (side == "long" && observed.BestPrice > current.BestPrice) ||
			(side == "short" && observed.BestPrice > 0 && observed.BestPrice < current.BestPrice) {
			current.BestPrice = observed.BestPrice
		}
	}
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

// ===== 高峰时段暂停 =====

func peakHourDefault(cfg *PeakHourPauseConfig, isStart bool) string {
	if cfg != nil {
		if isStart && cfg.Start != "" {
			return cfg.Start
		}
		if !isStart && cfg.End != "" {
			return cfg.End
		}
	}
	if isStart {
		return "09:00"
	}
	return "18:00"
}

var cstZone = time.FixedZone("CST", 8*3600)

// isInPeakHours 判断当前北京时间是否在高峰时段
func (at *AutoTrader) isInPeakHours() bool {
	if !at.peakHourEnabled {
		return false
	}
	now := time.Now().In(cstZone)
	nowMin := now.Hour()*60 + now.Minute()

	startMin := parseHHMM(at.peakHourStart)
	endMin := parseHHMM(at.peakHourEnd)
	if startMin < 0 || endMin < 0 {
		return false
	}

	if startMin < endMin {
		return nowMin >= startMin && nowMin < endMin
	}
	// 跨午夜（如 22:00~06:00）
	return nowMin >= startMin || nowMin < endMin
}

func parseHHMM(s string) int {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return -1
	}
	return h*60 + m
}

// shouldSkipForPeakHour 判断本周期是否应跳过 LLM 调用。
// 返回 true 表示跳过（高峰暂停生效且无持仓且无 override）。
func (at *AutoTrader) shouldSkipForPeakHour(positionCount int) bool {
	inPeak := at.isInPeakHours()

	at.peakMu.Lock()
	defer at.peakMu.Unlock()

	// 高峰期结束后重新进入 → 重置 override
	if !inPeak && at.lastPeakCheck {
		at.peakHourOverride = false
	}
	at.lastPeakCheck = inPeak

	if !inPeak {
		return false
	}
	if at.peakHourOverride {
		return false
	}
	if positionCount > 0 {
		return false
	}
	return true
}

// SetPeakHourOverride 手动切换高峰时段 override（用户恢复/重新暂停）
func (at *AutoTrader) SetPeakHourOverride(override bool) {
	at.peakMu.Lock()
	defer at.peakMu.Unlock()
	at.peakHourOverride = override
	action := "恢复运行"
	if !override {
		action = "重新暂停"
	}
	log.Printf("🔔 [%s] 高峰时段手动%s", at.name, action)
}

// SetPeakHourEnabled 动态开启/关闭高峰时段暂停
func (at *AutoTrader) SetPeakHourEnabled(enabled bool) {
	at.peakMu.Lock()
	defer at.peakMu.Unlock()
	at.peakHourEnabled = enabled
	if !enabled {
		at.peakHourOverride = false
	}
	log.Printf("🔔 [%s] 高峰时段暂停: %v", at.name, enabled)
}

// GetPeakHourStatus 返回高峰时段暂停状态（供 API/前端）
func (at *AutoTrader) GetPeakHourStatus() map[string]interface{} {
	inPeak := at.isInPeakHours()
	at.peakMu.RLock()
	override := at.peakHourOverride
	enabled := at.peakHourEnabled
	at.peakMu.RUnlock()

	paused := enabled && inPeak && !override
	return map[string]interface{}{
		"enabled":         enabled,
		"in_peak_hours":   inPeak,
		"paused":          paused,
		"override_active": override,
		"peak_start":      at.peakHourStart,
		"peak_end":        at.peakHourEnd,
	}
}

// GetRuntimeConfig 返回当前运行时配置快照（供 API 层调用）
func (at *AutoTrader) GetRuntimeConfig() RuntimeConfigSnapshot {
	snap := at.runtimeCfg.Get()
	at.peakMu.RLock()
	snap.PeakHourPause = PeakHourPauseSnapshot{
		Enabled: at.peakHourEnabled,
		Start:   at.peakHourStart,
		End:     at.peakHourEnd,
	}
	at.peakMu.RUnlock()
	return snap
}

// UpdateRuntimeConfig 部分更新运行时配置（供 API 层调用）
func (at *AutoTrader) UpdateRuntimeConfig(patch RuntimeConfigPatch) {
	changed, newInterval, riskChanged, riskCfg := at.runtimeCfg.Update(patch)
	if changed {
		// 非阻塞发送，避免 Run() 循环未就绪时死锁
		select {
		case at.scanIntervalCh <- newInterval:
		default:
		}
	}
	if riskChanged {
		select {
		case at.riskConfigCh <- riskCfg:
		default:
			// 保留最新配置，避免旧的未消费通知覆盖新值。
			select {
			case <-at.riskConfigCh:
			default:
			}
			select {
			case at.riskConfigCh <- riskCfg:
			default:
			}
		}
	}

	// 高峰时段暂停配置（状态存在 AutoTrader 上，不在 RuntimeConfig 中）
	if p := patch.PeakHourPause; p != nil {
		at.peakMu.Lock()
		if p.Enabled != nil {
			at.peakHourEnabled = *p.Enabled
			if !*p.Enabled {
				at.peakHourOverride = false
			}
			log.Printf("🔧 运行时配置更新: PeakHourPause.Enabled → %v", *p.Enabled)
		}
		if p.Start != nil && *p.Start != "" {
			log.Printf("🔧 运行时配置更新: PeakHourPause.Start %s → %s", at.peakHourStart, *p.Start)
			at.peakHourStart = *p.Start
		}
		if p.End != nil && *p.End != "" {
			log.Printf("🔧 运行时配置更新: PeakHourPause.End %s → %s", at.peakHourEnd, *p.End)
			at.peakHourEnd = *p.End
		}
		at.peakMu.Unlock()
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

	// 3.6 高峰时段暂停：无持仓时跳过 LLM 调用（节省 API 费用）
	if at.shouldSkipForPeakHour(ctx.Account.PositionCount) {
		now := time.Now().In(cstZone)
		log.Printf("💤 [%s] 高峰时段暂停（%s~%s 北京时间），当前 %s，无持仓，跳过 LLM 调用",
			at.name, at.peakHourStart, at.peakHourEnd, now.Format("15:04"))
		record.Success = true
		record.ErrorMessage = fmt.Sprintf("高峰时段暂停（%s~%s），无持仓，跳过 LLM", at.peakHourStart, at.peakHourEnd)
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
	at.mergeAutoStateObservations(ctx)

	// 记录 LLM 调用费用（即使决策解析失败，token 已消耗）
	if engeinDecision != nil && engeinDecision.LLMUsage != nil {
		at.llmCostTracker.RecordCall("decision", &mcp.CallResult{
			Model: at.aiClient.GetModel(),
			Usage: *engeinDecision.LLMUsage,
		})
		if engeinDecision.LLMCostUSDT > 0 {
			log.Printf("💰 本次 LLM 费用: $%.6f USDT | 累计: $%.4f USDT", engeinDecision.LLMCostUSDT, at.llmCostTracker.GetTotalCost())
		}
	}

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

	at.updateShortTermWatchlist(engeinDecision.Decisions, ctx)

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
			Reasoning:      d.Reasoning,
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

		at.tradeMu.Lock()
		execErr := at.executeDecisionWithRecord(ctx, &d, &actionRecord)
		at.tradeMu.Unlock()
		if execErr != nil {
			log.Printf("❌ 执行决策失败 (%s %s): %v", d.Symbol, d.Action, execErr)
			actionRecord.Error = execErr.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s 失败: %v", d.Symbol, d.Action, execErr))
			// 记录交易执行错误
			errType := stats.ClassifyTradeError(execErr.Error())
			at.errorStats.RecordError(errType, execErr.Error(), d.Symbol, at.callCount)
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
	at.autoStateMu.Lock()

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
				BestPrice:        mp,
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
				st.BestPrice = mp
				st.RealizedNetPnL = 0
				st.TrailingActive = false
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

		// 注入入场论据到 PositionInfo，供 prompt 构建时使用
		for i := range positionInfos {
			ep := at.tradeMemory.GetEpisodeSnapshot(positionInfos[i].Symbol, positionInfos[i].Side)
			if ep != nil {
				positionInfos[i].EntryReasoning = ep.EntryReasoning
				positionInfos[i].EntryStopLoss = ep.StopLoss
				positionInfos[i].EntryTakeProfit = ep.TakeProfit
				positionInfos[i].EntryConfidence = ep.EntryConfidence
				posKey := positionInfos[i].Symbol + "_" + strings.ToLower(positionInfos[i].Side)
				if st := at.autoDecisionState.TP[posKey]; st != nil {
					if st.InitialStop <= 0 {
						st.InitialStop = ep.StopLoss
					}
					if st.CurrentStop <= 0 {
						st.CurrentStop = ep.StopLoss
					}
					if st.CurrentTakeProfit <= 0 {
						st.CurrentTakeProfit = ep.TakeProfit
					}
				}
			}
		}
	}

	// 对账交易所自行触发的条件单。必须在清理自动状态前先保存上一份
	// 保护单快照，才能区分 stop-loss / take-profit；审计写入在解锁后进行，
	// 避免网络查询阻塞仓位状态锁。
	missingObserved := make([]observedPosition, 0)
	if at.observedPositions == nil {
		at.observedPositions = make(map[string]observedPosition)
	}
	for key, observed := range at.observedPositions {
		if !currentPositionKeys[key] {
			missingObserved = append(missingObserved, observed)
			delete(at.observedPositions, key)
		}
	}
	for _, pos := range positionInfos {
		key := pos.Symbol + "_" + strings.ToLower(pos.Side)
		observed := observedPosition{Info: pos, SeenAt: time.Now()}
		if st := at.autoDecisionState.TP[key]; st != nil {
			observed.InitialStop = st.InitialStop
			observed.CurrentStop = st.CurrentStop
			observed.TakeProfit = st.CurrentTakeProfit
		}
		at.observedPositions[key] = observed
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
	autoStateSnapshot := cloneAutoDecisionState(at.autoDecisionState)
	at.autoStateMu.Unlock()
	// 交易所条件单不会经过本地 executeClose...；在这里补写审计事件和
	// TradeMemory 的 trades.jsonl。它不增加 AI cycle，也不会触发新决策。
	at.auditExchangeClosedPositions(missingObserved)

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

	log.Printf("📋 合并币种池: AI500前%d + OI_Top10 = 总计%d个候选币种",
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
	performance, err := at.decisionLogger.AnalyzePerformance(100, 100)
	if err != nil {
		log.Printf("⚠️  分析历史表现失败: %v", err)
		// 不影响主流程，继续执行（但设置performance为nil以避免传递错误数据）
		performance = nil
	}

	// 5.5 提取近期系统自动操作记录和最近交易结果（供 prompt 注入）
	var recentAutoEvents []decision.AutoEventSummary
	var recentTrades []logger.TradeOutcome
	if performance != nil {
		if len(performance.RecentTrades) > 5 {
			recentTrades = performance.RecentTrades[:5]
		} else {
			recentTrades = performance.RecentTrades
		}
	}
	// 从最近决策日志中提取自动操作事件
	recentRecords, _ := at.decisionLogger.GetLatestRecords(10)
	for _, rec := range recentRecords {
		for _, act := range rec.Decisions {
			if !act.Success {
				continue
			}
			if act.DecisionSource != "auto_stop_loss" && act.DecisionSource != "auto_take_profit" && act.DecisionSource != "auto_trailing_stop" {
				continue
			}
			recentAutoEvents = append(recentAutoEvents, decision.AutoEventSummary{
				Time:      act.Timestamp.Format("15:04"),
				Symbol:    act.Symbol,
				Action:    act.Action,
				Source:    act.DecisionSource,
				Reasoning: act.Reasoning,
				Price:     act.Price,
			})
		}
	}
	// 只保留最近 10 条自动事件
	if len(recentAutoEvents) > 10 {
		recentAutoEvents = recentAutoEvents[len(recentAutoEvents)-10:]
	}

	// 5.6 更新全局峰值净值并计算回撤
	var peakEquity, drawdownFromPeakPct float64
	if statsCache := at.decisionLogger.GetTradeStatsCache(); statsCache != nil && totalEquity > 0 {
		peakEquity, drawdownFromPeakPct = statsCache.UpdatePeakEquity(totalEquity, time.Now().Format("2006-01-02 15:04:05"))
		_ = statsCache.Save()
	}

	// 6. 构建上下文（从运行时配置读取可热更新的参数）
	rcSnap := at.runtimeCfg.Get()
	ctx := &decision.Context{
		CurrentTime:                      time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes:                   int(time.Since(at.startTime).Minutes()),
		CallCount:                        at.callCount,
		BTCETHLeverage:                   rcSnap.BTCETHLeverage,                   // 从运行时配置读取
		AltcoinLeverage:                  rcSnap.AltcoinLeverage,                  // 从运行时配置读取
		AltcoinMaxPositionEquityMultiple: rcSnap.AltcoinMaxPositionEquityMultiple, // 从运行时配置读取
		LeverageClip:                     rcSnap.LeverageClip,                     // 从运行时配置读取
		MarginValidation:                 rcSnap.MarginValidation,                 // 从运行时配置读取
		Account: decision.AccountInfo{
			TotalEquity:         totalEquity,
			AvailableBalance:    availableBalance,
			TotalPnL:            totalPnL,
			TotalPnLPct:         totalPnLPct,
			MarginUsed:          totalMarginUsed,
			MarginUsedPct:       marginUsedPct,
			PositionCount:       len(positionInfos),
			PeakEquity:          peakEquity,
			DrawdownFromPeakPct: drawdownFromPeakPct,
		},
		Positions:           positionInfos,
		CandidateCoins:      candidateCoins,
		Performance:         performance,
		RecentAutoEvents:    recentAutoEvents,
		RecentTrades:        recentTrades,
		AssumedTakerFeeRate: at.config.AssumedTakerFeeRate,
		AssumedSlippageRate: at.config.AssumedSlippageRate,
		StopLossDistance:    rcSnap.StopLossDistance, // 从运行时配置读取
		AutoTakeProfit:      rcSnap.AutoTakeProfit,   // 从运行时配置读取
		ScanIntervalMin:     rcSnap.ScanIntervalMin,  // 用于 prompt 动态时间
		MinHoldMinutes:      rcSnap.MinHoldMinutes,   // LLM 最低持仓时间
		MinOIValueMillions:  rcSnap.MinOIValueMil,    // OI 价值过滤门槛
		ShortTermWatchlist:  at.snapshotShortTermWatchlist(),
		EnableRecording:     at.config.EnableRecording,
		TraderID:            at.config.ID,
		AutoState:           &autoStateSnapshot,
		AI:                  at.aiClient,
	}

	// 注入可插拔策略，默认使用 StrategyA
	if at.promptStrategy == nil {
		ctx.PromptStrategy = decision.StrategyA{}
	} else {
		ctx.PromptStrategy = at.promptStrategy
	}

	return ctx, nil
}

func (at *AutoTrader) isStrategyV() bool {
	return at != nil && at.promptStrategy != nil && at.promptStrategy.Name() == "V"
}

func (at *AutoTrader) snapshotShortTermWatchlist() []decision.ShortTermWatchItem {
	if !at.isStrategyV() || len(at.shortTermWatchlist) == 0 {
		return nil
	}
	items := make([]decision.ShortTermWatchItem, 0, len(at.shortTermWatchlist))
	for _, item := range at.shortTermWatchlist {
		if item == nil || item.Symbol == "" {
			continue
		}
		// 过期项不注入 prompt；清理由 updateShortTermWatchlist 做。
		if item.ExpiresAfterCycle > 0 && at.callCount > item.ExpiresAfterCycle {
			continue
		}
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority > items[j].Priority
		}
		return items[i].FirstSeenCycle > items[j].FirstSeenCycle
	})
	if len(items) > 3 {
		items = items[:3]
	}
	return items
}

func (at *AutoTrader) updateShortTermWatchlist(decisions []decision.Decision, ctx *decision.Context) {
	if !at.isStrategyV() {
		return
	}
	if at.shortTermWatchlist == nil {
		at.shortTermWatchlist = make(map[string]*decision.ShortTermWatchItem)
	}
	currentCycle := at.callCount

	// 清理过期项和已有持仓项。
	positionSymbols := make(map[string]bool)
	if ctx != nil {
		for _, pos := range ctx.Positions {
			positionSymbols[pos.Symbol] = true
		}
	}
	for sym, item := range at.shortTermWatchlist {
		if item == nil || item.Symbol == "" || positionSymbols[sym] || (item.ExpiresAfterCycle > 0 && currentCycle > item.ExpiresAfterCycle) {
			delete(at.shortTermWatchlist, sym)
		}
	}

	for _, d := range decisions {
		symbol := strings.ToUpper(strings.TrimSpace(d.Symbol))
		if symbol == "" {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(d.WatchlistAction))
		if action == "" {
			continue
		}
		switch action {
		case "remove", "delete", "invalidated", "expired":
			delete(at.shortTermWatchlist, symbol)
		case "add", "keep", "update":
			item := at.shortTermWatchlist[symbol]
			if item == nil {
				item = &decision.ShortTermWatchItem{Symbol: symbol, FirstSeenCycle: currentCycle}
			}
			item.Symbol = symbol
			item.LastSeenCycle = currentCycle
			if item.FirstSeenCycle <= 0 {
				item.FirstSeenCycle = currentCycle
			}
			if item.ExpiresAfterCycle <= currentCycle {
				item.ExpiresAfterCycle = currentCycle + 3
			}
			if d.SideBias != "" {
				item.SideBias = d.SideBias
			}
			if d.SetupType != "" {
				item.SetupType = d.SetupType
			}
			if d.TriggerCondition != "" {
				item.TriggerCondition = d.TriggerCondition
			}
			if d.InvalidationCondition != "" {
				item.InvalidationCondition = d.InvalidationCondition
			}
			if d.TriggerPrice > 0 {
				item.TriggerPrice = d.TriggerPrice
			}
			if d.InvalidationPrice > 0 {
				item.InvalidationPrice = d.InvalidationPrice
			}
			if d.SuggestedStopLoss > 0 {
				item.SuggestedStopLoss = d.SuggestedStopLoss
			}
			if d.SuggestedTakeProfit > 0 {
				item.SuggestedTakeProfit = d.SuggestedTakeProfit
			}
			if d.WatchPriority > 0 {
				item.Priority = d.WatchPriority
			}
			if strings.TrimSpace(d.Reasoning) != "" {
				item.LastReasoning = strings.TrimSpace(d.Reasoning)
			}
			if item.Source == "" {
				item.Source = "llm_wait"
			}
			at.shortTermWatchlist[symbol] = item
		}
	}

	// 轻量 watchlist：最多保留 3 个，按 priority 简单裁剪。
	if len(at.shortTermWatchlist) <= 3 {
		return
	}
	for len(at.shortTermWatchlist) > 3 {
		removeSym := ""
		removePriority := int(^uint(0) >> 1)
		oldest := int(^uint(0) >> 1)
		for sym, item := range at.shortTermWatchlist {
			priority := 0
			firstSeen := 999999
			if item != nil {
				priority = item.Priority
				if item.FirstSeenCycle > 0 {
					firstSeen = item.FirstSeenCycle
				}
			}
			if priority < removePriority || (priority == removePriority && firstSeen < oldest) {
				removeSym = sym
				removePriority = priority
				oldest = firstSeen
			}
		}
		if removeSym == "" {
			break
		}
		delete(at.shortTermWatchlist, removeSym)
	}
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
		return at.executeOpenWithRecord(ctx, dec, actionRecord, "long")
	case decision.ActionOpenShort:
		return at.executeOpenWithRecord(ctx, dec, actionRecord, "short")
	case decision.ActionCloseLong:
		return at.executeCloseWithRecord(ctx, dec, actionRecord, "long")
	case decision.ActionCloseShort:
		return at.executeCloseWithRecord(ctx, dec, actionRecord, "short")
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

// executeOpenWithRecord 执行开仓并记录详细信息
func (at *AutoTrader) executeOpenWithRecord(ctx *decision.Context, dec *decision.Decision, actionRecord *logger.DecisionAction, side string) error {
	positionSide, sideName, icon, err := normalizeTradeSide(side)
	if err != nil {
		return err
	}
	log.Printf("  %s 开%s仓: %s", icon, sideName, dec.Symbol)

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == dec.Symbol && pos["side"] == side {
				return fmt.Errorf("❌ %s 已有%s仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_%s 决策", dec.Symbol, sideName, side)
			}
		}
	}

	// 获取当前价格。开仓需要用价格计算下单数量，因此仍是硬依赖。
	currentPrice, err := at.trader.GetMarketPrice(dec.Symbol)
	if err != nil {
		return err
	}

	// 计算数量
	quantity := dec.PositionSizeUSD / currentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = currentPrice

	// 开仓
	var order map[string]interface{}
	if side == "long" {
		order, err = at.trader.OpenLong(dec.Symbol, quantity, dec.Leverage)
	} else {
		order, err = at.trader.OpenShort(dec.Symbol, quantity, dec.Leverage)
	}
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := dec.Symbol + "_" + side
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
	at.autoStateMu.Lock()
	if at.autoDecisionState.TP == nil {
		at.autoDecisionState.TP = make(map[string]*decision.AutoTPState)
	}
	at.autoDecisionState.TP[posKey] = &decision.AutoTPState{
		Stage:             0,
		BaselineEntry:     currentPrice,
		BaselineQty:       quantity,
		InitialStop:       dec.StopLoss,
		CurrentStop:       dec.StopLoss,
		CurrentTakeProfit: dec.TakeProfit,
		BestPrice:         currentPrice,
	}
	at.autoStateMu.Unlock()

	// ✅ 创建 TradeEpisode（开仓成功后）
	if at.tradeMemory != nil {
		at.tradeMemory.OnOpenSuccess(ctx, dec, quantity, currentPrice)
	}

	// 设置止损止盈
	if err := at.trader.SetStopLoss(dec.Symbol, positionSide, quantity, dec.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if err := at.trader.SetTakeProfit(dec.Symbol, positionSide, quantity, dec.TakeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败: %v", err)
	}

	return nil
}

// executeCloseWithRecord 执行平仓并记录详细信息
func (at *AutoTrader) executeCloseWithRecord(ctx *decision.Context, decision *decision.Decision, actionRecord *logger.DecisionAction, side string) error {
	_, sideName, _, err := normalizeTradeSide(side)
	if err != nil {
		return err
	}
	log.Printf("  🔄 平%s仓: %s", sideName, decision.Symbol)

	targetPosition, quantity, err := at.getLivePositionForExit(decision.Symbol, side)
	if err != nil {
		return err
	}
	actionRecord.Quantity = quantity
	currentPrice := at.bestEffortExitPrice(decision.Symbol, targetPosition)
	if currentPrice > 0 {
		actionRecord.Price = currentPrice
	}

	// 平仓：使用已确认的真实仓位数量，避免 ticker 失败或二次查仓阻断退出
	var order map[string]interface{}
	if side == "long" {
		order, err = at.trader.CloseLong(decision.Symbol, quantity)
	} else {
		order, err = at.trader.CloseShort(decision.Symbol, quantity)
	}
	if err != nil {
		return err
	}

	// 记录订单ID与成交价
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}
	if fillPrice := orderFillPrice(order); fillPrice > 0 {
		currentPrice = fillPrice
		actionRecord.Price = fillPrice
	}

	log.Printf("  ✓ 平仓成功")

	// ✅ 写入TradeRecord并触发复盘总结agent（异步）
	if at.tradeMemory != nil && currentPrice > 0 {
		reason := "ai_close"
		if strings.TrimSpace(decision.DecisionSource) != "" {
			reason = decision.DecisionSource
		}
		_, _ = at.tradeMemory.OnCloseSuccess(ctx, decision, currentPrice, reason)
	} else if at.tradeMemory != nil {
		log.Printf("  ⚠ 平仓成交价不可用，跳过TradeRecord写入: %s", decision.Symbol)
	}

	// 全平后无论来源都清理仓位管理状态，防止下一笔同币种交易继承旧阶段。
	posKey := decision.Symbol + "_" + side
	at.autoStateMu.Lock()
	delete(at.autoDecisionState.TP, posKey)
	delete(at.observedPositions, posKey) // 本地已记录的平仓，不应在下次快照被误判为交易所条件单
	at.autoStateMu.Unlock()
	return nil
}

func normalizeTradeSide(side string) (positionSide, sideName, openIcon string, err error) {
	switch strings.ToLower(side) {
	case "long":
		return "LONG", "多", "📈", nil
	case "short":
		return "SHORT", "空", "📉", nil
	default:
		return "", "", "", fmt.Errorf("未知持仓方向: %s", side)
	}
}

// getLivePositionForExit 获取交易所实时持仓作为退出动作的硬前置。
func (at *AutoTrader) getLivePositionForExit(symbol, expectedSide string) (map[string]interface{}, float64, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, 0, fmt.Errorf("获取持仓失败，跳过平仓以避免基于未知仓位下单: %w", err)
	}

	for _, pos := range positions {
		posSymbol, _ := pos["symbol"].(string)
		posSide, _ := pos["side"].(string)
		if posSymbol != symbol || strings.ToLower(posSide) != strings.ToLower(expectedSide) {
			continue
		}

		quantity := absFloatFromMap(pos, "positionAmt")
		if quantity <= 0 {
			return nil, 0, fmt.Errorf("%s %s 持仓数量为0，跳过平仓", symbol, expectedSide)
		}
		return pos, quantity, nil
	}

	return nil, 0, fmt.Errorf("未在交易所实时持仓中找到 %s 的%s仓，跳过平仓", symbol, expectedSide)
}

// bestEffortExitPrice 获取退出动作的参考价格。ticker 失败时只降级记录，不阻断市价退出。
func (at *AutoTrader) bestEffortExitPrice(symbol string, position map[string]interface{}) float64 {
	currentPrice, err := at.trader.GetMarketPrice(symbol)
	if err == nil && currentPrice > 0 {
		return currentPrice
	}
	log.Printf("  ⚠ 获取 %s ticker 失败，继续按交易所实时持仓执行市价退出: %v", symbol, err)

	for _, key := range []string{"markPrice", "entryPrice"} {
		if price := floatFromInterface(position[key]); price > 0 {
			log.Printf("  ℹ️ 使用持仓字段 %s=%.8f 作为退出记录参考价", key, price)
			return price
		}
	}
	return 0
}

func absFloatFromMap(m map[string]interface{}, key string) float64 {
	v := floatFromInterface(m[key])
	if v < 0 {
		return -v
	}
	return v
}

func floatFromInterface(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

func orderFillPrice(order map[string]interface{}) float64 {
	if order == nil {
		return 0
	}
	if avg := floatFromInterface(order["avgPrice"]); avg > 0 {
		return avg
	}
	executedQty := floatFromInterface(order["executedQty"])
	cumQuote := floatFromInterface(order["cumQuote"])
	if executedQty > 0 && cumQuote > 0 {
		return cumQuote / executedQty
	}
	if price := floatFromInterface(order["price"]); price > 0 {
		return price
	}
	return 0
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

	// 决策预校验后，同一批中的 partial_close 可能刚建立了更严格的 breakeven。
	// 下单前必须再次读取最新状态，防止后续 update_stop_loss 把它放宽。
	at.autoStateMu.Lock()
	latestProtectedStop := 0.0
	if st := at.autoDecisionState.TP[decision.Symbol+"_"+strings.ToLower(side)]; st != nil {
		latestProtectedStop = st.CurrentStop
	}
	at.autoStateMu.Unlock()
	if latestProtectedStop > 0 {
		if side == "long" && (decision.NewStopLoss < latestProtectedStop ||
			(decision.DecisionSource == "auto_trailing_stop" && decision.NewStopLoss <= latestProtectedStop)) {
			return fmt.Errorf("拒绝放宽多仓保护止损：当前 %.4f，新值 %.4f", latestProtectedStop, decision.NewStopLoss)
		}
		if side == "short" && (decision.NewStopLoss > latestProtectedStop ||
			(decision.DecisionSource == "auto_trailing_stop" && decision.NewStopLoss >= latestProtectedStop)) {
			return fmt.Errorf("拒绝放宽空仓保护止损：当前 %.4f，新值 %.4f", latestProtectedStop, decision.NewStopLoss)
		}
	}

	// 更新止损
	// TODO: 如果有旧止损单，需要先取消旧止损单
	positionSide := strings.ToUpper(side)
	if err := at.trader.SetStopLoss(decision.Symbol, positionSide, quantity, decision.NewStopLoss); err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}
	at.autoStateMu.Lock()
	if st := at.autoDecisionState.TP[decision.Symbol+"_"+strings.ToLower(side)]; st != nil {
		st.CurrentStop = decision.NewStopLoss
		now := time.Now().UnixMilli()
		if decision.DecisionSource == "auto_trailing_stop" {
			st.LastStopUpdateTimeMs = now
		} else {
			st.LastLLMStopUpdateTimeMs = now
		}
	}
	at.autoStateMu.Unlock()

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
	at.autoStateMu.Lock()
	if st := at.autoDecisionState.TP[decision.Symbol+"_"+strings.ToLower(side)]; st != nil {
		st.CurrentTakeProfit = decision.NewTakeProfit
	}
	at.autoStateMu.Unlock()

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

	if dec.DecisionSource == "auto_take_profit" && dec.HasExpectedStage {
		at.autoStateMu.Lock()
		st := at.autoDecisionState.TP[dec.Symbol+"_"+strings.ToLower(side)]
		actualStage := -1
		if st != nil {
			actualStage = st.Stage
		}
		at.autoStateMu.Unlock()
		if actualStage != dec.ExpectedStage {
			return fmt.Errorf("自动部分止盈动作已过期：expected_stage=%d actual_stage=%d", dec.ExpectedStage, actualStage)
		}
	}

	// 计算要平仓的数量
	closeQuantity := quantity * (dec.ClosePercentage / 100.0)

	currentPrice := at.bestEffortExitPrice(dec.Symbol, targetPosition)
	if currentPrice > 0 {
		actionRecord.Price = currentPrice
	}
	actionRecord.Quantity = closeQuantity

	// 执行部分减仓。Reduce* 不得清理剩余仓位的保护单；成交后会按真实余量重建。
	var order map[string]interface{}
	if side == "long" {
		order, err = at.trader.ReduceLong(dec.Symbol, closeQuantity)
	} else if side == "short" {
		order, err = at.trader.ReduceShort(dec.Symbol, closeQuantity)
	} else {
		return fmt.Errorf("未知的持仓方向: %s", side)
	}

	if err != nil {
		return err
	}

	// 记录订单ID与成交价
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}
	fillPrice := orderFillPrice(order)
	if fillPrice > 0 {
		actionRecord.Price = fillPrice
	} else {
		fillPrice = currentPrice
	}

	log.Printf("  ✓ 部分平仓成功，平仓数量: %.4f (%.0f%%)", closeQuantity, dec.ClosePercentage)

	// 以交易所真实仓位为准，避免数量精度、部分成交或并发操作造成估算偏差。
	remainingQuantity := quantity - closeQuantity
	if live, liveQty, liveErr := at.getLivePositionForExit(dec.Symbol, side); liveErr == nil {
		remainingQuantity = liveQty
		targetPosition = live
	} else if remainingQuantity < 0 {
		remainingQuantity = 0
	}
	log.Printf("  ℹ️ 剩余持仓数量: %.4f", remainingQuantity)

	posKey := dec.Symbol + "_" + strings.ToLower(side)
	at.autoStateMu.Lock()
	defer at.autoStateMu.Unlock()
	if at.autoDecisionState.TP == nil {
		at.autoDecisionState.TP = make(map[string]*decision.AutoTPState)
	}
	st := at.autoDecisionState.TP[posKey]
	if st == nil {
		entry := floatFromInterface(targetPosition["entryPrice"])
		st = &decision.AutoTPState{BaselineEntry: entry, BaselineQty: quantity, BestPrice: currentPrice}
		at.autoDecisionState.TP[posKey] = st
	}
	if st.BaselineEntry <= 0 {
		st.BaselineEntry = floatFromInterface(targetPosition["entryPrice"])
	}
	if st.BaselineQty <= 0 {
		st.BaselineQty = quantity
	}

	// 用真实/估算成交价累计部分平仓净收益，供整笔交易级 breakeven 计算。
	closedQty := quantity - remainingQuantity
	if closedQty <= 0 {
		closedQty = closeQuantity
	}
	st.RealizedNetPnL += estimatePartialCloseNetPnL(strings.ToLower(side), st.BaselineEntry, fillPrice, closedQty,
		at.config.AssumedTakerFeeRate+at.config.AssumedSlippageRate)

	tpCfg := decision.ResolveAutoTakeProfitConfig(dec.Symbol, at.runtimeCfg.Get().AutoTakeProfit)
	oldStage := st.Stage
	if dec.DecisionSource == "auto_take_profit" {
		st.Stage++
	} else if st.BaselineQty > 0 {
		remainingRatio := remainingQuantity / st.BaselineQty
		stage1Remaining := 1 - tpCfg.Stage0ClosePct/100
		stage2Remaining := stage1Remaining * (1 - tpCfg.Stage1ClosePct/100)
		if remainingRatio <= stage2Remaining+1e-6 {
			if st.Stage < 2 {
				st.Stage = 2
			}
		} else if remainingRatio <= stage1Remaining+1e-6 {
			if st.Stage < 1 {
				st.Stage = 1
			}
		}
	}
	st.LastActionTimeMs = time.Now().UnixMilli()
	if st.Stage >= 2 {
		st.TrailingActive = tpCfg.TrailingEnabled
	}

	// TP1（或等量 AI 主动减仓）后，把剩余仓位保护到整笔交易级 breakeven。
	previousStop := st.CurrentStop
	desiredStop := previousStop
	if desiredStop <= 0 {
		desiredStop = st.InitialStop
	}
	if oldStage < 1 && st.Stage >= 1 && tpCfg.BreakevenEnabled && remainingQuantity > 0 {
		if beStop := calculateEpisodeBreakevenStop(strings.ToLower(side), st.BaselineEntry, remainingQuantity,
			st.RealizedNetPnL, tpCfg.BreakevenFloorUSDT, at.config.AssumedTakerFeeRate+at.config.AssumedSlippageRate); beStop > 0 {
			if desiredStop <= 0 || (side == "long" && beStop > desiredStop) || (side == "short" && beStop < desiredStop) {
				desiredStop = beStop
			}
		}
	}

	// 部分成交后按真实余量重建 SL/TP；任一失败都明确记录，但不重试减仓动作。
	var protectionErrors []string
	positionSide := strings.ToUpper(side)
	markPrice := floatFromInterface(targetPosition["markPrice"])
	if markPrice <= 0 {
		markPrice = currentPrice
	}
	if desiredStop > 0 && remainingQuantity > 0 {
		validStop := (side == "long" && desiredStop < markPrice) || (side == "short" && desiredStop > markPrice)
		if !validStop {
			protectionErrors = append(protectionErrors, fmt.Sprintf("breakeven/止损价 %.4f 对当前价 %.4f 无效", desiredStop, markPrice))
		} else if err := at.trader.SetStopLoss(dec.Symbol, positionSide, remainingQuantity, desiredStop); err != nil {
			// SetStopLoss 可能已经撤掉旧单；尝试立即恢复上一档保护，避免剩余仓位裸奔。
			fallbackStop := previousStop
			if fallbackStop <= 0 {
				fallbackStop = st.InitialStop
			}
			if fallbackStop > 0 {
				if fallbackErr := at.trader.SetStopLoss(dec.Symbol, positionSide, remainingQuantity, fallbackStop); fallbackErr == nil {
					st.CurrentStop = fallbackStop
					protectionErrors = append(protectionErrors, fmt.Sprintf("新止损 %.4f 设置失败，已恢复旧保护 %.4f: %v", desiredStop, fallbackStop, err))
				} else {
					protectionErrors = append(protectionErrors, fmt.Sprintf("重建止损失败且恢复旧保护失败: new=%v fallback=%v", err, fallbackErr))
				}
			} else {
				protectionErrors = append(protectionErrors, "重建止损失败: "+err.Error())
			}
		} else {
			st.CurrentStop = desiredStop
		}
	}
	if st.CurrentTakeProfit > 0 && remainingQuantity > 0 {
		if err := at.trader.SetTakeProfit(dec.Symbol, positionSide, remainingQuantity, st.CurrentTakeProfit); err != nil {
			protectionErrors = append(protectionErrors, "重建止盈失败: "+err.Error())
		}
	}
	if len(protectionErrors) > 0 {
		warning := "部分平仓已成交，但保护单未完整重建: " + strings.Join(protectionErrors, "; ")
		log.Printf("  🚨 %s", warning)
		actionRecord.Error = warning
	}

	return nil
}

func estimatePartialCloseNetPnL(side string, entryPrice, fillPrice, quantity, oneWayCostRate float64) float64 {
	if entryPrice <= 0 || fillPrice <= 0 || quantity <= 0 {
		return 0
	}
	gross := (fillPrice - entryPrice) * quantity
	if side == "short" {
		gross = -gross
	}
	if oneWayCostRate < 0 {
		oneWayCostRate = 0
	}
	return gross - (entryPrice+fillPrice)*quantity*oneWayCostRate
}

func calculateEpisodeBreakevenStop(side string, entryPrice, remainingQty, realizedNetPnL, floorUSDT, oneWayCostRate float64) float64 {
	if entryPrice <= 0 || remainingQty <= 0 {
		return 0
	}
	if oneWayCostRate < 0 {
		oneWayCostRate = 0
	}
	available := realizedNetPnL - floorUSDT
	if side == "long" {
		denom := 1 - oneWayCostRate
		if denom <= 0 {
			return 0
		}
		return (entryPrice*(1+oneWayCostRate) - available/remainingQty) / denom
	}
	denom := 1 + oneWayCostRate
	return (entryPrice*(1-oneWayCostRate) + available/remainingQty) / denom
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

// GetAIClient 获取该 trader 的 AI 客户端实例
func (at *AutoTrader) GetAIClient() *mcp.Client {
	return at.aiClient
}

// GetDecisionLogger 获取决策日志记录器
func (at *AutoTrader) GetDecisionLogger() *logger.DecisionLogger {
	return at.decisionLogger
}

// GetAssumedTakerFeeRate 返回“估算手续费率”（用于订单级别复盘展示的估算，不影响真实下单）
func (at *AutoTrader) GetAssumedTakerFeeRate() float64 {
	return at.config.AssumedTakerFeeRate
}

// GetLLMCostSnapshot 返回 LLM 调用费用的只读快照
func (at *AutoTrader) GetLLMCostSnapshot() LLMCostSnapshot {
	return at.llmCostTracker.Snapshot()
}

// GetLLMCostTracker 返回 LLM 费用追踪器（供 decision 层回调）
func (at *AutoTrader) GetLLMCostTracker() *LLMCostTracker {
	return at.llmCostTracker
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

// GetIncome 获取收支流水（资金费用、手续费等）
func (at *AutoTrader) GetIncome(symbol string, incomeType string, startTimeMs, endTimeMs int64, limit int) ([]IncomeRecord, error) {
	return at.trader.ListIncome(symbol, incomeType, startTimeMs, endTimeMs, limit)
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

	// 添加高峰时段暂停状态
	result["peak_hour"] = at.GetPeakHourStatus()

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
		case decision.ActionUpdateStopLoss, decision.ActionUpdateTakeProfit:
			return 2 // 减仓后再按真实剩余数量更新保护单
		case decision.ActionOpenLong, decision.ActionOpenShort:
			return 3 // 保护动作完成后再开仓
		case decision.ActionHold, decision.ActionWait:
			return 4 // 最低优先级：观望
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

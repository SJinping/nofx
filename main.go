package main

import (
	"fmt"
	"log"
	"nofx/api"
	"nofx/config"
	"nofx/logger"
	"nofx/manager"
	"nofx/market"
	"nofx/pool"
	"nofx/stats"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║    🏆 AI模型交易竞赛系统 - Qwen vs DeepSeek               ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// 解析命令行参数
	configFile := "config.json"
	freshStart := false
	for _, arg := range os.Args[1:] {
		if arg == "--fresh" {
			freshStart = true
		} else if !strings.HasPrefix(arg, "-") {
			configFile = arg
		}
	}

	log.Printf("📋 加载配置文件: %s", configFile)
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		log.Fatalf("❌ 加载配置失败: %v", err)
	}

	// --fresh: 归档所有已有日志，从头开始
	if freshStart {
		log.Println("🧹 --fresh 模式：归档已有决策日志...")
		for _, traderCfg := range cfg.Traders {
			logDir := fmt.Sprintf("decision_logs/%s", traderCfg.ID)
			if err := logger.ArchiveLogs(logDir); err != nil {
				log.Printf("⚠️  归档 %s 日志失败: %v", traderCfg.ID, err)
			}
		}
		// --fresh 模式下忽略 auto_resume，强制从暂停状态开始
		cfg.AutoResume = false
	}

	log.Printf("✓ 配置加载成功，共%d个trader参赛", len(cfg.Traders))
	if cfg.AutoResume {
		log.Println("🔄 接续运行模式：启动后自动开始交易")
	}
	fmt.Println()

	// Binance market data source: mainnet vs testnet
	if cfg.BinanceTestnet {
		market.SetFAPIBaseURL("https://testnet.binancefuture.com")
		log.Printf("🧪 Binance Futures 测试网模式已启用（market data base: %s）", "https://testnet.binancefuture.com")
	} else {
		market.SetFAPIBaseURL("https://fapi.binance.com")
	}

	// 设置默认主流币种列表
	pool.SetDefaultCoins(cfg.DefaultCoins)

	// 设置是否使用默认主流币种
	pool.SetUseDefaultCoins(cfg.UseDefaultCoins)
	if cfg.UseDefaultCoins {
		log.Printf("✓ 已启用默认主流币种列表（共%d个币种）: %v", len(cfg.DefaultCoins), cfg.DefaultCoins)
	}

	// 设置币种池API URL
	if cfg.CoinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(cfg.CoinPoolAPIURL)
		log.Printf("✓ 已配置AI500币种池API")
	}
	if cfg.OITopAPIURL != "" {
		pool.SetOITopAPI(cfg.OITopAPIURL)
		log.Printf("✓ 已配置OI Top API")
	}

	// 创建TraderManager（传入配置文件路径，用于运行时配置持久化）
	traderManager := manager.NewTraderManager(configFile)

	// 添加所有trader
	for i, traderCfg := range cfg.Traders {
		log.Printf("📦 [%d/%d] 初始化 %s (%s模型)...",
			i+1, len(cfg.Traders), traderCfg.Name, strings.ToUpper(traderCfg.AIModel))

		err := traderManager.AddTrader(
			traderCfg,
			cfg.CoinPoolAPIURL,
			cfg.MaxDailyLoss,
			cfg.MaxDrawdown,
			cfg.StopTradingMinutes,
			cfg.Leverage,          // 传递杠杆配置
			cfg.EnableRecording,   // 传递录制开关
			cfg.BinanceTestnet,    // Binance 主网/测试网
			cfg.StopLossDistance,   // 止损最小距离配置
			cfg.AutoTakeProfit,    // 自动止盈配置
			cfg.AutoResume,        // 接续运行开关
		)
		if err != nil {
			log.Fatalf("❌ 初始化trader失败: %v", err)
		}
	}

	fmt.Println()
	fmt.Println("🏁 竞赛参赛者:")
	for _, traderCfg := range cfg.Traders {
		fmt.Printf("  • %s (%s) - 初始资金: %.0f USDT\n",
			traderCfg.Name, strings.ToUpper(traderCfg.AIModel), traderCfg.InitialBalance)
	}

	fmt.Println()
	fmt.Println("🤖 AI全权决策模式:")
	fmt.Printf("  • AI将自主决定每笔交易的杠杆倍数（山寨币最高%d倍，BTC/ETH最高%d倍）\n",
		cfg.Leverage.AltcoinLeverage, cfg.Leverage.BTCETHLeverage)
	fmt.Println("  • AI将自主决定每笔交易的仓位大小")
	fmt.Println("  • AI将自主设置止损和止盈价格")
	fmt.Println("  • AI将基于市场数据、技术指标、账户状态做出全面分析")
	fmt.Println()
	fmt.Println("⚠️  风险提示: AI自动交易有风险，建议小额资金测试！")
	fmt.Println()
	fmt.Println("按 Ctrl+C 停止运行")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// 创建并启动API服务器
	apiServer := api.NewServer(traderManager, cfg.APIServerPort)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("❌ API服务器错误: %v", err)
		}
	}()

	// 设置优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 启动所有trader
	traderManager.StartAll()

	// 等待退出信号
	<-sigChan
	fmt.Println()
	fmt.Println()
	log.Println("📛 收到退出信号，正在停止所有trader...")
	traderManager.StopAll()

	// 保存所有错误统计
	log.Println("💾 保存错误统计...")
	stats.SaveAllErrorStats()

	fmt.Println()
	fmt.Println("👋 感谢使用AI交易竞赛系统！")
}

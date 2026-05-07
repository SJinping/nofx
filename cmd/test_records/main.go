package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"nofx/decision"
	"nofx/mcp"
	"os"
)

func main() {
	// 命令行参数
	recordDir := flag.String("records", "", "录制数据目录 (data/records/xxx)")
	strategyName := flag.String("strategy", "B", "要测试的新策略 (A/B/V)")
	startCycle := flag.Int("start", 0, "开始周期（0=从头开始）")
	endCycle := flag.Int("end", 0, "结束周期（0=到最后）")
	output := flag.String("output", "", "输出结果文件")
	enableAI := flag.Bool("ai", true, "启用AI决策（false=仅模拟执行）")
	flag.Parse()

	if *recordDir == "" {
		fmt.Println("📖 用法示例:")
		fmt.Println()
		fmt.Println("  测试策略B在策略A的录制数据上的表现:")
		fmt.Println("    go run cmd/test_records/main.go \\")
		fmt.Println("      -records=data/records/binance_deepseek_paper_strategyA \\")
		fmt.Println("      -strategy=B \\")
		fmt.Println("      -output=results/strategyB_on_records_A.json")
		fmt.Println()
		fmt.Println("  仅模拟执行（不调用LLM）:")
		fmt.Println("    go run cmd/test_records/main.go \\")
		fmt.Println("      -records=data/records/binance_deepseek_paper_strategyA \\")
		fmt.Println("      -strategy=B \\")
		fmt.Println("      -ai=false")
		fmt.Println()
		fmt.Println("  测试前50个周期:")
		fmt.Println("    go run cmd/test_records/main.go \\")
		fmt.Println("      -records=data/records/binance_deepseek_paper_strategyA \\")
		fmt.Println("      -strategy=V \\")
		fmt.Println("      -start=1 -end=50")
		fmt.Println()
		os.Exit(1)
	}

	// 初始化MCP（仅在启用AI时需要）
	if *enableAI {
		if err := initMCP(); err != nil {
			log.Fatalf("❌ MCP初始化失败: %v", err)
		}
	}

	// 选择策略
	var strategy decision.PromptStrategy
	switch *strategyName {
	case "A":
		strategy = decision.StrategyA{}
	case "B":
		strategy = decision.StrategyB{}
	case "V":
		strategy = decision.StrategyV{}
	default:
		log.Fatalf("❌ 不支持的策略: %s (仅支持 A/B/V)", *strategyName)
	}

	fmt.Printf("🧪 回测配置:\n")
	fmt.Printf("  录制目录:   %s\n", *recordDir)
	fmt.Printf("  新策略:     %s\n", strategy.Name())
	if *startCycle > 0 || *endCycle > 0 {
		fmt.Printf("  周期范围:   %d - %d\n", *startCycle, *endCycle)
	}
	fmt.Printf("  AI决策:     %v\n", *enableAI)
	if *output != "" {
		fmt.Printf("  输出文件:   %s\n", *output)
	}
	fmt.Println()

	// 配置回测
	config := &decision.BacktestConfig{
		RecordDir:       *recordDir,
		Strategy:        strategy,
		StartCycle:      *startCycle,
		EndCycle:        *endCycle,
		EnableAI:        *enableAI,
		CompareOriginal: false,
	}

	// 运行回测
	fmt.Println("🚀 开始回测...")
	fmt.Println()
	result, err := decision.RunBacktest(config)
	if err != nil {
		log.Fatalf("❌ 回测失败: %v", err)
	}

	// 打印结果
	printResult(result)

	// 保存结果
	if *output != "" {
		if err := os.MkdirAll("results", 0755); err != nil {
			log.Printf("⚠️  创建results目录失败: %v", err)
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(*output, data, 0644); err != nil {
			log.Printf("⚠️  保存结果文件失败: %v", err)
		} else {
			fmt.Printf("\n✅ 详细结果已保存: %s\n", *output)
		}
	}
}

func initMCP() error {
	if key := os.Getenv("DEEPSEEK_OPENAI_KEY"); key != "" {
		mcp.SetDeepSeekAPIKey(key, "")
		fmt.Println("✅ 使用 DeepSeek API")
		return nil
	}
	if key := os.Getenv("QWEN_OPENAI_API_KEY"); key != "" {
		mcp.SetQwenAPIKey(key, "", "")
		fmt.Println("✅ 使用 Qwen API")
		return nil
	}
	return fmt.Errorf("未找到 AI API 密钥，请设置 DEEPSEEK_OPENAI_KEY 或 QWEN_OPENAI_API_KEY")
}

func printResult(result *decision.BacktestResult) {
	fmt.Println("============================================================")
	fmt.Println("                    回测结果")
	fmt.Println("============================================================")
	fmt.Println()

	fmt.Printf("📈 总周期数:   %d\n", result.TotalCycles)
	fmt.Printf("📊 起始净值:   %.2f USDT\n", result.StartEquity)
	fmt.Printf("💰 结束净值:   %.2f USDT\n", result.EndEquity)
	fmt.Printf("📈 总收益率:   %+.2f%%\n", result.TotalReturn)
	fmt.Printf("📉 最大回撤:   %.2f%%\n", result.MaxDrawdown)
	fmt.Printf("⚡ 夏普比率:   %.2f\n", result.SharpeRatio)
	fmt.Printf("🔄 总交易数:   %d\n", result.TotalTrades)
	fmt.Printf("🎯 胜率:       %.2f%%\n", result.WinRate)
	fmt.Println()

	// 绩效评价
	fmt.Printf("🎯 绩效评价:   ")
	if result.SharpeRatio > 1.0 {
		fmt.Println("优秀 ⭐⭐⭐")
	} else if result.SharpeRatio > 0.5 {
		fmt.Println("良好 ⭐⭐")
	} else if result.SharpeRatio > 0 {
		fmt.Println("一般 ⭐")
	} else {
		fmt.Println("需改进 ⚠️")
	}

	fmt.Println()
	fmt.Println("============================================================")
}


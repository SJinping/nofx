package decision

import (
	"encoding/json"
	"fmt"
	"math"
	"nofx/market"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BacktestConfig 回测配置
type BacktestConfig struct {
	RecordDir       string         // 录制数据目录
	Strategy        PromptStrategy // 要测试的策略
	StartCycle      int            // 开始周期（可选）
	EndCycle        int            // 结束周期（可选）
	EnableAI        bool           // 是否启用AI决策（false=仅模拟执行）
	CompareOriginal bool           // 是否对比原始决策
}

// BacktestResult 回测结果
type BacktestResult struct {
	TotalCycles  int                 `json:"total_cycles"`
	StartEquity  float64             `json:"start_equity"`
	EndEquity    float64             `json:"end_equity"`
	TotalReturn  float64             `json:"total_return"`
	MaxDrawdown  float64             `json:"max_drawdown"`
	SharpeRatio  float64             `json:"sharpe_ratio"`
	TotalTrades  int                 `json:"total_trades"`
	WinRate      float64             `json:"win_rate"`
	Decisions    []*BacktestDecision `json:"decisions,omitempty"`
	EquityCurve  []float64           `json:"equity_curve"`
	TradeRecords []*TradeRecord      `json:"trade_records"` // 详细的交易记录
}

// BacktestDecision 回测决策记录
type BacktestDecision struct {
	Cycle            int        `json:"cycle"`
	Timestamp        string     `json:"timestamp"`
	Decisions        []Decision `json:"decisions"`
	OriginalDecision []Decision `json:"original_decision,omitempty"` // 原始AI决策（如果有）
}

// RunBacktest 运行离线回测
func RunBacktest(config *BacktestConfig) (*BacktestResult, error) {
	// 1. 加载所有录制文件
	records, err := loadRecordedContexts(config.RecordDir)
	if err != nil {
		return nil, fmt.Errorf("加载录制数据失败: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("没有找到录制数据")
	}

	// 2. 按周期排序
	sort.Slice(records, func(i, j int) bool {
		return records[i].CallCount < records[j].CallCount
	})

	// 3. 过滤周期范围
	if config.StartCycle > 0 || config.EndCycle > 0 {
		records = filterRecordsByCycle(records, config.StartCycle, config.EndCycle)
	}

	// 4. 初始化虚拟账户和回测结果
	va := &VirtualAccount{
		Equity:    records[0].Account.TotalEquity,
		Positions: make(map[string]*VirtualPosition),
	}

	totalTrades := 0
	winningTrades := 0

	result := &BacktestResult{
		TotalCycles:  len(records),
		StartEquity:  va.Equity,
		Decisions:    make([]*BacktestDecision, 0, len(records)),
		EquityCurve:  make([]float64, 0, len(records)),
		TradeRecords: make([]*TradeRecord, 0),
	}

	// 5. 模拟执行每个周期
	for _, record := range records {
		// 5.1 转换为Context（用于决策）
		ctx := recordToContext(record, config.Strategy)

		// 5.2 提取当前价格（从录制的 MarketData 中）
		prices := extractPricesFromMarketData(record.MarketData)

		// 5.3 执行决策
		var decisions []Decision
		if config.EnableAI {
			// 使用录制的市场数据进行离线回测（不调用Binance接口）
			systemPrompt := config.Strategy.BuildSystemPrompt(ctx)
			userPrompt := config.Strategy.BuildUserPrompt(ctx)
			fullDecision, err := GetFullDecisionFromText(ctx, systemPrompt, userPrompt)
			if err != nil {
				fmt.Printf("⚠️ 周期%d AI决策失败: %v\n", record.CallCount, err)
				continue
			}
			decisions = fullDecision.Decisions
		} else {
			// 仅模拟执行（使用空决策）
			decisions = []Decision{{Action: ActionWait}}
		}

		// 5.4 在虚拟账户上执行决策，并记录交易详情
		deltaTrades, deltaWins, tradeRecords := applyDecisions(va, decisions, prices, record.CallCount)
		totalTrades += deltaTrades
		winningTrades += deltaWins

		// 保存交易记录
		if len(tradeRecords) > 0 {
			result.TradeRecords = append(result.TradeRecords, tradeRecords...)
		}

		// 5.5 计算当前总净值（已实现权益 + 未实现盈亏）
		equity := va.Equity
		for _, pos := range va.Positions {
			price, ok := prices[pos.Symbol]
			if !ok || price <= 0 || pos.EntryPrice <= 0 {
				continue
			}

			// 计算未实现盈亏
			var pct float64
			if strings.ToLower(pos.Side) == "long" {
				pct = (price - pos.EntryPrice) / pos.EntryPrice
			} else {
				pct = (pos.EntryPrice - price) / pos.EntryPrice
			}
			unrealizedPnL := pos.EntryPrice * pos.Quantity * pct * float64(pos.Leverage)
			equity += unrealizedPnL
		}

		// 5.6 记录决策和权益曲线
		backTestDec := &BacktestDecision{
			Cycle:     record.CallCount,
			Timestamp: record.CurrentTime,
			Decisions: decisions,
		}
		result.Decisions = append(result.Decisions, backTestDec)
		result.EquityCurve = append(result.EquityCurve, equity)

		// 打印进度
		if record.CallCount%20 == 0 {
			fmt.Printf("📊 周期 %d/%d | 权益: %.2f | 收益率: %.2f%% | 交易: %d | 胜率: %.1f%%\n",
				record.CallCount, len(records), equity, (equity/result.StartEquity-1.0)*100, totalTrades,
				calculateWinRate(totalTrades, winningTrades))
		}
	}

	// 6. 强制平仓所有剩余持仓（使用最后一个周期的价格）
	if len(records) > 0 && len(va.Positions) > 0 {
		lastRecord := records[len(records)-1]
		lastPrices := extractPricesFromMarketData(lastRecord.MarketData)
		closeTime := time.Now()

		realizedFromFinalClose := 0.0
		for key, pos := range va.Positions {
			price, ok := lastPrices[pos.Symbol]
			if !ok || price <= 0 || pos.EntryPrice <= 0 || pos.Quantity <= 0 {
				continue
			}

			// 计算平仓盈亏
			var pct float64
			if strings.ToLower(pos.Side) == "long" {
				pct = (price - pos.EntryPrice) / pos.EntryPrice
			} else {
				pct = (pos.EntryPrice - price) / pos.EntryPrice
			}

			pnl := pos.EntryPrice * pos.Quantity * pct * float64(pos.Leverage)
			isWin := pnl > 0
			if pnl != 0 {
				totalTrades++
				if isWin {
					winningTrades++
				}
			}

			// 记录强制平仓交易
			holdTime := formatDuration(closeTime.Sub(pos.OpenTime))
			result.TradeRecords = append(result.TradeRecords, &TradeRecord{
				Cycle:          lastRecord.CallCount,
				Symbol:         pos.Symbol,
				Side:           pos.Side,
				Action:         "force_close",
				DecisionSource: "backtest_force_close",
				EntryPrice:     pos.EntryPrice,
				ExitPrice:      price,
				Quantity:       pos.Quantity,
				Leverage:       pos.Leverage,
				PnL:            pnl,
				PnLPercent:     pct * 100,
				IsWin:          isWin,
				HoldTime:       holdTime,
				CloseTime:      closeTime,
				PartialClose:   false,
			})

			realizedFromFinalClose += pnl
			delete(va.Positions, key)
		}

		if realizedFromFinalClose != 0 {
			va.Equity += realizedFromFinalClose
			// 用强制平仓后的净值覆盖最后一个点
			if len(result.EquityCurve) > 0 {
				result.EquityCurve[len(result.EquityCurve)-1] = va.Equity
			}
		}
	}

	// 7. 计算回测指标（基于新策略的权益曲线）
	if len(result.EquityCurve) > 0 {
		result.EndEquity = result.EquityCurve[len(result.EquityCurve)-1]
	} else {
		result.EndEquity = result.StartEquity
	}

	if result.StartEquity > 0 {
		result.TotalReturn = ((result.EndEquity - result.StartEquity) / result.StartEquity) * 100
	}
	result.MaxDrawdown = calculateMaxDrawdown(result.EquityCurve)
	result.SharpeRatio = calculateSharpeRatio(result.EquityCurve)
	result.TotalTrades = totalTrades
	if totalTrades > 0 {
		result.WinRate = (float64(winningTrades) / float64(totalTrades)) * 100
	}

	return result, nil
}

// loadRecordedContexts 加载所有录制的上下文
func loadRecordedContexts(dir string) ([]*RecordedContext, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_cycle*.json"))
	if err != nil {
		return nil, err
	}

	records := make([]*RecordedContext, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Printf("⚠️ 读取文件失败 %s: %v\n", file, err)
			continue
		}

		var record RecordedContext
		if err := json.Unmarshal(data, &record); err != nil {
			fmt.Printf("⚠️ 解析文件失败 %s: %v\n", file, err)
			continue
		}

		records = append(records, &record)
	}

	return records, nil
}

// recordToContext 将录制数据转换为决策上下文
func recordToContext(record *RecordedContext, strategy PromptStrategy) *Context {
	ctx := &Context{
		CurrentTime:     record.CurrentTime,
		RuntimeMinutes:  record.RuntimeMinutes,
		CallCount:       record.CallCount,
		Account:         record.Account,
		Positions:       record.Positions,
		CandidateCoins:  record.CandidateCoins,
		MarketDataMap:   record.MarketData,
		OITopDataMap:    record.OITopData,
		BTCETHLeverage:  record.BTCETHLeverage,
		AltcoinLeverage: record.AltcoinLeverage,
		PromptStrategy:  strategy,
		EnableRecording: false, // 回测时不录制
	}

	if record.Performance != nil {
		ctx.Performance = record.Performance
	}

	return ctx
}

// calculateMaxDrawdown 计算最大回撤
func calculateMaxDrawdown(equityCurve []float64) float64 {
	if len(equityCurve) == 0 {
		return 0
	}

	maxEquity := equityCurve[0]
	maxDrawdown := 0.0

	for _, equity := range equityCurve {
		if equity > maxEquity {
			maxEquity = equity
		}

		drawdown := (maxEquity - equity) / maxEquity * 100
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	return maxDrawdown
}

// calculateSharpeRatio 计算夏普比率
func calculateSharpeRatio(equityCurve []float64) float64 {
	if len(equityCurve) < 2 {
		return 0
	}

	// 计算收益率序列
	returns := make([]float64, len(equityCurve)-1)
	for i := 1; i < len(equityCurve); i++ {
		returns[i-1] = (equityCurve[i] - equityCurve[i-1]) / equityCurve[i-1]
	}

	// 计算平均收益和标准差
	meanReturn := 0.0
	for _, r := range returns {
		meanReturn += r
	}
	meanReturn /= float64(len(returns))

	variance := 0.0
	for _, r := range returns {
		variance += (r - meanReturn) * (r - meanReturn)
	}
	stdDev := math.Sqrt(variance / float64(len(returns)))

	if stdDev == 0 {
		return 0
	}

	// 假设无风险利率为0
	return meanReturn / stdDev
}

// filterRecordsByCycle 过滤周期范围
func filterRecordsByCycle(records []*RecordedContext, start, end int) []*RecordedContext {
	filtered := make([]*RecordedContext, 0)
	for _, r := range records {
		if (start == 0 || r.CallCount >= start) && (end == 0 || r.CallCount <= end) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// extractPricesFromMarketData 从 MarketData map 中提取当前价格
func extractPricesFromMarketData(marketData map[string]*market.Data) map[string]float64 {
	prices := make(map[string]float64)
	if marketData == nil {
		return prices
	}

	for symbol, data := range marketData {
		if data != nil && data.CurrentPrice > 0 {
			prices[symbol] = data.CurrentPrice
		}
	}
	return prices
}

// calculateWinRate 计算胜率
func calculateWinRate(totalTrades, winningTrades int) float64 {
	if totalTrades == 0 {
		return 0
	}
	return (float64(winningTrades) / float64(totalTrades)) * 100
}

// formatDuration 格式化持仓时长
func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

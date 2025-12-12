package decision

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/stats"
	"strings"
	"time"
)

// validateDecisions 验证所有决策（需要账户信息和杠杆配置）
func validateDecisions(decisions []Decision, ctx *Context) error {
	for i, decision := range decisions {
		if err := coreValidateDecision(&decisions[i], ctx); err != nil {
			errType := stats.ClassifyDecisionValidateError(err.Error())
			recordError(errType, err.Error(), decision.Symbol)
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}

		// 策略自己的额外校验（在通用校验之后）
		strategy := ctx.PromptStrategy
		if strategy != nil {
			if err := strategy.ExtraValidate(&decisions[i], ctx); err != nil {
				errType := stats.ClassifyDecisionValidateError(err.Error())
				recordError(errType, err.Error(), decision.Symbol)
				return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
			}
		}
	}
	return nil
}

// coreValidateDecision 验证单个决策的有效性
func coreValidateDecision(d *Decision, ctx *Context) error {
	accountEquity := ctx.Account.TotalEquity
	btcEthLeverage := ctx.BTCETHLeverage
	altcoinLeverage := ctx.AltcoinLeverage

	// 验证action
	if !ValidActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	// 验证需要symbol的操作必须提供有效的symbol字段
	if ActionsNeedingSymbol[d.Action] && strings.TrimSpace(d.Symbol) == "" {
		return fmt.Errorf("action '%s' 必须提供有效的symbol字段，当前为空", d.Action)
	}

	// === 针对不同 Action 的校验逻辑 ===

	// 1. 移动止损逻辑
	if d.Action == ActionUpdateStopLoss {
		if d.NewStopLoss <= 0 {
			// 为了兼容性，如果 LLM 填到了原来的 StopLoss 字段，也可，并自动迁移数据
			if d.StopLoss > 0 {
				d.NewStopLoss = d.StopLoss
			} else {
				return fmt.Errorf("update_stop_loss 必须提供 new_stop_loss 且大于 0")
			}
		}
		return nil // 校验通过
	}

	// 1b. 移动止盈逻辑
	if d.Action == ActionUpdateTakeProfit {
		if d.NewTakeProfit <= 0 {
			// 兼容旧字段 take_profit
			if d.TakeProfit > 0 {
				d.NewTakeProfit = d.TakeProfit
			} else {
				return fmt.Errorf("update_take_profit 必须提供 new_take_profit 且大于 0")
			}
		}
		return nil // 校验通过
	}

	// 2. 部分平仓逻辑
	if d.Action == ActionPartialClose {
		if d.ClosePercentage <= 0 || d.ClosePercentage > 100 {
			return fmt.Errorf("partial_close 必须提供 close_percentage 且在 0-100 之间，当前: %.2f", d.ClosePercentage)
		}
		return nil // 校验通过
	}

	// 开仓操作必须提供完整参数
	if d.Action == ActionOpenLong || d.Action == ActionOpenShort {
		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage          // 山寨币使用配置的杠杆
		maxPositionValue := accountEquity * 1.5 // 山寨币最多1.5倍账户净值
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage          // BTC和ETH使用配置的杠杆
			maxPositionValue = accountEquity * 10 // BTC/ETH最多10倍账户净值
		}

		if d.Leverage <= 0 || d.Leverage > maxLeverage {
			return fmt.Errorf("杠杆必须在1-%d之间（%s，当前配置上限%d倍）: %d", maxLeverage, d.Symbol, maxLeverage, d.Leverage)
		}
		if d.PositionSizeUSD < minPositionSizeUSD {
			return fmt.Errorf("仓位大小必须大于或等于%.2f: %.2f", minPositionSizeUSD, d.PositionSizeUSD)
		}
		// 验证仓位价值上限（加1%容差以避免浮点数精度问题）
		tolerance := maxPositionValue * 0.01 // 1%容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
				return fmt.Errorf("单币种仓位价值不能超过%.0f USDT（10倍账户净值，BTC/ETH），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			} else {
				return fmt.Errorf("山寨币单币种仓位价值不能超过%.0f USDT（1.5倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			}
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 验证止损止盈的合理性
		if d.Action == ActionOpenLong {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		// 验证风险回报比（必须≥1:minRiskReward）
		// 计算入场价（假设当前市价）
		var entryPrice float64
		if d.Action == ActionOpenLong {
			// 做多：入场价在止损和止盈之间
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // 假设在20%位置入场
		} else {
			// 做空：入场价在止损和止盈之间
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // 假设在20%位置入场
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == ActionOpenLong {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		// 硬约束：风险回报比必须≥minRiskReward
		if riskRewardRatio < minRiskReward {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥%.2f:1 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, minRiskReward, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	return nil
}

// GenerateAutoDecisions 策略A的自动决策生成
// 用于紧急止损、风险控制等自动规则
func GenerateAutoDecisions(ctx *Context) []Decision {
	// 没仓位就不需要自动风控，交给 LLM 正常决策
	if len(ctx.Positions) == 0 {
		return nil
	}

	// 0) ✅ 自动止盈（优先级最高）：一旦触发则本周期跳过LLM
	if tpDecisions := generateAutoTakeProfitDecisions(ctx); len(tpDecisions) > 0 {
		return tpDecisions
	}

	// 1) 自动止损/降风险：仅在 Sharpe 很差 或 连续亏损较多 或 保证金过高 时触发
	perf, ok := ctx.Performance.(*logger.PerformanceAnalysis)
	if !ok || perf == nil {
		return nil
	}

	// 只有在 Sharpe 很差 或 连续亏损较多 时才触发自动降风险
	badSharpe := perf.SharpeRatio < -0.8
	longLosingStreak := perf.CurrentLosingStreak >= 3
	highMargin := ctx.Account.MarginUsedPct > 85

	if !(badSharpe || longLosingStreak || highMargin) {
		return nil
	}

	var decisions []Decision

	for _, pos := range ctx.Positions {
		// 1. 浮亏超过 -3%
		// 2. 或整体保证金过高时，全部仓位平仓50%
		if pos.UnrealizedPnLPct < -3 || ctx.Account.MarginUsedPct > 90 {
			action := ActionPartialClose
			pct := 30.0
			if pos.UnrealizedPnLPct < -6 {
				pct = 50.0
			} else if pos.UnrealizedPnLPct < -10 {
				pct = 70.0
			}

			decisions = append(decisions, Decision{
				Symbol:          pos.Symbol,
				Action:          action,
				ClosePercentage: pct,
				Reasoning: fmt.Sprintf("Sharpe=%.2f, 连亏=%d, 保证金=%.1f%%，自动减仓%.1f%%止损",
					perf.SharpeRatio,
					perf.CurrentLosingStreak,
					ctx.Account.MarginUsedPct,
					pct,
				),
				Confidence:     100,
				DecisionSource: "auto_stop_loss",
			})
		}
	}

	if len(decisions) == 0 {
		// 没有严重问题的仓位，不强制操作，让 LLM 决定（ExtraValidate 会封杀新开仓）
		return nil
	}

	return decisions
}

// ===== 自动止盈（积极落袋）=====
// 规则：
// - TP阈值按“波动率(ATR14/Price)倍数”自适应，不同币种阈值不同
// - TP1: 达标后部分平仓25% + 止损上移到保本
// - TP2: 达标后部分平仓50% + 止损改为ATR跟踪
// - TP1/TP2每个持仓只触发一次（依赖 ctx.AutoState 维护跨周期状态）
func generateAutoTakeProfitDecisions(ctx *Context) []Decision {
	nowMs := time.Now().UnixMilli()

	// 参数（偏积极）
	const (
		k1 = 1.0 // TP1 = k1 * vol
		k2 = 2.0 // TP2 = k2 * vol

		tp1Min = 0.8
		tp1Max = 4.0
		tp2Min = 1.6
		tp2Max = 8.0

		closeTP1 = 25.0
		closeTP2 = 50.0

		breakEvenBufferPct = 0.10 // 0.10%：保本略微盈利，覆盖手续费/滑点
		trailingAtrMult    = 1.0  // ATR跟踪距离（越小越紧）

		cooldownMs = int64(9 * 60 * 1000) // 9分钟冷却（3min周期≈3次）
	)

	state := ctx.AutoState
	if state == nil {
		// 没注入跨周期状态时，仍可工作，但无法保证“只触发一次”
		state = &AutoDecisionState{TP: make(map[string]*AutoTPState)}
	}
	if state.TP == nil {
		state.TP = make(map[string]*AutoTPState)
	}

	var decisions []Decision

	for _, pos := range ctx.Positions {
		// 只对盈利持仓触发止盈
		if pos.UnrealizedPnLPct <= 0 {
			continue
		}

		md := ctx.MarketDataMap[pos.Symbol]
		if md == nil || md.CurrentPrice <= 0 {
			continue
		}

		// 波动率（百分比）
		volPct := md.VolatilityPct * 100
		if volPct <= 0 {
			continue
		}

		tp1Pct := clamp(k1*volPct, tp1Min, tp1Max)
		tp2Pct := clamp(k2*volPct, tp2Min, tp2Max)
		if tp2Pct <= tp1Pct {
			tp2Pct = tp1Pct + 0.5
		}

		// 读取状态
		posKey := pos.Symbol + "_" + pos.Side
		st := state.TP[posKey]
		if st == nil {
			st = &AutoTPState{}
			state.TP[posKey] = st
		}
		if st.LastActionTimeMs > 0 && nowMs-st.LastActionTimeMs < cooldownMs {
			continue
		}

		pnlPct := pos.UnrealizedPnLPct
		targetStage := 0
		closePct := 0.0

		// TP2优先
		if st.Stage < 2 && pnlPct >= tp2Pct {
			targetStage = 2
			closePct = closeTP2
		} else if st.Stage < 1 && pnlPct >= tp1Pct {
			targetStage = 1
			closePct = closeTP1
		} else {
			continue
		}

		// 计算联动止损
		breakEvenSL := calcBreakEvenStopLoss(pos.Side, pos.EntryPrice, breakEvenBufferPct)
		newSL := breakEvenSL

		if targetStage >= 2 {
			atr := 0.0
			if md.LongerTermContext != nil && md.LongerTermContext.ATR14 > 0 {
				atr = md.LongerTermContext.ATR14
			}
			newSL = calcTrailingStopLoss(pos.Side, pos.MarkPrice, md.VolatilityPct, atr, trailingAtrMult)

			// 不允许比保本更差
			if pos.Side == "long" {
				newSL = math.Max(newSL, breakEvenSL)
			} else {
				newSL = math.Min(newSL, breakEvenSL)
			}
		}

		// 生成两条决策：部分平仓 + 更新止损
		reason := fmt.Sprintf(
			"自动止盈TP%d触发: pnl=%.2f%%, vol=%.2f%%, TP1=%.2f%%, TP2=%.2f%% → 部分平仓%.0f%%并联动止损(newSL=%.4f)",
			targetStage, pnlPct, volPct, tp1Pct, tp2Pct, closePct, newSL,
		)

		decisions = append(decisions,
			Decision{
				Symbol:          pos.Symbol,
				Action:          ActionPartialClose,
				ClosePercentage: closePct,
				Reasoning:       reason,
				Confidence:      100,
				DecisionSource:  "auto_take_profit",
			},
			Decision{
				Symbol:         pos.Symbol,
				Action:         ActionUpdateStopLoss,
				NewStopLoss:    newSL,
				Reasoning:      reason,
				Confidence:     100,
				DecisionSource: "auto_take_profit",
			},
		)
	}

	if len(decisions) == 0 {
		return nil
	}
	return decisions
}

func clamp(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func calcBreakEvenStopLoss(side string, entryPrice float64, bufferPct float64) float64 {
	if entryPrice <= 0 {
		return 0
	}
	b := bufferPct / 100.0
	if side == "short" {
		return entryPrice * (1.0 - b)
	}
	return entryPrice * (1.0 + b)
}

// calcTrailingStopLoss 计算跟踪止损
// - 优先用 ATR14（价格单位）
// - 若ATR不可用，则用 volatilityPct * price 作为近似距离
func calcTrailingStopLoss(side string, markPrice float64, volatilityPct float64, atr14 float64, atrMult float64) float64 {
	if markPrice <= 0 {
		return 0
	}

	dist := 0.0
	if atr14 > 0 {
		dist = atrMult * atr14
	} else if volatilityPct > 0 {
		dist = atrMult * (markPrice * volatilityPct)
	}

	// 兜底：至少离当前价 0.2%（避免贴得太近触发）
	minDist := markPrice * 0.002
	if dist < minDist {
		dist = minDist
	}

	if side == "short" {
		return markPrice + dist
	}
	return markPrice - dist
}

// ExtraValidate 策略A的额外验证
// 用于禁止逆势开仓、Sharpe过低禁止开仓等
func ExtraValidate(d *Decision, ctx *Context) error {
	perf, ok := ctx.Performance.(*logger.PerformanceAnalysis)
	if ok && perf != nil {
		if d.Action == ActionOpenLong || d.Action == ActionOpenShort {
			if perf.SharpeRatio < -0.5 {
				return fmt.Errorf("sharpe 比率 %.2f 过差，禁止新开仓，只允许平仓/减仓/移动止损", perf.SharpeRatio)
			}
			if perf.CurrentLosingStreak >= 2 {
				return fmt.Errorf("当前已连续亏损 %d 笔，禁止新开仓，先处理已有仓位", perf.CurrentLosingStreak)
			}
		}
	}

	// 禁止逆势开仓逻辑
	if marketData, ok := ctx.MarketDataMap[d.Symbol]; ok {
		if d.Action == ActionOpenLong {
			if marketData.PriceChange1h < -1.0 && marketData.PriceChange4h < -2.0 {
				return fmt.Errorf("%s 在1h/4h级别显著下跌 (%.2f%% / %.2f%%)，禁止逆势开多",
					d.Symbol, marketData.PriceChange1h, marketData.PriceChange4h)
			}
		}
		if d.Action == ActionOpenShort {
			if marketData.PriceChange1h > 1.0 && marketData.PriceChange4h > 2.0 {
				return fmt.Errorf("%s 在1h/4h级别显著上涨 (%.2f%% / %.2f%%)，禁止逆势开空",
					d.Symbol, marketData.PriceChange1h, marketData.PriceChange4h)
			}
		}
	}

	return nil
}

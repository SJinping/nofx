package decision

import (
	"fmt"
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
		// ✅ 计算入场价：优先用当前市价（更贴近实盘），取不到才fallback到启发式
		entryPrice := 0.0
		if ctx != nil && ctx.MarketDataMap != nil {
			if md, ok := ctx.MarketDataMap[d.Symbol]; ok && md != nil && md.CurrentPrice > 0 {
				entryPrice = md.CurrentPrice
			}
		}
		if entryPrice <= 0 {
			if d.Action == ActionOpenLong {
				// 做多：入场价在止损和止盈之间
				entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // fallback：20%位置
			} else {
				// 做空：入场价在止损和止盈之间
				entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // fallback：20%位置
			}
		}

		var riskPercent, rewardPercent float64
		if d.Action == ActionOpenLong {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
		}

		// 基础有效性
		if riskPercent <= 0 || rewardPercent <= 0 {
			return fmt.Errorf("止损/止盈与入场价不匹配，无法计算RR(entry=%.4f sl=%.4f tp=%.4f)", entryPrice, d.StopLoss, d.TakeProfit)
		}

		// ✅ 净RR（扣除手续费+滑点，按杠杆换算为“保证金ROI口径”）
		// round-trip成本（按名义价值）：开+平
		taker := 0.0004
		slippage := 0.0005
		if ctx != nil {
			if ctx.AssumedTakerFeeRate >= 0 {
				taker = ctx.AssumedTakerFeeRate
			}
			if ctx.AssumedSlippageRate >= 0 {
				slippage = ctx.AssumedSlippageRate
			}
		}
		L := float64(maxInt(d.Leverage, 1))
		roundTripCostROIPct := 2.0 * (taker + slippage) * L * 100.0

		netRisk := riskPercent*L + roundTripCostROIPct
		netReward := rewardPercent*L - roundTripCostROIPct
		if netReward <= 0 {
			return fmt.Errorf("扣除成本后预期收益<=0（净收益=%.2f%% 成本=%.2f%%），拒绝开仓", netReward, roundTripCostROIPct)
		}
		riskRewardRatio := netReward / netRisk

		// 硬约束：净风险回报比必须≥minRiskReward
		if riskRewardRatio < minRiskReward {
			return fmt.Errorf("净风险回报比过低(%.2f:1)，必须≥%.2f:1 [净风险:%.2f%% 净收益:%.2f%% 成本:%.2f%%] [止损:%.2f 止盈:%.2f 入场:%.2f 杠杆:%dx]",
				riskRewardRatio, minRiskReward, netRisk, netReward, roundTripCostROIPct, d.StopLoss, d.TakeProfit, entryPrice, d.Leverage)
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
	badSharpe := perf.SharpeRatio < -0.65
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

// ===== 自动止盈（带状态&冷却，避免每周期重复触发）=====
// 规则（按“浮动盈亏百分比”触发，且对同一持仓分阶段只触发一次）：
// - Stage 0 且 pnl>=1%：部分平仓 50%（Stage->1）
// - Stage 1 且 pnl>=2%：部分平仓 30%（累计约80%，Stage->2）
// - pnl>=4%：全部平仓（无论Stage，直接退出）
//
// 冷却：同一持仓两次自动止盈动作之间至少间隔 15 分钟。
// 说明：
// - 仅基于 pos.UnrealizedPnLPct（该字段对多/空都为“盈利为正”口径）。
func generateAutoTakeProfitDecisions(ctx *Context) []Decision {
	var decisions []Decision

	// 安全：没有状态就不做自动止盈
	if ctx == nil || ctx.AutoState == nil || ctx.AutoState.TP == nil {
		return nil
	}

	const cooldownMs int64 = 15 * 60 * 1000 // 15分钟

	// 成本假设：用于把“价格涨跌%”换算成“净ROI%（保证金口径）”
	taker := 0.0004
	slippage := 0.0005
	if ctx.AssumedTakerFeeRate >= 0 {
		taker = ctx.AssumedTakerFeeRate
	}
	if ctx.AssumedSlippageRate >= 0 {
		slippage = ctx.AssumedSlippageRate
	}

	for _, pos := range ctx.Positions {
		pricePct := pos.UnrealizedPnLPct // 价格变化%（不含杠杆/成本）
		// 只对盈利持仓触发止盈（盈利为正）
		if pricePct <= 0 {
			continue
		}

		posKey := pos.Symbol + "_" + strings.ToLower(pos.Side)
		st := ctx.AutoState.TP[posKey]
		if st == nil {
			st = &AutoTPState{Stage: 0, LastActionTimeMs: 0, BaselineEntry: pos.EntryPrice, BaselineQty: pos.Quantity}
			ctx.AutoState.TP[posKey] = st
		}

		// 冷却检查
		if st.LastActionTimeMs > 0 {
			nowMs := time.Now().UnixMilli()
			if nowMs-st.LastActionTimeMs < cooldownMs {
				continue
			}
		}

		// 计算“净ROI%（保证金口径）”
		L := float64(maxInt(pos.Leverage, 1))
		roundTripCostROIPct := 2.0 * (taker + slippage) * L * 100.0 // 开+平两个操作，乘以2
		netROIPct := pricePct*L - roundTripCostROIPct               // 价格变化*杠杆-成本
		if netROIPct <= 0 {
			continue
		}

		// 最高档：直接全平
		if netROIPct >= 4.0 {
			closeAction := ActionCloseLong
			if strings.ToLower(pos.Side) == "short" {
				closeAction = ActionCloseShort
			}
			decisions = append(decisions, Decision{
				Symbol:         pos.Symbol,
				Action:         closeAction,
				Reasoning:      fmt.Sprintf("自动止盈触发：净ROI=%.2f%% ≥ 4%% → 全部平仓（价格变动=%.2f%%, 杠杆=%dx, 成本=%.2f%%）", netROIPct, pricePct, pos.Leverage, roundTripCostROIPct),
				Confidence:     100,
				DecisionSource: "auto_take_profit",
			})
			continue
		}

		// 分阶段止盈：只触发一次
		if st.Stage <= 0 && netROIPct >= 1.0 {
			decisions = append(decisions, Decision{
				Symbol:          pos.Symbol,
				Action:          ActionPartialClose,
				ClosePercentage: 50.0,
				Reasoning:       fmt.Sprintf("自动止盈触发：净ROI=%.2f%% ≥ 1%% 且 Stage=0 → 部分平仓50%%（价格变动=%.2f%%, 杠杆=%dx, 成本=%.2f%%）", netROIPct, pricePct, pos.Leverage, roundTripCostROIPct),
				Confidence:      100,
				DecisionSource:  "auto_take_profit",
			})
			continue
		}
		if st.Stage == 1 && netROIPct >= 2.0 {
			decisions = append(decisions, Decision{
				Symbol:          pos.Symbol,
				Action:          ActionPartialClose,
				ClosePercentage: 30.0,
				Reasoning:       fmt.Sprintf("自动止盈触发：净ROI=%.2f%% ≥ 2%% 且 Stage=1 → 部分平仓30%%（价格变动=%.2f%%, 杠杆=%dx, 成本=%.2f%%）", netROIPct, pricePct, pos.Leverage, roundTripCostROIPct),
				Confidence:      100,
				DecisionSource:  "auto_take_profit",
			})
			continue
		}
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

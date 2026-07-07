package decision

import (
	"fmt"
	"nofx/logger"
	"nofx/market"
	"nofx/stats"
	"strings"
	"time"
)

// stopLossMinDistance 计算“止损最小距离”（价格单位）。
// 目的：避免止损贴得太近导致 3m 噪声/滑点触发；并对山寨币（更高波动）使用更宽的最小距离。
//
// 规则：
// - BTC/ETH：minDist = max(majorMinPct * price, majorATRMult * ATR14, majorVolMult * price * volatilityPct)
// - Alt：    minDist = max(altMinPct * price, altATRMult * ATR14, altVolMult * price * volatilityPct)
//
// 参数通过 StopLossDistanceConfig 配置，零值时使用默认值。
func stopLossMinDistance(symbol string, currentPrice float64, md *market.Data, slCfg *StopLossDistanceConfig) float64 {
	if currentPrice <= 0 {
		return 0
	}

	// 零值配置 → 使用默认值
	cfg := DefaultStopLossDistanceConfig()
	if slCfg != nil && slCfg.AltMinPct > 0 {
		cfg = *slCfg
	}

	s := strings.ToUpper(strings.TrimSpace(symbol))
	isMajor := (s == "BTCUSDT" || s == "ETHUSDT")

	// 根据币种选择参数
	minPct := cfg.AltMinPct
	atrMult := cfg.AltATRMult
	volMult := cfg.AltVolMult
	if isMajor {
		minPct = cfg.MajorMinPct
		atrMult = cfg.MajorATRMult
		volMult = cfg.MajorVolMult
	}

	minDist := currentPrice * minPct
	if md != nil {
		if md.IntradayATR14 > 0 {
			atrDist := atrMult * md.IntradayATR14
			if atrDist > minDist {
				minDist = atrDist
			}
		}
		if md.VolatilityPct > 0 {
			// volatilityPct = ATR14(4h)/price；用其估算“最低应留出的波动空间”
			volDist := volMult * (currentPrice * md.VolatilityPct)
			if volDist > minDist {
				minDist = volDist
			}
		}
	}

	return minDist
}

const minAdjustedPositionSizeUSD = 50.0

func maybeAdjustOpenStopLossToMinDistance(d *Decision, entryPrice, minDist float64) (bool, error) {
	if d == nil || entryPrice <= 0 || minDist <= 0 {
		return false, nil
	}
	if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
		return false, nil
	}
	if d.PositionSizeUSD <= 0 {
		return false, nil
	}

	originalStopLoss := d.StopLoss
	originalPositionSize := d.PositionSizeUSD
	originalDist := 0.0
	adjustedStopLoss := originalStopLoss
	if d.Action == ActionOpenLong {
		originalDist = entryPrice - originalStopLoss
		if originalDist >= minDist {
			return false, nil
		}
		adjustedStopLoss = entryPrice - minDist
	} else {
		originalDist = originalStopLoss - entryPrice
		if originalDist >= minDist {
			return false, nil
		}
		adjustedStopLoss = entryPrice + minDist
	}
	if originalDist <= 0 {
		return false, fmt.Errorf("止损/止盈与入场价不匹配，无法自适应调整止损(entry=%.4f sl=%.4f)", entryPrice, originalStopLoss)
	}

	adjustedPositionSize := originalPositionSize * originalDist / minDist
	if adjustedPositionSize < minAdjustedPositionSizeUSD {
		return false, fmt.Errorf("止损过近且自适应缩仓后仓位过小：symbol=%s 原仓位=%.2f USDT 原距离=%.4f 调整后距离=%.4f 调整后仓位=%.2f USDT，必须≥%.2f USDT",
			d.Symbol, originalPositionSize, originalDist, minDist, adjustedPositionSize, minAdjustedPositionSizeUSD)
	}

	d.StopLoss = adjustedStopLoss
	d.PositionSizeUSD = adjustedPositionSize
	msg := fmt.Sprintf("止损距离自适应调整：SL %.4f→%.4f，仓位 %.2f→%.2f USDT，保持原始名义风险不变（距离 %.4f→%.4f）",
		originalStopLoss, d.StopLoss, originalPositionSize, d.PositionSizeUSD, originalDist, minDist)
	if strings.TrimSpace(d.Reasoning) == "" {
		d.Reasoning = msg
	} else {
		d.Reasoning = strings.TrimSpace(d.Reasoning) + "；" + msg
	}
	return true, nil
}

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

		// ✅ 额外硬约束：避免止损贴得太近导致3分钟噪声“瞬间触发”
		// - 需要知道当前价与持仓方向（从 ctx.Positions 推断）
		// - 只做“最小距离”校验，不限制你把止损放得更远（更宽松）的情况
		if ctx != nil && ctx.MarketDataMap != nil {
			if md, ok := ctx.MarketDataMap[d.Symbol]; ok && md != nil && md.CurrentPrice > 0 {
				currentPrice := md.CurrentPrice

				// 推断方向
				side := ""
				for _, p := range ctx.Positions {
					if p.Symbol == d.Symbol {
						side = strings.ToLower(strings.TrimSpace(p.Side))
						break
					}
				}

				// 计算最小距离：优先用 3m ATR14（价格单位），fallback 到 normalized_volatility
				// ✅ 分层：BTC/ETH vs Alt（Alt 给更宽的最小距离）
				minDist := stopLossMinDistance(d.Symbol, currentPrice, md, &ctx.StopLossDistance)

				if side == "long" {
					if d.NewStopLoss > currentPrice-minDist {
						return fmt.Errorf("update_stop_loss 过近：多仓 new_stop_loss=%.4f 距离当前价=%.4f 仅%.4f，必须≥%.4f（防止3分钟噪声扫损）",
							d.NewStopLoss, currentPrice, currentPrice-d.NewStopLoss, minDist)
					}
				} else if side == "short" {
					if d.NewStopLoss < currentPrice+minDist {
						return fmt.Errorf("update_stop_loss 过近：空仓 new_stop_loss=%.4f 距离当前价=%.4f 仅%.4f，必须≥%.4f（防止3分钟噪声扫损）",
							d.NewStopLoss, currentPrice, d.NewStopLoss-currentPrice, minDist)
					}
				}
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

		// ✅ 止损太近时自适应拉宽到最小距离，并按方案A缩小仓位以保持AI原始名义风险金额不变。
		// 之后仍会用调整后的 SL/仓位重新计算净RR、保证金和仓位下限；RR不达标则拒绝。
		if ctx != nil && ctx.MarketDataMap != nil {
			if md, ok := ctx.MarketDataMap[d.Symbol]; ok && md != nil && md.CurrentPrice > 0 && entryPrice > 0 {
				minDist := stopLossMinDistance(d.Symbol, entryPrice, md, &ctx.StopLossDistance)
				if _, err := maybeAdjustOpenStopLossToMinDistance(d, entryPrice, minDist); err != nil {
					return err
				}
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

		if err := validateRequiredMargin(d, ctx); err != nil {
			return err
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
	badSharpe := perf.SharpeRatio < -0.5
	longLosingStreak := perf.CurrentLosingStreak >= 3
	highMargin := ctx.Account.MarginUsedPct > 85

	// 无条件止损线：任何仓位浮亏超过 5% 直接全平（不需要 Sharpe/连亏前置条件）
	var unconditionalDecisions []Decision
	for _, pos := range ctx.Positions {
		if pos.UnrealizedPnLPct < -5 {
			closeAction := ActionCloseLong
			if strings.ToLower(pos.Side) == "short" {
				closeAction = ActionCloseShort
			}
			unconditionalDecisions = append(unconditionalDecisions, Decision{
				Symbol:         pos.Symbol,
				Action:         closeAction,
				Reasoning:      fmt.Sprintf("无条件止损：浮亏 %.2f%% 超过 -5%% 硬限制，强制全平", pos.UnrealizedPnLPct),
				Confidence:     100,
				DecisionSource: "auto_stop_loss",
			})
		}
	}
	if len(unconditionalDecisions) > 0 {
		return unconditionalDecisions
	}

	if !(badSharpe || longLosingStreak || highMargin) {
		return nil
	}

	var decisions []Decision

	for _, pos := range ctx.Positions {
		if pos.UnrealizedPnLPct < -2 || ctx.Account.MarginUsedPct > 90 {
			if pos.UnrealizedPnLPct < -6 {
				closeAction := ActionCloseLong
				if strings.ToLower(pos.Side) == "short" {
					closeAction = ActionCloseShort
				}
				decisions = append(decisions, Decision{
					Symbol: pos.Symbol,
					Action: closeAction,
					Reasoning: fmt.Sprintf("Sharpe=%.2f, 连亏=%d, 浮亏%.2f%% 超 -6%%，全部平仓止损",
						perf.SharpeRatio, perf.CurrentLosingStreak, pos.UnrealizedPnLPct),
					Confidence:     100,
					DecisionSource: "auto_stop_loss",
				})
			} else {
				pct := 50.0
				if pos.UnrealizedPnLPct < -4 {
					pct = 70.0
				}
				decisions = append(decisions, Decision{
					Symbol:          pos.Symbol,
					Action:          ActionPartialClose,
					ClosePercentage: pct,
					Reasoning: fmt.Sprintf("Sharpe=%.2f, 连亏=%d, 保证金=%.1f%%，自动减仓%.0f%%止损",
						perf.SharpeRatio, perf.CurrentLosingStreak, ctx.Account.MarginUsedPct, pct),
					Confidence:     100,
					DecisionSource: "auto_stop_loss",
				})
			}
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

	// 从配置读取止盈参数，零值使用默认
	tpCfg := ctx.AutoTakeProfit
	if tpCfg.Stage0Threshold <= 0 {
		tpCfg = DefaultAutoTakeProfitConfig()
	}

	cooldownMs := int64(tpCfg.CooldownMinutes) * 60 * 1000

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
		roundTripCostROIPct := 2.0 * (taker + slippage) * L * 100.0
		netROIPct := pricePct*L - roundTripCostROIPct
		if netROIPct <= 0 {
			continue
		}

		// 最高档：直接全平
		if netROIPct >= tpCfg.FullCloseThreshold {
			closeAction := ActionCloseLong
			if strings.ToLower(pos.Side) == "short" {
				closeAction = ActionCloseShort
			}
			decisions = append(decisions, Decision{
				Symbol:         pos.Symbol,
				Action:         closeAction,
				Reasoning:      fmt.Sprintf("自动止盈触发：净ROI=%.2f%% ≥ %.1f%% → 全部平仓（价格变动=%.2f%%, 杠杆=%dx, 成本=%.2f%%）", netROIPct, tpCfg.FullCloseThreshold, pricePct, pos.Leverage, roundTripCostROIPct),
				Confidence:     100,
				DecisionSource: "auto_take_profit",
			})
			continue
		}

		// 分阶段止盈：只触发一次
		if st.Stage <= 0 && netROIPct >= tpCfg.Stage0Threshold {
			decisions = append(decisions, Decision{
				Symbol:          pos.Symbol,
				Action:          ActionPartialClose,
				ClosePercentage: tpCfg.Stage0ClosePct,
				Reasoning:       fmt.Sprintf("自动止盈触发：净ROI=%.2f%% ≥ %.1f%% 且 Stage=0 → 部分平仓%.0f%%（价格变动=%.2f%%, 杠杆=%dx, 成本=%.2f%%）", netROIPct, tpCfg.Stage0Threshold, tpCfg.Stage0ClosePct, pricePct, pos.Leverage, roundTripCostROIPct),
				Confidence:      100,
				DecisionSource:  "auto_take_profit",
			})
			continue
		}
		if st.Stage == 1 && netROIPct >= tpCfg.Stage1Threshold {
			decisions = append(decisions, Decision{
				Symbol:          pos.Symbol,
				Action:          ActionPartialClose,
				ClosePercentage: tpCfg.Stage1ClosePct,
				Reasoning:       fmt.Sprintf("自动止盈触发：净ROI=%.2f%% ≥ %.1f%% 且 Stage=1 → 部分平仓%.0f%%（价格变动=%.2f%%, 杠杆=%dx, 成本=%.2f%%）", netROIPct, tpCfg.Stage1Threshold, tpCfg.Stage1ClosePct, pricePct, pos.Leverage, roundTripCostROIPct),
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
			if perf.CurrentLosingStreak >= 4 {
				return fmt.Errorf("当前已连续亏损 %d 笔，暂停新开仓，等待市场明确信号", perf.CurrentLosingStreak)
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

	// BTC 市场环境方向约束：BTC downtrend 时山寨币做多需 confidence >= 90；BTC uptrend 时做空需 >= 90
	if err := validateBTCDirectionConstraint(d, ctx); err != nil {
		return err
	}

	// 最低持仓时间硬约束：阻止过早平仓
	if err := validateMinHoldingTime(d, ctx); err != nil {
		return err
	}

	return nil
}

// validateBTCDirectionConstraint BTC 市场环境硬约束。
// - BTC 4h downtrend 时：山寨币（非 BTC/ETH）open_long 需 confidence >= 90
// - BTC 4h uptrend 时：任何 open_short 需 confidence >= 90
// 不完全禁止操作，而是大幅提高门槛；BTC/ETH 自身做空不受 downtrend 限制（它们有自身技术信号）。
func validateBTCDirectionConstraint(d *Decision, ctx *Context) error {
	if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
		return nil
	}

	btcStructure := GetBTCMarketStructure(ctx)
	if btcStructure == "" {
		return nil
	}

	sym := strings.ToUpper(strings.TrimSpace(d.Symbol))
	isMajor := sym == "BTCUSDT" || sym == "ETHUSDT"

	const requiredConfidence = 90

	if btcStructure == "downtrend" && d.Action == ActionOpenLong && !isMajor {
		if d.Confidence < requiredConfidence {
			return fmt.Errorf("BTC 4h 处于下跌趋势，山寨币 %s 做多需信心度 >= %d（当前 %d），请优先考虑做空或观望",
				d.Symbol, requiredConfidence, d.Confidence)
		}
	}

	if btcStructure == "uptrend" && d.Action == ActionOpenShort {
		if d.Confidence < requiredConfidence {
			return fmt.Errorf("BTC 4h 处于上涨趋势，%s 做空需信心度 >= %d（当前 %d），逆势做空风险极大",
				d.Symbol, requiredConfidence, d.Confidence)
		}
	}

	return nil
}

// validateMinHoldingTime 检查 LLM 平仓/减仓时是否满足最低持仓时间要求。
// 例外条件（放行）：
//  1. 止损价已被击穿
//  2. 浮亏超过 -8%
//  3. 距离强平价不足 15%
func validateMinHoldingTime(d *Decision, ctx *Context) error {
	minHold := ctx.MinHoldMinutes
	if minHold <= 0 {
		return nil
	}

	isCloseAction := d.Action == ActionCloseLong || d.Action == ActionCloseShort || d.Action == ActionPartialClose
	if !isCloseAction {
		return nil
	}

	var pos *PositionInfo
	for i := range ctx.Positions {
		if ctx.Positions[i].Symbol == d.Symbol {
			pos = &ctx.Positions[i]
			break
		}
	}
	if pos == nil {
		return nil
	}

	holdMs := time.Now().UnixMilli() - pos.UpdateTime
	if holdMs < 0 {
		holdMs = 0
	}
	holdMinutes := float64(holdMs) / 60000.0
	if holdMinutes >= float64(minHold) {
		return nil
	}

	// 例外 1: 浮亏严重（> -8%）
	if pos.UnrealizedPnLPct < -8.0 {
		return nil
	}

	// 例外 2: 止损价已被击穿
	if pos.EntryStopLoss > 0 && pos.MarkPrice > 0 {
		if pos.Side == "long" && pos.MarkPrice <= pos.EntryStopLoss {
			return nil
		}
		if pos.Side == "short" && pos.MarkPrice >= pos.EntryStopLoss {
			return nil
		}
	}

	// 例外 3: 距离强平价不足 15%
	if pos.LiquidationPrice > 0 && pos.MarkPrice > 0 {
		distPct := 0.0
		if pos.Side == "long" {
			distPct = (pos.MarkPrice - pos.LiquidationPrice) / pos.MarkPrice * 100
		} else {
			distPct = (pos.LiquidationPrice - pos.MarkPrice) / pos.MarkPrice * 100
		}
		if distPct < 15.0 {
			return nil
		}
	}

	return fmt.Errorf("持仓 %s 仅持有 %.1f 分钟，最低要求 %d 分钟，禁止过早平仓（浮亏 %.2f%% 未达阈值）",
		d.Symbol, holdMinutes, minHold, pos.UnrealizedPnLPct)
}

func validateRequiredMargin(d *Decision, ctx *Context) error {
	if ctx == nil || !ctx.MarginValidation.Enabled {
		return nil
	}
	if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
		return nil
	}
	if d.Leverage <= 0 || d.PositionSizeUSD <= 0 {
		return nil
	}

	available := ctx.Account.AvailableBalance
	if available <= 0 {
		return fmt.Errorf("可用保证金无效，无法进行保证金预检：available_balance=%.2f", available)
	}

	usagePct := ctx.MarginValidation.AvailableBalanceUsagePct
	if usagePct <= 0 || usagePct > 100 {
		usagePct = 95.0
	}
	feeBufferPct := ctx.MarginValidation.FeeBufferPct
	if feeBufferPct < 0 {
		feeBufferPct = 0
	}
	if feeBufferPct == 0 {
		feeBufferPct = 0.1
	}

	requiredMargin := d.PositionSizeUSD / float64(d.Leverage)
	feeBuffer := d.PositionSizeUSD * feeBufferPct / 100.0
	requiredTotal := requiredMargin + feeBuffer
	allowed := available * usagePct / 100.0
	if requiredTotal > allowed {
		return fmt.Errorf("可用保证金不足：%s %s 仓位 %.2f USDT / %dx 需保证金 %.2f + buffer %.2f = %.2f，可用 %.2f，允许使用 %.1f%%=%.2f",
			d.Symbol, d.Action, d.PositionSizeUSD, d.Leverage, requiredMargin, feeBuffer, requiredTotal, available, usagePct, allowed)
	}
	return nil
}

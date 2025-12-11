package decision

import (
	"strings"
	"time"
)

// VirtualPosition 用于新策略回测的虚拟持仓
type VirtualPosition struct {
	Symbol     string
	Side       string
	EntryPrice float64
	Quantity   float64
	Leverage   int
	OpenTime   time.Time // 开仓时间
}

// TradeRecord 交易记录
type TradeRecord struct {
	Cycle        int       `json:"cycle"`         // 周期编号
	Symbol       string    `json:"symbol"`        // 币种
	Side         string    `json:"side"`          // long/short
	Action       string    `json:"action"`        // close_long/close_short/partial_close
	EntryPrice   float64   `json:"entry_price"`   // 入场价
	ExitPrice    float64   `json:"exit_price"`    // 出场价
	Quantity     float64   `json:"quantity"`      // 交易数量
	Leverage     int       `json:"leverage"`      // 杠杆
	PnL          float64   `json:"pnl"`           // 盈亏金额（USDT）
	PnLPercent   float64   `json:"pnl_percent"`   // 盈亏百分比
	IsWin        bool      `json:"is_win"`        // 是否盈利
	HoldTime     string    `json:"hold_time"`     // 持仓时长
	CloseTime    time.Time `json:"close_time"`    // 平仓时间
	PartialClose bool      `json:"partial_close"` // 是否部分平仓
	ClosePercent float64   `json:"close_percent"` // 平仓百分比（部分平仓时）
	RemainingQty float64   `json:"remaining_qty"` // 剩余数量（部分平仓时）
}

// VirtualAccount 用于新策略回测的虚拟账户
type VirtualAccount struct {
	Equity    float64
	Positions map[string]*VirtualPosition // key: symbol_side
}

// extractCurrentPrices 从市场数据文本中提取各币种 current_price
func extractCurrentPrices(text string) map[string]float64 {
	prices := make(map[string]float64)
	if text == "" {
		return prices
	}

	lines := strings.Split(text, "\n")
	var currentSymbol string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "### ") {
			// 形如: "### 1. BTCUSDT"
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				currentSymbol = strings.TrimSpace(parts[2])
			}
			continue
		}
		if currentSymbol == "" {
			continue
		}
		if strings.HasPrefix(line, "current_price") {
			// 形如: current_price = 92887.80, ...
			eqIdx := strings.Index(line, "=")
			if eqIdx <= 0 {
				continue
			}
			rest := strings.TrimSpace(line[eqIdx+1:])
			// 去掉后面的逗号和内容
			if commaIdx := strings.Index(rest, ","); commaIdx > 0 {
				rest = rest[:commaIdx]
			}
			v := parseFloat(rest)
			if v > 0 {
				prices[currentSymbol] = v
			}
		}
	}

	return prices
}

// applyDecisions 在虚拟账户上执行新策略的决策，返回本周期开平仓次数、赢的次数和交易记录
func applyDecisions(va *VirtualAccount, decisions []Decision, prices map[string]float64, cycleNum int) (int, int, []*TradeRecord) {
	if va == nil {
		return 0, 0, nil
	}

	trades := 0
	wins := 0
	tradeRecords := make([]*TradeRecord, 0)

	closeTime := time.Now()

	for _, d := range decisions {
		price, ok := prices[d.Symbol]
		if !ok || price <= 0 {
			// 找不到价格就跳过该决策
			continue
		}

		keyLong := d.Symbol + "_long"
		keyShort := d.Symbol + "_short"

		switch d.Action {
		case ActionOpenLong:
			if d.PositionSizeUSD <= 0 {
				continue
			}
			qty := d.PositionSizeUSD / price
			va.Positions[keyLong] = &VirtualPosition{
				Symbol:     d.Symbol,
				Side:       "long",
				EntryPrice: price,
				Quantity:   qty,
				Leverage:   maxInt(d.Leverage, 1),
				OpenTime:   closeTime,
			}

		case ActionOpenShort:
			if d.PositionSizeUSD <= 0 {
				continue
			}
			qty := d.PositionSizeUSD / price
			va.Positions[keyShort] = &VirtualPosition{
				Symbol:     d.Symbol,
				Side:       "short",
				EntryPrice: price,
				Quantity:   qty,
				Leverage:   maxInt(d.Leverage, 1),
				OpenTime:   closeTime,
			}

		case ActionCloseLong:
			if pos, ok := va.Positions[keyLong]; ok && pos.EntryPrice > 0 {
				trades++
				pct := (price - pos.EntryPrice) / pos.EntryPrice
				pnl := pos.EntryPrice * pos.Quantity * pct * float64(pos.Leverage)
				isWin := pnl > 0
				if isWin {
					wins++
				}
				va.Equity += pnl

				// 记录交易详情
				holdTime := formatDuration(closeTime.Sub(pos.OpenTime))
				tradeRecords = append(tradeRecords, &TradeRecord{
					Cycle:        cycleNum,
					Symbol:       d.Symbol,
					Side:         "long",
					Action:       "close_long",
					EntryPrice:   pos.EntryPrice,
					ExitPrice:    price,
					Quantity:     pos.Quantity,
					Leverage:     pos.Leverage,
					PnL:          pnl,
					PnLPercent:   pct * 100,
					IsWin:        isWin,
					HoldTime:     holdTime,
					CloseTime:    closeTime,
					PartialClose: false,
				})

				delete(va.Positions, keyLong)
			}

		case ActionCloseShort:
			if pos, ok := va.Positions[keyShort]; ok && pos.EntryPrice > 0 {
				trades++
				pct := (pos.EntryPrice - price) / pos.EntryPrice
				pnl := pos.EntryPrice * pos.Quantity * pct * float64(pos.Leverage)
				isWin := pnl > 0
				if isWin {
					wins++
				}
				va.Equity += pnl

				// 记录交易详情
				holdTime := formatDuration(closeTime.Sub(pos.OpenTime))
				tradeRecords = append(tradeRecords, &TradeRecord{
					Cycle:        cycleNum,
					Symbol:       d.Symbol,
					Side:         "short",
					Action:       "close_short",
					EntryPrice:   pos.EntryPrice,
					ExitPrice:    price,
					Quantity:     pos.Quantity,
					Leverage:     pos.Leverage,
					PnL:          pnl,
					PnLPercent:   pct * 100,
					IsWin:        isWin,
					HoldTime:     holdTime,
					CloseTime:    closeTime,
					PartialClose: false,
				})

				delete(va.Positions, keyShort)
			}

		case ActionPartialClose:
			// 部分平仓：按百分比平掉当前持仓的一部分，并实时记入权益
			if d.ClosePercentage <= 0 {
				continue
			}
			closePct := d.ClosePercentage / 100.0
			if closePct <= 0 || closePct > 1 {
				continue
			}

			// 优先找多仓，其次空仓（和实盘逻辑类似：一个symbol只会有一个方向的仓位）
			if pos, ok := va.Positions[keyLong]; ok && pos.EntryPrice > 0 && pos.Quantity > 0 {
				closeQty := pos.Quantity * closePct
				if closeQty <= 0 {
					continue
				}
				trades++
				pct := (price - pos.EntryPrice) / pos.EntryPrice
				pnl := pos.EntryPrice * closeQty * pct * float64(pos.Leverage)
				isWin := pnl > 0
				if isWin {
					wins++
				}
				va.Equity += pnl

				remainingQty := pos.Quantity - closeQty

				// 记录部分平仓详情
				holdTime := formatDuration(closeTime.Sub(pos.OpenTime))
				tradeRecords = append(tradeRecords, &TradeRecord{
					Cycle:        cycleNum,
					Symbol:       d.Symbol,
					Side:         "long",
					Action:       "partial_close",
					EntryPrice:   pos.EntryPrice,
					ExitPrice:    price,
					Quantity:     closeQty,
					Leverage:     pos.Leverage,
					PnL:          pnl,
					PnLPercent:   pct * 100,
					IsWin:        isWin,
					HoldTime:     holdTime,
					CloseTime:    closeTime,
					PartialClose: true,
					ClosePercent: d.ClosePercentage,
					RemainingQty: remainingQty,
				})

				pos.Quantity = remainingQty
				if pos.Quantity <= 0 {
					delete(va.Positions, keyLong)
				}
				continue
			}

			if pos, ok := va.Positions[keyShort]; ok && pos.EntryPrice > 0 && pos.Quantity > 0 {
				closeQty := pos.Quantity * closePct
				if closeQty <= 0 {
					continue
				}
				trades++
				pct := (pos.EntryPrice - price) / pos.EntryPrice
				pnl := pos.EntryPrice * closeQty * pct * float64(pos.Leverage)
				isWin := pnl > 0
				if isWin {
					wins++
				}
				va.Equity += pnl

				remainingQty := pos.Quantity - closeQty

				// 记录部分平仓详情
				holdTime := formatDuration(closeTime.Sub(pos.OpenTime))
				tradeRecords = append(tradeRecords, &TradeRecord{
					Cycle:        cycleNum,
					Symbol:       d.Symbol,
					Side:         "short",
					Action:       "partial_close",
					EntryPrice:   pos.EntryPrice,
					ExitPrice:    price,
					Quantity:     closeQty,
					Leverage:     pos.Leverage,
					PnL:          pnl,
					PnLPercent:   pct * 100,
					IsWin:        isWin,
					HoldTime:     holdTime,
					CloseTime:    closeTime,
					PartialClose: true,
					ClosePercent: d.ClosePercentage,
					RemainingQty: remainingQty,
				})

				pos.Quantity = remainingQty
				if pos.Quantity <= 0 {
					delete(va.Positions, keyShort)
				}
			}

		// 其他动作（update_stop_loss / update_take_profit / hold / wait）
		// 不直接影响本周期的已实现盈亏，这里保持为no-op
		default:
			continue
		}
	}

	return trades, wins, tradeRecords
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

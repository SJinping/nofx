package trader

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// PaperTradingConfig 纸上交易配置
type PaperTradingConfig struct {
	EnableFees     bool    // 是否启用手续费
	EnableSlippage bool    // 是否启用滑点
	TakerFeeRate   float64 // Taker手续费率（默认0.0004，即0.04%）
	MakerFeeRate   float64 // Maker手续费率（默认0.0002，即0.02%）
	SlippageRate   float64 // 滑点比例（默认0.0005，即0.05%）
}

// DefaultPaperTradingConfig 返回默认配置（币安标准费率）
func DefaultPaperTradingConfig() *PaperTradingConfig {
	return &PaperTradingConfig{
		EnableFees:     true,
		EnableSlippage: true,
		TakerFeeRate:   0.0004, // 0.04% - 币安Taker费率
		MakerFeeRate:   0.0002, // 0.02% - 币安Maker费率
		SlippageRate:   0.0005, // 0.05% - 滑点
	}
}

// PaperPosition 纸上交易持仓
type PaperPosition struct {
	Symbol        string    `json:"symbol"`
	Side          string    `json:"side"` // "long" or "short"
	Quantity      float64   `json:"quantity"`
	EntryPrice    float64   `json:"entry_price"`
	Leverage      int       `json:"leverage"`
	OpenTime      time.Time `json:"open_time"`
	UnrealizedPnL float64   `json:"unrealized_pnl"`
	MarginUsed    float64   `json:"margin_used"`
}

// PaperTrader 纸上交易器（模拟交易，不实际下单）
type PaperTrader struct {
	realTrader     Trader                    // 真实交易器（用于获取市场数据）
	initialBalance float64                   // 初始余额
	balance        float64                   // 当前余额
	positions      map[string]*PaperPosition // 持仓 (symbol_side -> position)
	mutex          sync.RWMutex              // 并发保护
	traderName     string                    // 交易器名称
	config         *PaperTradingConfig       // 交易配置
	totalFeesPaid  float64                   // 累计支付的手续费
}

// NewPaperTrader 创建纸上交易器（使用默认配置）
func NewPaperTrader(realTrader Trader, initialBalance float64, traderName string) *PaperTrader {
	return NewPaperTraderWithConfig(realTrader, initialBalance, traderName, DefaultPaperTradingConfig())
}

// NewPaperTraderWithConfig 创建带自定义配置的纸上交易器
func NewPaperTraderWithConfig(realTrader Trader, initialBalance float64, traderName string, config *PaperTradingConfig) *PaperTrader {
	if config == nil {
		config = DefaultPaperTradingConfig()
	}
	return &PaperTrader{
		realTrader:     realTrader,
		initialBalance: initialBalance,
		balance:        initialBalance,
		positions:      make(map[string]*PaperPosition),
		traderName:     traderName,
		config:         config,
		totalFeesPaid:  0,
	}
}

// GetBalance 获取模拟账户余额
func (pt *PaperTrader) GetBalance() (map[string]interface{}, error) {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()

	// 计算总权益（余额 + 未实现盈亏）
	totalUnrealizedPnL := 0.0
	totalMarginUsed := 0.0

	for _, pos := range pt.positions {
		// 获取当前市价计算未实现盈亏
		currentPrice, err := pt.realTrader.GetMarketPrice(pos.Symbol)
		if err != nil {
			log.Printf("⚠️  [%s] 获取 %s 市价失败: %v", pt.traderName, pos.Symbol, err)
			continue
		}

		// 计算未实现盈亏
		pnl := pt.calculateUnrealizedPnL(pos, currentPrice)
		pos.UnrealizedPnL = pnl
		totalUnrealizedPnL += pnl
		totalMarginUsed += pos.MarginUsed
	}

	totalEquity := pt.balance + totalUnrealizedPnL
	availableBalance := pt.balance - totalMarginUsed
	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	return map[string]interface{}{
		"totalWalletBalance":         totalEquity,
		"totalUnrealizedProfit":      totalUnrealizedPnL,
		"totalMarginBalance":         totalEquity,
		"availableBalance":           availableBalance,
		"totalPositionInitialMargin": totalMarginUsed,
		"marginUsedPct":              marginUsedPct,
		"paper_trading_mode":         true, // 标识这是纸上交易
		"totalFeesPaid":              pt.totalFeesPaid,
	}, nil
}

// GetPositions 获取模拟持仓
func (pt *PaperTrader) GetPositions() ([]map[string]interface{}, error) {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()

	var positions []map[string]interface{}

	for _, pos := range pt.positions {
		// 获取当前市价
		currentPrice, err := pt.realTrader.GetMarketPrice(pos.Symbol)
		if err != nil {
			log.Printf("⚠️  [%s] 获取 %s 市价失败: %v", pt.traderName, pos.Symbol, err)
			// 网络失败时使用入场价兜底，避免丢失持仓导致上层崩溃
			// 但使用入场价会导致净值曲线波动很大
			// TODO: 如果使用市场价失败，使用上一次的市场价。但是太麻烦了，先勉强用
			currentPrice = pos.EntryPrice
		}

		// 计算未实现盈亏
		pnl := pt.calculateUnrealizedPnL(pos, currentPrice)

		position := map[string]interface{}{
			"symbol":           pos.Symbol,            // string
			"side":             pos.Side,              // string: "long"/"short"
			"positionAmt":      pos.Quantity,          // float64
			"entryPrice":       pos.EntryPrice,        // float64
			"markPrice":        currentPrice,          // float64
			"unRealizedProfit": pnl,                   // float64
			"leverage":         float64(pos.Leverage), // float64
			"liquidationPrice": 0.0,                   // float64（纸上交易无强平价）
			"updateTime":       pos.OpenTime.UnixMilli(),
			"paper_trading":    true, // 标识这是纸上交易
		}
		positions = append(positions, position)
	}

	return positions, nil
}

// OpenLong 开多仓（模拟）
func (pt *PaperTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return pt.openPosition(symbol, "long", quantity, leverage)
}

// OpenShort 开空仓（模拟）
func (pt *PaperTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return pt.openPosition(symbol, "short", quantity, leverage)
}

// openPosition 开仓（内部方法）
func (pt *PaperTrader) openPosition(symbol, side string, quantity float64, leverage int) (map[string]interface{}, error) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	// 获取当前市价
	marketPrice, err := pt.realTrader.GetMarketPrice(symbol)
	if err != nil {
		return nil, fmt.Errorf("获取市价失败: %w", err)
	}

	// 应用滑点（开多/开空都是买入操作）
	executionPrice := pt.applySlippage(marketPrice, true)

	// 计算名义价值和手续费
	notionalValue := quantity * executionPrice
	fee := pt.calculateFee(notionalValue)

	// 计算保证金
	marginUsed := notionalValue / float64(leverage)

	// 计算当前已占用保证金（累计）
	currentTotalMargin := 0.0
	for _, p := range pt.positions {
		currentTotalMargin += p.MarginUsed
	}

	// 校验：累计占用 + 新增占用 + 手续费 不能超过钱包余额
	totalCost := marginUsed + fee
	if currentTotalMargin+totalCost > pt.balance {
		return nil, fmt.Errorf("余额不足，需要保证金: %.2f, 手续费: %.2f, 已占用: %.2f, 可用钱包: %.2f",
			marginUsed, fee, currentTotalMargin, pt.balance)
	}

	// 扣除手续费
	if fee > 0 {
		pt.balance -= fee
		pt.totalFeesPaid += fee
	}

	// 创建持仓
	posKey := fmt.Sprintf("%s_%s", symbol, side)

	// 如果已有同方向持仓，累加数量
	if existingPos, exists := pt.positions[posKey]; exists {
		// 计算新的平均入场价
		totalValue := existingPos.Quantity*existingPos.EntryPrice + quantity*executionPrice
		totalQuantity := existingPos.Quantity + quantity
		newEntryPrice := totalValue / totalQuantity

		existingPos.Quantity = totalQuantity
		existingPos.EntryPrice = newEntryPrice
		existingPos.MarginUsed += marginUsed

		if fee > 0 {
			log.Printf("📈 [%s] 纸上交易 - 加仓 %s %s: 数量%.4f, 新均价%.4f, 杠杆%dx, 手续费%.4f USDT",
				pt.traderName, symbol, strings.ToUpper(side), quantity, newEntryPrice, leverage, fee)
		} else {
			log.Printf("📈 [%s] 纸上交易 - 加仓 %s %s: 数量%.4f, 新均价%.4f, 杠杆%dx",
				pt.traderName, symbol, strings.ToUpper(side), quantity, newEntryPrice, leverage)
		}
	} else {
		// 新开仓
		pt.positions[posKey] = &PaperPosition{
			Symbol:     symbol,
			Side:       side,
			Quantity:   quantity,
			EntryPrice: executionPrice,
			Leverage:   leverage,
			OpenTime:   time.Now(),
			MarginUsed: marginUsed,
		}

		if fee > 0 {
			log.Printf("📈 [%s] 纸上交易 - 开仓 %s %s: 数量%.4f, 价格%.4f, 杠杆%dx, 手续费%.4f USDT",
				pt.traderName, symbol, strings.ToUpper(side), quantity, executionPrice, leverage, fee)
		} else {
			log.Printf("📈 [%s] 纸上交易 - 开仓 %s %s: 数量%.4f, 价格%.4f, 杠杆%dx",
				pt.traderName, symbol, strings.ToUpper(side), quantity, executionPrice, leverage)
		}
	}

	// 注意：不从 pt.balance 扣除保证金（避免双扣；可用余额通过 GetBalance 计算体现）

	return map[string]interface{}{
		"symbol":        symbol,
		"side":          strings.ToUpper(side),
		"type":          "MARKET",
		"quantity":      fmt.Sprintf("%.8f", quantity),
		"price":         fmt.Sprintf("%.8f", executionPrice),
		"status":        "FILLED",
		"paper_trading": true,
		"fee":           fmt.Sprintf("%.8f", fee),
	}, nil
}

// CloseLong 平多仓（模拟）
func (pt *PaperTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return pt.closePosition(symbol, "long", quantity)
}

// CloseShort 平空仓（模拟）
func (pt *PaperTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return pt.closePosition(symbol, "short", quantity)
}

// closePosition 平仓（内部方法）
func (pt *PaperTrader) closePosition(symbol, side string, quantity float64) (map[string]interface{}, error) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()

	posKey := fmt.Sprintf("%s_%s", symbol, side)
	pos, exists := pt.positions[posKey]
	if !exists {
		return nil, fmt.Errorf("没有找到 %s %s 持仓", symbol, side)
	}

	// 获取当前市价
	marketPrice, err := pt.realTrader.GetMarketPrice(symbol)
	if err != nil {
		return nil, fmt.Errorf("获取市价失败: %w", err)
	}

	// 如果quantity为0，平全部仓位
	if quantity == 0 {
		quantity = pos.Quantity
	}

	// 检查平仓数量
	if quantity > pos.Quantity {
		return nil, fmt.Errorf("平仓数量(%.4f)超过持仓数量(%.4f)", quantity, pos.Quantity)
	}

	// 应用滑点（平仓是卖出操作）
	executionPrice := pt.applySlippage(marketPrice, false)

	// 计算名义价值和手续费
	notionalValue := quantity * executionPrice
	fee := pt.calculateFee(notionalValue)

	// 计算盈亏（使用应用滑点后的执行价格）
	pnl := pt.calculateRealizedPnL(pos, executionPrice, quantity)

	// 扣除手续费
	netPnl := pnl - fee
	if fee > 0 {
		pt.totalFeesPaid += fee
	}

	// 计算释放的保证金
	marginReleased := (quantity / pos.Quantity) * pos.MarginUsed

	// 更新钱包余额：加入净盈亏（盈亏 - 手续费）；保证金释放不会改变钱包，只影响可用
	pt.balance += netPnl

	if fee > 0 {
		log.Printf("📉 [%s] 纸上交易 - 平仓 %s %s: 数量%.4f, 价格%.4f, 盈亏%.2f USDT, 手续费%.4f USDT, 净盈亏%.2f USDT",
			pt.traderName, symbol, strings.ToUpper(side), quantity, executionPrice, pnl, fee, netPnl)
	} else {
		log.Printf("📉 [%s] 纸上交易 - 平仓 %s %s: 数量%.4f, 价格%.4f, 盈亏%.2f USDT",
			pt.traderName, symbol, strings.ToUpper(side), quantity, executionPrice, pnl)
	}

	// 更新或删除持仓
	if quantity >= pos.Quantity {
		// 全部平仓
		delete(pt.positions, posKey)
	} else {
		// 部分平仓
		pos.Quantity -= quantity
		pos.MarginUsed -= marginReleased
	}

	return map[string]interface{}{
		"symbol":        symbol,
		"side":          "SELL", // 平仓都是卖出
		"type":          "MARKET",
		"quantity":      fmt.Sprintf("%.8f", quantity),
		"price":         fmt.Sprintf("%.8f", executionPrice),
		"status":        "FILLED",
		"realizedPnl":   fmt.Sprintf("%.8f", netPnl),
		"fee":           fmt.Sprintf("%.8f", fee),
		"paper_trading": true,
	}, nil
}

// calculateUnrealizedPnL 计算未实现盈亏
func (pt *PaperTrader) calculateUnrealizedPnL(pos *PaperPosition, currentPrice float64) float64 {
	if pos.Side == "long" {
		return (currentPrice - pos.EntryPrice) * pos.Quantity
	} else {
		return (pos.EntryPrice - currentPrice) * pos.Quantity
	}
}

// calculateRealizedPnL 计算已实现盈亏
func (pt *PaperTrader) calculateRealizedPnL(pos *PaperPosition, exitPrice float64, quantity float64) float64 {
	if pos.Side == "long" {
		return (exitPrice - pos.EntryPrice) * quantity
	} else {
		return (pos.EntryPrice - exitPrice) * quantity
	}
}

// applySlippage 应用滑点到价格
// isBuy: true表示买入（开多/平空），false表示卖出（开空/平多）
func (pt *PaperTrader) applySlippage(price float64, isBuy bool) float64 {
	if !pt.config.EnableSlippage {
		return price
	}

	// 滑点在0到配置的最大滑点之间随机
	slippage := rand.Float64() * pt.config.SlippageRate

	if isBuy {
		// 买入时价格更高（不利滑点）
		return price * (1 + slippage)
	} else {
		// 卖出时价格更低（不利滑点）
		return price * (1 - slippage)
	}
}

// calculateFee 计算手续费（使用Taker费率，因为纸上交易使用市价单）
func (pt *PaperTrader) calculateFee(notionalValue float64) float64 {
	if !pt.config.EnableFees {
		return 0
	}
	return notionalValue * pt.config.TakerFeeRate
}

// 以下方法直接委托给真实交易器（用于获取市场数据）

func (pt *PaperTrader) SetLeverage(symbol string, leverage int) error {
	log.Printf("📝 [%s] 纸上交易 - 设置杠杆 %s: %dx (仅记录)", pt.traderName, symbol, leverage)
	return nil // 纸上交易不需要实际设置杠杆
}

func (pt *PaperTrader) GetMarketPrice(symbol string) (float64, error) {
	return pt.realTrader.GetMarketPrice(symbol)
}

func (pt *PaperTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	log.Printf("📝 [%s] 纸上交易 - 设置止损 %s %s: 数量%.4f, 止损价%.4f (仅记录)",
		pt.traderName, symbol, positionSide, quantity, stopPrice)
	return nil // 纸上交易不实际设置止损
}

func (pt *PaperTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	log.Printf("📝 [%s] 纸上交易 - 设置止盈 %s %s: 数量%.4f, 止盈价%.4f (仅记录)",
		pt.traderName, symbol, positionSide, quantity, takeProfitPrice)
	return nil // 纸上交易不实际设置止盈
}

func (pt *PaperTrader) CancelAllOrders(symbol string) error {
	log.Printf("📝 [%s] 纸上交易 - 取消所有订单 %s (仅记录)", pt.traderName, symbol)
	return nil // 纸上交易不需要取消订单
}

func (pt *PaperTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	return pt.realTrader.FormatQuantity(symbol, quantity)
}

// GetConfig 获取纸上交易配置
func (pt *PaperTrader) GetConfig() *PaperTradingConfig {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()
	return pt.config
}

// SetConfig 设置纸上交易配置
func (pt *PaperTrader) SetConfig(config *PaperTradingConfig) {
	pt.mutex.Lock()
	defer pt.mutex.Unlock()
	if config != nil {
		pt.config = config
	}
}

// GetTotalFeesPaid 获取累计支付的手续费
func (pt *PaperTrader) GetTotalFeesPaid() float64 {
	pt.mutex.RLock()
	defer pt.mutex.RUnlock()
	return pt.totalFeesPaid
}

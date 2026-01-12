package trader

import "time"

// OrderRecord 标准化订单记录（用于交易所订单历史展示/复盘）
// 说明：这是“订单”维度的数据（不是成交/Fills），字段尽量贴近 Binance Futures /fapi/v1/allOrders。
type OrderRecord struct {
	Symbol       string `json:"symbol"`
	OrderID      int64  `json:"order_id"`
	ClientOrder  string `json:"client_order_id,omitempty"`
	Side         string `json:"side"`          // BUY/SELL
	PositionSide string `json:"position_side"` // LONG/SHORT/BOTH
	Type         string `json:"type"`          // MARKET/LIMIT/...
	Status       string `json:"status"`        // NEW/FILLED/CANCELED/...
	ReduceOnly   bool   `json:"reduce_only"`

	Price       float64   `json:"price"`         // 委托价（市价单通常为0）
	StopPrice   float64   `json:"stop_price"`    // 条件单触发价（无则0）
	AvgPrice    float64   `json:"avg_price"`     // 平均成交价（无成交则0）
	OrigQty     float64   `json:"orig_qty"`      // 委托数量
	ExecutedQty float64   `json:"executed_qty"`  // 已成交数量
	CumQuote    float64   `json:"cum_quote"`     // 成交额（quote）
	TimeInForce string    `json:"time_in_force"` // GTC/IOC/...
	WorkingType string    `json:"working_type"`  // MARK_PRICE/CONTRACT_PRICE/...
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Trader 交易器统一接口
// 支持多个交易平台（币安、Hyperliquid等）
type Trader interface {
	// GetBalance 获取账户余额
	GetBalance() (map[string]interface{}, error)

	// GetPositions 获取所有持仓
	GetPositions() ([]map[string]interface{}, error)

	// OpenLong 开多仓
	OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error)

	// OpenShort 开空仓
	OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error)

	// CloseLong 平多仓（quantity=0表示全部平仓）
	CloseLong(symbol string, quantity float64) (map[string]interface{}, error)

	// CloseShort 平空仓（quantity=0表示全部平仓）
	CloseShort(symbol string, quantity float64) (map[string]interface{}, error)

	// SetLeverage 设置杠杆
	SetLeverage(symbol string, leverage int) error

	// GetMarketPrice 获取市场价格
	GetMarketPrice(symbol string) (float64, error)

	// SetStopLoss 设置止损单
	SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error

	// SetTakeProfit 设置止盈单
	SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error

	// CancelAllOrders 取消该币种的所有挂单
	CancelAllOrders(symbol string) error

	// FormatQuantity 格式化数量到正确的精度
	FormatQuantity(symbol string, quantity float64) (string, error)

	// ListOrders 获取某币种在时间范围内的订单历史（平台不支持可返回 error）
	// startTimeMs/endTimeMs 为毫秒时间戳（UnixMilli），limit 为最大返回数量（0 表示使用默认值）
	ListOrders(symbol string, startTimeMs, endTimeMs int64, limit int) ([]OrderRecord, error)
}

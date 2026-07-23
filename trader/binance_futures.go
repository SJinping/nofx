package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	xproxy "golang.org/x/net/proxy"
)

// FuturesTrader 币安合约交易器
type FuturesTrader struct {
	client *futures.Client
}

func isReduceOnlyNotRequiredErr(err error) bool {
	if err == nil {
		return false
	}
	// go-binance 的错误通常长这样：
	// <APIError> code=-1106, msg=Parameter 'reduceonly' sent when not required.
	msg := err.Error()
	if strings.Contains(msg, "code=-1106") {
		return true
	}
	// 兜底：避免不同大小写/格式差异
	msgLower := strings.ToLower(msg)
	return strings.Contains(msgLower, "reduceonly") && strings.Contains(msgLower, "not required")
}

// cancelOpenAlgoOrdersPrecise 精准取消指定 symbol 下、指定 positionSide + 指定 orderTypes 的未成交 algo 条件单。
// 这样不会误伤同 symbol 的另一边仓位（Hedge Mode）或其它类型条件单。
func (t *FuturesTrader) cancelOpenAlgoOrdersPrecise(symbol string, posSide futures.PositionSideType, orderTypes map[futures.AlgoOrderType]bool) {
	if symbol == "" || len(orderTypes) == 0 {
		return
	}

	orders, err := t.client.NewListOpenAlgoOrdersService().
		AlgoType(futures.OrderAlgoTypeConditional).
		Symbol(symbol).
		Do(context.Background())
	if err != nil {
		// best-effort：取消失败不阻断主流程（否则会导致无法更新 SL/TP）
		log.Printf("⚠️ 获取 open algo orders 失败（跳过精准取消）: %v", err)
		return
	}

	for _, o := range orders {
		// 只处理该方向 + 目标类型
		if o.PositionSide != posSide {
			continue
		}
		if !orderTypes[o.OrderType] {
			continue
		}

		// 优先用 algoId 取消
		if o.AlgoId == 0 {
			continue
		}
		if _, err := t.client.NewCancelAlgoOrderService().AlgoID(o.AlgoId).Do(context.Background()); err != nil {
			// best-effort：单个取消失败继续处理其它
			log.Printf("⚠️ 取消 algo 条件单失败: symbol=%s posSide=%s orderType=%s algoId=%d err=%v",
				symbol, string(posSide), string(o.OrderType), o.AlgoId, err)
		}
	}
}

// V2RayConfig V2Ray配置结构
type V2RayConfig struct {
	Inbounds []struct {
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	} `json:"inbounds"`
}

// getV2RayProxyPort 读取V2Ray配置获取代理端口
func getV2RayProxyPort() int {
	configPath := "/usr/local/etc/v2ray/config.json"

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return 0
	}

	var config V2RayConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return 0
	}

	for _, inbound := range config.Inbounds {
		if inbound.Protocol == "http" {
			return inbound.Port
		}
	}

	return 0
}

// getV2RaySocksPort 读取V2Ray配置获取SOCKS5端口
func getV2RaySocksPort() int {
	configPath := "/usr/local/etc/v2ray/config.json"

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return 0
	}

	var config V2RayConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return 0
	}

	for _, inbound := range config.Inbounds {
		if inbound.Protocol == "socks" {
			return inbound.Port
		}
	}

	return 0
}

// setupProxyForBinance 为币安客户端设置代理
func setupProxyForBinance() {
	// 优先使用 SOCKS5 (ALL_PROXY)
	if ap := os.Getenv("ALL_PROXY"); ap != "" {
		log.Printf("🌐 使用环境变量 SOCKS5 代理: %s", ap)
		return
	}
	// 其次使用 HTTPS_PROXY（HTTP 代理）
	if proxy := os.Getenv("HTTPS_PROXY"); proxy != "" {
		log.Printf("🌐 使用环境变量 HTTP 代理: %s", proxy)
		return
	}

	// 从V2Ray配置读取 SOCKS5 端口
	if socksPort := getV2RaySocksPort(); socksPort > 0 {
		ap := fmt.Sprintf("socks5h://127.0.0.1:%d", socksPort)
		os.Setenv("ALL_PROXY", ap)
		log.Printf("🌐 使用V2Ray SOCKS5 代理: %s", ap)
		return
	}

	// 回退：从V2Ray读取 HTTP 端口
	if httpPort := getV2RayProxyPort(); httpPort > 0 {
		proxyURL := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
		os.Setenv("HTTP_PROXY", proxyURL)
		os.Setenv("HTTPS_PROXY", proxyURL)
		log.Printf("🌐 使用V2Ray HTTP 代理: %s", proxyURL)
	}
}

// NewFuturesTrader 创建合约交易器
func NewFuturesTrader(apiKey, secretKey string, testnet bool) *FuturesTrader {
	// 设置代理
	setupProxyForBinance()

	// ✅ 切换主网/测试网（影响 futures.NewClient 的 BaseURL）
	// 注意：这是 go-binance futures 包的全局开关，当前实现假设单进程只运行一种环境。
	futures.UseTestnet = testnet

	client := futures.NewClient(apiKey, secretKey)

	// 配置HTTP客户端，优先 SOCKS5
	if allProxy := os.Getenv("ALL_PROXY"); allProxy != "" && (strings.HasPrefix(allProxy, "socks5://") || strings.HasPrefix(allProxy, "socks5h://")) {
		if u, err := url.Parse(allProxy); err == nil {
			dialer, err := xproxy.FromURL(u, &net.Dialer{Timeout: 30 * time.Second})
			if err == nil {
				transport := &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						return dialer.Dial(network, addr)
					},
				}
				httpClient := &http.Client{Transport: transport, Timeout: 30 * time.Second}
				client.HTTPClient = httpClient
				log.Printf("✓ 币安客户端已配置 SOCKS5 代理")
			} else {
				log.Printf("⚠️ SOCKS5 代理配置失败: %v", err)
			}
		}
	} else if proxyURL := os.Getenv("HTTPS_PROXY"); proxyURL != "" {
		// 回退到HTTP代理
		if proxy, err := url.Parse(proxyURL); err == nil {
			httpClient := &http.Client{
				Transport: &http.Transport{Proxy: http.ProxyURL(proxy)},
				Timeout:   30 * time.Second,
			}
			client.HTTPClient = httpClient
			log.Printf("✓ 币安客户端已配置 HTTP 代理")
		}
	}

	return &FuturesTrader{
		client: client,
	}
}

// GetBalance 获取账户余额（带重试机制）
func (t *FuturesTrader) GetBalance() (map[string]interface{}, error) {
	const maxRetries = 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			log.Printf("🔄 第%d次重试获取账户余额...", i+1)
			time.Sleep(2 * time.Second)
		}

		log.Printf("🔄 正在调用币安API获取账户余额...")
		account, err := t.client.NewGetAccountService().Do(context.Background())
		if err == nil {
			// 成功，返回结果
			result := make(map[string]interface{})
			result["totalWalletBalance"], _ = strconv.ParseFloat(account.TotalWalletBalance, 64)
			result["availableBalance"], _ = strconv.ParseFloat(account.AvailableBalance, 64)
			result["totalUnrealizedProfit"], _ = strconv.ParseFloat(account.TotalUnrealizedProfit, 64)

			log.Printf("✓ 币安API返回: 总余额=%s, 可用=%s, 未实现盈亏=%s",
				account.TotalWalletBalance,
				account.AvailableBalance,
				account.TotalUnrealizedProfit)

			return result, nil
		}

		lastErr = err
		log.Printf("❌ 币安API调用失败: %v", err)
	}

	return nil, fmt.Errorf("获取账户信息失败（重试%d次）: %w", maxRetries, lastErr)
}

// GetPositions 获取所有持仓
func (t *FuturesTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := t.client.NewGetPositionRiskService().Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		posAmt, _ := strconv.ParseFloat(pos.PositionAmt, 64)
		if posAmt == 0 {
			continue // 跳过无持仓的
		}

		posMap := make(map[string]interface{})
		posMap["symbol"] = pos.Symbol
		posMap["positionAmt"], _ = strconv.ParseFloat(pos.PositionAmt, 64)
		posMap["entryPrice"], _ = strconv.ParseFloat(pos.EntryPrice, 64)
		posMap["markPrice"], _ = strconv.ParseFloat(pos.MarkPrice, 64)
		posMap["unRealizedProfit"], _ = strconv.ParseFloat(pos.UnRealizedProfit, 64)
		posMap["leverage"], _ = strconv.ParseFloat(pos.Leverage, 64)
		posMap["liquidationPrice"], _ = strconv.ParseFloat(pos.LiquidationPrice, 64)

		// 判断方向
		if posAmt > 0 {
			posMap["side"] = "long"
		} else {
			posMap["side"] = "short"
		}

		result = append(result, posMap)
	}

	return result, nil
}

// SetLeverage 设置杠杆（智能判断+冷却期）
func (t *FuturesTrader) SetLeverage(symbol string, leverage int) error {
	// 先尝试获取当前杠杆（从持仓信息）
	currentLeverage := 0
	positions, err := t.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == symbol {
				if lev, ok := pos["leverage"].(float64); ok {
					currentLeverage = int(lev)
					break
				}
			}
		}
	}

	// 如果当前杠杆已经是目标杠杆，跳过
	if currentLeverage == leverage && currentLeverage > 0 {
		log.Printf("  ✓ %s 杠杆已是 %dx，无需切换", symbol, leverage)
		return nil
	}

	// 切换杠杆
	_, err = t.client.NewChangeLeverageService().
		Symbol(symbol).
		Leverage(leverage).
		Do(context.Background())

	if err != nil {
		// 如果错误信息包含"No need to change"，说明杠杆已经是目标值
		if contains(err.Error(), "No need to change") {
			log.Printf("  ✓ %s 杠杆已是 %dx", symbol, leverage)
			return nil
		}
		return fmt.Errorf("设置杠杆失败: %w", err)
	}

	log.Printf("  ✓ %s 杠杆已切换为 %dx", symbol, leverage)

	// 切换杠杆后等待5秒（避免冷却期错误）
	log.Printf("  ⏱ 等待5秒冷却期...")
	time.Sleep(5 * time.Second)

	return nil
}

// SetMarginType 设置保证金模式
func (t *FuturesTrader) SetMarginType(symbol string, marginType futures.MarginType) error {
	err := t.client.NewChangeMarginTypeService().
		Symbol(symbol).
		MarginType(marginType).
		Do(context.Background())

	if err != nil {
		errMsg := err.Error()
		// 如果已经是该模式，不算错误
		if contains(errMsg, "No need to change") {
			log.Printf("  ✓ %s 保证金模式已是 %s", symbol, marginType)
			return nil
		}
		// -4067: 存在挂单时无法切换保证金模式，可能是其他交易对的挂单或异步未完成
		// 此时大概率已经是目标模式，记录警告但不中断交易流程
		if contains(errMsg, "-4067") || contains(errMsg, "Position side cannot be changed") {
			log.Printf("  ⚠ %s 存在挂单无法切换保证金模式，当前模式可能已是 %s，继续执行", symbol, marginType)
			return nil
		}
		return fmt.Errorf("设置保证金模式失败: %w", err)
	}

	log.Printf("  ✓ %s 保证金模式已切换为 %s", symbol, marginType)

	// 切换保证金模式后等待3秒（避免冷却期错误）
	log.Printf("  ⏱ 等待3秒冷却期...")
	time.Sleep(3 * time.Second)

	return nil
}

// OpenLong 开多仓
func (t *FuturesTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 先取消该币种的所有委托单（清理旧的止损止盈单）
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消旧委托单失败（可能没有委托单）: %v", err)
	}

	// 设置逐仓模式（在取消挂单后立即执行，避免挂单干扰）
	if err := t.SetMarginType(symbol, futures.MarginTypeIsolated); err != nil {
		return nil, err
	}

	// 设置杠杆
	if err := t.SetLeverage(symbol, leverage); err != nil {
		return nil, err
	}

	// 格式化数量到正确精度
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// 创建市价买入订单
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeBuy).
		PositionSide(futures.PositionSideTypeLong).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("开多仓失败: %w", err)
	}

	log.Printf("✓ 开多仓成功: %s 数量: %s", symbol, quantityStr)
	log.Printf("  订单ID: %d", order.OrderID)

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// OpenShort 开空仓
func (t *FuturesTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 先取消该币种的所有委托单（清理旧的止损止盈单）
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消旧委托单失败（可能没有委托单）: %v", err)
	}

	// 设置逐仓模式（在取消挂单后立即执行，避免挂单干扰）
	if err := t.SetMarginType(symbol, futures.MarginTypeIsolated); err != nil {
		return nil, err
	}

	// 设置杠杆
	if err := t.SetLeverage(symbol, leverage); err != nil {
		return nil, err
	}

	// 格式化数量到正确精度
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// 创建市价卖出订单
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeSell).
		PositionSide(futures.PositionSideTypeShort).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("开空仓失败: %w", err)
	}

	log.Printf("✓ 开空仓成功: %s 数量: %s", symbol, quantityStr)
	log.Printf("  订单ID: %d", order.OrderID)

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// CloseLong 平多仓
func (t *FuturesTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	// 如果数量为0，获取当前持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "long" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的多仓", symbol)
		}
	}

	// 格式化数量
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// 创建市价卖出订单（平多）
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeSell).
		PositionSide(futures.PositionSideTypeLong).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("平多仓失败: %w", err)
	}

	log.Printf("✓ 平多仓成功: %s 数量: %s", symbol, quantityStr)

	// 平仓后取消该币种的所有挂单（止损止盈单）
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消挂单失败: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	result["avgPrice"] = order.AvgPrice
	result["executedQty"] = order.ExecutedQuantity
	result["cumQuote"] = order.CumQuote
	return result, nil
}

// CloseShort 平空仓
func (t *FuturesTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	// 如果数量为0，获取当前持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "short" {
				quantity = -pos["positionAmt"].(float64) // 空仓数量是负的，取绝对值
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的空仓", symbol)
		}
	}

	// 格式化数量
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// 创建市价买入订单（平空）
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeBuy).
		PositionSide(futures.PositionSideTypeShort).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("平空仓失败: %w", err)
	}

	log.Printf("✓ 平空仓成功: %s 数量: %s", symbol, quantityStr)

	// 平仓后取消该币种的所有挂单（止损止盈单）
	if err := t.CancelAllOrders(symbol); err != nil {
		log.Printf("  ⚠ 取消挂单失败: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	result["avgPrice"] = order.AvgPrice
	result["executedQty"] = order.ExecutedQuantity
	result["cumQuote"] = order.CumQuote
	return result, nil
}

// CancelAllOrders 取消该币种的所有挂单
func (t *FuturesTrader) CancelAllOrders(symbol string) error {
	// 1. 取消普通订单
	err := t.client.NewCancelAllOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		return fmt.Errorf("取消挂单失败: %w", err)
	}

	// 2. 同时取消该 symbol 的所有 Algo 条件单（止损/止盈）
	// 避免止损触发平仓后，孤儿止盈单残留导致后续 SetMarginType 报 -4067
	algoOrders, algoErr := t.client.NewListOpenAlgoOrdersService().
		AlgoType(futures.OrderAlgoTypeConditional).
		Symbol(symbol).
		Do(context.Background())
	if algoErr != nil {
		log.Printf("  ⚠ 获取 %s algo 条件单失败（跳过）: %v", symbol, algoErr)
	} else if len(algoOrders) > 0 {
		cancelled := 0
		for _, o := range algoOrders {
			if o.AlgoId == 0 {
				continue
			}
			if _, err := t.client.NewCancelAlgoOrderService().AlgoID(o.AlgoId).Do(context.Background()); err != nil {
				log.Printf("  ⚠ 取消 algo 条件单失败: algoId=%d err=%v", o.AlgoId, err)
			} else {
				cancelled++
			}
		}
		if cancelled > 0 {
			log.Printf("  ✓ 已取消 %s 的 %d 个 algo 条件单", symbol, cancelled)
		}
	}

	log.Printf("  ✓ 已取消 %s 的所有挂单", symbol)
	return nil
}

// GetMarketPrice 获取市场价格
func (t *FuturesTrader) GetMarketPrice(symbol string) (float64, error) {
	prices, err := t.client.NewListPricesService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return 0, fmt.Errorf("获取价格失败: %w", err)
	}

	if len(prices) == 0 {
		return 0, fmt.Errorf("未找到价格")
	}

	price, err := strconv.ParseFloat(prices[0].Price, 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}

// ListOrders 获取订单历史（Binance Futures: ListOrdersService /fapi/v1/allOrders）
func (t *FuturesTrader) ListOrders(symbol string, startTimeMs, endTimeMs int64, limit int) ([]OrderRecord, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil, fmt.Errorf("ListOrders: symbol 不能为空")
	}

	if startTimeMs > 0 && endTimeMs > 0 && startTimeMs > endTimeMs {
		return nil, fmt.Errorf("ListOrders: startTimeMs(%d) > endTimeMs(%d)", startTimeMs, endTimeMs)
	}

	fetchRange := func(rangeStartMs, rangeEndMs int64, rangeLimit int) ([]*futures.Order, error) {
		svc := t.client.NewListOrdersService().Symbol(symbol)
		if rangeStartMs > 0 {
			svc = svc.StartTime(rangeStartMs)
		}
		if rangeEndMs > 0 {
			svc = svc.EndTime(rangeEndMs)
		}
		if rangeLimit > 0 {
			svc = svc.Limit(rangeLimit)
		}
		return svc.Do(context.Background())
	}

	orders := make([]*futures.Order, 0)
	// Binance Futures /fapi/v1/allOrders: 当同时传 startTime + endTime 时，时间区间最大 7 天。
	const maxWindowMs int64 = 7 * 24 * 60 * 60 * 1000

	// 超过 7 天时，按 7 天窗口倒序分片拉取，避免 -4165。
	// 倒序可以在设置 limit 时优先拿到“最近”的订单。
	if startTimeMs > 0 && endTimeMs > 0 && (endTimeMs-startTimeMs) > maxWindowMs {
		cursorEnd := endTimeMs
		remaining := limit

		for {
			rangeStart := cursorEnd - maxWindowMs + 1
			if rangeStart < startTimeMs {
				rangeStart = startTimeMs
			}

			rangeLimit := 0
			if remaining > 0 {
				rangeLimit = remaining
			}

			chunk, err := fetchRange(rangeStart, cursorEnd, rangeLimit)
			if err != nil {
				return nil, err
			}
			orders = append(orders, chunk...)

			if remaining > 0 {
				remaining -= len(chunk)
				if remaining <= 0 {
					break
				}
			}

			if rangeStart <= startTimeMs {
				break
			}
			cursorEnd = rangeStart - 1
		}
	} else {
		var err error
		orders, err = fetchRange(startTimeMs, endTimeMs, limit)
		if err != nil {
			return nil, err
		}
	}

	parseF := func(s string) float64 {
		if s == "" {
			return 0
		}
		v, e := strconv.ParseFloat(s, 64)
		if e != nil {
			return 0
		}
		return v
	}

	out := make([]OrderRecord, 0, len(orders))
	for _, o := range orders {
		if o == nil {
			continue
		}
		out = append(out, OrderRecord{
			Symbol:       o.Symbol,
			OrderID:      o.OrderID,
			ClientOrder:  o.ClientOrderID,
			Side:         string(o.Side),
			PositionSide: string(o.PositionSide),
			Type:         string(o.Type),
			Status:       string(o.Status),
			ReduceOnly:   o.ReduceOnly,
			Price:        parseF(o.Price),
			StopPrice:    parseF(o.StopPrice),
			AvgPrice:     parseF(o.AvgPrice),
			OrigQty:      parseF(o.OrigQuantity),
			ExecutedQty:  parseF(o.ExecutedQuantity),
			CumQuote:     parseF(o.CumQuote),
			TimeInForce:  string(o.TimeInForce),
			WorkingType:  string(o.WorkingType),
			CreatedAt:    time.UnixMilli(o.Time),
			UpdatedAt:    time.UnixMilli(o.UpdateTime),
		})
	}
	return out, nil
}

// ListIncome 获取收支流水（Binance Futures: /fapi/v1/income）
func (t *FuturesTrader) ListIncome(symbol string, incomeType string, startTimeMs, endTimeMs int64, limit int) ([]IncomeRecord, error) {
	svc := t.client.NewGetIncomeHistoryService()
	if symbol != "" {
		svc = svc.Symbol(strings.ToUpper(strings.TrimSpace(symbol)))
	}
	if incomeType != "" {
		svc = svc.IncomeType(incomeType)
	}
	if startTimeMs > 0 {
		svc = svc.StartTime(startTimeMs)
	}
	if endTimeMs > 0 {
		svc = svc.EndTime(endTimeMs)
	}
	if limit > 0 {
		svc = svc.Limit(int64(limit))
	}

	history, err := svc.Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("ListIncome: %w", err)
	}

	out := make([]IncomeRecord, 0, len(history))
	for _, h := range history {
		if h == nil {
			continue
		}
		income, _ := strconv.ParseFloat(h.Income, 64)
		out = append(out, IncomeRecord{
			Symbol:     h.Symbol,
			IncomeType: h.IncomeType,
			Income:     income,
			Asset:      h.Asset,
			Time:       h.Time,
			TranID:     h.TranID,
			TradeID:    h.TradeID,
		})
	}
	return out, nil
}

// CalculatePositionSize 计算仓位大小
func (t *FuturesTrader) CalculatePositionSize(balance, riskPercent, price float64, leverage int) float64 {
	riskAmount := balance * (riskPercent / 100.0)
	positionValue := riskAmount * float64(leverage)
	quantity := positionValue / price
	return quantity
}

// SetStopLoss 设置止损单
func (t *FuturesTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	if quantity <= 0 {
		return fmt.Errorf("设置止损失败: quantity必须>0，当前: %.8f", quantity)
	}
	if stopPrice <= 0 {
		return fmt.Errorf("设置止损失败: stopPrice必须>0，当前: %.8f", stopPrice)
	}

	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	// 格式化数量
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return err
	}

	// Binance 条件单（STOP_MARKET/TAKE_PROFIT_MARKET/追踪止损等）已迁移至 Algo Order 端点：
	// 旧端点 /fapi/v1/order 会返回 -4120: "Please use the Algo Order API endpoints instead."
	//
	// 为避免重复挂单：精准取消同 symbol + 同方向 的止损类条件单（不误伤 TP/另一边仓位）。
	t.cancelOpenAlgoOrdersPrecise(symbol, posSide, map[futures.AlgoOrderType]bool{
		futures.AlgoOrderTypeStop:               true,
		futures.AlgoOrderTypeStopMarket:         true,
		futures.AlgoOrderTypeTrailingStopMarket: true,
	})

	createSL := func(withReduceOnly bool) error {
		svc := t.client.NewCreateAlgoOrderService().
			AlgoType(futures.OrderAlgoTypeConditional).
			Symbol(symbol).
			Side(side).
			Type(futures.AlgoOrderTypeStopMarket).
			PositionSide(posSide).
			TriggerPrice(fmt.Sprintf("%.8f", stopPrice)).
			Quantity(quantityStr).
			WorkingType(futures.WorkingTypeMarkPrice).
			PriceProtect(true)
		if withReduceOnly {
			svc = svc.ReduceOnly(true)
		}
		_, e := svc.Do(context.Background())
		return e
	}

	// 先尝试带 reduceOnly（更安全），若某些账户/模式不接受则自动重试不带 reduceOnly
	err = createSL(true)
	if isReduceOnlyNotRequiredErr(err) {
		log.Printf("  ⚠️ reduceOnly not required (binance -1106). retry without reduceOnly for %s %s", symbol, positionSide)
		err = createSL(false)
	}

	if err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	log.Printf("  止损价设置: %.4f", stopPrice)
	return nil
}

// SetTakeProfit 设置止盈单
func (t *FuturesTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	if quantity <= 0 {
		return fmt.Errorf("设置止盈失败: quantity必须>0，当前: %.8f", quantity)
	}
	if takeProfitPrice <= 0 {
		return fmt.Errorf("设置止盈失败: takeProfitPrice必须>0，当前: %.8f", takeProfitPrice)
	}

	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	// 格式化数量
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return err
	}

	// 为避免重复挂单：精准取消同 symbol + 同方向 的止盈类条件单（不误伤 SL/另一边仓位）。
	t.cancelOpenAlgoOrdersPrecise(symbol, posSide, map[futures.AlgoOrderType]bool{
		futures.AlgoOrderTypeTakeProfit:       true,
		futures.AlgoOrderTypeTakeProfitMarket: true,
	})

	createTP := func(withReduceOnly bool) error {
		svc := t.client.NewCreateAlgoOrderService().
			AlgoType(futures.OrderAlgoTypeConditional).
			Symbol(symbol).
			Side(side).
			Type(futures.AlgoOrderTypeTakeProfitMarket).
			PositionSide(posSide).
			TriggerPrice(fmt.Sprintf("%.8f", takeProfitPrice)).
			Quantity(quantityStr).
			WorkingType(futures.WorkingTypeMarkPrice).
			PriceProtect(true)
		if withReduceOnly {
			svc = svc.ReduceOnly(true)
		}
		_, e := svc.Do(context.Background())
		return e
	}

	err = createTP(true)
	if isReduceOnlyNotRequiredErr(err) {
		log.Printf("  ⚠️ reduceOnly not required (binance -1106). retry without reduceOnly for %s %s", symbol, positionSide)
		err = createTP(false)
	}

	if err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	log.Printf("  止盈价设置: %.4f", takeProfitPrice)
	return nil
}

// GetSymbolPrecision 获取交易对的数量精度
func (t *FuturesTrader) GetSymbolPrecision(symbol string) (int, error) {
	exchangeInfo, err := t.client.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		return 0, fmt.Errorf("获取交易规则失败: %w", err)
	}

	for _, s := range exchangeInfo.Symbols {
		if s.Symbol == symbol {
			// 从LOT_SIZE filter获取精度
			for _, filter := range s.Filters {
				if filter["filterType"] == "LOT_SIZE" {
					stepSize := filter["stepSize"].(string)
					precision := calculatePrecision(stepSize)
					log.Printf("  %s 数量精度: %d (stepSize: %s)", symbol, precision, stepSize)
					return precision, nil
				}
			}
		}
	}

	log.Printf("  ⚠ %s 未找到精度信息，使用默认精度3", symbol)
	return 3, nil // 默认精度为3
}

// calculatePrecision 从stepSize计算精度
func calculatePrecision(stepSize string) int {
	// 去除尾部的0
	stepSize = trimTrailingZeros(stepSize)

	// 查找小数点
	dotIndex := -1
	for i := 0; i < len(stepSize); i++ {
		if stepSize[i] == '.' {
			dotIndex = i
			break
		}
	}

	// 如果没有小数点或小数点在最后，精度为0
	if dotIndex == -1 || dotIndex == len(stepSize)-1 {
		return 0
	}

	// 返回小数点后的位数
	return len(stepSize) - dotIndex - 1
}

// trimTrailingZeros 去除尾部的0
func trimTrailingZeros(s string) string {
	// 如果没有小数点，直接返回
	if !stringContains(s, ".") {
		return s
	}

	// 从后向前遍历，去除尾部的0
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}

	// 如果最后一位是小数点，也去掉
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}

	return s
}

// FormatQuantity 格式化数量到正确的精度
func (t *FuturesTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	precision, err := t.GetSymbolPrecision(symbol)
	if err != nil {
		// 如果获取失败，使用默认格式
		return fmt.Sprintf("%.3f", quantity), nil
	}

	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, quantity), nil
}

// 辅助函数
func contains(s, substr string) bool {
	return len(s) >= len(substr) && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

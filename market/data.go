package market

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	proxy "golang.org/x/net/proxy"
)

// Binance Futures REST base URL (FAPI). Default is mainnet.
// When using testnet, set to "https://testnet.binancefuture.com".
var fapiBaseURL = "https://fapi.binance.com"

// SetFAPIBaseURL switches market data source between mainnet/testnet.
// Note: this is a process-wide setting. Use one environment per process.
func SetFAPIBaseURL(baseURL string) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return
	}
	// normalize: remove trailing slashes
	for strings.HasSuffix(baseURL, "/") {
		baseURL = strings.TrimSuffix(baseURL, "/")
	}
	fapiBaseURL = baseURL
}

// GetFAPIBaseURL returns the current FAPI base URL (for reuse outside this package).
func GetFAPIBaseURL() string {
	return fapiBaseURL
}

func fapiHostForDialTest() string {
	u, err := url.Parse(fapiBaseURL)
	if err == nil && u.Host != "" {
		return u.Host
	}
	// fallback: default mainnet host
	return "fapi.binance.com"
}

// 统一HTTP客户端（支持 SOCKS5 / HTTP(S) 代理）
var httpClient = newHTTPClient()

func newHTTPClient() *http.Client {
	// 1. 优先尝试 SOCKS5 代理（ALL_PROXY=socks5h://host:port）
	if proxyEnv := os.Getenv("ALL_PROXY"); proxyEnv != "" {
		if u, err := url.Parse(proxyEnv); err == nil && (u.Scheme == "socks5" || u.Scheme == "socks5h") {
			addr := u.Host
			var auth *proxy.Auth
			if u.User != nil {
				pw, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: pw}
			}
			if dialer, err := proxy.SOCKS5("tcp", addr, auth, proxy.Direct); err == nil {
					// 测试 SOCKS5 连接是否可用（快速连接测试到币安API）
					testConn, testErr := dialer.Dial("tcp", fapiHostForDialTest()+":443")
				if testErr == nil {
					testConn.Close()
					// SOCKS5 可用，使用它
					log.Printf("✓ 使用 SOCKS5 代理: %s", addr)
					dialContext := func(ctx context.Context, network, address string) (net.Conn, error) {
						return dialer.Dial(network, address)
					}
					transport := &http.Transport{
						DialContext:     dialContext,
						TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
					}
					return &http.Client{
						Transport: transport,
						Timeout:   15 * time.Second,
					}
				}
				// SOCKS5 配置存在但连接失败，记录并回退
				log.Printf("⚠️  SOCKS5 代理 %s 连接失败: %v，回退到 HTTP(S)_PROXY", addr, testErr)
			}
		}
	}

	// 2. 回退到 HTTP(S)_PROXY（标准库自动检测环境变量）
	if httpProxy := os.Getenv("HTTPS_PROXY"); httpProxy != "" {
		log.Printf("✓ 使用 HTTP(S) 代理: %s", httpProxy)
	} else if httpProxy := os.Getenv("HTTP_PROXY"); httpProxy != "" {
		log.Printf("✓ 使用 HTTP 代理: %s", httpProxy)
	}

	transport := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}
}

// Data 市场数据结构
type Data struct {
	Symbol            string
	CurrentPrice      float64
	PriceChange1h     float64 // 1小时价格变化百分比
	PriceChange4h     float64 // 4小时价格变化百分比
	CurrentEMA20      float64
	CurrentMACD       float64
	CurrentRSI7       float64
	VolatilityPct     float64 // 归一化波动率（例如 ATR14 / 当前价格）
	OpenInterest      *OIData
	FundingRate       float64
	IntradaySeries    *IntradayData
	IntradayATR14     float64 // 3分钟K线 ATR14（价格单位）；用于短线止损距离等
	LongerTermContext *LongerTermData
	MidTermContext    *MidTermData // 1小时级别上下文（仅在需要时填充，用于“更长日内结构”）
}

// OIData Open Interest数据
type OIData struct {
	Latest  float64
	Average float64
}

// IntradayData 日内数据(3分钟间隔)
type IntradayData struct {
	// 为兼容旧prompt字段，MidPrices 仍保留，但实际使用 Close 作为序列来源
	MidPrices []float64

	// 更细粒度的3分钟OHLCV（可选：仅在“重数据模式”填充）
	Opens   []float64
	Highs   []float64
	Lows    []float64
	Closes  []float64
	Volumes []float64

	// 技术序列（与输出点数对齐）
	EMA20Values []float64
	MACDValues  []float64
	RSI7Values  []float64
	RSI14Values []float64

	// 3分钟周期的OI采样序列（可选：由上层采样缓存注入，不由K线直接计算）
	OIValues      []float64
	OIDeltaValues []float64
}

// LongerTermData 长期数据(4小时时间框架)
type LongerTermData struct {
	EMA20         float64
	EMA50         float64
	ATR3          float64
	ATR14         float64
	CurrentVolume float64
	AverageVolume float64

	// 衍生的高级指标（供上层策略 / prompt 使用）
	VolatilityPct   float64 // ATR14 / 当前价格
	MarketStructure string  // "uptrend" / "downtrend" / "range"
	CandleSignal    string  // "long_upper_wick" / "long_lower_wick" / "indecision" / ...
	Support         float64 // 简化支撑位（最近一段时间低点附近）
	Resistance      float64 // 简化阻力位（最近一段时间高点附近）
	RecentHigh      float64 // 最近一段时间高点（用于策略A参考）
	RecentLow       float64 // 最近一段时间低点（用于策略A参考）

	MACDValues  []float64
	RSI14Values []float64
}

// MidTermData 中期数据（1小时时间框架）
// 字段保持与 LongerTermData 相近，便于 prompt 输出与后续扩展。
type MidTermData struct {
	EMA20         float64
	EMA50         float64
	ATR3          float64
	ATR14         float64
	CurrentVolume float64
	AverageVolume float64

	VolatilityPct   float64
	MarketStructure string
	CandleSignal    string
	Support         float64
	Resistance      float64
	RecentHigh      float64
	RecentLow       float64

	MACDValues  []float64
	RSI14Values []float64
}

// Kline K线数据
type Kline struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
}

// Get 获取指定代币的市场数据
func Get(symbol string) (*Data, error) {
	return GetWithOptions(symbol, FetchOptions{})
}

// FetchOptions 控制市场数据拉取与输出数据体积
// 注意：用于 prompt 的“重数据”应只在 TopN 场景启用，避免 prompt 膨胀。
type FetchOptions struct {
	// IntradayOutputPoints：输出到 prompt 的3分钟序列点数（例如 10 或 20）
	IntradayOutputPoints int

	// IncludeIntradayOHLCV：是否填充3分钟OHLCV数组
	IncludeIntradayOHLCV bool

	// IncludeMidTermContext：是否额外拉取1小时K线并计算中期结构（用于“更长日内结构”）
	IncludeMidTermContext bool
}

// GetWithOptions 获取指定代币的市场数据（可选“重数据”）
func GetWithOptions(symbol string, opt FetchOptions) (*Data, error) {
	// 标准化symbol
	symbol = Normalize(symbol)

	// 默认：轻量输出 10 根（约30分钟）；重数据可提升到 20 根（约60分钟）
	outPts := opt.IntradayOutputPoints
	if outPts <= 0 {
		outPts = 10
	}
	if outPts < 10 {
		outPts = 10
	}
	if outPts > 40 {
		outPts = 40 // 安全上限，避免异常膨胀
	}

	// 为了计算EMA/MACD/RSI/ATR，需要多取一些K线做指标窗口
	// - EMA20 需要 >= 20
	// - MACD 需要更长（>= 26）
	// - RSI14 需要 >= 14
	// - ATR14 需要 >= 15
	need3m := maxInt(80, outPts+60)

	// 获取3分钟K线数据
	klines3m, err := GetKlines(symbol, "3m", need3m)
	if err != nil {
		return nil, fmt.Errorf("获取3分钟K线失败: %v", err)
	}

	// 获取4小时K线数据
	klines4h, err := GetKlines(symbol, "4h", 60) // 多获取用于计算指标
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 计算当前指标 (基于3分钟最新数据)
	currentPrice := klines3m[len(klines3m)-1].Close
	currentEMA20 := calculateEMA(klines3m, 20)
	currentMACD := calculateMACD(klines3m)
	currentRSI7 := calculateRSI(klines3m, 7)

	// 计算价格变化百分比
	// 1小时价格变化 = 20个3分钟K线前的价格
	priceChange1h := 0.0
	if len(klines3m) >= 21 { // 至少需要21根K线 (当前 + 20根前)
		price1hAgo := klines3m[len(klines3m)-21].Close
		if price1hAgo > 0 {
			priceChange1h = ((currentPrice - price1hAgo) / price1hAgo) * 100
		}
	}

	// 4小时价格变化 = 1个4小时K线前的价格
	priceChange4h := 0.0
	if len(klines4h) >= 2 {
		price4hAgo := klines4h[len(klines4h)-2].Close
		if price4hAgo > 0 {
			priceChange4h = ((currentPrice - price4hAgo) / price4hAgo) * 100
		}
	}

	// 获取OI数据
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		// OI失败不影响整体,使用默认值
		oiData = &OIData{Latest: 0, Average: 0}
	}

	// 获取Funding Rate
	fundingRate, _ := getFundingRate(symbol)

	// 计算日内系列数据
	intradayData := calculateIntradaySeries(klines3m, outPts, opt.IncludeIntradayOHLCV)

	// 3分钟ATR14（价格单位）
	intradayATR14 := 0.0
	if len(klines3m) >= 16 {
		intradayATR14 = calculateATR(klines3m, 14)
	}

	// 计算长期数据
	longerTermData := calculateLongerTermData(klines4h)

	volatilityPct := 0.0
	if longerTermData != nil && longerTermData.ATR14 > 0 && currentPrice > 0 {
		volatilityPct = longerTermData.ATR14 / currentPrice
	}

	// 可选：1小时中期上下文（用于更长日内结构）
	var midTermData *MidTermData
	if opt.IncludeMidTermContext {
		klines1h, err := GetKlines(symbol, "1h", 80)
		if err == nil && len(klines1h) > 0 {
			midTermData = calculateMidTermData(klines1h)
		}
	}

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		CurrentEMA20:      currentEMA20,
		CurrentMACD:       currentMACD,
		CurrentRSI7:       currentRSI7,
		VolatilityPct:     volatilityPct,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		IntradaySeries:    intradayData,
		IntradayATR14:     intradayATR14,
		LongerTermContext: longerTermData,
		MidTermContext:    midTermData,
	}, nil
}

// GetKlines 从Binance获取K线数据
func GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	url := fmt.Sprintf("%s/fapi/v1/klines?symbol=%s&interval=%s&limit=%d",
		fapiBaseURL,
		symbol, interval, limit)

	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawData [][]interface{}
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, err
	}

	klines := make([]Kline, len(rawData))
	for i, item := range rawData {
		openTime := int64(item[0].(float64))
		open, _ := parseFloat(item[1])
		high, _ := parseFloat(item[2])
		low, _ := parseFloat(item[3])
		close, _ := parseFloat(item[4])
		volume, _ := parseFloat(item[5])
		closeTime := int64(item[6].(float64))

		klines[i] = Kline{
			OpenTime:  openTime,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: closeTime,
		}
	}

	return klines, nil
}

// calculateEMA 计算EMA
func calculateEMA(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}

	// 计算SMA作为初始EMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += klines[i].Close
	}
	ema := sum / float64(period)

	// 计算EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(klines); i++ {
		ema = (klines[i].Close-ema)*multiplier + ema
	}

	return ema
}

// calculateMACD 计算MACD
func calculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}

	// 计算12期和26期EMA
	ema12 := calculateEMA(klines, 12)
	ema26 := calculateEMA(klines, 26)

	// MACD = EMA12 - EMA26
	return ema12 - ema26
}

// calculateRSI 计算RSI
func calculateRSI(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	gains := 0.0
	losses := 0.0

	// 计算初始平均涨跌幅
	for i := 1; i <= period; i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	// 使用Wilder平滑方法计算后续RSI
	for i := period + 1; i < len(klines); i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			avgGain = (avgGain*float64(period-1) + change) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-change)) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// calculateATR 计算ATR
func calculateATR(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// 计算初始ATR
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	// Wilder平滑
	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// calculateIntradaySeries 计算日内系列数据
func calculateIntradaySeries(klines []Kline, outPoints int, includeOHLCV bool) *IntradayData {
	data := &IntradayData{
		MidPrices:   make([]float64, 0, outPoints),
		EMA20Values: make([]float64, 0, outPoints),
		MACDValues:  make([]float64, 0, outPoints),
		RSI7Values:  make([]float64, 0, outPoints),
		RSI14Values: make([]float64, 0, outPoints),
	}

	if includeOHLCV {
		data.Opens = make([]float64, 0, outPoints)
		data.Highs = make([]float64, 0, outPoints)
		data.Lows = make([]float64, 0, outPoints)
		data.Closes = make([]float64, 0, outPoints)
		data.Volumes = make([]float64, 0, outPoints)
	}

	// 获取最近 outPoints 个数据点
	start := len(klines) - outPoints
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		closePrice := klines[i].Close
		data.MidPrices = append(data.MidPrices, closePrice)

		if includeOHLCV {
			data.Opens = append(data.Opens, klines[i].Open)
			data.Highs = append(data.Highs, klines[i].High)
			data.Lows = append(data.Lows, klines[i].Low)
			data.Closes = append(data.Closes, closePrice)
			data.Volumes = append(data.Volumes, klines[i].Volume)
		}

		// 计算每个点的EMA20
		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		// 计算每个点的MACD
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		// 计算每个点的RSI
		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

func calculateMidTermData(klines []Kline) *MidTermData {
	// 复用 longer term 的计算逻辑（但结构体不同）
	lt := calculateLongerTermData(klines)
	if lt == nil {
		return nil
	}
	return &MidTermData{
		EMA20:           lt.EMA20,
		EMA50:           lt.EMA50,
		ATR3:            lt.ATR3,
		ATR14:           lt.ATR14,
		CurrentVolume:   lt.CurrentVolume,
		AverageVolume:   lt.AverageVolume,
		VolatilityPct:   lt.VolatilityPct,
		MarketStructure: lt.MarketStructure,
		CandleSignal:    lt.CandleSignal,
		Support:         lt.Support,
		Resistance:      lt.Resistance,
		RecentHigh:      lt.RecentHigh,
		RecentLow:       lt.RecentLow,
		MACDValues:      append([]float64(nil), lt.MACDValues...),
		RSI14Values:     append([]float64(nil), lt.RSI14Values...),
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// calculateLongerTermData 计算长期数据
func calculateLongerTermData(klines []Kline) *LongerTermData {
	data := &LongerTermData{
		MACDValues:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	// 计算EMA
	data.EMA20 = calculateEMA(klines, 20)
	data.EMA50 = calculateEMA(klines, 50)

	// 计算ATR
	data.ATR3 = calculateATR(klines, 3)
	data.ATR14 = calculateATR(klines, 14)

	// 计算成交量
	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		// 计算平均成交量
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	// 计算归一化波动率（ATR14 / 当前价格）
	if len(klines) > 0 && data.ATR14 > 0 {
		lastClose := klines[len(klines)-1].Close
		if lastClose > 0 {
			data.VolatilityPct = data.ATR14 / lastClose
		}
	}

	// 简单的市场结构与支撑/阻力和 K 线形态示例，可根据需要再优化
	data.MarketStructure = detectMarketStructure(klines, data.EMA20, data.EMA50)
	data.Support, data.Resistance = findSupportResistance(klines, 20)

	// 同时也记录明确的 RecentHigh / RecentLow (目前逻辑与 Support/Resistance 一致，都是20周期极值)
	data.RecentLow = data.Support
	data.RecentHigh = data.Resistance

	if len(klines) > 0 {
		data.CandleSignal = detectCandleSignal(klines[len(klines)-1])
	}

	// 计算MACD和RSI序列
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// Format 格式化输出市场数据
func Format(data *Data) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("current_price = %.2f, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period) = %.3f\n\n",
		data.CurrentPrice, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %.2f Average: %.2f\n\n",
			data.OpenInterest.Latest, data.OpenInterest.Average))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	if data.IntradaySeries != nil {
		sb.WriteString("Intraday series (3‑minute intervals, oldest → latest):\n\n")

		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}

		// 可选：更细粒度OHLCV（仅在TopN重数据时填充）
		if len(data.IntradaySeries.Opens) > 0 && len(data.IntradaySeries.Closes) > 0 {
			sb.WriteString(fmt.Sprintf("Open: %s\n\n", formatFloatSlice(data.IntradaySeries.Opens)))
			sb.WriteString(fmt.Sprintf("High: %s\n\n", formatFloatSlice(data.IntradaySeries.Highs)))
			sb.WriteString(fmt.Sprintf("Low: %s\n\n", formatFloatSlice(data.IntradaySeries.Lows)))
			sb.WriteString(fmt.Sprintf("Close: %s\n\n", formatFloatSlice(data.IntradaySeries.Closes)))
		}
		if len(data.IntradaySeries.Volumes) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.IntradaySeries.Volumes)))
		}

		// 可选：OI采样序列（按扫描周期采样）
		if len(data.IntradaySeries.OIValues) > 0 {
			sb.WriteString(fmt.Sprintf("Open Interest series (sampled): %s\n\n", formatFloatSlice(data.IntradaySeries.OIValues)))
		}
		if len(data.IntradaySeries.OIDeltaValues) > 0 {
			sb.WriteString(fmt.Sprintf("Open Interest delta (sampled): %s\n\n", formatFloatSlice(data.IntradaySeries.OIDeltaValues)))
		}

		if len(data.IntradaySeries.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20‑period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
		}

		if len(data.IntradaySeries.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
		}

		if len(data.IntradaySeries.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
		}

		if len(data.IntradaySeries.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
		}
	}

	if data.LongerTermContext != nil {
		sb.WriteString("Longer‑term context (4‑hour timeframe):\n\n")

		sb.WriteString(fmt.Sprintf("20‑Period EMA: %.3f vs. 50‑Period EMA: %.3f\n\n",
			data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))

		sb.WriteString(fmt.Sprintf("3‑Period ATR: %.3f vs. 14‑Period ATR: %.3f\n\n",
			data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))

		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))

		// 2025。12.1 新增两个指标作为基础策略
		if data.LongerTermContext.RecentHigh > 0 && data.LongerTermContext.RecentLow > 0 {
			sb.WriteString(fmt.Sprintf("Recent High (4h): %.3f | Recent Low (4h): %.3f\n\n",
				data.LongerTermContext.RecentHigh, data.LongerTermContext.RecentLow))
		}

		if len(data.LongerTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
		}

		if len(data.LongerTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
		}
	}

	// 可选：更长日内结构（1h）
	if data.MidTermContext != nil {
		sb.WriteString("Mid‑term context (1‑hour timeframe):\n\n")
		sb.WriteString(fmt.Sprintf("20‑Period EMA: %.3f vs. 50‑Period EMA: %.3f\n\n",
			data.MidTermContext.EMA20, data.MidTermContext.EMA50))
		sb.WriteString(fmt.Sprintf("3‑Period ATR: %.3f vs. 14‑Period ATR: %.3f\n\n",
			data.MidTermContext.ATR3, data.MidTermContext.ATR14))
		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.MidTermContext.CurrentVolume, data.MidTermContext.AverageVolume))
		if data.MidTermContext.RecentHigh > 0 && data.MidTermContext.RecentLow > 0 {
			sb.WriteString(fmt.Sprintf("Recent High (1h): %.3f | Recent Low (1h): %.3f\n\n",
				data.MidTermContext.RecentHigh, data.MidTermContext.RecentLow))
		}
		if len(data.MidTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.MidTermContext.MACDValues)))
		}
		if len(data.MidTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.MidTermContext.RSI14Values)))
		}
	}

	return sb.String()
}

// formatFloatSlice 格式化float64切片为字符串
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprintf("%.3f", v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

// Normalize 标准化symbol,确保是USDT交易对
func Normalize(symbol string) string {
	symbol = strings.ToUpper(symbol)
	if strings.HasSuffix(symbol, "USDT") {
		return symbol
	}
	return symbol + "USDT"
}

// parseFloat 解析float值
func parseFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case string:
		return strconv.ParseFloat(val, 64)
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}

// detectMarketStructure 粗略判断市场结构：上升趋势 / 下降趋势 / 区间震荡
func detectMarketStructure(klines []Kline, ema20, ema50 float64) string {
	if len(klines) == 0 {
		return ""
	}
	lastClose := klines[len(klines)-1].Close

	// 简单规则：收盘价与 EMA20/EMA50 的相对位置
	if lastClose > ema20 && ema20 > ema50 {
		return "uptrend"
	}
	if lastClose < ema20 && ema20 < ema50 {
		return "downtrend"
	}
	return "range"
}

// detectCandleSignal 粗略识别当前 K 线形态（长上影/长下影/十字等）
func detectCandleSignal(k Kline) string {
	body := math.Abs(k.Close - k.Open)
	total := k.High - k.Low
	if total <= 0 {
		return "flat"
	}
	upperWick := k.High - math.Max(k.Close, k.Open)
	lowerWick := math.Min(k.Close, k.Open) - k.Low

	bodyRatio := body / total
	upperRatio := upperWick / total
	lowerRatio := lowerWick / total

	// 很长的上影线，实体较小 → 看空形态
	if upperRatio > 0.5 && bodyRatio < 0.3 {
		return "long_upper_wick"
	}
	// 很长的下影线，实体较小 → 看多形态
	if lowerRatio > 0.5 && bodyRatio < 0.3 {
		return "long_lower_wick"
	}
	// 实体很小，两端影线都不大 → 盘整/犹豫
	if bodyRatio < 0.2 {
		return "indecision"
	}
	// 实体较大 → 趋势型 K 线
	if k.Close > k.Open {
		return "bullish_body"
	}
	return "bearish_body"
}

// findSupportResistance 使用简单的最近区间高低点估算支撑/阻力
func findSupportResistance(klines []Kline, lookback int) (support, resistance float64) {
	n := len(klines)
	if n == 0 {
		return 0, 0
	}
	if lookback > n {
		lookback = n
	}
	start := n - lookback

	minLow := klines[start].Low
	maxHigh := klines[start].High
	for i := start + 1; i < n; i++ {
		if klines[i].Low < minLow {
			minLow = klines[i].Low
		}
		if klines[i].High > maxHigh {
			maxHigh = klines[i].High
		}
	}
	return minLow, maxHigh
}

// getOpenInterestData 获取OI数据
func getOpenInterestData(symbol string) (*OIData, error) {
	url := fmt.Sprintf("%s/fapi/v1/openInterest?symbol=%s", fapiBaseURL, symbol)

	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OpenInterest string `json:"openInterest"`
		Symbol       string `json:"symbol"`
		Time         int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	oi, _ := strconv.ParseFloat(result.OpenInterest, 64)

	return &OIData{
		Latest:  oi,
		Average: oi * 0.999, // 近似平均值
	}, nil
}

// getFundingRate 获取资金费率
func getFundingRate(symbol string) (float64, error) {
	url := fmt.Sprintf("%s/fapi/v1/premiumIndex?symbol=%s", fapiBaseURL, symbol)

	resp, err := httpClient.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
		InterestRate    string `json:"interestRate"`
		Time            int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	rate, _ := strconv.ParseFloat(result.LastFundingRate, 64)
	return rate, nil
}

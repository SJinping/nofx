package market

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// ShortTermBarIntervalMinutes is the fixed intraday bar interval used by
	// StrategyV. It is intentionally independent from the LLM scan interval.
	ShortTermBarIntervalMinutes = 3

	// DefaultShortTermOutputPoints is the number of closed intraday bars exposed
	// to StrategyV unless a different value is explicitly requested.
	DefaultShortTermOutputPoints = 20
)

// GetClosedWithOptions is the StrategyV market-data entry point.
//
// It preserves CurrentPrice from the latest Binance 3m bar so the prompt and
// risk checks still see a near-real-time price, while all technical indicators,
// OHLCV sequences, ATR values and 1h/4h context are calculated only from bars
// whose CloseTime has already passed. The regular Get/GetWithOptions path is
// unchanged, so StrategyA and StrategyB retain their existing behaviour.
func GetClosedWithOptions(symbol string, opt FetchOptions) (*Data, error) {
	symbol = Normalize(symbol)

	outPts := opt.IntradayOutputPoints
	if outPts <= 0 {
		outPts = DefaultShortTermOutputPoints
	}
	if outPts < 10 {
		outPts = 10
	}
	if outPts > 40 {
		outPts = 40
	}

	// Keep the same warm-up depth as GetWithOptions so EMA/MACD/RSI/ATR have
	// sufficient history even after the currently forming bar is removed.
	need3m := maxInt(80, outPts+60)
	raw3m, err := GetKlines(symbol, "3m", need3m)
	if err != nil {
		return nil, fmt.Errorf("获取3分钟K线失败: %v", err)
	}
	if len(raw3m) == 0 {
		return nil, fmt.Errorf("获取3分钟K线失败: %s 返回空数据", symbol)
	}

	raw4h, err := GetKlines(symbol, "4h", 60)
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}
	if len(raw4h) == 0 {
		return nil, fmt.Errorf("获取4小时K线失败: %s 返回空数据", symbol)
	}

	nowMs := time.Now().UnixMilli()
	closed3m := closedKlinesAt(raw3m, nowMs)
	closed4h := closedKlinesAt(raw4h, nowMs)
	if len(closed3m) < maxInt(26, outPts) {
		return nil, fmt.Errorf("%s 已闭合3分钟K线不足: got=%d need>=%d", symbol, len(closed3m), maxInt(26, outPts))
	}
	if len(closed4h) < 50 {
		return nil, fmt.Errorf("%s 已闭合4小时K线不足: got=%d need>=50", symbol, len(closed4h))
	}

	// The latest raw 3m close is the current/forming-bar price. It is deliberately
	// not used to calculate indicators or confirm a setup.
	currentPrice := raw3m[len(raw3m)-1].Close
	currentEMA20 := calculateEMA(closed3m, 20)
	currentMACD := calculateMACD(closed3m)
	currentRSI7 := calculateRSI(closed3m, 7)

	priceChange1h := 0.0
	if len(closed3m) >= 21 {
		latestClosed := closed3m[len(closed3m)-1].Close
		price1hAgo := closed3m[len(closed3m)-21].Close
		if price1hAgo > 0 {
			priceChange1h = (latestClosed - price1hAgo) / price1hAgo * 100
		}
	}

	priceChange4h := 0.0
	if len(closed4h) >= 2 {
		latestClosed := closed4h[len(closed4h)-1].Close
		previousClosed := closed4h[len(closed4h)-2].Close
		if previousClosed > 0 {
			priceChange4h = (latestClosed - previousClosed) / previousClosed * 100
		}
	}

	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		// Match the existing best-effort behaviour: OI failure does not abort the
		// entire market-data request, and the StrategyV validator can still apply
		// its configured liquidity policy.
		oiData = &OIData{Latest: 0, Average: 0}
	}
	fundingRate, _ := getFundingRate(symbol)

	intradayData := calculateIntradaySeries(closed3m, outPts, opt.IncludeIntradayOHLCV)
	intradayATR14 := 0.0
	if len(closed3m) >= 16 {
		intradayATR14 = calculateATR(closed3m, 14)
	}

	longerTermData := calculateLongerTermData(closed4h)
	volatilityPct := 0.0
	if longerTermData != nil && longerTermData.ATR14 > 0 && currentPrice > 0 {
		volatilityPct = longerTermData.ATR14 / currentPrice
	}

	var midTermData *MidTermData
	if opt.IncludeMidTermContext {
		raw1h, fetchErr := GetKlines(symbol, "1h", 80)
		if fetchErr == nil {
			closed1h := closedKlinesAt(raw1h, nowMs)
			if len(closed1h) >= 50 {
				midTermData = calculateMidTermData(closed1h)
			}
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

// GetRealtimePrice returns Binance Futures' latest ticker price. StrategyV uses
// it after the LLM decision has been validated but before the decision is returned
// to the execution layer.
func GetRealtimePrice(symbol string) (float64, error) {
	symbol = Normalize(symbol)
	url := fmt.Sprintf("%s/fapi/v1/ticker/price?symbol=%s", fapiBaseURL, symbol)
	resp, err := httpClient.Get(url)
	if err != nil {
		return 0, fmt.Errorf("获取%s实时价格失败: %w", symbol, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("获取%s实时价格返回状态码%d", symbol, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取%s实时价格失败: %w", symbol, err)
	}
	var result struct {
		Price string `json:"price"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("解析%s实时价格失败: %w", symbol, err)
	}
	price, err := strconv.ParseFloat(result.Price, 64)
	if err != nil || price <= 0 {
		return 0, fmt.Errorf("%s实时价格无效: %q", symbol, result.Price)
	}
	return price, nil
}

// closedKlinesAt returns a copy containing only bars that were fully closed at
// nowMs. Binance CloseTime is inclusive, so CloseTime <= nowMs is safe.
func closedKlinesAt(klines []Kline, nowMs int64) []Kline {
	closed := make([]Kline, 0, len(klines))
	for _, k := range klines {
		if k.CloseTime > 0 && k.CloseTime <= nowMs {
			closed = append(closed, k)
		}
	}
	return closed
}

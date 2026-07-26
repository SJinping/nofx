package market

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
)

// DailySummary BTC日线级别趋势摘要
type DailySummary struct {
	EMA20           float64
	EMA50           float64
	RSI14           float64
	MACD            float64
	MarketStructure string    // "uptrend" / "downtrend" / "range"
	RecentChanges   []float64 // 最近5日涨跌幅(%)
}

// FearGreedData 恐贪指数数据
type FearGreedData struct {
	Value int    // 0-100
	Label string // "Extreme Fear", "Fear", "Neutral", "Greed", "Extreme Greed"
}

// FetchFearGreedIndex 从 alternative.me 获取恐贪指数（best-effort）
func FetchFearGreedIndex() *FearGreedData {
	resp, err := httpClient.Get("https://api.alternative.me/fng/?limit=1")
	if err != nil {
		log.Printf("⚠️  获取恐贪指数失败: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("⚠️  读取恐贪指数响应失败: %v", err)
		return nil
	}

	var result struct {
		Data []struct {
			Value               string `json:"value"`
			ValueClassification string `json:"value_classification"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Data) == 0 {
		log.Printf("⚠️  解析恐贪指数失败: %v", err)
		return nil
	}

	v, err := strconv.Atoi(result.Data[0].Value)
	if err != nil {
		return nil
	}
	return &FearGreedData{
		Value: v,
		Label: result.Data[0].ValueClassification,
	}
}

// GetBTCDailySummary 获取BTC日线级别趋势摘要
func GetBTCDailySummary() *DailySummary {
	klines, err := GetKlines("BTCUSDT", "1d", 60)
	if err != nil || len(klines) < 50 {
		log.Printf("⚠️  获取BTC日线数据失败: %v (klines=%d)", err, len(klines))
		return nil
	}

	ema20 := calculateEMA(klines, 20)
	ema50 := calculateEMA(klines, 50)
	rsi14 := calculateRSI(klines, 14)
	macd := calculateMACD(klines)
	structure := detectMarketStructure(klines, ema20, ema50)

	// 最近5日涨跌幅
	recentChanges := make([]float64, 0, 5)
	start := len(klines) - 5
	if start < 1 {
		start = 1
	}
	for i := start; i < len(klines); i++ {
		prevClose := klines[i-1].Close
		if prevClose > 0 {
			chg := (klines[i].Close - prevClose) / prevClose * 100
			recentChanges = append(recentChanges, chg)
		}
	}

	return &DailySummary{
		EMA20:           ema20,
		EMA50:           ema50,
		RSI14:           rsi14,
		MACD:            macd,
		MarketStructure: structure,
		RecentChanges:   recentChanges,
	}
}

// FormatFearGreed 格式化恐贪指数为prompt文本
func FormatFearGreed(fg *FearGreedData) string {
	if fg == nil {
		return ""
	}
	sentiment := "中性"
	switch {
	case fg.Value <= 25:
		sentiment = "极度恐惧"
	case fg.Value <= 45:
		sentiment = "恐惧"
	case fg.Value <= 55:
		sentiment = "中性"
	case fg.Value <= 75:
		sentiment = "贪婪"
	default:
		sentiment = "极度贪婪"
	}
	return fmt.Sprintf("**恐贪指数**: %d/100 (%s)\n", fg.Value, sentiment)
}

// FormatBTCDailySummary 格式化BTC日线摘要为prompt文本
func FormatBTCDailySummary(ds *DailySummary) string {
	if ds == nil {
		return ""
	}

	label := ""
	switch ds.MarketStructure {
	case "uptrend":
		label = "上涨趋势"
	case "downtrend":
		label = "下跌趋势"
	case "range":
		label = "震荡"
	default:
		label = ds.MarketStructure
	}

	macdStr := ""
	if ds.MACD < 0 {
		macdStr = "MACD<0"
	} else {
		macdStr = "MACD>0"
	}

	changesStr := ""
	if len(ds.RecentChanges) > 0 {
		changesStr = " | 近5日: "
		for i, c := range ds.RecentChanges {
			if i > 0 {
				changesStr += ", "
			}
			changesStr += fmt.Sprintf("%+.1f%%", c)
		}
	}

	return fmt.Sprintf("**BTC 日线趋势**: %s (EMA20: %.0f, EMA50: %.0f, %s, RSI14: %.1f%s)\n",
		label, ds.EMA20, ds.EMA50, macdStr, ds.RSI14, changesStr)
}

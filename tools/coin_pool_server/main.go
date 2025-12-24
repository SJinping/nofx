package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ==========
// 该服务用于给 nofx 主程序提供两个“可选外部API”：
// - /api/coins  : 候选币种池（带 score）
// - /api/oi/top : OI 增长 Top（1h窗口）
//
// 重点优化：避免币安限速
// - 二阶段筛选：先用 24h ticker 选 TopK（按 quoteVolume），再只对 TopK 拉 OI
// - 限速器 + worker pool
// - OI 增长按固定 1h 窗口计算（与更新周期解耦）
// ==========

// ========== 数据结构 ==========

type BinanceOI struct {
	Symbol       string `json:"symbol"`
	OpenInterest string `json:"openInterest"`
	Time         int64  `json:"time"`
}

type BinanceTicker struct {
	Symbol             string `json:"symbol"`
	PriceChangePercent string `json:"priceChangePercent"`
	LastPrice          string `json:"lastPrice"`
	QuoteVolume        string `json:"quoteVolume"` // 24h交易额(USDT)
}

type OIPosition struct {
	Symbol            string  `json:"symbol"`
	Rank              int     `json:"rank"`
	CurrentOI         float64 `json:"current_oi"`
	OIDeltaPercent    float64 `json:"oi_delta_percent"`
	OIDeltaValue      float64 `json:"oi_delta_value"`
	PriceDeltaPercent float64 `json:"price_delta_percent"`
}

type CoinInfo struct {
	Pair  string  `json:"pair"`
	Score float64 `json:"score"`
}

type OISample struct {
	T  time.Time
	OI float64
}

var (
	cacheMu          sync.RWMutex
	oiTopCache       []OIPosition
	coinPoolCache    []CoinInfo
	oiHistory        map[string][]OISample // 仅记录被采样的 TopK
	cacheUpdatedAtOI time.Time
	cacheUpdatedAtCP time.Time
)

type AppConfig struct {
	Port             string
	UpdateIntervalOI time.Duration
	UpdateIntervalCP time.Duration
	OITopN           int
	CoinTopN         int
	OITopKByVolume   int
	MinQuoteVolume   float64
	HTTPTimeout      time.Duration
	MaxWorkers       int
	MaxRPS           int
	OIWindow         time.Duration
	MaxRetry         int
	RetryBaseBackoff time.Duration
}

// ========== 轻量限速器（token bucket, RPS级） ==========

type RateLimiter struct {
	tokens chan struct{}
	stop   chan struct{}
}

func NewRateLimiter(rps int) *RateLimiter {
	if rps <= 0 {
		rps = 5
	}
	rl := &RateLimiter{
		tokens: make(chan struct{}, rps),
		stop:   make(chan struct{}),
	}
	// 预填充
	for i := 0; i < rps; i++ {
		rl.tokens <- struct{}{}
	}

	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(rps))
		defer ticker.Stop()
		for {
			select {
			case <-rl.stop:
				return
			case <-ticker.C:
				select {
				case rl.tokens <- struct{}{}:
				default:
				}
			}
		}
	}()

	return rl
}

func (rl *RateLimiter) Acquire() {
	<-rl.tokens
}

func (rl *RateLimiter) Close() {
	close(rl.stop)
}

// ========== 币安 API ==========

func fetchFuturesSymbols() ([]string, error) {
	resp, err := http.Get("https://fapi.binance.com/fapi/v1/exchangeInfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var info struct {
		Symbols []struct {
			Symbol       string `json:"symbol"`
			ContractType string `json:"contractType"`
			QuoteAsset   string `json:"quoteAsset"`
			Status       string `json:"status"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}

	var symbols []string
	for _, s := range info.Symbols {
		// 只要 USDT 永续合约
		if s.QuoteAsset == "USDT" && s.ContractType == "PERPETUAL" && s.Status == "TRADING" {
			symbols = append(symbols, s.Symbol)
		}
	}
	return symbols, nil
}

func fetchTickers(client *http.Client) (map[string]*BinanceTicker, error) {
	resp, err := client.Get("https://fapi.binance.com/fapi/v1/ticker/24hr")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var tickers []BinanceTicker
	if err := json.Unmarshal(body, &tickers); err != nil {
		return nil, err
	}

	m := make(map[string]*BinanceTicker)
	for i := range tickers {
		m[tickers[i].Symbol] = &tickers[i]
	}
	return m, nil
}

func fetchOpenInterest(client *http.Client, rl *RateLimiter, cfg AppConfig, symbol string) (float64, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/openInterest?symbol=%s", symbol)
	backoff := cfg.RetryBaseBackoff
	if backoff <= 0 {
		backoff = 800 * time.Millisecond
	}

	for attempt := 0; attempt <= cfg.MaxRetry; attempt++ {
		rl.Acquire()
		resp, err := client.Get(url)
		if err != nil {
			if attempt == cfg.MaxRetry {
				return 0, err
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// 429/418：退避
		if resp.StatusCode == 429 || resp.StatusCode == 418 {
			if attempt == cfg.MaxRetry {
				return 0, fmt.Errorf("rate limited (status %d): %s", resp.StatusCode, string(body))
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			if attempt == cfg.MaxRetry {
				return 0, fmt.Errorf("openInterest status=%d body=%s", resp.StatusCode, string(body))
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		var oi BinanceOI
		if err := json.Unmarshal(body, &oi); err != nil {
			return 0, err
		}
		v, err := strconv.ParseFloat(oi.OpenInterest, 64)
		if err != nil {
			return 0, err
		}
		return v, nil
	}
	return 0, fmt.Errorf("unreachable")
}

func fetchOIForSymbols(client *http.Client, rl *RateLimiter, cfg AppConfig, symbols []string) map[string]float64 {
	results := make(map[string]float64)
	if len(symbols) == 0 {
		return results
	}

	workers := cfg.MaxWorkers
	if workers <= 0 {
		workers = 6
	}
	if workers > len(symbols) {
		workers = len(symbols)
	}

	jobs := make(chan string, len(symbols))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sym := range jobs {
				oi, err := fetchOpenInterest(client, rl, cfg, sym)
				if err != nil {
					continue
				}
				mu.Lock()
				results[sym] = oi
				mu.Unlock()
			}
		}()
	}

	for _, sym := range symbols {
		jobs <- sym
	}
	close(jobs)
	wg.Wait()

	return results
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func getEnvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func getEnvFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func loadConfig() AppConfig {
	return AppConfig{
		Port:             strings.TrimSpace(getEnvString("COIN_POOL_PORT", "8081")),
		UpdateIntervalOI: getEnvDuration("UPDATE_INTERVAL_OI", 5*time.Minute),
		UpdateIntervalCP: getEnvDuration("UPDATE_INTERVAL_COINS", 15*time.Minute),
		OITopN:           getEnvInt("OI_TOP_N", 10),
		CoinTopN:         getEnvInt("COIN_TOP_N", 30),
		OITopKByVolume:   getEnvInt("OI_TOP_K", 120),
		MinQuoteVolume:   getEnvFloat("MIN_QUOTE_VOLUME", 5_000_000), // 24h成交额过滤（USDT）
		HTTPTimeout:      getEnvDuration("HTTP_TIMEOUT", 12*time.Second),
		MaxWorkers:       getEnvInt("MAX_WORKERS", 8),
		MaxRPS:           getEnvInt("MAX_RPS", 6),
		OIWindow:         getEnvDuration("OI_WINDOW", 60*time.Minute),
		MaxRetry:         getEnvInt("MAX_RETRY", 2),
		RetryBaseBackoff: getEnvDuration("RETRY_BASE_BACKOFF", 800*time.Millisecond),
	}
}

func getEnvString(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

// ========== 数据处理 ==========

type symbolVol struct {
	Symbol string
	Vol    float64
}

func selectTopKByVolume(tickers map[string]*BinanceTicker, allow map[string]bool, minVol float64, k int) []string {
	if k <= 0 {
		k = 100
	}
	arr := make([]symbolVol, 0, len(tickers))
	for sym, t := range tickers {
		if allow != nil {
			if !allow[sym] {
				continue
			}
		}
		vol := parseFloat(t.QuoteVolume)
		if vol < minVol {
			continue
		}
		arr = append(arr, symbolVol{Symbol: sym, Vol: vol})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].Vol > arr[j].Vol })
	if len(arr) > k {
		arr = arr[:k]
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, x.Symbol)
	}
	return out
}

func updateCoinPool(cfg AppConfig, client *http.Client, allow map[string]bool) {
	tickers, err := fetchTickers(client)
	if err != nil {
		log.Printf("❌ 获取 ticker 失败: %v", err)
		return
	}

	// 按交易量+波动性打分（可根据需要替换为更复杂模型）
	var coinList []CoinInfo
	for sym, ticker := range tickers {
		if allow != nil && !allow[sym] {
			continue
		}
		vol := parseFloat(ticker.QuoteVolume)
		if vol <= 0 {
			continue
		}
		priceChange := parseFloat(ticker.PriceChangePercent)
		if priceChange < 0 {
			priceChange = -priceChange
		}
		// 分数：成交额(归一化) × (1 + 波动性权重)
		score := (vol / 1e9) * (1 + priceChange/10)
		coinList = append(coinList, CoinInfo{Pair: sym, Score: score})
	}
	sort.Slice(coinList, func(i, j int) bool { return coinList[i].Score > coinList[j].Score })
	if len(coinList) > cfg.CoinTopN {
		coinList = coinList[:cfg.CoinTopN]
	}

	cacheMu.Lock()
	coinPoolCache = coinList
	cacheUpdatedAtCP = time.Now()
	cacheMu.Unlock()

	log.Printf("✅ coin pool 更新完成: %d", len(coinList))
}

func pruneSamples(samples []OISample, keepAfter time.Time) []OISample {
	if len(samples) == 0 {
		return samples
	}
	// samples 递增
	idx := 0
	for idx < len(samples) && samples[idx].T.Before(keepAfter) {
		idx++
	}
	if idx <= 0 {
		return samples
	}
	if idx >= len(samples) {
		return []OISample{}
	}
	return samples[idx:]
}

func findBaselineOI(samples []OISample, target time.Time) (float64, bool) {
	// 找 <= target 的最后一个点
	for i := len(samples) - 1; i >= 0; i-- {
		if !samples[i].T.After(target) {
			return samples[i].OI, true
		}
	}
	return 0, false
}

func updateOITop(cfg AppConfig, client *http.Client, rl *RateLimiter, allow map[string]bool) {
	tickers, err := fetchTickers(client)
	if err != nil {
		log.Printf("❌ 获取 ticker 失败: %v", err)
		return
	}

	// 二阶段：先选 TopK（按 24h 成交额），再只对 TopK 拉 OI
	topSymbols := selectTopKByVolume(tickers, allow, cfg.MinQuoteVolume, cfg.OITopKByVolume)
	if len(topSymbols) == 0 {
		log.Printf("⚠️  OI TopK 为空（可能是 allowlist 或 minQuoteVolume 过高）")
		return
	}
	oiMap := fetchOIForSymbols(client, rl, cfg, topSymbols)
	now := time.Now()
	keepAfter := now.Add(-cfg.OIWindow - 10*time.Minute) // 多留一点容差
	target := now.Add(-cfg.OIWindow)

	cacheMu.Lock()
	if oiHistory == nil {
		oiHistory = make(map[string][]OISample)
	}

	// 先更新历史
	for sym, oi := range oiMap {
		h := oiHistory[sym]
		h = append(h, OISample{T: now, OI: oi})
		h = pruneSamples(h, keepAfter)
		oiHistory[sym] = h
	}
	cacheMu.Unlock()

	// 计算排名（使用 1h 窗口）
	var oiList []OIPosition
	cacheMu.RLock()
	for sym, oi := range oiMap {
		ticker := tickers[sym]
		if ticker == nil {
			continue
		}
		price := parseFloat(ticker.LastPrice)
		priceDelta := parseFloat(ticker.PriceChangePercent)

		h := oiHistory[sym]
		base, ok := findBaselineOI(h, target)
		if !ok || base <= 0 {
			continue
		}
		deltaPct := (oi - base) / base * 100
		oiList = append(oiList, OIPosition{
			Symbol:            sym,
			CurrentOI:         oi,
			OIDeltaPercent:    deltaPct,
			OIDeltaValue:      (oi - base) * price,
			PriceDeltaPercent: priceDelta,
		})
	}
	cacheMu.RUnlock()

	sort.Slice(oiList, func(i, j int) bool { return oiList[i].OIDeltaPercent > oiList[j].OIDeltaPercent })
	if len(oiList) > cfg.OITopN {
		oiList = oiList[:cfg.OITopN]
	}
	for i := range oiList {
		oiList[i].Rank = i + 1
	}

	cacheMu.Lock()
	oiTopCache = oiList
	cacheUpdatedAtOI = now
	cacheMu.Unlock()

	log.Printf("✅ OI Top 更新完成: topN=%d (from topK=%d)", len(oiList), len(topSymbols))
}

// ========== HTTP 接口 ==========

func handleOITop(w http.ResponseWriter, r *http.Request) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()

	resp := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"positions":  oiTopCache,
			"count":      len(oiTopCache),
			"exchange":   "binance",
			"time_range": "1h",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleCoinPool(w http.ResponseWriter, r *http.Request) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()

	resp := map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"coins": coinPoolCache,
			"count": len(coinPoolCache),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()

	resp := map[string]interface{}{
		"status":          "ok",
		"updated_at_oi":   cacheUpdatedAtOI.Format(time.RFC3339),
		"updated_at_coin": cacheUpdatedAtCP.Format(time.RFC3339),
		"oi_count":        len(oiTopCache),
		"coin_count":      len(coinPoolCache),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	cfg := loadConfig()
	log.Printf("⚙️  coin_pool_server config: port=%s oi_interval=%s coin_interval=%s oi_top_n=%d oi_top_k=%d coin_top_n=%d min_quote_volume=%.0f max_workers=%d max_rps=%d oi_window=%s",
		cfg.Port, cfg.UpdateIntervalOI, cfg.UpdateIntervalCP, cfg.OITopN, cfg.OITopKByVolume, cfg.CoinTopN, cfg.MinQuoteVolume, cfg.MaxWorkers, cfg.MaxRPS, cfg.OIWindow)

	// allowlist：USDT 永续合约（只拉一次，避免频繁 exchangeInfo）
	allow := make(map[string]bool)
	if syms, err := fetchFuturesSymbols(); err == nil {
		for _, s := range syms {
			allow[s] = true
		}
		log.Printf("✓ allowlist loaded: %d symbols", len(allow))
	} else {
		log.Printf("⚠️  allowlist load failed: %v (fallback: allow all symbols in ticker)", err)
		allow = nil
	}

	client := &http.Client{Timeout: cfg.HTTPTimeout}
	rl := NewRateLimiter(cfg.MaxRPS)
	defer rl.Close()

	// 首次更新
	updateCoinPool(cfg, client, allow)
	updateOITop(cfg, client, rl, allow)

	// 分别定时更新：coin pool 慢、OI top 快（避免不必要请求）
	go func() {
		ticker := time.NewTicker(cfg.UpdateIntervalCP)
		defer ticker.Stop()
		for range ticker.C {
			updateCoinPool(cfg, client, allow)
		}
	}()
	go func() {
		ticker := time.NewTicker(cfg.UpdateIntervalOI)
		defer ticker.Stop()
		for range ticker.C {
			updateOITop(cfg, client, rl, allow)
		}
	}()

	http.HandleFunc("/api/oi/top", handleOITop)
	http.HandleFunc("/api/coins", handleCoinPool)
	http.HandleFunc("/health", handleHealth)

	log.Printf("🚀 币池服务启动: http://localhost:%s", cfg.Port)
	log.Printf("   - OI Top:    http://localhost:%s/api/oi/top", cfg.Port)
	log.Printf("   - Coin Pool: http://localhost:%s/api/coins", cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, nil))
}

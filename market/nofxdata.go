package market

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// NOFX Data API: AI300 资金流信号 + 挂单深度热力图
// 文档: https://nofxos.ai/api-docs

const nofxDataBaseURL = "https://nofxos.ai"

var (
	nofxAPIKey     string
	nofxAPIKeyOnce sync.Once
	nofxClient     = &http.Client{Timeout: 3 * time.Second}
)

func getNofxAPIKey() string {
	nofxAPIKeyOnce.Do(func() {
		nofxAPIKey = strings.TrimSpace(os.Getenv("NOFX_DATA_API_KEY"))
	})
	return nofxAPIKey
}

// NofxAI300 represents a single coin's AI300 signal.
type NofxAI300 struct {
	Symbol     string  `json:"symbol"`
	FutureFlow float64 `json:"future_flow"` // 期货 1h 净流入 (USDT)
	SpotFlow   float64 `json:"spot_flow"`   // 现货 1h 净流入 (USDT)
	Level      string  `json:"level"`       // 信号强度: S / A / B / C
}

// NofxHeatmapOrder represents a large order from the heatmap.
type NofxHeatmapOrder struct {
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`
	Volume   float64 `json:"volume"` // USDT value
}

// NofxHeatmap represents order book heatmap data.
type NofxHeatmap struct {
	BidVolume float64            `json:"bid_volume"` // 总买单量 (USDT)
	AskVolume float64            `json:"ask_volume"` // 总卖单量 (USDT)
	Delta     float64            `json:"delta"`      // bid - ask
	LargeBids []NofxHeatmapOrder `json:"large_bids"`
	LargeAsks []NofxHeatmapOrder `json:"large_asks"`
}

// nofxGet makes a GET request to the NOFX Data API.
// path should include query params if needed, e.g. "/api/ai300/list?limit=100".
// The auth key is appended automatically.
func nofxGet(path string) ([]byte, error) {
	key := getNofxAPIKey()
	if key == "" {
		return nil, fmt.Errorf("NOFX_DATA_API_KEY not set")
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	url := fmt.Sprintf("%s%s%sauth=%s", nofxDataBaseURL, path, sep, key)
	resp, err := nofxClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nofx api %s: status %d", path, resp.StatusCode)
	}
	return body, nil
}

// FetchNofxHeatmap fetches order book heatmap for a symbol. Returns nil on failure.
func FetchNofxHeatmap(symbol string) *NofxHeatmap {
	coin := strings.TrimSuffix(strings.ToUpper(symbol), "USDT")
	body, err := nofxGet(fmt.Sprintf("/api/heatmap/future/%sUSDT", coin))
	if err != nil {
		return nil
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Heatmap struct {
				BidVolume float64 `json:"bid_volume"`
				AskVolume float64 `json:"ask_volume"`
				Delta     float64 `json:"delta"`
				LargeBids []struct {
					Price    float64 `json:"price"`
					Quantity float64 `json:"quantity"`
					Volume   float64 `json:"volume"`
				} `json:"large_bids"`
				LargeAsks []struct {
					Price    float64 `json:"price"`
					Quantity float64 `json:"quantity"`
					Volume   float64 `json:"volume"`
				} `json:"large_asks"`
			} `json:"heatmap"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &resp) != nil || !resp.Success {
		return nil
	}

	h := resp.Data.Heatmap
	result := &NofxHeatmap{
		BidVolume: h.BidVolume,
		AskVolume: h.AskVolume,
		Delta:     h.Delta,
	}
	for i, b := range h.LargeBids {
		if i >= 3 {
			break
		}
		result.LargeBids = append(result.LargeBids, NofxHeatmapOrder{Price: b.Price, Quantity: b.Quantity, Volume: b.Volume})
	}
	for i, a := range h.LargeAsks {
		if i >= 3 {
			break
		}
		result.LargeAsks = append(result.LargeAsks, NofxHeatmapOrder{Price: a.Price, Quantity: a.Quantity, Volume: a.Volume})
	}
	return result
}

// BatchFetchNofxData fetches AI300 list once + per-symbol heatmaps concurrently.
// Returns maps keyed by symbol (e.g. "BTCUSDT"). Best-effort: missing data = nil.
func BatchFetchNofxData(symbols []string) (ai300Map map[string]*NofxAI300, heatmapMap map[string]*NofxHeatmap) {
	ai300Map = make(map[string]*NofxAI300, len(symbols))
	heatmapMap = make(map[string]*NofxHeatmap, len(symbols))

	if getNofxAPIKey() == "" {
		return
	}

	// AI300: one call for all symbols
	ai300List := fetchAI300List()

	for _, sym := range symbols {
		coin := strings.TrimSuffix(strings.ToUpper(sym), "USDT")
		for _, c := range ai300List {
			if strings.ToUpper(c.Symbol) == coin || strings.ToUpper(c.Symbol) == sym {
				ai300Map[sym] = &c
				break
			}
		}
	}

	// Heatmap: concurrent per-symbol with concurrency limit
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // max 5 concurrent
	var mu sync.Mutex

	for _, sym := range symbols {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			hm := FetchNofxHeatmap(s)
			if hm != nil {
				mu.Lock()
				heatmapMap[s] = hm
				mu.Unlock()
			}
		}(sym)
	}
	wg.Wait()

	return
}

func fetchAI300List() []NofxAI300 {
	body, err := nofxGet("/api/ai300/list?limit=100")
	if err != nil {
		log.Printf("⚠️ nofx ai300 fetch failed: %v", err)
		return nil
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Coins []struct {
				Symbol     string  `json:"symbol"`
				FutureFlow float64 `json:"future_flow"`
				SpotFlow   float64 `json:"spot_flow"`
				Level      string  `json:"level"`
			} `json:"coins"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &resp) != nil || !resp.Success {
		return nil
	}

	out := make([]NofxAI300, 0, len(resp.Data.Coins))
	for _, c := range resp.Data.Coins {
		out = append(out, NofxAI300{
			Symbol:     c.Symbol,
			FutureFlow: c.FutureFlow,
			SpotFlow:   c.SpotFlow,
			Level:      c.Level,
		})
	}
	return out
}

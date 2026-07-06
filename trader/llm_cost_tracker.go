package trader

import (
	"encoding/json"
	"fmt"
	"nofx/mcp"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LLMCostEntry 单次 LLM 调用的费用记录
type LLMCostEntry struct {
	Time     time.Time      `json:"time"`
	Source   string         `json:"source"` // "decision" / "open_guard" / "post_trade_review"
	Model    string         `json:"model"`
	CostUSDT float64       `json:"cost_usdt"`
	Usage    mcp.TokenUsage `json:"usage"`
}

// LLMCostSnapshot 费用追踪器的只读快照（用于 API 响应）
type LLMCostSnapshot struct {
	TotalCostUSDT       float64  `json:"total_cost_usdt"`
	TotalCalls          int      `json:"total_calls"`
	AvgCostPerCall      float64  `json:"avg_cost_per_call_usdt"`
	TotalPromptTokens   int64    `json:"total_prompt_tokens"`
	TotalOutputTokens   int64    `json:"total_output_tokens"`
	TotalCacheHitTokens int64    `json:"total_cache_hit_tokens"`
	CacheHitRate        float64  `json:"cache_hit_rate"`
	RecentCalls         []LLMCostEntry `json:"recent_calls,omitempty"`
}

// llmCostState 持久化到磁盘的状态（不含 recentCalls，避免文件过大）
type llmCostState struct {
	TotalCostUSDT     float64 `json:"total_cost_usdt"`
	TotalCalls        int     `json:"total_calls"`
	TotalPromptTokens int64   `json:"total_prompt_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
	TotalCacheHit     int64   `json:"total_cache_hit"`
	TotalCacheMiss    int64   `json:"total_cache_miss"`
	SavedAt           string  `json:"saved_at"`
}

const llmCostStateFile = "llm_cost_state.json"

// LLMCostTracker 每个 trader 独立的 LLM 调用费用累计器（线程安全，支持持久化）
type LLMCostTracker struct {
	mu                sync.RWMutex
	totalCostUSDT     float64
	totalCalls        int
	totalPromptTokens int64
	totalOutputTokens int64
	totalCacheHit     int64
	totalCacheMiss    int64
	recentCalls       []LLMCostEntry // 保留最近 20 条

	stateFilePath string // 持久化文件路径；为空则不持久化
}

// NewLLMCostTracker 创建费用追踪器，从 logDir 下的状态文件恢复历史数据
func NewLLMCostTracker(logDir string) *LLMCostTracker {
	t := &LLMCostTracker{}

	if logDir != "" {
		t.stateFilePath = filepath.Join(logDir, llmCostStateFile)
		t.load()
	}

	return t
}

// RecordCall 记录一次 LLM 调用的费用
func (t *LLMCostTracker) RecordCall(source string, result *mcp.CallResult) {
	if result == nil {
		return
	}

	cost := mcp.CalcCostUSDT(result.Model, result.Usage)

	entry := LLMCostEntry{
		Time:     time.Now(),
		Source:   source,
		Model:    result.Model,
		CostUSDT: cost,
		Usage:    result.Usage,
	}

	t.mu.Lock()
	t.totalCostUSDT += cost
	t.totalCalls++
	t.totalPromptTokens += int64(result.Usage.PromptTokens)
	t.totalOutputTokens += int64(result.Usage.CompletionTokens)
	t.totalCacheHit += int64(result.Usage.CacheHitTokens)
	t.totalCacheMiss += int64(result.Usage.CacheMissTokens)

	t.recentCalls = append(t.recentCalls, entry)
	if len(t.recentCalls) > 20 {
		t.recentCalls = t.recentCalls[len(t.recentCalls)-20:]
	}
	t.mu.Unlock()

	t.save()
}

// Snapshot 返回只读快照
func (t *LLMCostTracker) Snapshot() LLMCostSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	snap := LLMCostSnapshot{
		TotalCostUSDT:       t.totalCostUSDT,
		TotalCalls:          t.totalCalls,
		TotalPromptTokens:   t.totalPromptTokens,
		TotalOutputTokens:   t.totalOutputTokens,
		TotalCacheHitTokens: t.totalCacheHit,
	}

	if t.totalCalls > 0 {
		snap.AvgCostPerCall = t.totalCostUSDT / float64(t.totalCalls)
	}

	totalInput := t.totalCacheHit + t.totalCacheMiss
	if totalInput > 0 {
		snap.CacheHitRate = float64(t.totalCacheHit) / float64(totalInput)
	}

	recent := make([]LLMCostEntry, len(t.recentCalls))
	copy(recent, t.recentCalls)
	snap.RecentCalls = recent

	return snap
}

// GetTotalCost 返回累计总费用 USDT
func (t *LLMCostTracker) GetTotalCost() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.totalCostUSDT
}

// load 从磁盘恢复累计状态（启动时调用一次）
func (t *LLMCostTracker) load() {
	if t.stateFilePath == "" {
		return
	}

	data, err := os.ReadFile(t.stateFilePath)
	if err != nil {
		// 文件不存在是正常情况（首次运行）
		return
	}

	var state llmCostState
	if err := json.Unmarshal(data, &state); err != nil {
		fmt.Printf("⚠ LLM 费用状态文件解析失败，将从零开始: %v\n", err)
		return
	}

	t.totalCostUSDT = state.TotalCostUSDT
	t.totalCalls = state.TotalCalls
	t.totalPromptTokens = state.TotalPromptTokens
	t.totalOutputTokens = state.TotalOutputTokens
	t.totalCacheHit = state.TotalCacheHit
	t.totalCacheMiss = state.TotalCacheMiss

	fmt.Printf("🔄 已恢复 LLM 费用统计: %d 次调用, 累计 $%.4f USDT (saved at %s)\n",
		state.TotalCalls, state.TotalCostUSDT, state.SavedAt)
}

// save 将累计状态写入磁盘（每次 RecordCall 后调用）
func (t *LLMCostTracker) save() {
	if t.stateFilePath == "" {
		return
	}

	t.mu.RLock()
	state := llmCostState{
		TotalCostUSDT:     t.totalCostUSDT,
		TotalCalls:        t.totalCalls,
		TotalPromptTokens: t.totalPromptTokens,
		TotalOutputTokens: t.totalOutputTokens,
		TotalCacheHit:     t.totalCacheHit,
		TotalCacheMiss:    t.totalCacheMiss,
		SavedAt:           time.Now().Format(time.RFC3339),
	}
	t.mu.RUnlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		fmt.Printf("⚠ LLM 费用状态序列化失败: %v\n", err)
		return
	}

	// 原子写入：先写临时文件，再 rename
	tmpPath := t.stateFilePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		fmt.Printf("⚠ LLM 费用状态写入失败: %v\n", err)
		return
	}
	if err := os.Rename(tmpPath, t.stateFilePath); err != nil {
		fmt.Printf("⚠ LLM 费用状态 rename 失败: %v\n", err)
	}
}

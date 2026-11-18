package mcp

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Provider AI提供商类型
type Provider string

const (
	ProviderDeepSeek Provider = "deepseek"
	ProviderQwen     Provider = "qwen"
	ProviderCustom   Provider = "custom"
	ProviderOpenAI   Provider = "openai"
)

// Config AI API配置
type Config struct {
	Provider   Provider
	APIKey     string
	SecretKey  string // 阿里云需要
	BaseURL    string
	Model      string
	Timeout    time.Duration
	UseFullURL bool // 是否使用完整URL（不添加/chat/completions）
	MaxTokens  int
	Options    ModelOptions
}

// TokenUsage LLM API 返回的 token 用量统计
type TokenUsage struct {
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	CacheHitTokens      int    `json:"prompt_cache_hit_tokens"`
	CacheMissTokens     int    `json:"prompt_cache_miss_tokens"`
	ReasoningTokens     int    `json:"reasoning_tokens"`
	CacheWriteTokens    int    `json:"cache_write_tokens,omitempty"`
	CacheWrite30mTokens int    `json:"cache_write_30m_tokens,omitempty"`
	CacheWrite12hTokens int    `json:"cache_write_12h_tokens,omitempty"`
	ServiceTier         string `json:"service_tier,omitempty"`
}

// CallResult LLM 调用结果（内容 + token 用量）
type CallResult struct {
	Content    string
	Usage      TokenUsage
	Model      string
	ResponseID string
	Status     string
}

// Client 独立的 AI 客户端实例（线程安全）。
// 每个 trader 持有自己的 Client，互不干扰。
type Client struct {
	mu  sync.RWMutex
	cfg Config
}

// NewClient 创建新的 AI 客户端
func NewClient() *Client {
	return &Client{
		cfg: Config{
			Timeout:   120 * time.Second,
			MaxTokens: 10000,
		},
	}
}

// NewDeepSeekClient 创建 DeepSeek 客户端
func NewDeepSeekClient(apiKey, modelName string) *Client {
	model := "deepseek-reasoner"
	if modelName != "" {
		model = modelName
	}
	return &Client{
		cfg: Config{
			Provider:  ProviderDeepSeek,
			APIKey:    apiKey,
			BaseURL:   "https://api.deepseek.com/v1",
			Model:     model,
			Timeout:   120 * time.Second,
			MaxTokens: 10000,
		},
	}
}

// NewQwenClient 创建 Qwen 客户端
func NewQwenClient(apiKey, secretKey, modelName string) *Client {
	model := "qwen3.5-plus"
	if modelName != "" {
		model = modelName
	}
	return &Client{
		cfg: Config{
			Provider:  ProviderQwen,
			APIKey:    apiKey,
			SecretKey: secretKey,
			BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Model:     model,
			Timeout:   120 * time.Second,
			MaxTokens: 10000,
		},
	}
}

// NewCustomClient 创建自定义 OpenAI 兼容客户端
func NewCustomClient(apiURL, apiKey, modelName string) *Client {
	c := &Client{
		cfg: Config{
			Provider:  ProviderCustom,
			APIKey:    apiKey,
			Model:     modelName,
			Timeout:   120 * time.Second,
			MaxTokens: 10000,
		},
	}
	if strings.HasSuffix(apiURL, "#") {
		c.cfg.BaseURL = strings.TrimSuffix(apiURL, "#")
		c.cfg.UseFullURL = true
	} else {
		c.cfg.BaseURL = apiURL
	}
	return c
}

// SetModel 运行时切换模型名称（热更新，下次 AI 调用立即生效）
func (c *Client) SetModel(modelName string) {
	if modelName == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Model = modelName
}

// GetModel 获取当前使用的模型名称
func (c *Client) GetModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Model
}

// GetProvider 获取当前 Provider 类型
func (c *Client) GetProvider() Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg.Provider
}

// CallWithMessages 使用 system + user prompt 调用AI API（兼容旧接口，只返回内容）
func (c *Client) CallWithMessages(systemPrompt, userPrompt string) (string, error) {
	result, err := c.CallWithMessagesDetailed(systemPrompt, userPrompt)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// CallWithMessagesDetailed 使用 system + user prompt 调用AI API，返回完整结果（含 token 用量）
func (c *Client) CallWithMessagesDetailed(systemPrompt, userPrompt string) (*CallResult, error) {
	c.mu.RLock()
	cfg := c.cfg // options are immutable snapshots after SetModelOptions
	c.mu.RUnlock()

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("AI API密钥未设置")
	}

	// One attempt: a transport failure can occur after generation has started.
	// Retrying silently could charge twice or generate a different decision.
	return doCall(cfg, systemPrompt, userPrompt)
}

// ========== 全局兼容函数（供 cmd 工具和回测使用）==========

var defaultClient = NewClient()

// SetDeepSeekAPIKey 设置全局 DeepSeek 客户端（兼容旧调用方式）
func SetDeepSeekAPIKey(apiKey string, modelName string) {
	defaultClient.mu.Lock()
	defer defaultClient.mu.Unlock()
	defaultClient.cfg.Options = defaultModelOptions(ProviderDeepSeek)
	defaultClient.cfg.UseFullURL = false
	defaultClient.cfg.Provider = ProviderDeepSeek
	defaultClient.cfg.APIKey = apiKey
	defaultClient.cfg.BaseURL = "https://api.deepseek.com/v1"
	if modelName != "" {
		defaultClient.cfg.Model = modelName
	} else {
		defaultClient.cfg.Model = "deepseek-reasoner"
	}
}

// SetQwenAPIKey 设置全局 Qwen 客户端（兼容旧调用方式）
func SetQwenAPIKey(apiKey, secretKey string, modelName string) {
	defaultClient.mu.Lock()
	defer defaultClient.mu.Unlock()
	defaultClient.cfg.Options = defaultModelOptions(ProviderQwen)
	defaultClient.cfg.UseFullURL = false
	defaultClient.cfg.Provider = ProviderQwen
	defaultClient.cfg.APIKey = apiKey
	defaultClient.cfg.SecretKey = secretKey
	defaultClient.cfg.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	if modelName != "" {
		defaultClient.cfg.Model = modelName
	} else {
		defaultClient.cfg.Model = "qwen3.5-plus"
	}
}

// SetCustomAPI 设置全局自定义 API（兼容旧调用方式）
func SetCustomAPI(apiURL, apiKey, modelName string) {
	defaultClient.mu.Lock()
	defer defaultClient.mu.Unlock()
	defaultClient.cfg.Options = defaultModelOptions(ProviderCustom)
	defaultClient.cfg.Provider = ProviderCustom
	defaultClient.cfg.APIKey = apiKey
	if strings.HasSuffix(apiURL, "#") {
		defaultClient.cfg.BaseURL = strings.TrimSuffix(apiURL, "#")
		defaultClient.cfg.UseFullURL = true
	} else {
		defaultClient.cfg.BaseURL = apiURL
		defaultClient.cfg.UseFullURL = false
	}
	defaultClient.cfg.Model = modelName
	defaultClient.cfg.Timeout = 120 * time.Second
}

// CallWithMessagesGlobal 全局调用（供 cmd 工具使用）
func CallWithMessagesGlobal(systemPrompt, userPrompt string) (string, error) {
	return defaultClient.CallWithMessages(systemPrompt, userPrompt)
}

// CallWithMessagesDetailedGlobal 全局调用（返回完整结果含 token 用量）
func CallWithMessagesDetailedGlobal(systemPrompt, userPrompt string) (*CallResult, error) {
	return defaultClient.CallWithMessagesDetailed(systemPrompt, userPrompt)
}

// SetModel 全局设置模型名（兼容）
func SetModel(modelName string) {
	defaultClient.SetModel(modelName)
}

// GetModel 全局获取模型名（兼容）
func GetModel() string {
	return defaultClient.GetModel()
}

// GetProvider 全局获取 Provider（兼容）
func GetProvider() Provider {
	return defaultClient.GetProvider()
}

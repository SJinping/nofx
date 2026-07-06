package mcp

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

// TokenUsage LLM API 返回的 token 用量统计
type TokenUsage struct {
	PromptTokens      int `json:"prompt_tokens"`
	CompletionTokens  int `json:"completion_tokens"`
	TotalTokens       int `json:"total_tokens"`
	CacheHitTokens    int `json:"prompt_cache_hit_tokens"`
	CacheMissTokens   int `json:"prompt_cache_miss_tokens"`
	ReasoningTokens   int `json:"reasoning_tokens"`
}

// CallResult LLM 调用结果（内容 + token 用量）
type CallResult struct {
	Content string
	Usage   TokenUsage
	Model   string
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
			MaxTokens: 5000,
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
			MaxTokens: 5000,
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
			MaxTokens: 5000,
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
			MaxTokens: 5000,
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
	cfg := c.cfg // 取快照
	c.mu.RUnlock()

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("AI API密钥未设置")
	}

	maxRetries := 1
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			fmt.Printf("⚠️  AI API调用失败，正在重试 (%d/%d)...\n", attempt, maxRetries)
		}

		result, err := doCall(cfg, systemPrompt, userPrompt)
		if err == nil {
			if attempt > 1 {
				fmt.Printf("✓ AI API重试成功\n")
			}
			return result, nil
		}

		lastErr = err
		if !isRetryableError(err) {
			return nil, err
		}

		if attempt < maxRetries {
			waitTime := time.Duration(attempt) * 2 * time.Second
			fmt.Printf("⏳ 等待%v后重试...\n", waitTime)
			time.Sleep(waitTime)
		}
	}

	return nil, fmt.Errorf("重试%d次后仍然失败: %w", maxRetries, lastErr)
}

// doCall 单次调用AI API（纯函数，无副作用）
func doCall(cfg Config, systemPrompt, userPrompt string) (*CallResult, error) {
	messages := []map[string]string{}

	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userPrompt,
	})

	requestBody := map[string]interface{}{
		"model":       cfg.Model,
		"messages":    messages,
		"temperature": 0.5,
		"max_tokens":  cfg.MaxTokens,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	var url string
	if cfg.UseFullURL {
		url = cfg.BaseURL
	} else {
		url = fmt.Sprintf("%s/chat/completions", cfg.BaseURL)
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	switch cfg.Provider {
	case ProviderDeepSeek:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	case ProviderQwen:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	default:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.APIKey))
	}

	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API返回错误 (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens    int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens     int `json:"total_tokens"`
			CacheHitTokens  int `json:"prompt_cache_hit_tokens"`
			CacheMissTokens int `json:"prompt_cache_miss_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("API返回空响应")
	}

	return &CallResult{
		Content: result.Choices[0].Message.Content,
		Usage: TokenUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
			CacheHitTokens:   result.Usage.CacheHitTokens,
			CacheMissTokens:  result.Usage.CacheMissTokens,
			ReasoningTokens:  result.Usage.CompletionTokensDetails.ReasoningTokens,
		},
		Model: cfg.Model,
	}, nil
}

// isRetryableError 判断错误是否可重试
func isRetryableError(err error) bool {
	errStr := err.Error()
	retryableErrors := []string{
		"EOF",
		"timeout",
		"connection reset",
		"connection refused",
		"temporary failure",
		"no such host",
	}
	for _, retryable := range retryableErrors {
		if strings.Contains(errStr, retryable) {
			return true
		}
	}
	return false
}

// ========== 全局兼容函数（供 cmd 工具和回测使用）==========

var defaultClient = NewClient()

// SetDeepSeekAPIKey 设置全局 DeepSeek 客户端（兼容旧调用方式）
func SetDeepSeekAPIKey(apiKey string, modelName string) {
	defaultClient.mu.Lock()
	defer defaultClient.mu.Unlock()
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

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type chatCompletionsAdapter struct{}

// All providers use the same request/response path. Presets select optional
// capabilities; there is no provider-specific response parsing branch.
func (chatCompletionsAdapter) Call(ctx context.Context, cfg Config, systemPrompt, userPrompt string) (*CallResult, error) {
	options := mergeModelOptions(defaultModelOptions(cfg.Provider), cfg.Options)
	if cfg.Options.MaxOutputTokens == 0 && cfg.MaxTokens > 0 {
		options.MaxOutputTokens = cfg.MaxTokens
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	messages := []map[string]string{}
	if systemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, map[string]string{"role": "user", "content": userPrompt})
	request := map[string]interface{}{"model": cfg.Model, "messages": messages, options.TokenLimitParameter: options.MaxOutputTokens}
	if options.Temperature != nil && (options.OmitTemperature == nil || !*options.OmitTemperature) {
		request["temperature"] = *options.Temperature
	}
	for name, value := range map[string]*string{"reasoning_effort": options.ReasoningEffort, "verbosity": options.Verbosity, "service_tier": options.ServiceTier} {
		if value != nil && *value != "" {
			v := *value
			if name == "service_tier" && v == "standard" {
				v = "default"
			}
			request[name] = v
		}
	}
	if options.Store != nil {
		request["store"] = *options.Store
	}
	// Standard reasoning mode is the Chat Completions execution path, not an API
	// field named reasoning_mode. Text is the default response format.
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("serialize AI request: %w", err)
	}
	endpoint := cfg.BaseURL
	if !cfg.UseFullURL {
		endpoint = strings.TrimRight(endpoint, "/") + "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create AI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	client := compatibleHTTPClient
	if cfg.Provider == ProviderOpenAI {
		client = openAIHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send AI request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read AI response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AI API status %d: %s", resp.StatusCode, safeAPIError(body, cfg.APIKey))
	}
	return parseChatCompletion(body, cfg, options)
}

func safeAPIError(raw []byte, key string) string {
	text := string(raw)
	if key != "" {
		text = strings.ReplaceAll(text, key, "[REDACTED]")
	}
	if len(text) > 4096 {
		text = text[:4096] + "..."
	}
	return text
}

func parseChatCompletion(body []byte, cfg Config, options ModelOptions) (*CallResult, error) {
	var response struct {
		ID          string `json:"id"`
		Model       string `json:"model"`
		ServiceTier string `json:"service_tier"`
		Choices     []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int  `json:"prompt_tokens"`
			CompletionTokens int  `json:"completion_tokens"`
			TotalTokens      int  `json:"total_tokens"`
			CacheHitTokens   *int `json:"prompt_cache_hit_tokens"`
			CacheMissTokens  *int `json:"prompt_cache_miss_tokens"`
			PromptDetails    struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
				WriteDetails     struct {
					TTL30m int `json:"ttl_30m_tokens"`
					TTL12h int `json:"ttl_12h_tokens"`
				} `json:"cache_write_tokens_details"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse AI response: %w", err)
	}
	u := response.Usage
	hit := u.PromptDetails.CachedTokens
	if u.CacheHitTokens != nil {
		hit = *u.CacheHitTokens
	}
	w30, w12 := u.PromptDetails.WriteDetails.TTL30m, u.PromptDetails.WriteDetails.TTL12h
	written := max(u.PromptDetails.CacheWriteTokens, w30+w12)
	w30 += written - w30 - w12 // no explicit 12h retention is requested by this client
	miss := max(0, u.PromptTokens-hit-written)
	if u.CacheMissTokens != nil {
		miss = *u.CacheMissTokens
	}
	tier := response.ServiceTier
	if tier == "" && options.ServiceTier != nil {
		tier = *options.ServiceTier
	}
	result := &CallResult{Model: response.Model, ResponseID: response.ID, Usage: TokenUsage{
		PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
		CacheHitTokens: hit, CacheMissTokens: miss, ReasoningTokens: u.CompletionDetails.ReasoningTokens,
		CacheWriteTokens: written, CacheWrite30mTokens: w30, CacheWrite12hTokens: w12, ServiceTier: tier,
	}}
	if result.Model == "" {
		result.Model = cfg.Model
	}
	if len(response.Choices) == 0 {
		return result, fmt.Errorf("AI API returned no choices")
	}
	choice := response.Choices[0]
	result.Content = choice.Message.Content
	result.Status = choice.FinishReason
	if choice.Message.Refusal != "" {
		return result, fmt.Errorf("AI API returned a refusal")
	}
	if choice.FinishReason != "" && choice.FinishReason != "stop" {
		return result, fmt.Errorf("AI response finish_reason=%q; incomplete or non-text decision must not be executed", choice.FinishReason)
	}
	if strings.TrimSpace(result.Content) == "" {
		return result, fmt.Errorf("AI API returned empty assistant content (reasoning_tokens=%d)", result.Usage.ReasoningTokens)
	}
	return result, nil
}

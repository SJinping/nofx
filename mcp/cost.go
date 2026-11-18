package mcp

import "strings"

// ModelPricing 模型定价（单位：USDT / 百万 tokens）
// DeepSeek 中文页面为人民币，按 1 USDT ≈ 7.25 CNY 换算
type ModelPricing struct {
	InputCacheMiss       float64 // 输入 token（缓存未命中）
	InputCacheHit        float64 // 输入 token（缓存命中）
	Output               float64 // 输出 token
	InputCacheWrite30m   float64
	InputCacheWrite12h   float64
	LongContextThreshold int
}

var defaultPricing = map[string]ModelPricing{
	// OpenAI Standard USD rates, checked 2026-11-18. Reasoning is already
	// included in completion tokens. https://developers.openai.com/api/docs/pricing
	"gpt-6.2-sol": {InputCacheMiss: 2, InputCacheHit: 0.1, InputCacheWrite30m: 2.5, InputCacheWrite12h: 5, Output: 10, LongContextThreshold: 272000},
	"gpt-6.1-sol": {InputCacheMiss: 2, InputCacheHit: 0.1, InputCacheWrite30m: 2.5, InputCacheWrite12h: 5, Output: 10, LongContextThreshold: 272000},
	// DeepSeek V4-Flash (deepseek-reasoner/deepseek-chat 均映射到此)
	// CNY: 输入缓存未命中 ¥1, 缓存命中 ¥0.02, 输出 ¥2
	// deepseek全时段统一计费，注意后期要改成按高峰时段和平峰时段单独计费
	"deepseek-v4-flash": {InputCacheMiss: 0.14, InputCacheHit: 0.0028, Output: 0.28},
	"deepseek-reasoner": {InputCacheMiss: 0.14, InputCacheHit: 0.0028, Output: 0.28},
	"deepseek-chat":     {InputCacheMiss: 0.14, InputCacheHit: 0.0028, Output: 0.28},
	// DeepSeek V4-Pro
	// CNY: 输入缓存未命中 ¥3, 缓存命中 ¥0.025, 输出 ¥6
	"deepseek-v4-pro": {InputCacheMiss: 0.42, InputCacheHit: 0.0035, Output: 0.83},
	// Qwen 系列（阿里云 DashScope 国际版定价）
	"qwen3.5-plus":  {InputCacheMiss: 0.40, InputCacheHit: 0, Output: 2.40},
	"qwen3.6-plus":  {InputCacheMiss: 0.40, InputCacheHit: 0, Output: 1.60},
	"qwen3.7-plus":  {InputCacheMiss: 0.40, InputCacheHit: 0, Output: 1.60},
	"qwen3.5-flash": {InputCacheMiss: 0.10, InputCacheHit: 0, Output: 0.40},
	"qwen3.6-flash": {InputCacheMiss: 0.25, InputCacheHit: 0, Output: 1.50},
	"qwen3.7-max":   {InputCacheMiss: 2.50, InputCacheHit: 0, Output: 7.50},
}

// customPricing 用户通过 config 覆盖的定价（优先级高于 defaultPricing）
var customPricing = map[string]ModelPricing{}

// SetCustomPricing 设置用户自定义模型定价（通过 config.json 加载）
func SetCustomPricing(pricing map[string]ModelPricing) {
	customPricing = pricing
}

// getPricing 获取模型定价（先查 custom，再查 default，支持前缀模糊匹配）
func getPricing(model string) *ModelPricing {
	m := strings.ToLower(strings.TrimSpace(model))

	if p, ok := customPricing[m]; ok {
		return &p
	}
	if p, ok := defaultPricing[m]; ok {
		return &p
	}

	// 前缀匹配: "qwen3.5-plus-2026-02-15" → "qwen3.5-plus"
	for key, p := range customPricing {
		if strings.HasPrefix(m, key) {
			return &p
		}
	}
	for key, p := range defaultPricing {
		if strings.HasPrefix(m, key) {
			return &p
		}
	}

	return nil
}

// CalcCostUSDT 根据 token 用量和模型名计算本次调用费用（USDT）
// 返回 0 表示无法计算（模型未配置定价）
func CalcCostUSDT(model string, usage TokenUsage) float64 {
	p := getPricing(model)
	if p == nil {
		return 0
	}

	cacheHit := usage.CacheHitTokens
	cacheMiss := usage.CacheMissTokens
	w30, w12 := usage.CacheWrite30mTokens, usage.CacheWrite12hTokens
	if usage.CacheWriteTokens > w30+w12 {
		w30 += usage.CacheWriteTokens - w30 - w12
	}
	// 如果 API 没有返回 cache 分拆字段，用 prompt_tokens 作为 cache miss
	if cacheMiss == 0 && usage.PromptTokens > cacheHit+w30+w12 {
		cacheMiss = usage.PromptTokens - cacheHit - w30 - w12
	}
	write30, write12 := p.InputCacheWrite30m, p.InputCacheWrite12h
	if write30 == 0 {
		write30 = p.InputCacheMiss
	}
	if write12 == 0 {
		write12 = p.InputCacheMiss
	}

	inputCost := float64(cacheMiss)*p.InputCacheMiss/1_000_000 +
		float64(cacheHit)*p.InputCacheHit/1_000_000 +
		float64(w30)*write30/1_000_000 + float64(w12)*write12/1_000_000
	outputCost := float64(usage.CompletionTokens) * p.Output / 1_000_000
	if p.LongContextThreshold > 0 && usage.PromptTokens > p.LongContextThreshold {
		inputCost *= 2
		outputCost *= 1.5
	}
	// These tier multipliers apply to OpenAI pricing profiles only.
	if p.LongContextThreshold > 0 {
		switch usage.ServiceTier {
		case "fast", "priority":
			inputCost *= 2
			outputCost *= 2
		case "flex":
			inputCost *= 0.5
			outputCost *= 0.5
		}
	}

	return inputCost + outputCost
}

// GetPricingInfo 返回模型的定价信息（供 API 展示）；未配置时返回 nil
func GetPricingInfo(model string) *ModelPricing {
	return getPricing(model)
}

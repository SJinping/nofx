package mcp

import (
	"math"
	"testing"
)

func TestOpenAIPricingWithCacheWritesAndLongContext(t *testing.T) {
	cases := []struct {
		name, model string
		usage       TokenUsage
		want        float64
	}{
		{"ordinary input/output", "gpt-6.2-sol", TokenUsage{PromptTokens: 10000, CompletionTokens: 2000, ReasoningTokens: 1500}, 0.04},
		{"cached input only reported", "gpt-6.1-sol", TokenUsage{PromptTokens: 10000, CacheHitTokens: 8000, CompletionTokens: 2000}, 0.0248},
		{"cache write not additional input", "gpt-6.2-sol", TokenUsage{PromptTokens: 10000, CacheHitTokens: 2000, CacheWriteTokens: 7000, CompletionTokens: 1000}, 0.0297},
		{"long context full request", "gpt-6.2-sol", TokenUsage{PromptTokens: 300000, CompletionTokens: 1000}, 1.215},
		{"boundary short context", "gpt-6.2-sol", TokenUsage{PromptTokens: 272000, CompletionTokens: 1000}, 0.554},
		{"fast tier", "gpt-6.2-sol", TokenUsage{PromptTokens: 10000, CompletionTokens: 2000, ServiceTier: "fast"}, 0.08},
		{"flex tier", "gpt-6.1-sol", TokenUsage{PromptTokens: 10000, CompletionTokens: 2000, ServiceTier: "flex"}, 0.02},
		{"model snapshot", "gpt-6.2-sol-2026-11-18", TokenUsage{PromptTokens: 10000}, 0.02},
		{"legacy DeepSeek", "deepseek-v4-flash", TokenUsage{PromptTokens: 10000, CacheHitTokens: 8000, CacheMissTokens: 2000, CompletionTokens: 1000}, 0.0005824},
		{"unknown price", "future-model", TokenUsage{PromptTokens: 10000}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcCostUSDT(tc.model, tc.usage)
			if math.Abs(got-tc.want) > 1e-10 {
				t.Fatalf("got %.10f want %.10f", got, tc.want)
			}
		})
	}
}

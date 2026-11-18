package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSharedChatCompletionsAdapterPreservesProviderContracts(t *testing.T) {
	for _, provider := range []Provider{ProviderOpenAI, ProviderDeepSeek, ProviderQwen, ProviderCustom} {
		t.Run(string(provider), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("wrong routing or authorization")
				}
				var body map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body["model"] != "test-model" {
					t.Errorf("wrong model %v", body["model"])
				}
				messages := body["messages"].([]interface{})
				if len(messages) != 2 || messages[0].(map[string]interface{})["content"] != "system" || messages[1].(map[string]interface{})["content"] != "user" {
					t.Error("prompt was altered")
				}
				if provider == ProviderOpenAI {
					for _, field := range []string{"temperature", "max_tokens", "reasoning_mode", "reasoning", "summary", "text", "stream"} {
						if _, ok := body[field]; ok {
							t.Errorf("unsupported/unrequested field %s", field)
						}
					}
					for key, want := range map[string]interface{}{"max_completion_tokens": float64(25000), "reasoning_effort": "high", "verbosity": "medium", "service_tier": "default", "store": true} {
						if body[key] != want {
							t.Errorf("%s: got %v want %v", key, body[key], want)
						}
					}
				} else {
					if body["temperature"] != 0.25 || body["max_tokens"] != float64(10000) {
						t.Error("legacy provider defaults changed")
					}
					for _, field := range []string{"max_completion_tokens", "reasoning_effort", "verbosity", "service_tier", "store"} {
						if _, ok := body[field]; ok {
							t.Errorf("OpenAI option leaked to %s", provider)
						}
					}
				}
				io.WriteString(w, `{"id":"call-id","model":"test-model","choices":[{"message":{"content":"[{\"action\":\"wait\"}]"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`)
			}))
			defer server.Close()
			var client *Client
			switch provider {
			case ProviderOpenAI:
				client = NewOpenAIClient("test-key", "test-model")
			case ProviderDeepSeek:
				client = NewDeepSeekClient("test-key", "test-model")
			case ProviderQwen:
				client = NewQwenClient("test-key", "", "test-model")
			default:
				client = NewCustomClient(server.URL+"/v1/", "test-key", "test-model")
			}
			client.cfg.BaseURL = server.URL + "/v1/"
			result, err := client.CallWithMessagesDetailed("system", "user")
			if err != nil {
				t.Fatal(err)
			}
			if result.ResponseID != "call-id" || result.Content != `[{"action":"wait"}]` || result.Usage.CacheMissTokens != 100 {
				t.Fatalf("bad normalized result: %+v", result)
			}
		})
	}
}

func TestModelSettingsAndHotModelChangeAreIsolated(t *testing.T) {
	var bodies []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		io.WriteString(w, `{"choices":[{"message":{"content":"[]"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	client := NewOpenAIClient("test-key", "")
	client.cfg.BaseURL = server.URL
	effort := "low"
	store := false
	if err := client.SetModelOptions(ModelOptions{ReasoningEffort: &effort, Store: &store, MaxOutputTokens: 8000}); err != nil {
		t.Fatal(err)
	}
	effort = "max"
	store = true // the client must own copies of caller-provided settings
	client.SetModel("gpt-6.1-sol")
	if _, err := client.CallWithMessagesDetailed("", "user"); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["model"] != "gpt-6.1-sol" || bodies[0]["reasoning_effort"] != "low" || bodies[0]["store"] != false || bodies[0]["max_completion_tokens"] != float64(8000) {
		t.Fatalf("settings mutated or ignored: %+v", bodies[0])
	}
	other := NewOpenAIClient("other-key", "")
	if other.GetModel() != DefaultOpenAIModel || *other.cfg.Options.ReasoningEffort != "high" {
		t.Fatal("another client's settings were changed")
	}
	if err := client.SetModelOptions(ModelOptions{ReasoningMode: "pro"}); err == nil {
		t.Fatal("unsupported reasoning mode silently accepted")
	}
}

func TestChatUsageAndFailedOutputsRetainBilledUsage(t *testing.T) {
	for _, tc := range []struct {
		name, content, finish string
		emptyChoices, refusal bool
		wantError             bool
	}{
		{name: "complete", content: `[{"action":"wait"}]`, finish: "stop"},
		{name: "truncated valid JSON", content: `[{"action":"wait"}]`, finish: "length", wantError: true},
		{name: "empty visible content", finish: "stop", wantError: true},
		{name: "no choices", emptyChoices: true, wantError: true},
		{name: "refusal", finish: "stop", refusal: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := map[string]interface{}{"content": tc.content}
			if tc.refusal {
				msg["refusal"] = "refused"
			}
			choices := []interface{}{map[string]interface{}{"message": msg, "finish_reason": tc.finish}}
			if tc.emptyChoices {
				choices = nil
			}
			raw, _ := json.Marshal(map[string]interface{}{"model": "gpt-6.2-sol", "service_tier": "default", "choices": choices, "usage": map[string]interface{}{"prompt_tokens": 12000, "completion_tokens": 2000, "total_tokens": 14000, "prompt_tokens_details": map[string]interface{}{"cached_tokens": 4000, "cache_write_tokens": 7000, "cache_write_tokens_details": map[string]int{"ttl_30m_tokens": 3000, "ttl_12h_tokens": 4000}}, "completion_tokens_details": map[string]int{"reasoning_tokens": 1500}}})
			result, err := parseChatCompletion(raw, Config{Model: "gpt-6.2-sol"}, defaultModelOptions(ProviderOpenAI))
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil || result.Usage.CacheHitTokens != 4000 || result.Usage.CacheMissTokens != 1000 || result.Usage.CacheWriteTokens != 7000 || result.Usage.ReasoningTokens != 1500 {
				t.Fatalf("billed usage lost: %+v", result)
			}
			if got := CalcCostUSDT(result.Model, result.Usage); got < 0.049899 || got > 0.049901 {
				t.Fatalf("wrong cost incl. writes/reasoning: %f", got)
			}
		})
	}
	// DeepSeek's top-level cache counters remain supported by the same parser.
	raw := []byte(`{"choices":[{"message":{"content":"[]"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"prompt_cache_hit_tokens":70,"prompt_cache_miss_tokens":30}}`)
	result, err := parseChatCompletion(raw, Config{Model: "deepseek-v4-flash"}, ModelOptions{})
	if err != nil || result.Usage.CacheHitTokens != 70 || result.Usage.CacheMissTokens != 30 {
		t.Fatalf("DeepSeek usage regression: %+v %v", result, err)
	}
}

func TestCustomFullURLAndHTTPFailures(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/exact-endpoint" {
			t.Error(r.URL.Path)
		}
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":"secret-test-key"}`)
	}))
	defer server.Close()
	client := NewCustomClient(server.URL+"/exact-endpoint#", "secret-test-key", "x")
	result, err := client.CallWithMessagesDetailed("", "u")
	if result != nil || err == nil || strings.Contains(err.Error(), "secret-test-key") || !strings.Contains(err.Error(), "429") || calls != 1 {
		t.Fatalf("bad error handling: %v %d", err, calls)
	}
	client.cfg.APIKey = ""
	if _, err := client.CallWithMessagesDetailed("", "u"); err == nil || calls != 1 {
		t.Fatal("missing key issued a request")
	}
}

func TestRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(150 * time.Millisecond):
		}
	}))
	defer server.Close()
	client := NewOpenAIClient("test-key", "")
	client.cfg.BaseURL = server.URL
	client.cfg.Timeout = 30 * time.Millisecond
	if _, err := client.CallWithMessagesDetailed("", "u"); err == nil {
		t.Fatal("timeout ignored")
	}
}

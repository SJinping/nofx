package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAIConfigurationLoadsDotEnvAndGenericModelOptions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	os.Unsetenv("OPENAI_API_KEY") // exercise the dotenv fallback; Setenv restores it after this test
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"traders":[{"id":"openai-test","name":"OpenAI","ai_model":"openai","model_name":"gpt-6.2-sol","exchange":"binance","binance_api_key":"fake","binance_secret_key":"fake","initial_balance":1000,"model_options":{"reasoning_mode":"standard","reasoning_effort":"high","verbosity":"medium","service_tier":"default","store":false}}]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_API_KEY=fake-dotenv-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	tc := cfg.Traders[0]
	if tc.OpenAIKey != "fake-dotenv-key" || tc.ModelName != "gpt-6.2-sol" || tc.ModelOptions.ReasoningEffort == nil || *tc.ModelOptions.ReasoningEffort != "high" || *tc.ModelOptions.Store {
		t.Fatal("OpenAI config not wired")
	}
	t.Setenv("OPENAI_API_KEY", "fake-exported-key")
	cfg, err = LoadConfig(path)
	if err != nil || cfg.Traders[0].OpenAIKey != "fake-exported-key" {
		t.Fatal("dotenv overrode exported key")
	}
}

func TestOpenAIMissingKeyAndInvalidOptionsAreRejected(t *testing.T) {
	c := Config{Traders: []TraderConfig{{ID: "openai-test", Name: "OpenAI", AIModel: "openai", Exchange: "binance", BinanceAPIKey: "fake", BinanceSecretKey: "fake", InitialBalance: 1000}}}
	if err := c.Validate(); err == nil {
		t.Fatal("missing OpenAI key accepted")
	}
	c.Traders[0].OpenAIKey = "fake"
	c.Traders[0].ModelOptions.ReasoningMode = "pro"
	if err := c.Validate(); err == nil {
		t.Fatal("unsupported mode accepted")
	}
	c.Traders[0].ModelOptions.ReasoningMode = "standard"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

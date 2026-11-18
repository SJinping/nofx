package manager

import (
	"encoding/json"
	"nofx/config"
	"nofx/trader"
	"os"
	"path/filepath"
	"testing"
)

func TestModelNamePersistenceSurvivesConfigReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"traders":[{"id":"openai-test","name":"OpenAI","ai_model":"openai","model_name":"gpt-6.2-sol","openai_api_key":"fake","exchange":"binance","binance_api_key":"fake","binance_secret_key":"fake","initial_balance":1000,"model_options":{"reasoning_effort":"high","store":false}}]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewTraderManager(path)
	interval := 7
	if err := manager.persistConfigPatch(trader.RuntimeConfigPatch{ScanIntervalMin: &interval}, "openai-test"); err != nil {
		t.Fatal(err)
	}
	model := "gpt-6.1-sol"
	if err := manager.persistConfigPatch(trader.RuntimeConfigPatch{AIModel: &model}, "openai-test"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Traders[0].ModelName != model || cfg.Traders[0].ScanIntervalMinutes != 7 || cfg.Traders[0].ModelOptions.Store == nil || *cfg.Traders[0].ModelOptions.Store {
		t.Fatal("runtime update lost generic model or options")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]interface{}
	json.Unmarshal(data, &saved)
	tc := saved["traders"].([]interface{})[0].(map[string]interface{})
	if _, exists := tc["deepseek_model"]; exists {
		t.Fatal("OpenAI model persisted into DeepSeek field")
	}
}

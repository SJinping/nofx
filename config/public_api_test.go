package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublicAPIRequiresExplicitValidPublication(t *testing.T) {
	ids := map[string]bool{"private-a": true, "private-b": true}
	valid := func() PublicAPIConfig {
		return PublicAPIConfig{Enabled: true, Strategies: []PublicStrategyConfig{{Slug: "strategy-a", Name: "Strategy A", TraderID: "private-a"}}}
	}
	cfg := valid()
	if err := cfg.Validate(ids, 8080); err != nil || cfg.Port != 8082 {
		t.Fatalf("valid config: %+v %v", cfg, err)
	}
	for _, mutate := range []func(*PublicAPIConfig){
		func(c *PublicAPIConfig) { c.Strategies = nil },
		func(c *PublicAPIConfig) { c.Port = 8080 },
		func(c *PublicAPIConfig) { c.Port = -1 },
		func(c *PublicAPIConfig) { c.Port = 65536 },
		func(c *PublicAPIConfig) { c.Strategies[0].TraderID = "missing" },
		func(c *PublicAPIConfig) { c.Strategies[0].Slug = "../config" },
		func(c *PublicAPIConfig) { c.Strategies[0].Slug = "Strategy-A" },
		func(c *PublicAPIConfig) { c.Strategies[0].Slug = "-a" },
		func(c *PublicAPIConfig) { c.Strategies[0].Name = " " },
		func(c *PublicAPIConfig) { c.Strategies = append(c.Strategies, c.Strategies[0]) },
		func(c *PublicAPIConfig) {
			c.Strategies = append(c.Strategies, PublicStrategyConfig{Slug: "strategy-b", Name: "B", TraderID: "private-a"})
		},
	} {
		c := valid()
		mutate(&c)
		if err := c.Validate(ids, 8080); err == nil {
			t.Errorf("invalid publication accepted: %+v", c)
		}
	}
	// Existing configurations remain opt-out; nothing is published implicitly.
	if err := (&PublicAPIConfig{}).Validate(ids, 8080); err != nil {
		t.Fatal(err)
	}
}

func TestManagementListenHostAndEnvironment(t *testing.T) {
	for _, input := range []string{"", "localhost", "127.0.0.1", " 127.0.0.1 "} {
		if host, err := NormalizeAPIListenHost(input); err != nil || host != "127.0.0.1" {
			t.Fatalf("default host %q: %q %v", input, host, err)
		}
	}
	for _, input := range []string{"0.0.0.0", "::1", "::"} {
		if host, err := NormalizeAPIListenHost(input); err != nil || host != input {
			t.Fatalf("explicit IP %q: %q %v", input, host, err)
		}
	}
	for _, input := range []string{"*", ":8080", "127.0.0.1:8080", "example.com"} {
		if _, err := NormalizeAPIListenHost(input); err == nil {
			t.Fatalf("invalid bind host accepted: %q", input)
		}
	}
	t.Setenv("NOFX_API_HOST", "0.0.0.0")
	c := Config{}
	c.loadFromEnvironment()
	if c.APIListenHost != "0.0.0.0" {
		t.Fatal("Docker host environment ignored")
	}
	c.APIListenHost = "127.0.0.1"
	c.loadFromEnvironment()
	if c.APIListenHost != "127.0.0.1" {
		t.Fatal("explicit host config overridden by environment")
	}
}

func TestLoadConfigWiresPublicAPIAndKeepsLegacyOptOut(t *testing.T) {
	t.Setenv("NOFX_API_HOST", "")
	path := filepath.Join(t.TempDir(), "config.json")
	traders := `"traders":[{"id":"private-a","name":"A","ai_model":"qwen","exchange":"binance","binance_api_key":"fake","binance_secret_key":"fake","qwen_key":"fake","initial_balance":1000}]`
	if err := os.WriteFile(path, []byte(`{`+traders+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := LoadConfig(path)
	if err != nil || legacy.PublicAPI.Enabled || legacy.APIListenHost != "127.0.0.1" {
		t.Fatalf("legacy configuration changed publication: %+v %v", legacy, err)
	}
	if err := os.WriteFile(path, []byte(`{`+traders+`,"public_api":{"enabled":true,"strategies":[{"slug":"strategy-a","name":"A","trader_id":"private-a"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil || !cfg.PublicAPI.Enabled || cfg.PublicAPI.Port != 8082 || cfg.APIServerPort != 8080 {
		t.Fatalf("public configuration not wired: %+v %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte(`{`+traders+`,"public_api":{"enabled":true,"port":8080,"strategies":[{"slug":"strategy-a","name":"A","trader_id":"private-a"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("public/management port collision accepted by LoadConfig")
	}
}

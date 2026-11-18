package mcp

import (
	"fmt"
	"math"
)

const DefaultOpenAIModel = "gpt-6.2-sol"

// ModelOptions describes optional Chat Completions capabilities. Nil pointers
// inherit the provider preset; an explicit empty string omits that parameter.
// No arbitrary JSON map is accepted, so model options cannot replace messages,
// credentials or routing fields.
type ModelOptions struct {
	MaxOutputTokens     int      `json:"max_output_tokens,omitempty"`
	TokenLimitParameter string   `json:"token_limit_parameter,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	OmitTemperature     *bool    `json:"omit_temperature,omitempty"`
	ReasoningMode       string   `json:"reasoning_mode,omitempty"`
	ReasoningEffort     *string  `json:"reasoning_effort,omitempty"`
	Verbosity           *string  `json:"verbosity,omitempty"`
	ServiceTier         *string  `json:"service_tier,omitempty"`
	Store               *bool    `json:"store,omitempty"`
}

func (o ModelOptions) Validate() error {
	if o.MaxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens must be positive")
	}
	if !oneOf(o.TokenLimitParameter, "", "max_tokens", "max_completion_tokens") {
		return fmt.Errorf("token_limit_parameter must be max_tokens or max_completion_tokens")
	}
	if o.Temperature != nil && (math.IsNaN(*o.Temperature) || math.IsInf(*o.Temperature, 0) || *o.Temperature < 0 || *o.Temperature > 2) {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	if !oneOf(o.ReasoningMode, "", "standard") {
		return fmt.Errorf("Chat Completions uses standard reasoning mode; pro mode requires the Responses API")
	}
	if o.ReasoningEffort != nil && !oneOf(*o.ReasoningEffort, "", "none", "minimal", "low", "medium", "high", "xhigh", "max") {
		return fmt.Errorf("invalid reasoning_effort")
	}
	if o.Verbosity != nil && !oneOf(*o.Verbosity, "", "low", "medium", "high") {
		return fmt.Errorf("invalid verbosity")
	}
	if o.ServiceTier != nil && !oneOf(*o.ServiceTier, "", "default", "standard", "flex", "fast", "priority") {
		return fmt.Errorf("invalid Chat Completions service_tier")
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, s := range allowed {
		if value == s {
			return true
		}
	}
	return false
}
func optionPtr[T any](value T) *T { return &value }
func copyOption[T any](value *T) *T {
	if value == nil {
		return nil
	}
	return optionPtr(*value)
}

func (o ModelOptions) clone() ModelOptions {
	o.Temperature = copyOption(o.Temperature)
	o.OmitTemperature = copyOption(o.OmitTemperature)
	o.ReasoningEffort = copyOption(o.ReasoningEffort)
	o.Verbosity = copyOption(o.Verbosity)
	o.ServiceTier = copyOption(o.ServiceTier)
	o.Store = copyOption(o.Store)
	return o
}

func defaultModelOptions(provider Provider) ModelOptions {
	if provider == ProviderOpenAI {
		return ModelOptions{MaxOutputTokens: 25000, TokenLimitParameter: "max_completion_tokens", OmitTemperature: optionPtr(true), ReasoningMode: "standard", ReasoningEffort: optionPtr("high"), Verbosity: optionPtr("medium"), ServiceTier: optionPtr("default"), Store: optionPtr(true)}
	}
	return ModelOptions{MaxOutputTokens: 10000, TokenLimitParameter: "max_tokens", Temperature: optionPtr(0.25)}
}

func mergeModelOptions(base, override ModelOptions) ModelOptions {
	base = base.clone()
	override = override.clone()
	if override.MaxOutputTokens != 0 {
		base.MaxOutputTokens = override.MaxOutputTokens
	}
	if override.TokenLimitParameter != "" {
		base.TokenLimitParameter = override.TokenLimitParameter
	}
	if override.Temperature != nil {
		base.Temperature = override.Temperature
	}
	if override.OmitTemperature != nil {
		base.OmitTemperature = override.OmitTemperature
	}
	if override.ReasoningMode != "" {
		base.ReasoningMode = override.ReasoningMode
	}
	if override.ReasoningEffort != nil {
		base.ReasoningEffort = override.ReasoningEffort
	}
	if override.Verbosity != nil {
		base.Verbosity = override.Verbosity
	}
	if override.ServiceTier != nil {
		base.ServiceTier = override.ServiceTier
	}
	if override.Store != nil {
		base.Store = override.Store
	}
	return base
}

// SetModelOptions changes only request settings, preserving provider and keys.
func (c *Client) SetModelOptions(options ModelOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Options = mergeModelOptions(c.cfg.Options, options)
	return nil
}

func NewOpenAIClient(apiKey, modelName string) *Client {
	if modelName == "" {
		modelName = DefaultOpenAIModel
	}
	c := NewClient()
	c.cfg.Provider = ProviderOpenAI
	c.cfg.APIKey = apiKey
	c.cfg.BaseURL = "https://api.openai.com/v1"
	c.cfg.Model = modelName
	c.cfg.Options = defaultModelOptions(ProviderOpenAI)
	return c
}

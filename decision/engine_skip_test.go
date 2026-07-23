package decision

import (
	"strings"
	"testing"

	"nofx/market"
)

func TestShouldSkipLLMNoMarketDataEmptyContext(t *testing.T) {
	ctx := &Context{PromptStrategy: StrategyV{}}
	if !shouldSkipLLMNoMarketData(ctx) {
		t.Fatalf("expected empty StrategyV context to skip LLM")
	}
}

func TestShouldSkipLLMNoMarketDataDoesNotSkipWhenActionableContextExists(t *testing.T) {
	tests := []struct {
		name string
		ctx  *Context
	}{
		{
			name: "position",
			ctx:  &Context{Positions: []PositionInfo{{Symbol: "BTCUSDT"}}, PromptStrategy: StrategyV{}},
		},
		{
			name: "candidate",
			ctx:  &Context{CandidateCoins: []CandidateCoin{{Symbol: "BTCUSDT"}}, PromptStrategy: StrategyV{}},
		},
		{
			name: "market data",
			ctx:  &Context{MarketDataMap: map[string]*market.Data{"BTCUSDT": {Symbol: "BTCUSDT"}}, PromptStrategy: StrategyV{}},
		},
		{
			name: "strategyv watchlist",
			ctx:  &Context{ShortTermWatchlist: []ShortTermWatchItem{{Symbol: "BTCUSDT"}}, PromptStrategy: StrategyV{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if shouldSkipLLMNoMarketData(tt.ctx) {
				t.Fatalf("did not expect skip for %s", tt.name)
			}
		})
	}
}

func TestBuildNoMarketDataSkipDecisionFormat(t *testing.T) {
	fd := buildNoMarketDataSkipDecision(&Context{PromptStrategy: StrategyV{}}, "V")
	if fd == nil {
		t.Fatalf("expected decision")
	}
	if fd.UserPrompt != "(system_skip:no_market_data)" {
		t.Fatalf("unexpected user prompt: %q", fd.UserPrompt)
	}
	if !strings.Contains(fd.CoTTrace, "system_skip:no_market_data") || !strings.Contains(fd.CoTTrace, "llm_called=false") {
		t.Fatalf("unexpected trace: %q", fd.CoTTrace)
	}
	if len(fd.Decisions) != 1 {
		t.Fatalf("expected one decision, got %d", len(fd.Decisions))
	}
	d := fd.Decisions[0]
	if d.Action != ActionWait {
		t.Fatalf("expected wait action, got %s", d.Action)
	}
	if d.DecisionSource != "system_skip_no_market_data" {
		t.Fatalf("unexpected decision source: %s", d.DecisionSource)
	}
	if !strings.Contains(d.Reasoning, "empty_context") || !strings.Contains(d.Reasoning, "wait next cycle") {
		t.Fatalf("unexpected reasoning: %q", d.Reasoning)
	}
}

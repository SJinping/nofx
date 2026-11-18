package decision

import (
	"errors"
	"nofx/mcp"
	"testing"
)

type auditTestCaller struct {
	result       *mcp.CallResult
	err          error
	system, user string
}

func (c *auditTestCaller) CallWithMessages(s, u string) (string, error) {
	r, e := c.CallWithMessagesDetailed(s, u)
	if r == nil {
		return "", e
	}
	return r.Content, e
}
func (c *auditTestCaller) CallWithMessagesDetailed(s, u string) (*mcp.CallResult, error) {
	c.system = s
	c.user = u
	return c.result, c.err
}

func TestDecisionFailuresPreservePromptUsageButNeverActions(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		callError     error
	}{
		{name: "parse error", content: "analysis without a decision array"},
		{name: "truncated but syntactically valid", content: `[{"action":"wait"}]`, callError: errors.New("finish_reason=length")},
		{name: "empty content", callError: errors.New("empty assistant content")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &auditTestCaller{result: &mcp.CallResult{Model: "gpt-6.2-sol", ResponseID: "audit-call", Status: "length", Content: tc.content, Usage: mcp.TokenUsage{PromptTokens: 10000, CompletionTokens: 2000}}, err: tc.callError}
			d, err := requestAndParseDecision(&Context{AI: caller}, "system", "DOGE current_price = 0.05938")
			if err == nil || d == nil {
				t.Fatal("failed call was accepted or audit was lost")
			}
			if len(d.Decisions) != 0 || d.UserPrompt != caller.user || caller.system != "system" || d.RawAIResponse != tc.content || d.LLMUsage == nil || d.LLMUsage.CompletionTokens != 2000 || d.LLMResponseID != "audit-call" || d.LLMCostUSDT != 0.04 {
				t.Fatalf("bad failure audit: %+v", d)
			}
		})
	}
	caller := &auditTestCaller{err: errors.New("network failure")}
	d, err := requestAndParseDecision(&Context{AI: caller}, "system", "input")
	if err == nil || d == nil || d.UserPrompt != "input" || d.LLMUsage != nil || len(d.Decisions) != 0 {
		t.Fatal("network failure audit is incorrect")
	}
}

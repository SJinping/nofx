package mcp

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"time"
)

// completionAdapter is the shared wire protocol for all current providers.
// Provider/model differences are request settings, not separate transports.
type completionAdapter interface {
	Call(context.Context, Config, string, string) (*CallResult, error)
}

var sharedCompletionAdapter completionAdapter = chatCompletionsAdapter{}

// Existing compatible providers retain direct networking. OpenAI also supports
// the standard proxy environment variables.
var compatibleHTTPClient = newProviderHTTPClient(nil)
var openAIHTTPClient = newProviderHTTPClient(http.ProxyFromEnvironment)

func newProviderHTTPClient(proxy func(*http.Request) (*url.URL, error)) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:               proxy,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}}
}

func doCall(cfg Config, systemPrompt, userPrompt string) (*CallResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	return sharedCompletionAdapter.Call(ctx, cfg, systemPrompt, userPrompt)
}

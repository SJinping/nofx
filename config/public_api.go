package config

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// PublicAPIConfig is opt-in. Only explicitly listed traders may be published.
// The public listener is loopback-only; expose it through a path allowlist proxy.
type PublicAPIConfig struct {
	Enabled    bool                   `json:"enabled"`
	Port       int                    `json:"port,omitempty"`
	Strategies []PublicStrategyConfig `json:"strategies,omitempty"`
}

type PublicStrategyConfig struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	TraderID string `json:"trader_id"`
}

var publicSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (c *PublicAPIConfig) Validate(traderIDs map[string]bool, managementPort int) error {
	if !c.Enabled {
		return nil
	}
	if c.Port == 0 {
		c.Port = 8082
	}
	if c.Port < 1 || c.Port > 65535 || c.Port == managementPort {
		return fmt.Errorf("public_api.port must be a valid port different from api_server_port")
	}
	if len(c.Strategies) == 0 || len(c.Strategies) > 20 {
		return fmt.Errorf("public_api.strategies must explicitly list between 1 and 20 strategies")
	}
	slugs, publishedTraders := map[string]bool{}, map[string]bool{}
	for _, s := range c.Strategies {
		if len(s.Slug) > 64 || !publicSlugPattern.MatchString(s.Slug) || slugs[s.Slug] {
			return fmt.Errorf("public_api strategy slug must be unique, lowercase and URL-safe (max 64 bytes)")
		}
		if strings.TrimSpace(s.Name) == "" || len(s.Name) > 120 || strings.ContainsAny(s.Name, "\r\n\t") {
			return fmt.Errorf("public_api strategy name must be nonempty and at most 120 bytes")
		}
		if !traderIDs[s.TraderID] || publishedTraders[s.TraderID] {
			return fmt.Errorf("public_api strategy must reference an existing, uniquely published trader")
		}
		slugs[s.Slug], publishedTraders[s.TraderID] = true, true
	}
	return nil
}

// NormalizeAPIListenHost defaults the unauthenticated management API to loopback.
// Explicit wildcard binding is needed inside the Docker bridge network only.
func NormalizeAPIListenHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" {
		return "127.0.0.1", nil
	}
	if net.ParseIP(host) == nil {
		return "", fmt.Errorf("api_listen_host must be an IP address or localhost")
	}
	return host, nil
}

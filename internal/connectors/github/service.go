package github

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

func buildURL(c *connector.Ctx, path string) string {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		base = defaultBaseURL
	}
	return base + path
}

func requireOwnerRepo(c *connector.Ctx) (owner, repo string, err error) {
	owner = strings.TrimSpace(c.Input("owner"))
	repo = strings.TrimSpace(c.Input("repo"))
	if owner == "" {
		return "", "", fmt.Errorf("owner is required")
	}
	if repo == "" {
		return "", "", fmt.Errorf("repo is required")
	}
	return owner, repo, nil
}

func parseCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

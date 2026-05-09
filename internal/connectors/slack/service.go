package slack

import "strings"

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

// slackField extracts a top-level key from a Slack API response map.
func slackField(resp any, key string) (any, error) {
	m, ok := resp.(map[string]any)
	if !ok {
		return resp, nil
	}
	if v, exists := m[key]; exists {
		return v, nil
	}
	return resp, nil
}

// pickFields returns a new map containing only the specified keys.
func pickFields(resp any, keys ...string) (any, error) {
	m, ok := resp.(map[string]any)
	if !ok {
		return resp, nil
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, exists := m[k]; exists {
			out[k] = v
		}
	}
	return out, nil
}

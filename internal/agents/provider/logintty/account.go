package logintty

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Account is the credential-file snapshot for one provider instance:
// who is logged in and on what plan. Zero value = not connected.
type Account struct {
	Connected bool   `json:"connected"`
	Email     string `json:"email,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Org       string `json:"org,omitempty"`
	// AuthMethod is the user-facing credential kind ("Claude AI",
	// "ChatGPT", "API key", "Google") — the CLI usage screen's "Auth
	// method" row.
	AuthMethod string    `json:"auth_method,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"` // zero = unknown / not applicable
}

// envValue returns the value of key in a KEY=VALUE env list, or "".
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// decodeJWTClaims parses the payload segment of a JWT without
// verifying the signature — wick only displays who the CLI says is
// logged in; it never trusts these claims for auth decisions.
func decodeJWTClaims(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil, false
	}
	return claims, true
}

package logintty

// codex per-type login TTY support. Only the account probe (connect
// status display) is implemented today; the TTY login command for
// `codex login` lands here when codex reconnect is enabled.

import (
	"os"
	"path/filepath"
	"time"
)

// codexConfigDir honours the instance's CODEX_HOME, falling back to
// ~/.codex.
func codexConfigDir(env []string) string {
	if dir := envValue(env, "CODEX_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func readCodexAccount(dir string) Account {
	var auth struct {
		OpenAIAPIKey string `json:"OPENAI_API_KEY"`
		Tokens       struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if !readJSON(filepath.Join(dir, "auth.json"), &auth) {
		return Account{}
	}
	var acc Account
	if auth.Tokens.IDToken != "" {
		acc.Connected = true
		acc.AuthMethod = "ChatGPT"
		if claims, ok := decodeJWTClaims(auth.Tokens.IDToken); ok {
			if email, _ := claims["email"].(string); email != "" {
				acc.Email = email
			}
			if authClaim, _ := claims["https://api.openai.com/auth"].(map[string]any); authClaim != nil {
				if plan, _ := authClaim["chatgpt_plan_type"].(string); plan != "" {
					acc.Plan = plan
				}
			}
			if exp, _ := claims["exp"].(float64); exp > 0 {
				acc.ExpiresAt = time.Unix(int64(exp), 0)
			}
		}
		return acc
	}
	if auth.OpenAIAPIKey != "" {
		acc.Connected = true
		acc.Plan = "api-key"
		acc.AuthMethod = "API key"
	}
	return acc
}

package logintty

// codex per-type login TTY support: the login argv and the credential
// -file account probe.

import (
	"os"
	"path/filepath"
	"time"
)

// codexLoginCommand builds the codex argv for a wick-hosted login.
//
// --device-auth, not the default flow, because the default one binds a
// callback on localhost:1455 and waits for the browser to hit it. On the
// wick host nobody is sitting at that browser, so the flow never
// completes. Device auth instead prints a link plus a one-time code and
// polls OpenAI for the result, which is exactly the shape the login TTY
// can relay: the parser lifts the link out of the stream, and the code
// is read off the live terminal.
//
// The instance's ExtraArgs are NOT forwarded. Unlike claude, where
// /login is a prompt to the same REPL the flags configure, `codex login`
// is a subcommand with its own flag set (-c, --enable/--disable,
// --with-api-key, --with-access-token, --device-auth). A REPL flag such
// as --model or --sandbox is rejected by the arg parser, and the spawn
// would die before printing anything for the user to act on.
func codexLoginCommand() []string {
	return []string{"login", "--device-auth"}
}

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

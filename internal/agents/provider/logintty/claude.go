package logintty

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// anthropicAPIBase is the OAuth API host the usage probe talks to.
// Overridable in tests.
var anthropicAPIBase = "https://api.anthropic.com"

// claudeLoginCommand builds the claude argv: instance flags first,
// /login as the positional prompt — the REPL executes it and prints
// the OAuth link.
func claudeLoginCommand(extraArgs []string) []string {
	return append(append([]string{}, extraArgs...), "/login")
}

// claudeLoginEnv stops claude from opening a browser tab on the wick
// host during /login — wick parses the link and the user opens it
// where they want.
//
// The CLI's open-url path is: settings.browser ?? $BROWSER, else the
// OS opener (rundll32 on Windows) — with NO ssh/remote check on the
// Windows branch. A $BROWSER pointing at a nonexistent binary makes
// the spawn fail, which the CLI treats as "browser didn't open" and
// just prints the URL; there is no fallback to the OS opener on that
// path. The SSH markers are kept for the unix branch, where a
// detected remote session skips the opener entirely.
func claudeLoginEnv() []string {
	return []string{
		"BROWSER=wick-logintty-no-browser",
		"SSH_TTY=/dev/wick-logintty",
		"SSH_CLIENT=127.0.0.1 0 22",
		"SSH_CONNECTION=127.0.0.1 0 127.0.0.1 22",
	}
}

// claudeConfigDir honours the instance's CLAUDE_CONFIG_DIR, falling
// back to ~/.claude.
func claudeConfigDir(env []string) string {
	if dir := envValue(env, "CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeCredentialsPath is the file the CLI keeps this instance's OAuth
// tokens in — the one wick reads, and the one the CLI rewrites on every
// refresh.
func claudeCredentialsPath(dir string) string {
	return filepath.Join(dir, ".credentials.json")
}

// claudeCredentialsChangedAt is the credential file's mtime, or zero
// when there is no file to stat (never logged in, unreadable dir).
func claudeCredentialsChangedAt(env []string) time.Time {
	fi, err := os.Stat(claudeCredentialsPath(claudeConfigDir(env)))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// claudeAccessToken reads the OAuth access token from the instance's
// credential file.
func claudeAccessToken(dir string) (string, error) {
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if !readJSON(claudeCredentialsPath(dir), &creds) || creds.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("no claude credentials — not logged in")
	}
	return creds.ClaudeAiOauth.AccessToken, nil
}

// claudeUsageOrder pins the well-known windows first; unknown keys
// follow alphabetically so new API fields still show up.
var claudeUsageOrder = map[string]int{"five_hour": 0, "seven_day": 1, "seven_day_opus": 2}

// readClaudeUsage calls the same OAuth usage endpoint the claude CLI's
// /usage screen reads: rolling-window utilization percentages.
func readClaudeUsage(env []string) ([]UsageWindow, error) {
	token, err := claudeAccessToken(claudeConfigDir(env))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, anthropicAPIBase+"/api/oauth/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		// Typed, so the caller backs off instead of retrying on its
		// next tick — and honours the server's own cooldown when it
		// bothered to send one.
		return nil, &RateLimitedError{
			Status:     resp.Status,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage endpoint: %s", resp.Status)
	}

	// Tolerant parse: any top-level object carrying a numeric
	// "utilization" is a window; everything else is ignored.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	var windows []UsageWindow
	for key, val := range raw {
		var w struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		}
		if json.Unmarshal(val, &w) != nil || w.Utilization == nil {
			continue
		}
		win := UsageWindow{Key: key, Utilization: *w.Utilization}
		if ts, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
			win.ResetsAt = ts
		}
		windows = append(windows, win)
	}
	sort.Slice(windows, func(i, j int) bool {
		oi, iok := claudeUsageOrder[windows[i].Key]
		oj, jok := claudeUsageOrder[windows[j].Key]
		switch {
		case iok && jok:
			return oi < oj
		case iok:
			return true
		case jok:
			return false
		default:
			return windows[i].Key < windows[j].Key
		}
	})
	return windows, nil
}

// claudeAccountEmail reads the logged-in email for ONE credential dir,
// without the home-directory fallback readClaudeAccount uses.
//
// That fallback is right for display (the default instance keeps its
// .claude.json at ~/.claude.json, outside the dir) but wrong for
// identity: every dir lacking a local .claude.json would resolve to the
// home account and two unrelated logins would be merged into one probe.
// So the home file counts only for the dir it actually belongs to.
func claudeAccountEmail(dir string) string {
	var cfg struct {
		OauthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if readJSON(filepath.Join(dir, ".claude.json"), &cfg) && cfg.OauthAccount.EmailAddress != "" {
		return cfg.OauthAccount.EmailAddress
	}
	if home, err := os.UserHomeDir(); err == nil && dir == filepath.Join(home, ".claude") {
		if readJSON(filepath.Join(home, ".claude.json"), &cfg) {
			return cfg.OauthAccount.EmailAddress
		}
	}
	return ""
}

// claudeUsageIdentity keys the usage probe on the ACCOUNT rather than
// the folder: the endpoint rate-limits the login, so two instances on
// one login must cost one request.
//
// Email first — it survives a token refresh, so the key stays stable
// across the day. Then a hash of the access token: an identical token
// is literally the same upstream subject (a copied credential dir).
// Only the hash is kept, never the token itself. Not logged in yet ->
// the dir, which can split but can never merge two accounts.
func claudeUsageIdentity(env []string) string {
	dir := claudeConfigDir(env)
	if email := claudeAccountEmail(dir); email != "" {
		return "claude:email:" + strings.ToLower(email)
	}
	if token, err := claudeAccessToken(dir); err == nil {
		sum := sha256.Sum256([]byte(token))
		return "claude:token:" + hex.EncodeToString(sum[:8])
	}
	return "claude:dir:" + dir
}

func readClaudeAccount(dir string) Account {
	var acc Account
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken      string `json:"accessToken"`
			ExpiresAt        int64  `json:"expiresAt"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if readJSON(filepath.Join(dir, ".credentials.json"), &creds) {
		acc.Connected = creds.ClaudeAiOauth.AccessToken != ""
		acc.Plan = creds.ClaudeAiOauth.SubscriptionType
		if acc.Connected {
			acc.AuthMethod = "Claude AI"
		}
		if creds.ClaudeAiOauth.ExpiresAt > 0 {
			acc.ExpiresAt = time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt)
		}
	}
	var cfg struct {
		OauthAccount struct {
			EmailAddress     string `json:"emailAddress"`
			OrganizationName string `json:"organizationName"`
		} `json:"oauthAccount"`
	}
	// .claude.json sits inside CLAUDE_CONFIG_DIR when the override is
	// set, and at ~/.claude.json (home root, NOT ~/.claude/) by default.
	candidates := []string{filepath.Join(dir, ".claude.json")}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".claude.json"))
	}
	for _, p := range candidates {
		if readJSON(p, &cfg) && cfg.OauthAccount.EmailAddress != "" {
			acc.Email = cfg.OauthAccount.EmailAddress
			acc.Org = cfg.OauthAccount.OrganizationName
			break
		}
	}
	return acc
}

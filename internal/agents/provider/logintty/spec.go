// Package logintty runs a provider CLI's interactive login flow inside
// a wick-owned PTY so the Providers page can offer "Reconnect": the
// user gets a live terminal in the browser, wick tees the output to
// parse the OAuth login link, enforces a TTL countdown on the session,
// and reads the resulting credential files to report which account is
// connected.
//
// Per-type specifics live in their own file (claude.go, codex.go,
// gemini.go): the login argv and the credential-file account probe.
// Only claude's TTY login is wired up today; codex and gemini keep the
// account probe (used for connect-status display) and grow login
// support in their own files later.
package logintty

import (
	"github.com/yogasw/wick/internal/agents/provider"
)

// LoginCommand returns the argv (after the binary) that starts the
// interactive login flow for one provider type, and whether that type
// supports TTY login at all.
//
// extraArgs is the instance's configured ExtraArgs, forwarded when the
// login runs the main REPL so the instance's flags shape the spawn.
func LoginCommand(t provider.Type, extraArgs []string) ([]string, bool) {
	switch t {
	case provider.TypeClaude:
		return claudeLoginCommand(extraArgs), true
	default:
		// codex / gemini: TTY login lands in codex.go / gemini.go later.
		return nil, false
	}
}

// LoginEnv returns extra env vars injected ONLY into the login TTY
// spawn (never regular agent spawns) to shape the CLI's login flow.
func LoginEnv(t provider.Type) []string {
	switch t {
	case provider.TypeClaude:
		return claudeLoginEnv()
	default:
		return nil
	}
}

// ConfigDir resolves where a provider type keeps its credential files
// for an instance with the given env (KEY=VALUE list, the instance's
// Env). An env override (CLAUDE_CONFIG_DIR / CODEX_HOME) wins; the
// per-type home default is the fallback. Empty for types with no
// on-disk credentials (wick).
func ConfigDir(t provider.Type, env []string) string {
	switch t {
	case provider.TypeClaude:
		return claudeConfigDir(env)
	case provider.TypeCodex:
		return codexConfigDir(env)
	case provider.TypeGemini:
		return geminiConfigDir()
	default:
		return ""
	}
}

// ReadAccount reads the credential files under ConfigDir and reports
// who is connected. Missing or unreadable files yield a zero Account
// (Connected=false), never an error — "not logged in" is a normal
// state, not a failure.
func ReadAccount(t provider.Type, env []string) Account {
	dir := ConfigDir(t, env)
	if dir == "" {
		return Account{}
	}
	switch t {
	case provider.TypeClaude:
		return readClaudeAccount(dir)
	case provider.TypeCodex:
		return readCodexAccount(dir)
	case provider.TypeGemini:
		return readGeminiAccount(dir)
	}
	return Account{}
}

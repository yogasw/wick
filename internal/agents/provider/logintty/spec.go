// Package logintty runs a provider CLI's interactive login flow inside
// a wick-owned PTY so the Providers page can offer "Reconnect": the
// user gets a live terminal in the browser, wick tees the output to
// parse the OAuth login link, enforces a TTL countdown on the session,
// and reads the resulting credential files to report which account is
// connected.
//
// Per-type specifics live in their own file (claude.go, codex.go,
// gemini.go): the login argv and the credential-file account probe.
// claude and codex have TTY login wired up; gemini keeps only the
// account probe (used for connect-status display) and grows login
// support in its own file later.
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
	case provider.TypeCodex:
		// extraArgs are deliberately dropped — see codexLoginCommand.
		return codexLoginCommand(), true
	case provider.TypeOMP, provider.TypeOpencode:
		// Supported; the real argv needs the instance (profile / choice)
		// — see LoginCommandFor.
		return LoginCommandFor(provider.Instance{Type: t, Name: string(t)}, "")
	default:
		// gemini: TTY login lands in gemini.go later.
		return nil, false
	}
}

// LoginCommandFor is LoginCommand with the instance and the login choice
// the user picked (omp: an OMPLoginProviders id; opencode: an
// OpencodeLoginProviders id or a provider id). Empty choice = default.
// omp/opencode need the instance because their argv names its account
// store; other types ignore choice and defer to LoginCommand.
func LoginCommandFor(ins provider.Instance, choice string) ([]string, bool) {
	switch ins.Type {
	case provider.TypeOMP:
		return ompLoginCommand(provider.AccountEnv(ins), choice), true
	case provider.TypeOpencode:
		return opencodeLoginCommand(ins, choice), true
	}
	return LoginCommand(ins.Type, ins.ExtraArgs)
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
	case provider.TypeOMP:
		return ompConfigDir(env)
	case provider.TypeOpencode:
		return opencodeConfigDir(env)
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
	case provider.TypeOMP:
		return readOMPAccount(env)
	case provider.TypeOpencode:
		return readOpencodeAccount(dir)
	}
	return Account{}
}

// LoginChoice is one entry of the UI login picker.
type LoginChoice struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Warning string `json:"warning,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// LoginChoices lists the picker entries for t (nil = no picker).
// omp asks its binary for the registry (with the instance's account env);
// opencode asks its server (opencodeLoginChoices).
func LoginChoices(ins provider.Instance) []LoginChoice {
	var out []LoginChoice
	switch ins.Type {
	case provider.TypeOMP:
		for _, p := range ompLoginProviders(provider.AccountEnv(ins)) {
			out = append(out, LoginChoice{ID: p.ID, Label: p.Label, Warning: p.Warning, Default: p.Default})
		}
	case provider.TypeOpencode:
		for _, p := range opencodeLoginChoices(ins) {
			out = append(out, LoginChoice{ID: p.ID, Label: p.Label, Default: p.Default})
		}
	}
	return out
}

// LoginNote is the caveat shown beside t's login picker.
func LoginNote(t provider.Type) string {
	switch t {
	case provider.TypeOMP:
		return "One instance can hold several accounts: log in again to add another, and omp rotates between them automatically when one hits its usage limit. Separate instances still work for keeping accounts apart."
	case provider.TypeOpencode:
		return OpencodeClaudeNote
	}
	return ""
}

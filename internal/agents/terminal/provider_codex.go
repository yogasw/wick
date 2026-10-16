package terminal

import provider "github.com/yogasw/wick/internal/agents/provider"

func init() {
	// Subcommands as listed by `codex --help` / `codex login --help`.
	// Login uses --device-auth: the default browser flow waits for a
	// callback on localhost of the machine running codex, which the
	// admin's browser can never reach through a web terminal.
	Register(provider.TypeCodex, Provider{Commands: func(provider.Instance) []Command {
		return []Command{
			{Key: "tui", Label: "codex"},
			{Key: "login", Label: "codex login --device-auth", Args: []string{"login", "--device-auth"}},
			{Key: "login-status", Label: "codex login status", Args: []string{"login", "status"}},
		}
	}})
}

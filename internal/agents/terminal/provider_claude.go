package terminal

import provider "github.com/yogasw/wick/internal/agents/provider"

func init() {
	// Subcommands as listed by `claude --help` / `claude auth --help`.
	Register(provider.TypeClaude, Provider{Commands: func(provider.Instance) []Command {
		return []Command{
			{Key: "tui", Label: "claude"},
			{Key: "auth-login", Label: "claude auth login", Args: []string{"auth", "login"}},
			{Key: "auth-status", Label: "claude auth status", Args: []string{"auth", "status"}},
		}
	}})
}

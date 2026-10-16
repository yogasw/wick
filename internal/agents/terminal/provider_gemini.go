package terminal

import provider "github.com/yogasw/wick/internal/agents/provider"

func init() {
	Register(provider.TypeGemini, Provider{Commands: func(provider.Instance) []Command {
		return []Command{
			{Key: "tui", Label: "gemini"},
		}
	}})
}

package terminal

import provider "github.com/yogasw/wick/internal/agents/provider"

func init() {
	Register(provider.TypeOpencode, Provider{
		Commands: func(provider.Instance) []Command {
			return []Command{
				{Key: "tui", Label: "opencode"},
				{Key: "auth-login", Label: "opencode auth login", Args: []string{"auth", "login"}},
				{Key: "auth-list", Label: "opencode auth list", Args: []string{"auth", "list"}},
			}
		},
		// The config overrides are blanked the same way a spawn blanks
		// them, so the host's opencode config stays out.
		Env: func(_ provider.Instance, env []string) []string {
			return append(env, "OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR=")
		},
		// The data dir must exist before opencode writes into it.
		Prepare: func(ins provider.Instance) error {
			_, err := provider.OpencodeEnv(ins)
			return err
		},
	})
}

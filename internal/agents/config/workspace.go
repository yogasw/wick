package config

// WorkspaceConfig is kept for future per-workspace settings. Currently empty —
// the data directory is resolved from the platform default (~/.wick/agents/)
// and is not user-editable via the UI. Per-channel workspace binding is done
// via the channel config (e.g. SlackConfig.SlackWorkspace).
type WorkspaceConfig struct{}

// DefaultWorkspaceConfig returns the empty default.
func DefaultWorkspaceConfig() WorkspaceConfig {
	return WorkspaceConfig{}
}

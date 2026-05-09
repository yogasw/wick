package config

// WorkspaceConfig holds the base directory for all on-disk Agents
// state. See agents-design.md §8.3.
//
// An empty BaseDir means "use the platform default" — see
// ResolveBaseDir. Persisting an empty value lets the operator change
// hosts without us hard-coding a user-specific path into the configs
// table.
type WorkspaceConfig struct {
	BaseDir          string `wick:"desc=Base directory for all agents data (projects, sessions, presets). Default: ~/.wick/agents/."`
	DefaultWorkspace string `wick:"desc=Name of the workspace used when a session has no explicit workspace. Leave empty to use a per-session temp dir."`
}

// DefaultWorkspaceConfig returns the empty default.
func DefaultWorkspaceConfig() WorkspaceConfig {
	return WorkspaceConfig{}
}

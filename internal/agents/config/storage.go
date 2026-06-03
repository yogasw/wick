package config

// StorageConfig holds the base directory for all on-disk Agents state
// plus the global default project. See agents-design.md §8.3.
//
// An empty BaseDir means "use the platform default" — see
// ResolveBaseDir. Persisting an empty value lets the operator change
// hosts without us hard-coding a user-specific path into the configs
// table.
//
// DefaultProjectID is the project a session binds to when none is
// supplied (channels, API). Empty = no global default; the pool falls
// back to a per-session temp cwd.
type StorageConfig struct {
	BaseDir string
	// DefaultProjectID is exposed in the agents Settings page as a
	// dropdown (options injected at render from the live project list).
	// New sessions with no explicit project — channels, API, quick-create
	// — bind here. Empty = unscoped (per-session temp cwd).
	DefaultProjectID string `wick:"dropdown;key=default_project_id;desc=Default project for new sessions when none is picked (channels, API, quick-create). Leave empty for unscoped."`
}

// DefaultStorageConfig returns the empty default.
func DefaultStorageConfig() StorageConfig {
	return StorageConfig{}
}

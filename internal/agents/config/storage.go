package config

// StorageConfig holds the base directory for all on-disk Agents state.
// See agents-design.md §8.3.
//
// An empty BaseDir means "use the platform default" — see
// ResolveBaseDir. Persisting an empty value lets the operator change
// hosts without us hard-coding a user-specific path into the configs
// table.
type StorageConfig struct {
	BaseDir string
}

// DefaultStorageConfig returns the empty default.
func DefaultStorageConfig() StorageConfig {
	return StorageConfig{}
}

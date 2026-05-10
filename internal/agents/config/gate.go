package config

// GateConfig holds command-gate knobs. Reflected into the configs table
// via pkg/entity.StructToConfigs (Owner = "agents" at registration time).
type GateConfig struct {
	GateEnabled bool   `wick:"checkbox;hidden;desc=Route every Bash command through the gate sidecar. When off, the provider's own default permission handling applies (claude headless: blocks; others vary)."`
	AllowedCmds string `wick:"kvlist=pattern|scope;desc=Command whitelist. pattern supports a trailing * wildcard (e.g. 'git *'). scope (optional) restricts path args to a directory prefix."`
}

// DefaultGateConfig returns the seed values for the gate config.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		GateEnabled: true,
		AllowedCmds: "",
	}
}

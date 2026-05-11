package config

// GateConfig holds command-gate knobs. The master GateEnabled flag
// gates every per-provider Instance.Hooks intent: even when an
// instance has Hooks[PreToolUse].Enabled=true, the spawner only
// installs the hook config when this master flag is also true. This
// gives operators a single kill-switch without losing per-provider
// state.
type GateConfig struct {
	GateEnabled bool   `wick:"checkbox;hidden;desc=Master switch. When off, every provider falls back to its own default permission handling regardless of per-instance hook intent."`
	AllowedCmds string `wick:"kvlist=pattern|scope;desc=Command whitelist. pattern supports a trailing * wildcard (e.g. 'git *'). scope (optional) restricts path args to a directory prefix."`
}

// DefaultGateConfig returns the seed values for the gate config.
// Default OFF: per-provider semantics mean operators must explicitly
// turn the master switch on AND enable individual providers before
// any hook gets installed. Avoids surprising existing users who
// re-import their userconfig but haven't seen the new UI yet.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		GateEnabled: false,
		AllowedCmds: "",
	}
}

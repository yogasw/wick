package provider

import (
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
	"github.com/yogasw/wick/internal/userconfig"
)

// managed.go connects the provider layer to wick-managed binaries
// (managedbin). Resolution order for an instance:
//
//  1. its Binary override — manual mode, never managed;
//  2. the managed `current` version, when the type is managed and enabled;
//  3. PATH, then the per-OS known install locations.
//
// Spawns go through the same resolver, and MemGuard.Wrap wraps whatever
// path it returns, so a managed binary runs inside the agent memory scope
// exactly like one on PATH. wick never writes to PATH.

func init() {
	managedbin.Default.Root = ManagedBinRoot
	managedbin.Default.KeepVersions = managedKeepVersions
	managedbin.Default.Enabled = func(t string) bool { return ManagedEnabled(Type(t)) }
}

// ManagedBinRoot is <wick data dir>/providers/bin, "" when the data dir
// cannot be resolved.
func ManagedBinRoot() string {
	d, err := userconfig.Dir(AppName())
	if err != nil {
		return ""
	}
	return filepath.Join(d, "providers", "bin")
}

func managedConfig() *userconfig.ManagedBinariesConfig {
	cfg, err := userconfig.Load(AppName())
	if err != nil {
		return nil
	}
	return cfg.Providers.ManagedBinaries
}

func managedKeepVersions() int {
	if c := managedConfig(); c != nil && c.KeepVersions != 0 {
		if c.KeepVersions < 0 {
			return 0
		}
		return c.KeepVersions
	}
	return 2
}

// ManagedEnabled reports whether wick manages t's binary: a source is
// registered for it and providers.managed_binaries.<t>.enabled is not false.
func ManagedEnabled(t Type) bool {
	if _, ok := managedbin.Lookup(string(t)); !ok {
		return false
	}
	if c := managedConfig(); c != nil {
		if tc, ok := c.Types[string(t)]; ok && tc.Enabled != nil {
			return *tc.Enabled
		}
	}
	return true
}

// ManagedTypes lists the types wick currently manages (registered + enabled).
func ManagedTypes() []Type {
	var out []Type
	for _, s := range managedbin.Types() {
		if t := Type(s); isSupported(t) && ManagedEnabled(t) {
			out = append(out, t)
		}
	}
	return out
}

// managedPath is the managed binary an instance without override would
// run, when there is one.
func managedPath(ins Instance) (string, bool) {
	if ins.Binary != "" || !ManagedEnabled(ins.Type) {
		return "", false
	}
	p, _, ok := managedbin.Default.CurrentPath(string(ins.Type))
	return p, ok
}

// Binary sources reported by ResolveBinarySource / Status.Source.
const (
	BinSourceOverride = "registry" // instance Binary override (manual path)
	BinSourceManaged  = "managed"  // wick-managed current version
	BinSourcePath     = "path"
	BinSourceScan     = "scan"
	BinSourceMiss     = "miss"
)

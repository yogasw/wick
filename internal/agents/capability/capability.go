// Package capability declares per-provider hook-support metadata and a
// self-registering registry for it.
//
// Why a separate package: provider sub-packages (provider/claude,
// provider/codex, …) need to declare their own Capability in init().
// If Capability lived in the parent provider/ package, those sub-packages
// would have to import their parent — but provider/ would then need to
// know about its children to seed the registry, creating a cycle.
// Hosting the registry here breaks that loop: every package imports
// capability/, and capability/ imports nothing from agents/.
//
// Lifecycle:
//
//  1. At program init, each provider sub-package calls Register(name, cap)
//     describing what its hook adapter can do statically (HookSupported,
//     InterceptScope). Nothing runs the provider binary yet.
//  2. Later, when the UI toggles gate ON for an instance, HookCapabilityCheck
//     spawns the real binary with a sentinel and flips HookVerified.
//     That runtime probe lives in a separate file (check.go, D5).
package capability

import (
	"sync"
	"time"
)

// Capability is the per-provider hook support metadata.
//
// HookSupported is the static declaration: "an adapter for this provider
// exists in the codebase and is wired into the gate binary." UI uses
// this to decide whether the gate toggle is even available.
//
// HookVerified is the dynamic claim: "we spawned the real binary and
// confirmed the deny path is honored." Set by HookCapabilityCheck (D5);
// stays false until that probe runs successfully.
//
// InterceptScope is a free-form short label describing which tool
// classes the hook covers ("bash+edit+mcp", "shell-only", "untested").
// Surfaced in the UI badge so users see coverage at a glance.
type Capability struct {
	HookSupported  bool
	HookVerified   bool
	HookProbedAt   time.Time
	HookError      string
	InterceptScope string
}

// registry holds the static Capability per provider name. Written by
// init() functions in provider sub-packages via Register, read by the
// UI / spawn path via Lookup.
//
// Concurrency: Register is expected to run during package init (single-
// threaded), but tests may call it in parallel — guard with a mutex so
// `go test -race` stays clean.
var (
	registryMu sync.RWMutex
	registry   = map[string]Capability{}
)

// Register stores a Capability under name. Last write wins — duplicate
// registration silently overwrites, which is what tests want when they
// re-seed the registry. Init-time duplicates from real code would be a
// bug worth catching, but checking that here would force tests to deal
// with panics; we accept the trade-off and rely on code review instead.
func Register(name string, c Capability) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = c
}

// Lookup returns the registered Capability for name. The second return
// is false when no provider has registered under name yet — callers
// should treat that as "hook not supported" rather than panicking.
func Lookup(name string) (Capability, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// All returns a snapshot of every registered Capability, keyed by name.
// Used by the Providers UI to render one row per known provider in a
// stable order regardless of registration timing.
func All() map[string]Capability {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]Capability, len(registry))
	for k, v := range registry {
		out[k] = v
	}
	return out
}

// reset clears the registry. Test-only — production code never unregisters.
func reset() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Capability{}
}

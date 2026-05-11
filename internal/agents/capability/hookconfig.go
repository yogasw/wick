package capability

import (
	"context"
	"errors"
	"sync"
)

// ErrUnsupported is returned by HookConfigWriter implementations whose
// provider does not yet have a verified hook integration. Gemini is the
// canonical case: an adapter is shipped in code (so Capability shows up
// in the registry) but the on-disk hook config format hasn't been
// validated against the real CLI, so the writer refuses to touch the
// filesystem rather than write something that might trip the user up.
var ErrUnsupported = errors.New("hook config not supported for this provider")

// HookConfigWriter is the per-provider strategy for installing /
// removing the hook command in the provider's project-scoped settings
// directory.
//
// Lifecycle (per spawn):
//
//   - GateEnabled=true  → Write(workspace, gateBin)
//   - GateEnabled=false → Remove(workspace) (clean up stale config
//     from a previous run where gate was on)
//
// Implementations must be idempotent: Write twice with the same args
// produces the same on-disk content; Remove on a non-existent file is
// a no-op. The point is that a partially-failed previous run leaves
// the world in a recoverable state.
//
// DryRun(workspace, gateBin) returns the path the writer *would* touch
// without actually writing. The capability probe (D5) uses it to spawn
// the provider against a temp workspace without polluting any real
// settings file.
type HookConfigWriter interface {
	Write(workspace, gateBin string) error
	Remove(workspace string) error
	DryRun(workspace, gateBin string) (path string, content []byte, err error)
}

var (
	writersMu sync.RWMutex
	writers   = map[string]HookConfigWriter{}
)

// RegisterHookConfigWriter pairs a HookConfigWriter with a provider
// name. Called from provider sub-package init() functions alongside
// Register so the gate-toggle path can look up "for this provider,
// which writer installs the hook?" without a central switch.
func RegisterHookConfigWriter(name string, w HookConfigWriter) {
	writersMu.Lock()
	defer writersMu.Unlock()
	writers[name] = w
}

// LookupHookConfigWriter returns the writer registered for name. The
// second return is false when no provider has registered one — the
// spawn path treats that as "this provider can't be gated yet" and
// skips both Write and Remove rather than guessing a default.
func LookupHookConfigWriter(name string) (HookConfigWriter, bool) {
	writersMu.RLock()
	defer writersMu.RUnlock()
	w, ok := writers[name]
	return w, ok
}

// resetWriters clears the writer registry. Test-only.
func resetWriters() {
	writersMu.Lock()
	defer writersMu.Unlock()
	writers = map[string]HookConfigWriter{}
}

// Prober is the per-provider strategy for verifying that a freshly-
// installed hook config actually intercepts shell commands. Called by
// HookCapabilityCheck (D5) once the writer (above) has put a probe
// config into a throwaway workspace. The implementation spawns the
// real provider binary, asks it to touch a sentinel file, waits for
// the spawn to exit, and reports nil if the sentinel did not appear
// (deny was honored). Any non-nil error — sentinel created, spawn
// failed, timeout — flips HookVerified to false on the registry entry.
//
// Probers are provider-specific because each CLI has its own way of
// receiving a one-shot prompt: claude takes stream-json on stdin,
// codex has `codex exec`, gemini has `-p`. Encoding that variety here
// would couple capability/ to every CLI's flag surface; the Prober
// interface keeps it abstract.
type Prober interface {
	SendSentinel(ctx context.Context, workspace, sentinelPath string) error
}

var (
	probersMu sync.RWMutex
	probers   = map[string]Prober{}
)

// RegisterProber pairs a Prober with a provider name. Same self-
// registration pattern as HookConfigWriter — called from provider
// sub-package init() so adding a new CLI doesn't require touching a
// central dispatch.
func RegisterProber(name string, p Prober) {
	probersMu.Lock()
	defer probersMu.Unlock()
	probers[name] = p
}

// LookupProber returns the prober registered for name. False means
// "this provider has no probe defined" — HookCapabilityCheck treats
// that as HookVerified=false with HookError="prober not registered".
func LookupProber(name string) (Prober, bool) {
	probersMu.RLock()
	defer probersMu.RUnlock()
	p, ok := probers[name]
	return p, ok
}

// resetProbers clears the prober registry. Test-only.
func resetProbers() {
	probersMu.Lock()
	defer probersMu.Unlock()
	probers = map[string]Prober{}
}

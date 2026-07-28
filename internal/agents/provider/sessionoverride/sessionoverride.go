// Package sessionoverride is the provider-agnostic registry + runtime store for
// per-session setting overrides — the values the composer's `/`-menu popover
// edits WITHOUT sending a chat message (e.g. wick's /thinking reasoning toggle).
//
// Two halves:
//
//   - Registry: each provider registers ONE wick-tagged struct describing its
//     overridable session settings. entity.StructToConfigs turns that struct
//     into a []entity.Config field spec the FE renders generically (toggle /
//     dropdown / text), and entity.MapToStruct reads the saved values back into
//     the typed struct for the provider to consume. Adding a new override =
//     adding a tagged field to that struct; no FE change.
//
//   - Store: a per-session map[key]value, write-through to a sidecar JSON in the
//     session dir (overrides.json) so a toggle survives what the in-memory map
//     cannot: an engine exit (idle reap), a page refresh, or a wick restart.
//     Same process as the HTTP handler and the engine goroutine, so a plain
//     guarded map fronts the file (no socket).
//
// Persistence is opt-in via SetSessionDirResolver: until a resolver is wired the
// store is memory-only, which keeps tests and any embedder that has no on-disk
// layout working unchanged. The sidecar is loaded lazily on first read of a
// session and cached, so the steady state stays a map lookup.
//
// The sidecar deliberately lives NEXT TO meta.json rather than inside it:
// override keys are provider-specific (wick's thinking_on, another provider's
// own fields), so folding them into session.Meta would tie a shared schema to
// one provider's knobs. Same reasoning as compaction.json.
//
// The registry is keyed by provider type ("wick", "claude", …) so different
// providers surface different fields; the composer asks for the schema of the
// session's active provider.
package sessionoverride

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/entity"
)

// specs maps provider type → the wick-tagged struct describing its session
// overrides. Registered once at init; read-only after.
var (
	specMu sync.RWMutex
	specs  = map[string]any{}
)

// Register records the override struct for a provider type. The struct's
// exported wick-tagged fields become the popover's editable rows. Call once
// per provider (typically from an init()). A second call for the same type
// replaces the first.
func Register(providerType string, structPtr any) {
	if providerType == "" || structPtr == nil {
		return
	}
	specMu.Lock()
	specs[providerType] = structPtr
	specMu.Unlock()
}

// Schema returns the field spec for a provider type as []entity.Config, or nil
// when the provider registered none. The Value on each row reflects the struct
// default (from the wick tag), NOT the session's current value — the caller
// overlays saved values via Values / applies them via Apply.
func Schema(providerType string) []entity.Config {
	specMu.RLock()
	s, ok := specs[providerType]
	specMu.RUnlock()
	if !ok {
		return nil
	}
	return entity.StructToConfigs(s)
}

// HasSchema reports whether a provider type registered any overrides — used to
// decide whether the composer should show the override affordance at all.
func HasSchema(providerType string) bool {
	specMu.RLock()
	_, ok := specs[providerType]
	specMu.RUnlock()
	return ok
}

// ── runtime value store ────────────────────────────────────────────────────

// sidecarName is the per-session override file, written next to meta.json /
// conversation.jsonl under the session dir.
const sidecarName = "overrides.json"

// store is the per-session override values: sessionID → (key → value). Fronts
// the on-disk sidecar; `loaded` marks which sessions have already been read from
// disk so an absent sidecar isn't re-stat'ed on every access.
var (
	storeMu sync.RWMutex
	store   = map[string]map[string]string{}
	loaded  = map[string]bool{}
)

// sessionDirFn resolves a session ID to its on-disk directory. nil (the default)
// means persistence is disabled and the store behaves exactly as the old
// memory-only one — the mode tests and layout-less embedders run in.
var (
	resolverMu   sync.RWMutex
	sessionDirFn func(sessionID string) string
)

// SetSessionDirResolver wires the session-dir lookup that enables persistence.
// Called once during boot with the app layout. Passing nil disables persistence
// again (used by tests to get a clean memory-only store).
func SetSessionDirResolver(fn func(sessionID string) string) {
	resolverMu.Lock()
	sessionDirFn = fn
	resolverMu.Unlock()
	// Drop cached load-marks so sessions touched before the resolver existed are
	// re-read from disk on next access rather than staying stuck at "empty".
	storeMu.Lock()
	loaded = map[string]bool{}
	storeMu.Unlock()
}

// sidecarPath returns the sidecar path for a session, or "" when persistence is
// off or the session dir is unknown/absent.
func sidecarPath(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	resolverMu.RLock()
	fn := sessionDirFn
	resolverMu.RUnlock()
	if fn == nil {
		return ""
	}
	dir := fn(sessionID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, sidecarName)
}

// loadLocked reads the sidecar into the store once per session. Caller must hold
// the write lock. A missing sidecar is the common case (no override ever set); a
// malformed one is logged and treated as absent so a corrupt file can't wedge
// the session.
func loadLocked(sessionID string) {
	if loaded[sessionID] {
		return
	}
	loaded[sessionID] = true
	path := sidecarPath(sessionID)
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var vals map[string]string
	if err := json.Unmarshal(b, &vals); err != nil {
		log.Warn().Err(err).Str("path", path).
			Msg("sessionoverride: malformed sidecar, ignoring")
		return
	}
	if len(vals) > 0 {
		store[sessionID] = vals
	}
}

// saveLocked overwrites the sidecar for a session (write temp, rename) so a
// crash mid-write can't leave a half-file. Caller must hold the write lock.
// Best-effort: a failure is logged, not returned — the in-memory value still
// took effect for this process.
func saveLocked(sessionID string) {
	path := sidecarPath(sessionID)
	if path == "" {
		return
	}
	vals := store[sessionID]
	if len(vals) == 0 {
		// Nothing left to remember — drop the file rather than leaving "{}".
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Warn().Err(err).Str("path", path).Msg("sessionoverride: remove sidecar failed")
		}
		return
	}
	b, err := json.Marshal(vals)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		// A session dir that doesn't exist yet is normal for a session whose
		// first action is flipping a toggle; debug, not warn.
		log.Debug().Err(err).Str("path", tmp).Msg("sessionoverride: write sidecar failed")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("sessionoverride: rename sidecar failed")
		_ = os.Remove(tmp)
	}
}

// Set records one override value for a session (key = the field's config key,
// value = its string form) and write-throughs to the sidecar so it survives a
// reap/refresh/restart. Empty sessionID / key is a no-op.
func Set(sessionID, key, value string) {
	if sessionID == "" || key == "" {
		return
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	loadLocked(sessionID) // merge onto any persisted values, don't clobber them
	m := store[sessionID]
	if m == nil {
		m = map[string]string{}
		store[sessionID] = m
	}
	m[key] = value
	saveLocked(sessionID)
}

// Values returns a COPY of a session's override map (empty when none), reading
// the sidecar on first access. Safe to read/mutate without holding the lock.
func Values(sessionID string) map[string]string {
	storeMu.Lock()
	defer storeMu.Unlock()
	loadLocked(sessionID)
	src := store[sessionID]
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Apply overlays a session's saved override values onto dst (a pointer to the
// provider's override struct), so the provider reads typed fields. Fields with
// no saved value keep their struct default (via the wick `default=` tag).
func Apply(sessionID string, dst any) {
	entity.MapToStruct(Values(sessionID), dst)
}

// Clear drops all overrides for a session — an explicit "reset to defaults", or
// session deletion. It removes the sidecar too, so the reset is permanent rather
// than reappearing on the next read.
//
// NOTE: this is deliberately NOT called on engine teardown any more. Doing so
// meant an idle-reaped session silently lost the user's toggle (the reported
// "refresh and it's off again" bug) — overrides are now expected to outlive the
// engine goroutine.
func Clear(sessionID string) {
	if sessionID == "" {
		return
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	delete(store, sessionID)
	// Mark loaded so a subsequent read doesn't pull the file back in before the
	// removal below lands (and so an absent file isn't re-read).
	loaded[sessionID] = true
	if path := sidecarPath(sessionID); path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Warn().Err(err).Str("path", path).Msg("sessionoverride: remove sidecar failed")
		}
	}
}

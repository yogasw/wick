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
//   - Store: a runtime, per-session map[key]value. NOT persisted — a fresh
//     session falls back to the provider's configured baseline. Cleared on
//     session teardown. Same process as the HTTP handler and the engine
//     goroutine, so a plain guarded map suffices (no socket).
//
// The registry is keyed by provider type ("wick", "claude", …) so different
// providers surface different fields; the composer asks for the schema of the
// session's active provider.
package sessionoverride

import (
	"sync"

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

// store is the per-session override values: sessionID → (key → value). Runtime
// only.
var (
	storeMu sync.RWMutex
	store   = map[string]map[string]string{}
)

// Set records one override value for a session (key = the field's config key,
// value = its string form). Empty sessionID / key is a no-op.
func Set(sessionID, key, value string) {
	if sessionID == "" || key == "" {
		return
	}
	storeMu.Lock()
	m := store[sessionID]
	if m == nil {
		m = map[string]string{}
		store[sessionID] = m
	}
	m[key] = value
	storeMu.Unlock()
}

// Values returns a COPY of a session's override map (empty when none). Safe to
// read/mutate without holding the lock.
func Values(sessionID string) map[string]string {
	storeMu.RLock()
	defer storeMu.RUnlock()
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

// Clear drops all overrides for a session (teardown, or a "reset to defaults"
// action). A no-op for an unknown session.
func Clear(sessionID string) {
	if sessionID == "" {
		return
	}
	storeMu.Lock()
	delete(store, sessionID)
	storeMu.Unlock()
}

package agentmemory

import (
	"context"
	"errors"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// ErrWebDisabled is what a backend returns when the read it was asked for only
// exists on the daemon's HTTP API and that API is not mounted — ai-memory
// serves /api/v1 and /web only under --enable-web (PLAN §13.1). Handlers turn
// it into a named reason on the response so the FE can say "turn on the web
// API in Settings" instead of showing a bare 404.
var ErrWebDisabled = errors.New("agentmemory: the backend's web API is disabled (enable_web is off)")

// ConfigStore persists the daemon-level Agent Memory settings and answers the
// access questions the control endpoints ask. The hosting package backs it
// with wick's config service + auth, so this package stays free of storage and
// login imports — the same split airouter.ConfigStore uses next door.
type ConfigStore interface {
	// Enabled is the master switch. False hides every Agent Memory surface.
	Enabled() bool
	// ReadAllowed reports whether the caller may SEE the panel — the
	// status, the projects, the wiki, the masked settings. Anyone logged
	// in, by Yoga's decision (PLAN §23.4): narrowing it later is a change
	// to this one method and nothing else.
	ReadAllowed(ctx context.Context) bool
	// ManageAllowed reports whether the caller may DRIVE it — start/stop,
	// install, save settings, backfill, compact, sweep, and read the
	// daemon log. Admin only: these change the host or expose paths and
	// values a viewer has no business seeing.
	ManageAllowed(ctx context.Context) bool
	// Settings reads the persisted settings for backend id.
	Settings(id string) Settings
	// SaveSettings persists them. Autostart is written as given; the lock
	// is derived at read time, never stored (see Settings.AutostartLocked).
	SaveSettings(ctx context.Context, id string, s Settings) error
}

// Settings are the daemon-level knobs — everything that belongs to the store
// and the process rather than to one provider instance.
//
// DataDir is the clearest case: one daemon already holds every workspace and
// project in one store, and the separation that matters happens on the
// workspace/project axis via the .ai-memory.toml marker. A store per instance
// would buy no isolation and cost ~130-190 MB of RSS each (PLAN §19).
type Settings struct {
	// DataDir is the store location, shared by every backend. Empty = the
	// backend's own default path.
	DataDir string `json:"data_dir"`
	// Port is the loopback port the daemon binds. 0 = the backend's
	// preferred port. A taken port is remapped at start.
	Port int `json:"port"`
	// EnableWeb mounts the backend's own HTTP API + web UI. ON by default
	// for a wick-managed daemon: the panel's project list and search are
	// served by that API, so with it off those two reads report
	// ErrWebDisabled and the Projects tab is empty out of the box. The bind
	// is loopback and wick is the only consumer, so there is no surface to
	// save by leaving it off (Yoga, 2026-09-25). Still a toggle — and
	// ErrWebDisabled still happens, for an external daemon wick did not
	// start (PLAN §13.2.1).
	EnableWeb bool `json:"enable_web"`
	// Autostart starts the daemon at boot. Stored, but read through
	// EffectiveAutostart — an instance using Agent Memory forces it on.
	Autostart bool `json:"autostart"`
	// AutostartLocked is derived, never stored: true while at least one
	// provider instance has the toggle on. Yoga's rule is that a daemon
	// something depends on cannot be left switched off, so the FE renders
	// this as a forced-on, disabled control rather than as a switch that
	// can disagree with the provider toggles (PLAN §10.3).
	AutostartLocked bool `json:"autostart_locked"`
	// BackfillMaxSessions is wick's default for `backfill --max-sessions`.
	// The backend's own default is 25, which silently truncates a long
	// history, so wick carries a much larger one (PLAN §10.6).
	BackfillMaxSessions int `json:"backfill_max_sessions"`
	// Tuning is the rest of the daemon's configuration — access, capture,
	// providers, retention, ranking. Embedded so it flattens into the same
	// JSON object the settings form posts and reads back.
	//
	// It is separate from the fields above because it reaches the daemon a
	// different way: those five are wick's own decisions (which port, which
	// store, start it or not), while Tuning is the backend's own config
	// applied on the launch line instead of by rewriting its config file.
	// See tuning.go.
	Tuning
}

// DefaultBackfillMaxSessions is wick's ceiling for one backfill run. The
// backend defaults to 25; for anyone who has been using an agent CLI for a
// while that quietly drops most of their history, and the dropped count only
// shows up as skipped_for_cap in the report. 2000 is high enough that the cap
// stops being the thing that decides what gets imported.
const DefaultBackfillMaxSessions = 2000

// EffectiveAutostart is the flag the daemon is actually started by: the stored
// value OR the lock. Read this, never Settings.Autostart.
func (s Settings) EffectiveAutostart() bool { return s.Autostart || s.AutostartLocked }

// MaxSessions resolves the backfill cap, falling back to wick's default so a
// setting nobody has touched never inherits the backend's 25.
func (s Settings) MaxSessions() int {
	if s.BackfillMaxSessions > 0 {
		return s.BackfillMaxSessions
	}
	return DefaultBackfillMaxSessions
}

// InstanceRef names one provider instance that uses a memory backend, with
// enough state for the Overview list to say whether it records or only reads.
type InstanceRef struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Capture false = recall only: the instance reads the store and writes
	// nothing back. A whole host of read-only instances is why a store can
	// look alive and still capture nothing (PLAN §7.1).
	Capture bool `json:"capture"`
	// ServerURL is set only when the instance overrides the managed daemon
	// — a sign its numbers come from somewhere else entirely.
	ServerURL string `json:"server_url,omitempty"`
}

// UsedBy lists the provider instances wired to backend id. It is what makes
// autostart a derived state instead of a switch: as long as this is non-empty,
// something depends on the daemon.
//
// A load failure yields nil, which unlocks autostart rather than pinning it on
// from a failed read — the user stays in control of a setting wick could not
// verify.
func UsedBy(id string) []InstanceRef {
	list, err := loadInstances()
	if err != nil {
		return nil
	}
	var out []InstanceRef
	for _, ins := range list {
		if !ins.UseAgentMemory || backendID(ins.AgentMemoryProvider) != id {
			continue
		}
		out = append(out, InstanceRef{
			Type:      string(ins.Type),
			Name:      ins.Name,
			Capture:   ins.AgentMemoryCapture,
			ServerURL: ins.AgentMemoryServerURL,
		})
	}
	return out
}

// loadInstances is where UsedBy gets the provider list. A var so the derived
// autostart rule can be tested without a config file on disk.
var loadInstances = provider.Load

// AutostartLock is the derived state behind the autostart control: whether it
// is forced on, and by whom. It is what the FE renders instead of a plain
// switch — a daemon something depends on cannot be left switched off, and a
// disabled control that says why is honest where a switch that silently
// disagrees with the provider toggles is not (PLAN §10.3).
type AutostartLock struct {
	// Locked is true while at least one provider instance uses the backend.
	Locked bool `json:"locked"`
	// UsedBy is who holds the lock — empty when nothing does.
	UsedBy []InstanceRef `json:"used_by"`
	// Reason is the sentence to show next to the disabled control, naming
	// the instances. Empty when unlocked.
	Reason string `json:"reason,omitempty"`
}

// AutostartLockFor computes the lock for backend id. One provider load per
// call, so a handler that needs both the lock and the instance list should
// call this once rather than calling UsedBy again.
func AutostartLockFor(id string) AutostartLock {
	used := UsedBy(id)
	l := AutostartLock{Locked: len(used) > 0, UsedBy: used}
	if !l.Locked {
		return l
	}
	names := make([]string, 0, len(used))
	for _, r := range used {
		names = append(names, r.Label())
	}
	l.Reason = "Autostart is on because " + strings.Join(names, ", ") + " use Agent Memory."
	if len(names) == 1 {
		l.Reason = "Autostart is on because " + names[0] + " uses Agent Memory."
	}
	return l
}

// Label is how an instance is named in the UI: "type/name", collapsed to the
// bare type for the per-type default instance (whose name is its type).
func (r InstanceRef) Label() string {
	if r.Name == "" || r.Name == r.Type {
		return r.Type
	}
	return r.Type + "/" + r.Name
}

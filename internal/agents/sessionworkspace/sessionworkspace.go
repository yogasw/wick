// Package sessionworkspace stores a per-session "workspace": a set of
// ephemeral connector instances cloned from a base module (httprest,
// custom API defs, …). It backs the wick_session_workspace MCP tool and
// the session Config tab.
//
// A session instance is a throwaway clone: it has its own id, a base
// module key, a label, and a config map — exactly the configurable
// fields of the base module, filled in for this session only. It shows
// up in wick_list / wick_get / wick_execute only when the caller passes
// the owning session_id, behaving like a brand-new connector that lives
// and dies with the session dir.
//
// Secret config values are stored as wick_cenc_ MASTER tokens (system
// decryptable only) — callers encrypt BEFORE Set/Add; this package
// never sees or persists plaintext secrets. The whole file dies with
// the session, which is the intended instance lifetime.
package sessionworkspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// IDPrefix marks a connector id as a session-workspace instance, so MCP
// handlers can route it to the workspace resolver instead of the DB.
const IDPrefix = "sw_"

// IsInstanceID reports whether an id refers to a session-workspace
// instance (vs a real DB connector row).
func IsInstanceID(id string) bool { return strings.HasPrefix(id, IDPrefix) }

// mu serializes read-modify-write cycles on the workspace file.
// Per-session granularity isn't worth the bookkeeping — the workspace
// changes at human speed (a modal submit), never in a hot path.
var mu sync.Mutex

// onChange is told the session id after every persisted change, so an
// open Config tab can re-read the workspace instead of waiting for a page
// reload. Set once at startup; nil means nobody is listening.
var onChange func(sessionID string)

// SetOnChange registers the change listener (see onChange).
func SetOnChange(fn func(sessionID string)) { onChange = fn }

// Instance is one ephemeral connector clone scoped to a session.
type Instance struct {
	ID      string `json:"id"`       // "sw_<uuid>"
	BaseKey string `json:"base_key"` // module key it clones (e.g. "httprest")
	Label   string `json:"label"`
	// Config holds the instance's config field values. Secret fields are
	// wick_cenc_ master tokens; non-secret fields are plaintext.
	Config map[string]string `json:"config"`
	// LockSeals holds, per session-locked config key, the server's seal of
	// the value a human set in the Config tab (see connectors.Service.
	// SealSessionLock). A locked key without a valid seal runs with the
	// module default, so editing this file cannot unlock it.
	LockSeals map[string]string `json:"lock_seals,omitempty"`
	// CreatedBy is "ai" (added via the MCP tool) or "user" (added in the
	// Config tab) — drives the "agent added this, please fill it" notice.
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	// ExpiresAt (RFC3339) pins the instance to a fixed lifetime the user
	// chose: it survives the session going idle and is deleted by the
	// session-connectors-expiry job once this time passes. Empty = the
	// default lifetime, reaped when the session sits idle past IdleGrace.
	ExpiresAt string `json:"expires_at,omitempty"`
	// IdleAfterSec widens the idle window for this instance alone: it is
	// reaped once its session has been idle this long instead of IdleGrace
	// (e.g. a staging connector kept for a day of inactivity). Zero = the
	// default window. Ignored when ExpiresAt is set.
	IdleAfterSec int `json:"idle_after_sec,omitempty"`
}

// IdleWindow is how long the instance's session may sit idle before the
// instance is reaped.
func (in Instance) IdleWindow() time.Duration {
	if in.IdleAfterSec > 0 {
		return time.Duration(in.IdleAfterSec) * time.Second
	}
	return IdleGrace
}

// MaxExpiry caps how far ahead an instance may be pinned. The config holds
// secrets, so even a pinned clone must not outlive a week of neglect.
const MaxExpiry = 7 * 24 * time.Hour

// Pinned reports whether the instance carries its own expiry, which exempts
// it from the idle reap.
func (in Instance) Pinned() bool { return in.ExpiresAt != "" }

// Expired reports whether a pinned instance's expiry has passed. An
// unparseable stamp counts as expired: a corrupt pin must not keep secrets
// alive forever.
func (in Instance) Expired(now time.Time) bool {
	if in.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, in.ExpiresAt)
	return err != nil || !now.Before(t)
}

// ParseExpiresIn turns a user-supplied lifetime ("90m", "4h", "2d") — an
// expires_in or an idle_after — into a
// duration. "", "0", "none" and "idle" mean "no pin" (zero). Anything above
// MaxExpiry or below one minute is refused rather than silently clamped, so
// the caller learns the real limit.
func ParseExpiresIn(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "0", "none", "idle":
		return 0, nil
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%q: want e.g. 90m, 4h or 2d", s)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q: want e.g. 90m, 4h or 2d", s)
		}
	}
	if d < time.Minute || d > MaxExpiry {
		return 0, fmt.Errorf("%q: must be between 1m and 7d", s)
	}
	return d, nil
}

// ParseLifetime reads the two lifetime knobs a caller may pass — expires_in
// (fixed lifetime) and idle_after (custom idle window) — and returns what
// SetLifetime takes. Passing both is refused; passing neither (or "none")
// means the default idle window.
func ParseLifetime(now time.Time, expiresIn, idleAfter string) (string, time.Duration, error) {
	ttl, err := ParseExpiresIn(expiresIn)
	if err != nil {
		return "", 0, fmt.Errorf("expires_in %w", err)
	}
	idle, err := ParseExpiresIn(idleAfter)
	if err != nil {
		return "", 0, fmt.Errorf("idle_after %w", err)
	}
	if ttl > 0 && idle > 0 {
		return "", 0, fmt.Errorf("pass expires_in or idle_after, not both")
	}
	return ExpiryStamp(now, ttl), idle, nil
}

// ExpiryStamp returns the ExpiresAt value for a lifetime starting now; zero
// yields "" (unpinned).
func ExpiryStamp(now time.Time, d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return now.Add(d).UTC().Format(time.RFC3339)
}

// Tombstone is the trace left behind when an instance is auto-reaped for
// session inactivity. It carries NO config — the whole point of reaping is
// that the config (secrets and all) is gone. It exists so the Config tab and
// the agent can say "this connector was here, it was deleted, re-create it if
// you still need it" instead of the instance silently vanishing.
type Tombstone struct {
	Label     string `json:"label"`
	BaseKey   string `json:"base_key"`
	DeletedAt string `json:"deleted_at"`       // RFC3339
	Reason    string `json:"reason,omitempty"` // e.g. "session idle"
}

// Workspace is the full per-session document. Tombstones records instances
// auto-reaped for inactivity so the UI can surface a "deleted — re-create"
// notice; they hold no config.
type Workspace struct {
	Instances  []Instance  `json:"instances"`
	Tombstones []Tombstone `json:"tombstones,omitempty"`
}

// NewInstanceID mints a fresh session-instance id.
func NewInstanceID() string { return IDPrefix + uuid.NewString() }

// Load reads the full workspace for one session. A missing file is an
// empty workspace, not an error.
func Load(layout agentconfig.Layout, sessionID string) (Workspace, error) {
	b, err := os.ReadFile(layout.SessionWorkspace(sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return Workspace{}, nil
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("read session workspace: %w", err)
	}
	var out Workspace
	if err := json.Unmarshal(b, &out); err != nil {
		return Workspace{}, fmt.Errorf("parse session workspace: %w", err)
	}
	return out, nil
}

// List returns all live instances in a session, in stored order. Expired
// instances are hidden here even before the sweeper reclaims their file — an
// expired instance is dead, so it must not appear or be usable in the gap
// between expiry and physical deletion.
func List(layout agentconfig.Layout, sessionID string) ([]Instance, error) {
	ws, err := Load(layout, sessionID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := ws.Instances[:0]
	for _, in := range ws.Instances {
		if !in.Expired(now) {
			out = append(out, in)
		}
	}
	return out, nil
}

// Tombstones returns the traces of instances auto-reaped for inactivity in a
// session, so the UI/agent can show "was here, deleted, re-create" notices.
func Tombstones(layout agentconfig.Layout, sessionID string) ([]Tombstone, error) {
	ws, err := Load(layout, sessionID)
	if err != nil {
		return nil, err
	}
	return ws.Tombstones, nil
}

// Get returns one instance by id. ok=false when it doesn't exist.
func Get(layout agentconfig.Layout, sessionID, instanceID string) (Instance, bool, error) {
	ws, err := Load(layout, sessionID)
	if err != nil {
		return Instance{}, false, err
	}
	for _, in := range ws.Instances {
		if in.ID == instanceID {
			if in.Expired(time.Now()) {
				return Instance{}, false, nil
			}
			return in, true, nil
		}
	}
	return Instance{}, false, nil
}

// Add appends a new instance and persists it. The id is minted here if
// empty. Returns the stored instance (with its final id).
func Add(layout agentconfig.Layout, sessionID string, in Instance) (Instance, error) {
	if strings.TrimSpace(in.BaseKey) == "" {
		return Instance{}, fmt.Errorf("base_key is required")
	}
	if in.ID == "" {
		in.ID = NewInstanceID()
	}
	if in.Config == nil {
		in.Config = map[string]string{}
	}
	mu.Lock()
	ws, err := Load(layout, sessionID)
	if err != nil {
		mu.Unlock()
		return Instance{}, err
	}
	ws.Instances = append(ws.Instances, in)
	// Re-creating a connector that was reaped clears its tombstone — the
	// "deleted, re-create it" notice no longer applies once it's back.
	if len(ws.Tombstones) > 0 && strings.TrimSpace(in.Label) != "" {
		kept := ws.Tombstones[:0]
		for _, t := range ws.Tombstones {
			if t.Label == in.Label && t.BaseKey == in.BaseKey {
				continue
			}
			kept = append(kept, t)
		}
		ws.Tombstones = kept
	}
	if err := save(layout, sessionID, ws); err != nil {
		mu.Unlock()
		return Instance{}, err
	}
	mu.Unlock()
	// A new instance means there's now something to reap — make sure the
	// sweeper is running. It stops itself once every workspace is empty, so
	// this is what respawns it after an idle period.
	ensureSweeper(layout)
	return in, nil
}

// reapIdle removes the UNPINNED instances of a session that has been idle for
// `idle` whose own idle window (IdleWindow) is shorter than that, records a
// tombstone for each (config dropped), and returns how many were reaped.
// Pinned instances (ExpiresAt set) are left for the expiry job. Tombstones
// accumulate so the "deleted — re-create" notice survives across reloads; a
// later Add of the same label clears its matching tombstone.
func reapIdle(layout agentconfig.Layout, sessionID, reason string, idle time.Duration, now time.Time) (int, error) {
	return reapWhere(layout, sessionID, reason, now, func(in Instance) bool {
		return !in.Pinned() && idle > in.IdleWindow()
	})
}

// reapAll removes every unpinned instance of a session regardless of idle
// windows — the session is gone, or the caller wants it emptied.
func reapAll(layout agentconfig.Layout, sessionID, reason string, now time.Time) (int, error) {
	return reapIdle(layout, sessionID, reason, time.Duration(1<<62-1), now)
}

// reapExpired removes the pinned instances of a session whose expiry passed.
func reapExpired(layout agentconfig.Layout, sessionID string, now time.Time) (int, error) {
	return reapWhere(layout, sessionID, ReasonExpired, now, func(in Instance) bool { return in.Expired(now) })
}

// ReasonExpired is the tombstone reason of an instance deleted because the
// expiry its user set passed.
const ReasonExpired = "expired"

func reapWhere(layout agentconfig.Layout, sessionID, reason string, now time.Time, drop func(Instance) bool) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	ws, err := Load(layout, sessionID)
	if err != nil {
		return 0, err
	}
	stamp := now.UTC().Format(time.RFC3339)
	kept := make([]Instance, 0, len(ws.Instances))
	n := 0
	for _, in := range ws.Instances {
		if !drop(in) {
			kept = append(kept, in)
			continue
		}
		ws.Tombstones = append(ws.Tombstones, Tombstone{
			Label:     in.Label,
			BaseKey:   in.BaseKey,
			DeletedAt: stamp,
			Reason:    reason,
		})
		n++
	}
	if n == 0 {
		return 0, nil
	}
	ws.Instances = kept
	if err := save(layout, sessionID, ws); err != nil {
		return 0, err
	}
	return n, nil
}

// SetLifetime sets how an instance dies: pinned to expiresAt (RFC3339), or
// reaped after idleAfter of session inactivity, or — both zero — the default
// idle window. The two are exclusive; setting one clears the other. Returns
// an os.ErrNotExist-wrapped error when the instance is gone.
func SetLifetime(layout agentconfig.Layout, sessionID, instanceID, expiresAt string, idleAfter time.Duration) error {
	mu.Lock()
	defer mu.Unlock()
	ws, err := Load(layout, sessionID)
	if err != nil {
		return err
	}
	for i := range ws.Instances {
		if ws.Instances[i].ID == instanceID {
			ws.Instances[i].ExpiresAt = expiresAt
			ws.Instances[i].IdleAfterSec = 0
			if expiresAt == "" {
				ws.Instances[i].IdleAfterSec = int(idleAfter / time.Second)
			}
			return save(layout, sessionID, ws)
		}
	}
	return fmt.Errorf("session instance %q not found: %w", instanceID, os.ErrNotExist)
}

// SetConfig merges config values into an instance and persists. Keys
// not present in values are kept. Returns os.ErrNotExist-wrapped error
// when the instance is gone.
func SetConfig(layout agentconfig.Layout, sessionID, instanceID string, values map[string]string) error {
	mu.Lock()
	defer mu.Unlock()
	ws, err := Load(layout, sessionID)
	if err != nil {
		return err
	}
	for i := range ws.Instances {
		if ws.Instances[i].ID != instanceID {
			continue
		}
		if ws.Instances[i].Config == nil {
			ws.Instances[i].Config = map[string]string{}
		}
		for k, v := range values {
			ws.Instances[i].Config[k] = v
		}
		return save(layout, sessionID, ws)
	}
	return fmt.Errorf("session instance %q not found: %w", instanceID, os.ErrNotExist)
}

// SetLockSeals merges seals for human-set locked config keys into an
// instance and persists.
func SetLockSeals(layout agentconfig.Layout, sessionID, instanceID string, seals map[string]string) error {
	mu.Lock()
	defer mu.Unlock()
	ws, err := Load(layout, sessionID)
	if err != nil {
		return err
	}
	for i := range ws.Instances {
		if ws.Instances[i].ID != instanceID {
			continue
		}
		if ws.Instances[i].LockSeals == nil {
			ws.Instances[i].LockSeals = map[string]string{}
		}
		for k, v := range seals {
			ws.Instances[i].LockSeals[k] = v
		}
		return save(layout, sessionID, ws)
	}
	return fmt.Errorf("session instance %q not found: %w", instanceID, os.ErrNotExist)
}

// SetLabel renames an instance. Returns an os.ErrNotExist-wrapped error
// when the instance is gone. Empty label is rejected — the caller should
// fall back to a default before calling.
func SetLabel(layout agentconfig.Layout, sessionID, instanceID, label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("label is required")
	}
	mu.Lock()
	defer mu.Unlock()
	ws, err := Load(layout, sessionID)
	if err != nil {
		return err
	}
	for i := range ws.Instances {
		if ws.Instances[i].ID == instanceID {
			ws.Instances[i].Label = label
			return save(layout, sessionID, ws)
		}
	}
	return fmt.Errorf("session instance %q not found: %w", instanceID, os.ErrNotExist)
}

// Remove deletes an instance. ok=false when it wasn't there.
func Remove(layout agentconfig.Layout, sessionID, instanceID string) (bool, error) {
	mu.Lock()
	defer mu.Unlock()
	ws, err := Load(layout, sessionID)
	if err != nil {
		return false, err
	}
	kept := ws.Instances[:0]
	removed := false
	for _, in := range ws.Instances {
		if in.ID == instanceID {
			removed = true
			continue
		}
		kept = append(kept, in)
	}
	if !removed {
		return false, nil
	}
	ws.Instances = kept
	return true, save(layout, sessionID, ws)
}

func save(layout agentconfig.Layout, sessionID string, ws Workspace) error {
	path := layout.SessionWorkspace(sessionID)
	// The file is removed only when nothing is left to persist — no live
	// instances AND no tombstones. A session reaped for inactivity keeps its
	// file (tombstones only) so the "deleted — re-create" notice survives.
	if len(ws.Instances) == 0 && len(ws.Tombstones) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove session workspace: %w", err)
		}
		notifyChange(sessionID)
		return nil
	}
	b, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session workspace: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write session workspace: %w", err)
	}
	notifyChange(sessionID)
	return nil
}

// notifyChange runs while mu is held, so the listener must not call back
// into this package; publishing an SSE event does not.
func notifyChange(sessionID string) {
	if fn := onChange; fn != nil {
		fn(sessionID)
	}
}

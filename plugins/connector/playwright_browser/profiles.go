package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// Named profiles are the persistent-identity layer on top of live sessions.
//
// A live session already launches Chromium with --user-data-dir=<sessionDir>/
// profile-<id>, a real Chromium profile (cookies, login, localStorage). But that
// dir is named by random session id and swept when the session closes, so login
// never survives. A NAMED profile fixes both: session_open(profile="fb-akun-A")
// uses a stable profile-fb-akun-A dir that removeSession keeps, so a later
// session_open(profile="fb-akun-A") reuses the same login without re-auth.
//
// profile_list reports every named profile on disk (persistent, shown even when
// no browser is running) and whether one currently has a live session driving
// it. profile_delete is the only way a named profile is removed.

// profilePrefix is the on-disk dir prefix for a browser profile. Anonymous
// sessions use profile-<session-id>; named profiles use profile-<name>. Both
// live directly under sessionDir alongside the <id>.json metadata files.
const profilePrefix = "profile-"

// profileNameRE constrains a caller-supplied profile name to a filesystem-safe
// token so it can be embedded in a dir name without path traversal. No slashes,
// no "..", non-empty. Mirrors sessionIDRE but is enforced at input time with a
// clear error rather than silently.
var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validProfileName(name string) bool { return profileNameRE.MatchString(name) }

// profileDir resolves the --user-data-dir for a session. A non-empty (validated)
// profile gives the stable profile-<name> dir shared across sessions; an empty
// profile gives the per-session profile-<id> dir (anonymous, swept on close).
func profileDir(dir, profile, sessionID string) string {
	if profile != "" {
		return filepath.Join(dir, profilePrefix+profile)
	}
	return filepath.Join(dir, profilePrefix+sessionID)
}

// profileInUse reports whether a named profile currently has a live session
// driving it (a Chromium --user-data-dir is single-owner). Returns the owning
// session id when so. listSessions already sweeps dead sessions, so a crashed
// browser doesn't falsely hold a profile.
func profileInUse(c *connector.Ctx, profile string) (sessionID string, inUse bool) {
	metas, err := listSessions(c)
	if err != nil {
		return "", false
	}
	for _, m := range metas {
		if m.Profile == profile {
			return m.ID, true
		}
	}
	return "", false
}

// ── ops ──────────────────────────────────────────────────────────────

// profileList scans sessionDir for profile-<name> dirs and reports each named
// profile: created/last-used timestamps (from the dir's stat) and whether a
// live session currently drives it. Anonymous profile-<session-id> dirs are
// skipped — those belong to sessions, not named profiles, and only surface
// through session_list.
func profileList(c *connector.Ctx) (any, error) {
	dir := sessionDir(c)

	// Map every named profile to its live session, if any, in one pass so the
	// list reflects current ownership.
	live := map[string]string{} // profile name -> session id
	if metas, err := listSessions(c); err == nil {
		for _, m := range metas {
			if m.Profile != "" {
				live[m.Profile] = m.ID
			}
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"profiles": []any{}, "count": 0}, nil
		}
		return nil, fmt.Errorf("read session dir: %w", err)
	}

	profiles := make([]map[string]any, 0)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), profilePrefix) {
			continue
		}
		name := strings.TrimPrefix(e.Name(), profilePrefix)
		// A named profile's suffix is a valid profile name; an anonymous dir's
		// suffix is a session id (inst-pid-nano) — those must not appear here.
		// A named profile currently live is authoritative regardless of shape.
		if _, isLive := live[name]; !isLive && !isNamedProfileDir(name) {
			continue
		}

		info, statErr := e.Info()
		var created, lastUsed time.Time
		if statErr == nil {
			lastUsed = info.ModTime()
			created = info.ModTime() // dir birth time isn't portable; ModTime is the best cross-OS proxy
		}
		sid, isLive := live[name]
		profiles = append(profiles, map[string]any{
			"name":       name,
			"created":    created,
			"last_used":  lastUsed,
			"live":       isLive,
			"session_id": sid,
		})
	}

	return map[string]any{"profiles": profiles, "count": len(profiles)}, nil
}

// isNamedProfileDir distinguishes a named-profile suffix from an anonymous
// session-id suffix. Session ids are "inst-pid-nano" — two dashes with numeric
// pid+nano tails — so a suffix that parses as one is treated as anonymous and
// excluded from the named-profile list. Anything else is a user-chosen name.
func isNamedProfileDir(suffix string) bool {
	parts := strings.Split(suffix, "-")
	if len(parts) < 3 {
		return true // too few segments to be a session id → a real name
	}
	// Last two segments of a session id are all-digits (pid, unix-nano).
	tail := parts[len(parts)-2:]
	for _, p := range tail {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return true // a non-numeric tail → not a session id → a real name
		}
	}
	return false
}

// profileDelete removes a named profile dir. It refuses while a live session is
// using the profile (close it first) so we never yank a --user-data-dir out from
// under a running Chromium. This is the only way a named profile is deleted.
func profileDelete(c *connector.Ctx) (any, error) {
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if !validProfileName(name) {
		return nil, fmt.Errorf("invalid profile name %q: use letters, digits, dash, underscore (no path separators)", name)
	}
	if owner, ok := profileInUse(c, name); ok {
		return nil, fmt.Errorf("profile %q is in use by live session %s: close it first (session_close) before deleting", name, owner)
	}

	dir := filepath.Join(sessionDir(c), profilePrefix+name)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no profile named %q", name)
		}
		return nil, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("delete profile %q: %w", name, err)
	}
	return map[string]any{"name": name, "deleted": true}, nil
}

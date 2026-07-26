package main

import (
	"path/filepath"
	"testing"
)

func TestValidProfileName(t *testing.T) {
	cases := map[string]bool{
		"fb-akun-A":  true,
		"acc_1":      true,
		"ABC123":     true,
		"a":          true,
		"":           false,
		"has space":  false,
		"a/b":        false,
		"../escape":  false,
		"dot.name":   false,
		`back\slash`: false,
	}
	for name, want := range cases {
		if got := validProfileName(name); got != want {
			t.Errorf("validProfileName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestProfileDir(t *testing.T) {
	dir := filepath.Join("base", "sessions")

	// Named profile → stable profile-<name> dir, session id ignored.
	got := profileDir(dir, "fb-akun-A", "inst-123-456")
	want := filepath.Join(dir, "profile-fb-akun-A")
	if got != want {
		t.Errorf("named profileDir = %q, want %q", got, want)
	}

	// Empty profile → per-session profile-<id> dir (anonymous).
	got = profileDir(dir, "", "inst-123-456")
	want = filepath.Join(dir, "profile-inst-123-456")
	if got != want {
		t.Errorf("anonymous profileDir = %q, want %q", got, want)
	}
}

func TestIsNamedProfileDir(t *testing.T) {
	// Session ids are "inst-pid-nano": trailing two segments all-digits → NOT a
	// named profile. Anything else → a user-chosen name.
	cases := map[string]bool{
		"abc123-4567-1700000000000000000": false, // full session id shape
		"pw-4567-1700000000000000000":     false, // fallback inst "pw"
		"fb-akun-A":                       true,  // name with a non-numeric tail
		"account1":                        true,  // single segment
		"my-profile":                      true,  // two segments, non-numeric tail
		"a-123-456":                       false, // digits tail → looks like a session id
		"team-01-02":                      false, // numeric tail pair → session-id-shaped
	}
	for suffix, want := range cases {
		if got := isNamedProfileDir(suffix); got != want {
			t.Errorf("isNamedProfileDir(%q) = %v, want %v", suffix, got, want)
		}
	}
}

// TestProfileOpsRegistered guards that the two profile ops are on the module
// with the right destructiveness (list read-only, delete destructive).
func TestProfileOpsRegistered(t *testing.T) {
	want := map[string]bool{"profile_list": false, "profile_delete": true} // key -> destructive
	seen := map[string]bool{}
	for _, op := range Module().AllOps() {
		if d, ok := want[op.Key]; ok {
			seen[op.Key] = true
			if op.Destructive != d {
				t.Errorf("%s: Destructive = %v, want %v", op.Key, op.Destructive, d)
			}
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("op %q not registered on module", k)
		}
	}
}

package entity

import "testing"

// session_locked tag maps to Config.SessionLocked; fields without it stay
// overridable from a session.
func TestStructToConfigs_SessionLockedTag(t *testing.T) {
	type S struct {
		BaseURL string `wick:"url;default=https://api.example.com;session_locked"`
		Token   string `wick:"secret"`
	}
	got := StructToConfigs(S{})
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if !got[0].SessionLocked {
		t.Fatalf("base_url: want SessionLocked")
	}
	if got[1].SessionLocked {
		t.Fatalf("token: want overridable")
	}
}

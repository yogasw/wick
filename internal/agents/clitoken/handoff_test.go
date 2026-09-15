package clitoken

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A deploy is exactly when a build is running, so the job holding a token
// usually finishes AFTER the swap. Without the handover the successor
// answers 401 and the report is lost to the very event it was reporting.
func TestHandoffCarriesLiveTokensAcrossASwap(t *testing.T) {
	dir := t.TempDir()
	old := New()

	live, err := old.Issue("sess-1", "usr-1", "build 0.1.261", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// An expired grant must not be resurrected by the trip.
	dead, _ := old.Issue("sess-1", "usr-1", "stale", time.Minute)
	old.m[dead.Token] = Grant{
		Token: dead.Token, SessionID: "sess-1", UserID: "usr-1",
		ExpiresAt: time.Now().Add(-time.Minute),
	}

	n, err := old.SaveHandoff(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("saved %d, want only the live one", n)
	}

	successor := New()
	if got := successor.LoadHandoff(dir); got != 1 {
		t.Fatalf("adopted %d, want 1", got)
	}
	g, ok := successor.Resolve(live.Token)
	if !ok || g.SessionID != "sess-1" {
		t.Fatalf("the successor does not honour the token it inherited: %+v %v", g, ok)
	}
	if !g.ExpiresAt.Equal(live.ExpiresAt) {
		t.Errorf("expiry moved: %s → %s — a handover must not extend a credential", live.ExpiresAt, g.ExpiresAt)
	}
	if _, ok := successor.Resolve(dead.Token); ok {
		t.Error("an expired token came back to life")
	}

	// The file is consumed, so a later boot cannot re-import stale grants.
	if _, err := os.Stat(filepath.Join(dir, handoffFileName)); !os.IsNotExist(err) {
		t.Error("the handoff file survived the adoption")
	}

	// Nothing live left to hand over: the previous file must go, not linger.
	empty := New()
	if n, err := empty.SaveHandoff(dir); err != nil || n != 0 {
		t.Fatalf("empty save = %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, handoffFileName)); !os.IsNotExist(err) {
		t.Error("an empty save should remove the stale file")
	}
}

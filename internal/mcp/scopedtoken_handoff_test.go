package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestScopedTokenHandoff locks the contract a graceful upgrade depends on: an
// agent still running in the outgoing process keeps working MCP tools after
// the successor owns the socket.
func TestScopedTokenHandoff(t *testing.T) {
	t.Run("successor honours a predecessor's token", func(t *testing.T) {
		dir := t.TempDir()
		old := NewScopedTokens()
		tok, err := old.IssueFor("user-1", []string{"tag-a", "tag-b"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := old.SaveHandoff(dir); err != nil || n != 1 {
			t.Fatalf("SaveHandoff = %d, %v", n, err)
		}

		fresh := NewScopedTokens()
		if _, _, ok := fresh.Lookup(tok); ok {
			t.Fatal("a fresh issuer must not know the token before importing")
		}
		if n := fresh.LoadHandoff(dir); n != 1 {
			t.Fatalf("LoadHandoff = %d, want 1", n)
		}
		user, tags, strip, ok := fresh.LookupGrant(tok)
		if !ok || user != "user-1" || strip != true || len(tags) != 2 {
			t.Fatalf("grant did not survive: %q %v %v %v", user, tags, strip, ok)
		}
	})

	t.Run("the file is removed once read", func(t *testing.T) {
		// These are credentials. A second boot must not be able to adopt a
		// generation-old set of them.
		dir := t.TempDir()
		old := NewScopedTokens()
		if _, err := old.Issue("user-1", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := old.SaveHandoff(dir); err != nil {
			t.Fatal(err)
		}
		NewScopedTokens().LoadHandoff(dir)
		if _, err := os.Stat(filepath.Join(dir, handoffFileName)); !os.IsNotExist(err) {
			t.Fatalf("handoff file survived the import: %v", err)
		}
		if n := NewScopedTokens().LoadHandoff(dir); n != 0 {
			t.Fatalf("a later boot adopted %d grants from a consumed file", n)
		}
	})

	t.Run("expired grants are not resurrected", func(t *testing.T) {
		dir := t.TempDir()
		past := time.Now().Add(-time.Hour)
		old := NewScopedTokens()
		old.now = func() time.Time { return past }
		tok, err := old.Issue("user-1", nil)
		if err != nil {
			t.Fatal(err)
		}
		// Save from the past (grant still valid then), import in the present
		// with the TTL long gone.
		if _, err := old.SaveHandoff(dir); err != nil {
			t.Fatal(err)
		}
		fresh := NewScopedTokens()
		fresh.now = func() time.Time { return past.Add(scopedTokenTTL + time.Hour) }
		if n := fresh.LoadHandoff(dir); n != 0 {
			t.Fatalf("imported %d expired grants", n)
		}
		if _, _, ok := fresh.Lookup(tok); ok {
			t.Fatal("an expired token was adopted")
		}
	})

	t.Run("no tokens leaves no file", func(t *testing.T) {
		dir := t.TempDir()
		if n, err := NewScopedTokens().SaveHandoff(dir); err != nil || n != 0 {
			t.Fatalf("SaveHandoff = %d, %v", n, err)
		}
		if _, err := os.Stat(filepath.Join(dir, handoffFileName)); !os.IsNotExist(err) {
			t.Fatal("wrote a handoff file with nothing to hand off")
		}
	})

	t.Run("a missing file is a normal cold start", func(t *testing.T) {
		if n := NewScopedTokens().LoadHandoff(t.TempDir()); n != 0 {
			t.Fatalf("LoadHandoff = %d on a cold start", n)
		}
	})
}

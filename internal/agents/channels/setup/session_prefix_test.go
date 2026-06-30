package setup

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/storage"
)

// TestSessionPrefixIsCharsetSafe pins the regression where the boot composer
// fed the registry instanceKey ("slack:<owner>") straight into SetSessionPrefix,
// producing session ids like "slack:<owner>:<thread_ts>" that
// storage.ValidateSessionID rejects (":" is outside [A-Za-z0-9._-] and is
// illegal in Windows filenames). The result dropped every Slack/Telegram
// message from a namespaced instance.
func TestSessionPrefixIsCharsetSafe(t *testing.T) {
	owner := "ec0c0b8b-e73c-4561-9d19-bfa9c481a816"
	threadTS := "1782713222.652969" // real Slack thread_ts shape

	cases := []struct {
		name        string
		channelType string
		owner       *string
		sessionID   string // prefix + transport key
	}{
		{"slack owner", "slack", nil, sessionPrefix("slack", nil) + threadTS},
		{"slack per-user", "slack", &owner, sessionPrefix("slack", &owner) + threadTS},
		{"telegram owner", "telegram", nil, sessionPrefix("telegram", nil) + "tg-123"},
		{"telegram per-user", "telegram", &owner, sessionPrefix("telegram", &owner) + "tg-123"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := storage.ValidateSessionID(tc.sessionID); err != nil {
				t.Fatalf("session id %q must be valid: %v", tc.sessionID, err)
			}
		})
	}
}

// TestSessionPrefixAppOwnerIsClean pins the App Owner instance's session
// prefix to the clean "<channelType>-" form — no "__owner__" segment. The
// registry instance key still uses ":__owner__", but that internal detail
// must not leak into session ids / on-disk folder names.
func TestSessionPrefixAppOwnerIsClean(t *testing.T) {
	if got := sessionPrefix("slack", nil); got != "slack-" {
		t.Errorf("app-owner slack prefix = %q, want %q (no __owner__)", got, "slack-")
	}
	owner := "ec0c0b8b-e73c-4561-9d19-bfa9c481a816"
	if got := sessionPrefix("slack", &owner); got != "slack-"+owner+"-" {
		t.Errorf("per-user slack prefix = %q, want %q", got, "slack-"+owner+"-")
	}
}

// TestSessionPrefixUniquePerOwner guards the namespacing guarantee: two owners
// of the same channel type get distinct prefixes, so a coincidentally-equal
// thread_ts never collides across instances.
func TestSessionPrefixUniquePerOwner(t *testing.T) {
	a := "owner-a"
	b := "owner-b"
	if sessionPrefix("slack", &a) == sessionPrefix("slack", &b) {
		t.Fatal("distinct owners must get distinct session prefixes")
	}
	if sessionPrefix("slack", nil) == sessionPrefix("telegram", nil) {
		t.Fatal("distinct channel types must get distinct session prefixes")
	}
}

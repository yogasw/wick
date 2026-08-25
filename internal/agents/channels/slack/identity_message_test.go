package slack

import (
	"strings"
	"testing"
)

// The wording is Slack's own — Slack markup, Slack-specific remedies (the
// users:read.email scope) — so it is tested here rather than beside the
// shared resolver.

// TestIdentityErrorMessage checks each refusal names the fix, since the person
// reading it in Slack is the one who has to unblock it.
func TestIdentityErrorMessage(t *testing.T) {
	if m := identityErrorMessage(ErrEmailRequired, false); !strings.Contains(m, "email is required") ||
		!strings.Contains(m, "users:read.email") {
		t.Errorf("email message should state the cause and the scope to add: %q", m)
	}
	if m := identityErrorMessage(ErrNoAccount, false); !strings.Contains(m, "Auto-register") {
		t.Errorf("with auto-register off, point at the setting: %q", m)
	}
	if m := identityErrorMessage(ErrNoAccount, true); strings.Contains(m, "Auto-register") {
		t.Errorf("with auto-register on, do not suggest enabling it: %q", m)
	}
	if m := identityErrorMessage(ErrGuestNotAllowed, true); !strings.Contains(m, "Guest") {
		t.Errorf("guest message unclear: %q", m)
	}
}

// TestIdentityErrorMessage_ToolAccess: the reply must say what to ask for, and
// must not imply Slack is a way around the permission.
func TestIdentityErrorMessage_ToolAccess(t *testing.T) {
	m := identityErrorMessage(ErrToolAccessDenied, true)
	if !strings.Contains(m, "approve") || !strings.Contains(m, "Admin") {
		t.Errorf("message should name the fix: %q", m)
	}
	if !strings.Contains(m, "does not bypass") {
		t.Errorf("message should state Slack is not a bypass: %q", m)
	}
}

// TestIdentityErrorMessage_PendingVsDenied: the two refusals must not read the
// same, or the sender cannot tell which fix to ask for.
func TestIdentityErrorMessage_PendingVsDenied(t *testing.T) {
	pending := identityErrorMessage(ErrPendingApproval, true)
	denied := identityErrorMessage(ErrToolAccessDenied, true)

	if pending == denied {
		t.Fatal("pending and denied produce the same message")
	}
	if !strings.Contains(pending, "pending approval") || !strings.Contains(pending, "approve") {
		t.Errorf("pending message should name approval as the fix: %q", pending)
	}
	if strings.Contains(pending, "granted access") {
		t.Errorf("pending message should not talk about grants: %q", pending)
	}
	if !strings.Contains(denied, "granted access") {
		t.Errorf("denied message should name the missing grant: %q", denied)
	}
	if !strings.Contains(denied, "does not bypass") {
		t.Errorf("denied message should state Slack is not a bypass: %q", denied)
	}
}

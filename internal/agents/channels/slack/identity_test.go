package slack

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeUsers struct {
	byEmail    map[string]string
	registered []string
	regErr     error
	// denyAgents holds user ids that fail the /tools/agents access gate,
	// standing in for "missing the required tag".
	denyAgents map[string]bool
	// pending holds user ids no admin has approved yet.
	pending map[string]bool
	// recorded captures RecordIdentity calls: external id -> wick user id.
	recorded map[string]string
	// byExternal is the existing channel-identity links: external id -> user id.
	byExternal map[string]string
	// autoRegister mirrors the install-level switch.
	autoRegister bool
}

func (f *fakeUsers) IsApproved(_ context.Context, id string) bool {
	return !f.pending[id]
}

func (f *fakeUsers) AutoRegisterEnabled(context.Context) bool { return f.autoRegister }

func (f *fakeUsers) FindByChannelIdentity(_ context.Context, _, _, externalUserID string) (string, bool) {
	id, ok := f.byExternal[externalUserID]
	return id, ok
}

func (f *fakeUsers) RecordIdentity(_ context.Context, wickUserID, externalUserID, _, _ string) {
	if f.recorded == nil {
		f.recorded = map[string]string{}
	}
	f.recorded[externalUserID] = wickUserID
}

func (f *fakeUsers) CanUseAgents(_ context.Context, id string) bool {
	return !f.denyAgents[id]
}

func (f *fakeUsers) FindByEmail(_ context.Context, email string) (string, bool) {
	id, ok := f.byEmail[email]
	return id, ok
}

func (f *fakeUsers) RegisterFromChannel(_ context.Context, email, _, _ string) (string, error) {
	if f.regErr != nil {
		return "", f.regErr
	}
	f.registered = append(f.registered, email)
	if f.byEmail == nil {
		f.byEmail = map[string]string{}
	}
	f.byEmail[email] = "new-" + email
	return "new-" + email, nil
}

// TestResolveWickUser_MatchesByEmail is the happy path: the sender already has
// a wick account, so the session runs as them.
func TestResolveWickUser_MatchesByEmail(t *testing.T) {
	f := &fakeUsers{byEmail: map[string]string{"ada@example.com": "u-ada"}}
	got, err := resolveWickUser(context.Background(), f, SlackUser{Email: "ada@example.com", Name: "Ada"}, false, "slack:acme", "U-ext")
	if err != nil || got != "u-ada" {
		t.Fatalf("got (%q, %v), want (u-ada, nil)", got, err)
	}
	if len(f.registered) != 0 {
		t.Fatalf("registered %v for an existing user", f.registered)
	}
}

// TestResolveWickUser_EmailIsCaseInsensitive: Slack casing must not fork one
// person into two accounts.
func TestResolveWickUser_EmailIsCaseInsensitive(t *testing.T) {
	f := &fakeUsers{byEmail: map[string]string{"ada@example.com": "u-ada"}}
	got, err := resolveWickUser(context.Background(), f, SlackUser{Email: "Ada@Example.COM"}, true, "slack:acme", "U-ext")
	if err != nil || got != "u-ada" {
		t.Fatalf("got (%q, %v), want (u-ada, nil)", got, err)
	}
	if len(f.registered) != 0 {
		t.Fatalf("case difference created a duplicate account: %v", f.registered)
	}
}

// TestResolveWickUser_EmailRequired covers the cases where Slack gives no
// usable email. These are NOT guest-only: a missing users:read.email scope
// returns a blank email with no error, and bots have no email at all. Both
// must be refused rather than silently owned by someone else.
func TestResolveWickUser_EmailRequired(t *testing.T) {
	f := &fakeUsers{}
	cases := []struct {
		name string
		u    SlackUser
	}{
		{"blank email (missing users:read.email scope)", SlackUser{Email: ""}},
		{"whitespace-only email", SlackUser{Email: "   "}},
		{"bot sender", SlackUser{Email: "bot@example.com", IsBot: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.u
			u.Email = strings.TrimSpace(u.Email)
			if _, err := resolveWickUser(context.Background(), f, u, true, "slack:acme", "U-ext"); !errors.Is(err, ErrEmailRequired) {
				t.Fatalf("err = %v, want ErrEmailRequired", err)
			}
			if len(f.registered) != 0 {
				t.Fatalf("created an account without a usable email: %v", f.registered)
			}
		})
	}
}

// TestResolveWickUser_GuestRefused: guest access is a workspace setting an
// admin can enable later without touching wick, which would let an outside
// party DM the bot. Refused even with a valid email.
func TestResolveWickUser_GuestRefused(t *testing.T) {
	f := &fakeUsers{}
	_, err := resolveWickUser(context.Background(), f, SlackUser{Email: "outsider@other.com", IsGuest: true}, true, "slack:acme", "U-ext")
	if !errors.Is(err, ErrGuestNotAllowed) {
		t.Fatalf("err = %v, want ErrGuestNotAllowed", err)
	}
	if len(f.registered) != 0 {
		t.Fatalf("guest got an account: %v", f.registered)
	}
}

// TestResolveWickUser_AutoRegisterOff: an unknown email is refused, and no
// account is created.
func TestResolveWickUser_AutoRegisterOff(t *testing.T) {
	f := &fakeUsers{byEmail: map[string]string{}}
	_, err := resolveWickUser(context.Background(), f, SlackUser{Email: "new@example.com"}, false, "slack:acme", "U-ext")
	if !errors.Is(err, ErrNoAccount) {
		t.Fatalf("err = %v, want ErrNoAccount", err)
	}
	if len(f.registered) != 0 {
		t.Fatalf("auto-register was off but created %v", f.registered)
	}
}

// TestResolveWickUser_AutoRegisterOn creates the account and returns its id.
func TestResolveWickUser_AutoRegisterOn(t *testing.T) {
	f := &fakeUsers{byEmail: map[string]string{}}
	got, err := resolveWickUser(context.Background(), f, SlackUser{Email: "new@example.com", Name: "New Person"}, true, "slack:acme", "U-ext")
	if err != nil || got != "new-new@example.com" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if len(f.registered) != 1 || f.registered[0] != "new@example.com" {
		t.Fatalf("registered = %v", f.registered)
	}
}

// TestResolveWickUser_NilResolverRefuses: with no resolver wired there is no
// way to establish identity, so nothing is assumed.
func TestResolveWickUser_NilResolverRefuses(t *testing.T) {
	if _, err := resolveWickUser(context.Background(), nil,
		SlackUser{Email: "ada@example.com"}, true, "slack:acme", "U-ext"); !errors.Is(err, ErrEmailRequired) {
		t.Fatalf("err = %v, want ErrEmailRequired", err)
	}
}

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

// TestResolveWickUser_ToolAccessGate is the anti-bypass check.
//
// Slack is a second door onto the agents tool, so it must ask the same
// question the dashboard asks. A user who cannot open /tools/agents in the web
// UI must not get an agent by messaging the bot instead.
func TestResolveWickUser_ToolAccessGate(t *testing.T) {
	f := &fakeUsers{
		byEmail:    map[string]string{"blocked@example.com": "u-blocked"},
		denyAgents: map[string]bool{"u-blocked": true},
	}
	_, err := resolveWickUser(context.Background(), f, SlackUser{Email: "blocked@example.com"}, false, "slack:acme", "U-ext")
	if !errors.Is(err, ErrToolAccessDenied) {
		t.Fatalf("err = %v, want ErrToolAccessDenied", err)
	}
}

// TestResolveWickUser_AutoRegisterStillGated: registering records who someone
// is; it does not grant access. A freshly auto-registered account is pending,
// so it must still be refused rather than let straight through.
func TestResolveWickUser_AutoRegisterStillGated(t *testing.T) {
	f := &fakeUsers{
		byEmail: map[string]string{},
		pending: map[string]bool{"new-new@example.com": true},
	}
	_, err := resolveWickUser(context.Background(), f, SlackUser{Email: "new@example.com", Name: "New"}, true, "slack:acme", "U-ext")
	if !errors.Is(err, ErrPendingApproval) {
		t.Fatalf("err = %v, want ErrPendingApproval", err)
	}
	// The account IS created — the identity is worth recording so an admin
	// has a row to approve.
	if len(f.registered) != 1 || f.registered[0] != "new@example.com" {
		t.Fatalf("registered = %v, want the new email recorded", f.registered)
	}
}

// TestResolveWickUser_GatePassesForPermittedUser keeps the gate from blocking
// everyone: a user who may use the tool resolves normally.
func TestResolveWickUser_GatePassesForPermittedUser(t *testing.T) {
	f := &fakeUsers{byEmail: map[string]string{"ada@example.com": "u-ada"}}
	got, err := resolveWickUser(context.Background(), f, SlackUser{Email: "ada@example.com"}, false, "slack:acme", "U-ext")
	if err != nil || got != "u-ada" {
		t.Fatalf("got (%q, %v), want (u-ada, nil)", got, err)
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

// TestResolveWickUser_PendingBeforeToolGate pins the ORDER the sender is told
// about: approval first, then agent access. An account that is both pending and
// ungranted must report pending, because that is the step that actually blocks
// it — telling them to ask for a grant would send them chasing the wrong fix.
func TestResolveWickUser_PendingBeforeToolGate(t *testing.T) {
	f := &fakeUsers{
		byEmail:    map[string]string{"both@example.com": "u-both"},
		pending:    map[string]bool{"u-both": true},
		denyAgents: map[string]bool{"u-both": true},
	}
	_, err := resolveWickUser(context.Background(), f, SlackUser{Email: "both@example.com"}, false, "slack:acme", "U-ext")
	if !errors.Is(err, ErrPendingApproval) {
		t.Fatalf("err = %v, want ErrPendingApproval (approval outranks the tool gate)", err)
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

// TestResolveWickUser_ExistingLinkSurvivesEmailChange closes the duplicate-account
// hole: someone edits their Slack email, messages again, and an email-only lookup
// would miss and register a SECOND wick account for the same person.
//
// The channel account id never changes, so an existing link resolves first and
// the register path is never reached.
func TestResolveWickUser_ExistingLinkSurvivesEmailChange(t *testing.T) {
	f := &fakeUsers{
		// The link was made earlier, under the OLD email.
		byExternal: map[string]string{"U123": "u-ada"},
		// The new email matches nothing — an email-first lookup would miss here.
		byEmail: map[string]string{},
	}
	got, err := resolveWickUser(context.Background(), f,
		SlackUser{Email: "ada.renamed@example.com", Name: "Ada"},
		true, "slack:acme", "U123")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "u-ada" {
		t.Fatalf("resolved %q, want the already-linked u-ada", got)
	}
	if len(f.registered) != 0 {
		t.Fatalf("created a duplicate account %v for an already-linked sender", f.registered)
	}
}

// TestResolveWickUser_LinkBeatsAConflictingEmail: if the new email happens to
// match a DIFFERENT wick user, the existing link still wins. Otherwise editing a
// Slack email to someone else's address would hand over their identity.
func TestResolveWickUser_LinkBeatsAConflictingEmail(t *testing.T) {
	f := &fakeUsers{
		byExternal: map[string]string{"U123": "u-ada"},
		byEmail:    map[string]string{"boss@example.com": "u-boss"},
	}
	got, err := resolveWickUser(context.Background(), f,
		SlackUser{Email: "boss@example.com"}, false, "slack:acme", "U123")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "u-ada" {
		t.Fatalf("resolved %q — an edited email took over another account", got)
	}
}

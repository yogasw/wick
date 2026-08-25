package channels

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
	got, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "ada@example.com", Name: "Ada"}, false, "slack", "slack:acme")
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
	got, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "Ada@Example.COM"}, true, "slack", "slack:acme")
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
		u    SenderIdentity
	}{
		{"blank email (missing users:read.email scope)", SenderIdentity{ExternalUserID: "U-ext", Email: ""}},
		{"whitespace-only email", SenderIdentity{ExternalUserID: "U-ext", Email: "   "}},
		{"bot sender", SenderIdentity{ExternalUserID: "U-ext", Email: "bot@example.com", IsBot: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.u
			u.Email = strings.TrimSpace(u.Email)
			if _, err := ResolveWickUser(context.Background(), f, u, true, "slack", "slack:acme"); !errors.Is(err, ErrEmailRequired) {
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
	_, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "outsider@other.com", IsGuest: true}, true, "slack", "slack:acme")
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
	_, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "new@example.com"}, false, "slack", "slack:acme")
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
	got, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "new@example.com", Name: "New Person"}, true, "slack", "slack:acme")
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
	if _, err := ResolveWickUser(context.Background(), nil,
		SenderIdentity{ExternalUserID: "U-ext", Email: "ada@example.com"}, true, "slack", "slack:acme"); !errors.Is(err, ErrEmailRequired) {
		t.Fatalf("err = %v, want ErrEmailRequired", err)
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
	_, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "blocked@example.com"}, false, "slack", "slack:acme")
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
	_, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "new@example.com", Name: "New"}, true, "slack", "slack:acme")
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
	got, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "ada@example.com"}, false, "slack", "slack:acme")
	if err != nil || got != "u-ada" {
		t.Fatalf("got (%q, %v), want (u-ada, nil)", got, err)
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
	_, err := ResolveWickUser(context.Background(), f, SenderIdentity{ExternalUserID: "U-ext", Email: "both@example.com"}, false, "slack", "slack:acme")
	if !errors.Is(err, ErrPendingApproval) {
		t.Fatalf("err = %v, want ErrPendingApproval (approval outranks the tool gate)", err)
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
	got, err := ResolveWickUser(context.Background(), f,
		SenderIdentity{ExternalUserID: "U123", Email: "ada.renamed@example.com", Name: "Ada"},
		true, "slack", "slack:acme")
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
	got, err := ResolveWickUser(context.Background(), f,
		SenderIdentity{ExternalUserID: "U123", Email: "boss@example.com"}, false, "slack", "slack:acme")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "u-ada" {
		t.Fatalf("resolved %q — an edited email took over another account", got)
	}
}

// The synthetic address is matched against real accounts with FindByEmail, so
// the domain has to be one nobody can ever own. A registrable domain would
// mean someone signing up under it gets matched into a channel session that
// is not theirs.
func TestSyntheticEmailUsesAReservedDomain(t *testing.T) {
	got := SyntheticEmail("8812")
	if got != "8812@telegram.local" {
		t.Fatalf("got %q, want 8812@telegram.local", got)
	}
	// .local and .invalid are reserved by RFC 6761/2606 — never resolvable,
	// never registrable. Anything else would be a real domain someone owns.
	if !strings.HasSuffix(got, ".local") && !strings.HasSuffix(got, ".invalid") {
		t.Errorf("domain %q is registrable; it must be reserved", got)
	}
}

func TestSyntheticEmailIsStableAndNormalised(t *testing.T) {
	if a, b := SyntheticEmail("8812"), SyntheticEmail("8812"); a != b {
		t.Errorf("not stable: %q vs %q", a, b)
	}
	if got := SyntheticEmail("  8812  "); got != "8812@telegram.local" {
		t.Errorf("whitespace not trimmed: %q", got)
	}
	if got := SyntheticEmail("ABC"); got != "abc@telegram.local" {
		t.Errorf("not lowercased: %q", got)
	}
}

// An empty id must not produce "@telegram.local" — every unidentified sender
// would then collapse onto one shared account.
func TestSyntheticEmailRefusesEmptyID(t *testing.T) {
	for _, in := range []string{"", "   "} {
		if got := SyntheticEmail(in); got != "" {
			t.Errorf("SyntheticEmail(%q) = %q, want empty", in, got)
		}
	}
}

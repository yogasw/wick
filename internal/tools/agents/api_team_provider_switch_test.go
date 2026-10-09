package agents

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// seedSwitchAgent is seedTeamAgent with allow_provider_switch stored.
func seedSwitchAgent(t *testing.T, owner, handle, projectID string, allow bool) entity.AgentPersona {
	t.Helper()
	p := &entity.AgentPersona{OwnerUserID: owner, Handle: handle, ProjectID: projectID, AllowedConnectors: "[]", AllowProviderSwitch: &allow}
	if err := globalTeam.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return *p
}

// switchAs posts a provider switch behind the session middlewares, as
// the router does.
func switchAs(t *testing.T, u *entity.User, sessionID, prov string) (int, string) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{"provider": prov}, map[string]string{"id": sessionID})
	sessionAccessMW(sharedChatReadOnlyMW(switchProvider))(c)
	return w.Code, w.Body.String()
}

// A Team agent's chat may switch provider whenever the agent allows it,
// started or not; the switch stays behind the agent's own setting, the
// provider access tags, and the read-only rule for another person's chat.
func TestTeamChatProviderSwitchGates(t *testing.T) {
	withTeamWorld(t)
	prevTag, prevKnob := providerAccessTagAllows, adminSeeAllProviders
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool { return name == "mine" }
	adminSeeAllProviders = func() bool { return false }
	t.Cleanup(func() { providerAccessTagAllows, adminSeeAllProviders = prevTag, prevKnob })

	owner := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	bob := &entity.User{ID: "bob", Role: entity.RoleUser, Approved: true}
	seedTeamProject(t, "p1", owner.ID)
	open := seedSwitchAgent(t, owner.ID, "open", "p1", true)
	fixed := seedSwitchAgent(t, owner.ID, "fixed", "p1", false)

	// Allowed: past every gate (the switch itself then fails on the temp
	// layout's empty provider list, which is not a 403).
	openChatID := openChat(t, owner, open.ID, false)
	if code, body := switchAs(t, owner, openChatID, "claude/mine"); code == http.StatusForbidden {
		t.Fatalf("owner switch, agent allows it: refused %s", body)
	}
	// The provider gate still holds.
	if code, body := switchAs(t, owner, openChatID, "claude/other"); code != http.StatusForbidden || !strings.Contains(body, "no access to provider") {
		t.Fatalf("switch to an instance without access: %d %s, want 403", code, body)
	}
	// allow_provider_switch off: refused for the owner too.
	fixedChatID := openChat(t, owner, fixed.ID, false)
	if code, body := switchAs(t, owner, fixedChatID, "claude/mine"); code != http.StatusForbidden || !strings.Contains(body, errProviderSetByAgent) {
		t.Fatalf("switch with allow_provider_switch off: %d %s, want 403", code, body)
	}

	// A recipient of the shared agent: the owner's chat is read-only, their
	// own chat follows the same agent setting as the owner's.
	for _, p := range []entity.AgentPersona{open, fixed} {
		if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
			t.Fatalf("share %s: %d", p.Handle, code)
		}
	}
	if code, _ := switchAs(t, bob, openChatID, "claude/mine"); code != http.StatusForbidden {
		t.Fatalf("recipient switches the owner's chat: %d, want 403", code)
	}
	if code, body := switchAs(t, bob, openChat(t, bob, open.ID, false), "claude/mine"); code == http.StatusForbidden {
		t.Fatalf("recipient switches own chat, agent allows it: refused %s", body)
	}
	if code, body := switchAs(t, bob, openChat(t, bob, fixed.ID, false), "claude/mine"); code != http.StatusForbidden || !strings.Contains(body, errProviderSetByAgent) {
		t.Fatalf("recipient switches own chat, agent forbids it: %d %s, want 403", code, body)
	}
}

// sendAs posts a chat message behind the session middlewares.
func sendAs(t *testing.T, u *entity.User, sessionID, text string) (int, string) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{"text": text}, map[string]string{"id": sessionID})
	sessionAccessMW(sharedChatReadOnlyMW(sendMessage))(c)
	return w.Code, w.Body.String()
}

func chatProvider(t *testing.T, sessionID string) string {
	t.Helper()
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok || len(sess.Agents) == 0 {
		t.Fatalf("session %s: no agent", sessionID)
	}
	return sess.Agents[0].Provider
}

// A "#tag" message is held to the same gates as the switch endpoint —
// in the UI (the caller's access) and on every other channel through
// provider.SwitchGuard (the session owner's access). A refused tag is
// ignored, not an error: the session keeps its provider.
func TestProviderTagSwitchGates(t *testing.T) {
	withTeamWorld(t)
	prevTag, prevKnob, prevOwner := providerAccessTagAllows, adminSeeAllProviders, lookupWorkflowOwner
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool { return name == "mine" }
	adminSeeAllProviders = func() bool { return false }
	owner := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	lookupWorkflowOwner = func(_ context.Context, id string) (*entity.User, error) {
		if id == owner.ID {
			return owner, nil
		}
		return nil, nil
	}
	t.Cleanup(func() {
		providerAccessTagAllows, adminSeeAllProviders, lookupWorkflowOwner = prevTag, prevKnob, prevOwner
	})

	seedTeamProject(t, "p1", owner.ID)
	open := seedSwitchAgent(t, owner.ID, "open", "p1", true)
	fixed := seedSwitchAgent(t, owner.ID, "fixed", "p1", false)
	openID, fixedID := openChat(t, owner, open.ID, false), openChat(t, owner, fixed.ID, false)

	// UI: a switch-only tag the gates refuse answers switch_ignored and
	// leaves the provider alone (a tag with a body would then send it).
	for _, tc := range []struct{ name, sessionID, tag, reason string }{
		{"agent keeps its provider", fixedID, "#claude/mine", errProviderSetByAgent},
		{"no access to the instance", openID, "#claude/other", "no access to provider claude/other"},
	} {
		before := chatProvider(t, tc.sessionID)
		code, body := sendAs(t, owner, tc.sessionID, tc.tag)
		if code != http.StatusOK || !strings.Contains(body, "switch_ignored") || !strings.Contains(body, tc.reason) {
			t.Fatalf("%s: %d %s", tc.name, code, body)
		}
		if got := chatProvider(t, tc.sessionID); got != before {
			t.Fatalf("%s: provider moved %q → %q", tc.name, before, got)
		}
	}

	// Channels and agentctl: the guard.
	if err := provider.CheckSwitch(context.Background(), fixedID, "claude/mine"); err == nil || err.Error() != errProviderSetByAgent {
		t.Fatalf("guard, agent keeps its provider: %v", err)
	}
	if err := provider.CheckSwitch(context.Background(), openID, "claude/other::opus"); err == nil || !strings.Contains(err.Error(), "no access to provider claude/other") {
		t.Fatalf("guard, owner without access: %v", err)
	}
	if err := provider.CheckSwitch(context.Background(), openID, "claude/mine"); err != nil {
		t.Fatalf("guard, allowed switch refused: %v", err)
	}
	if err := provider.CheckSwitch(context.Background(), "no-such-session", "claude/other"); err != nil {
		t.Fatalf("guard, unknown session: %v (Switch reports that itself)", err)
	}
}

package agents

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
)

// withFollowProviders is a provider registry of claude/old, claude/new
// and claude/mine, with every instance open to every caller.
func withFollowProviders(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	prev := provider.AppName()
	provider.SetAppName("wick-follow-" + strings.ReplaceAll(t.Name(), "/", "-"))
	t.Cleanup(func() {
		provider.WaitBackgroundRescans()
		provider.SetAppName(prev)
	})
	for _, n := range []string{"old", "new", "mine"} {
		if err := provider.Save(provider.Instance{Type: provider.TypeClaude, Name: n}); err != nil {
			t.Fatal(err)
		}
		provider.WaitBackgroundRescans()
	}
	prevTag, prevKnob := providerAccessTagAllows, adminSeeAllProviders
	providerAccessTagAllows = func(context.Context, *entity.User, provider.Type, string) bool { return true }
	adminSeeAllProviders = func() bool { return true }
	t.Cleanup(func() { providerAccessTagAllows, adminSeeAllProviders = prevTag, prevKnob })
}

// setProjectDefault stores provider/model as project pid's default.
func setProjectDefault(t *testing.T, pid, prov, model string) {
	t.Helper()
	proj, _ := globalMgr.Registry().Project(pid)
	meta := proj.Meta
	meta.Defaults.Provider, meta.Defaults.Model = prov, model
	if _, err := globalMgr.UpdateProject(context.Background(), pid, meta); err != nil {
		t.Fatal(err)
	}
}

func chatAgent(t *testing.T, sid string) session.AgentEntry {
	t.Helper()
	s, err := session.Load(globalLayout, sid)
	if err != nil || len(s.Agents) == 0 {
		t.Fatalf("load %s: %v %+v", sid, err, s.Agents)
	}
	return s.Agents[0]
}

func patchAgentProvider(t *testing.T, u *entity.User, id, prov, model string) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+id, map[string]any{"provider": prov, "model": model}, map[string]string{"id": id})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
}

// Changing an agent's default provider in Settings moves its existing
// chats: all of them when the agent keeps its provider, only the ones
// still on the old default when chats may switch (a hand pick wins).
func TestTeamAgentDefaultChangeMovesChats(t *testing.T) {
	withTeamWorld(t)
	withFollowProviders(t)
	owner := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedTeamProject(t, "p-fixed", owner.ID)
	seedTeamProject(t, "p-open", owner.ID)
	setProjectDefault(t, "p-fixed", "claude/old", "")
	setProjectDefault(t, "p-open", "claude/old", "")
	fixed := seedSwitchAgent(t, owner.ID, "fixed", "p-fixed", false)
	open := seedSwitchAgent(t, owner.ID, "open", "p-open", true)

	fixedChats := []string{openChat(t, owner, fixed.ID, false), openChat(t, owner, fixed.ID, true)}
	following := openChat(t, owner, open.ID, false)
	picked := openChat(t, owner, open.ID, true)
	for _, sid := range append(fixedChats, following, picked) {
		if a := chatAgent(t, sid); a.Provider != "claude/old" {
			t.Fatalf("%s made on %q, want claude/old", sid, a.Provider)
		}
	}
	if _, err := provider.ApplySwitch(globalLayout, picked, chatAgent(t, picked).Name, "claude/mine", provider.SwitchOptions{}); err != nil {
		t.Fatal(err)
	}

	patchAgentProvider(t, owner, fixed.ID, "claude/new", "opus")
	for _, sid := range fixedChats {
		if a := chatAgent(t, sid); a.Provider != "claude/new" || a.ModelID != "opus" {
			t.Fatalf("fixed chat %s = %q/%q, want claude/new/opus", sid, a.Provider, a.ModelID)
		}
	}

	patchAgentProvider(t, owner, open.ID, "claude/new", "")
	if a := chatAgent(t, following); a.Provider != "claude/new" {
		t.Fatalf("chat on the old default = %q, want claude/new", a.Provider)
	}
	if a := chatAgent(t, picked); a.Provider != "claude/mine" {
		t.Fatalf("hand-picked chat = %q, want claude/mine kept", a.Provider)
	}
}

// A chat left on an older default (made before this fix) is held to the
// default lazily, at its next message, only when the agent keeps its
// provider.
func TestAgentDefaultForHoldsFixedAgentsOnly(t *testing.T) {
	withTeamWorld(t)
	withFollowProviders(t)
	owner := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedTeamProject(t, "p-fixed", owner.ID)
	seedTeamProject(t, "p-open", owner.ID)
	setProjectDefault(t, "p-fixed", "claude/new", "opus")
	setProjectDefault(t, "p-open", "claude/new", "")
	fixed := seedSwitchAgent(t, owner.ID, "fixed", "p-fixed", false)
	open := seedSwitchAgent(t, owner.ID, "open", "p-open", true)
	ctx := context.Background()

	if prov, model, ok := agentDefaultFor(ctx, openChat(t, owner, fixed.ID, false)); !ok || prov != "claude/new" || model != "opus" {
		t.Fatalf("fixed agent: %q %q %v, want claude/new opus true", prov, model, ok)
	}
	if _, _, ok := agentDefaultFor(ctx, openChat(t, owner, open.ID, false)); ok {
		t.Fatal("an agent that lets chats switch must not hold them")
	}
}

// The Captain's update_persona moves the agent's chats too and says how
// many, also when the default it names is already stored.
func TestCaptainUpdatePersonaReportsSessionsSwitched(t *testing.T) {
	w := withCaptainWorld(t)
	withFollowProviders(t)
	ctx := context.Background()
	setProjectDefault(t, w.worker.ProjectID, "claude/new", "")
	if err := session.AddAgent(globalLayout, "s-worker", "default", "claude/old"); err != nil {
		t.Fatal(err)
	}
	model := "opus"
	out, err := (CaptainOps{}).UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", Model: &model})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["sessions_switched"] != 1 {
		t.Fatalf("result = %v, want sessions_switched 1", res)
	}
	if a := chatAgent(t, "s-worker"); a.Provider != "claude/new" || a.ModelID != "opus" {
		t.Fatalf("worker chat = %q/%q", a.Provider, a.ModelID)
	}
	// Named again, nothing stored changes and nothing is left to move.
	out, err = (CaptainOps{}).UpdatePersona(ctx, "s-captain", teamagents.PersonaInput{Agent: "@worker", Model: &model})
	if err != nil {
		t.Fatal(err)
	}
	if res := out.(map[string]any); res["status"] != "unchanged" || res["sessions_switched"] != 0 {
		t.Fatalf("re-save = %v", res)
	}
}

package agents

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/remote/pluginremote"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// The old "Who may use it" setting moves into the mention policy once:
// only_me (and a plugin row from before) becomes "Nobody", while
// me_and_my_agents keeps the policy the owner already chose. A second run
// changes nothing, so a later switch to "Any of my agents" sticks.
func TestMigrateRemoteUsage(t *testing.T) {
	withAgentA2AWorld(t)
	ctx := context.Background()
	mk := func(handle, kind string) entity.AgentPersona {
		p := &entity.AgentPersona{OwnerUserID: "u1", Handle: handle, ProjectID: "p-" + handle, Kind: kind, AllowedConnectors: "[]", MentionFrom: teamlink.MentionAll}
		if err := globalTeam.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		return *p
	}
	halo := mk("halodev", slackremote.Kind)
	open := mk("open", a2aremote.Kind)
	plug := mk("plug", pluginremote.Kind)
	if err := slackRemoteStore().Save(slackremote.Config{AgentID: halo.ID, OwnerUserID: "u1", Usage: slackremote.UsageOnlyMe}); err != nil {
		t.Fatal(err)
	}
	if err := remoteStore().Save(a2aremote.Config{AgentID: open.ID, OwnerUserID: "u1", Usage: a2aremote.UsageMeAndAgents}); err != nil {
		t.Fatal(err)
	}
	if err := pluginRemoteStore().Save(pluginremote.Config{AgentID: plug.ID, OwnerUserID: "u1", PluginKey: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []entity.AgentPersona{halo, open, plug} {
		if !remoteOwnerOnly(p) && p.ID != open.ID {
			t.Fatalf("@%s: before migration should be owner-only", p.Handle)
		}
	}

	migrateRemoteUsage(ctx)
	want := map[string]string{halo.ID: teamlink.MentionOff, open.ID: teamlink.MentionAll, plug.ID: teamlink.MentionOff}
	for id, mf := range want {
		p, err := globalTeam.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.MentionFrom != mf {
			t.Fatalf("@%s mention_from = %q, want %q", p.Handle, p.MentionFrom, mf)
		}
		if remoteOwnerOnly(p) {
			t.Fatalf("@%s still gated by the old usage after migration", p.Handle)
		}
		if u, _ := remoteUsage(p); u != remoteUsageByMention {
			t.Fatalf("@%s usage = %q, want %q", p.Handle, u, remoteUsageByMention)
		}
	}

	// The owner opens it up; a second migration leaves it alone.
	p, _ := globalTeam.Get(ctx, halo.ID)
	p.MentionFrom = teamlink.MentionAll
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	migrateRemoteUsage(ctx)
	if p, _ = globalTeam.Get(ctx, halo.ID); p.MentionFrom != teamlink.MentionAll {
		t.Fatalf("re-run reset the owner's choice: %q", p.MentionFrom)
	}
}

// A new remote agent defaults to "Nobody", as only_me did.
func TestRemoteMentionDefault(t *testing.T) {
	if got := remoteMentionDefault(""); got != teamlink.MentionOff {
		t.Fatalf("default = %q", got)
	}
	if got := remoteMentionDefault(a2aremote.UsageMeAndAgents); got != teamlink.MentionAll {
		t.Fatalf("me_and_my_agents = %q", got)
	}
}

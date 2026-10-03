package team

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

func TestPromptFor(t *testing.T) {
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	svc := NewService(testDB(t), layout)
	// The persona names the agent "Bob"; the block still says who it is.
	if _, err := project.Create(layout, project.CreateOptions{
		ID: "p-cap", Name: "Captain", Description: "Lead agent",
		Defaults: project.Defaults{SystemAddon: "You are Bob."},
	}); err != nil {
		t.Fatal(err)
	}
	capt := &entity.AgentPersona{OwnerUserID: "u1", Handle: "captain", ProjectID: "p-cap", IsCaptain: true, AllowedConnectors: "[]"}
	ops := &entity.AgentPersona{OwnerUserID: "u1", Handle: "ops", AllowedConnectors: "[]"}
	for _, p := range []*entity.AgentPersona{capt, ops} {
		if err := svc.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []session.CreateOptions{
		{ID: "s-cap", AgentID: capt.ID},
		{ID: "s-ops", AgentID: ops.ID},
		{ID: "s-child", ParentSessionID: "s-ops"},
		{ID: "s-plain"},
	} {
		if _, err := session.Create(ctx, layout, s); err != nil {
			t.Fatal(err)
		}
	}

	got := svc.PromptFor(ctx, "s-cap", false)
	for _, want := range []string{"## You are a Team agent", "Your name is Captain.", "@captain", "Your role: Lead agent.", "You are the Captain", "Your Team: ops (@ops)"} {
		if !strings.Contains(got, want) {
			t.Errorf("captain prompt missing %q", want)
		}
	}
	got = svc.PromptFor(ctx, "s-ops", false)
	if !strings.Contains(got, "Your name is ops.") || strings.Contains(got, "You are the Captain") || !strings.Contains(got, "Captain (@captain) — Lead agent") {
		t.Errorf("non-captain prompt wrong:\n%s", got)
	}
	if got := svc.PromptFor(ctx, "s-child", true); got != SubAgentOfTeam("ops") {
		t.Errorf("sub-agent prompt = %q", got)
	}
	if got := svc.PromptFor(ctx, "s-plain", false); got != "" {
		t.Errorf("ordinary session got %q", got)
	}
}

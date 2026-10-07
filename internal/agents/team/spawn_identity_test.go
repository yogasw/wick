package team

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

func TestSpawnIdentity(t *testing.T) {
	meta := session.Meta{UserID: "u-session-owner"}
	agent := func(runAs string) *entity.AgentPersona {
		return &entity.AgentPersona{OwnerUserID: "u-agent-owner", RunAs: runAs}
	}
	cases := []struct {
		name   string
		agent  *entity.AgentPersona
		caller string
		want   string
	}{
		{"ordinary session, human caller", nil, "u-human", "u-human"},
		{"ordinary session, no human", nil, "", "u-session-owner"},
		{"agent caller mode, human", agent(RunAsCaller), "u-human", "u-human"},
		{"agent caller mode, no human (schedule/bot)", agent(RunAsCaller), "", "u-agent-owner"},
		{"agent legacy row reads as caller", agent(""), "u-human", "u-human"},
		{"agent owner mode, another human", agent(RunAsOwner), "u-human", "u-agent-owner"},
		{"agent owner mode, no human", agent(RunAsOwner), "", "u-agent-owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SpawnIdentity(meta, tc.agent, tc.caller); got != tc.want {
				t.Fatalf("SpawnIdentity = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeRunAs(t *testing.T) {
	for in, want := range map[string]string{"": RunAsCaller, "caller": RunAsCaller, "owner": RunAsOwner, "root": RunAsCaller} {
		if got := NormalizeRunAs(in); got != want {
			t.Errorf("NormalizeRunAs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentityFixed(t *testing.T) {
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	svc := NewService(testDB(t), layout)
	p := &entity.AgentPersona{OwnerUserID: "u1", Handle: "worker", AllowedConnectors: "[]"}
	if err := svc.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, s := range []session.CreateOptions{{ID: "s-agent", AgentID: p.ID}, {ID: "s-plain"}} {
		if _, err := session.Create(ctx, layout, s); err != nil {
			t.Fatal(err)
		}
	}
	got := svc.AgentFor(ctx, "s-agent")
	if got == nil || NormalizeRunAs(got.RunAs) != RunAsCaller {
		t.Fatalf("new agent should default to caller, got %+v", got)
	}
	if svc.IdentityFixed(ctx, "s-agent") || svc.IdentityFixed(ctx, "s-plain") {
		t.Fatal("caller mode and ordinary sessions follow the caller")
	}
	p.RunAs = RunAsOwner
	if err := svc.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if !svc.IdentityFixed(ctx, "s-agent") {
		t.Fatal("owner mode must not depend on the caller")
	}
	var nilSvc *Service
	if nilSvc.IdentityFixed(ctx, "s-agent") || nilSvc.AgentFor(ctx, "s-agent") != nil {
		t.Fatal("nil service must read as no agent")
	}
}

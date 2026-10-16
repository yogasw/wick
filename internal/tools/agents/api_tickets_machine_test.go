package agents

import (
	"context"
	"net/http/httptest"
	"testing"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// A plugin calling the ticket REST API through the Host API carries no
// token: wick marks the request with login.WithMachineCaller instead. The
// per-project REST toggle and the "api" actor must treat it like a PAT.
func TestMachineCallerHonoursTicketAPIToggle(t *testing.T) {
	layout := agentsconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	p, err := project.Create(layout, project.CreateOptions{ID: "p1", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	prev := globalLayout
	globalLayout = layout
	t.Cleanup(func() { globalLayout = prev })

	u := &entity.User{ID: "u1", Name: "Ada", Approved: true}
	ctxFor := func(machine bool) *tool.Ctx {
		ctx := login.WithUser(context.Background(), u, nil)
		if machine {
			ctx = login.WithMachineCaller(ctx, "plugin:sync")
		}
		r := httptest.NewRequest("GET", "/tools/agents/api/tickets/p1", nil).WithContext(ctx)
		return tool.NewCtx(httptest.NewRecorder(), r, nil, tool.Tool{Key: "agents"}, nil, nil)
	}

	ui, plugin := ctxFor(false), ctxFor(true)
	if !requireTicketAPI(ui, "p1") {
		t.Fatal("the UI must not be gated by the REST toggle")
	}
	if requireTicketAPI(plugin, "p1") {
		t.Fatal("a plugin request passed with the REST toggle off")
	}
	if a := callerActor(ui); a.Type != ticket.ActorUser {
		t.Fatalf("UI actor = %+v", a)
	}
	if a := callerActor(plugin); a.Type != ticket.ActorAPI || a.ID != "u1" {
		t.Fatalf("plugin actor = %+v, want api as u1", a)
	}

	meta := p.Meta
	meta.Ticket.Integrations.APIEnabled = true
	if err := project.SaveMeta(layout, "p1", meta); err != nil {
		t.Fatal(err)
	}
	if !requireTicketAPI(plugin, "p1") {
		t.Fatal("a plugin request refused with the REST toggle on")
	}
}

package agents

import (
	"context"
	"net/http/httptest"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// A web turn must carry its sender as the CALLER on the detached ctx the pool
// spawns from. Missing, the spawn fell back to the session owner's MCP access,
// so a second person typing into someone else's session ran with that
// owner's connectors.
func TestWithComposerSenderStampsCaller(t *testing.T) {
	prev := globalPool
	globalPool = nil
	t.Cleanup(func() { globalPool = prev })

	r := httptest.NewRequest("POST", "/tools/agents/sessions/s1/send", nil)
	r = r.WithContext(login.WithUser(r.Context(), &entity.User{ID: "u-caller", Name: "Caller"}, nil))
	c := tool.NewCtx(httptest.NewRecorder(), r, nil, tool.Tool{Key: "agents"}, nil, nil)

	ctx := withComposerSender(c, context.Background(), "s1")

	if got := agentchannels.CallerUserID(ctx); got != "u-caller" {
		t.Fatalf("CallerUserID = %q, want u-caller", got)
	}
	if s := agentchannels.SenderFrom(ctx); s == nil || s.WickUserID != "u-caller" {
		t.Fatalf("sender = %+v, want WickUserID u-caller", s)
	}
}

func TestWithComposerSenderNoUserLeavesCallerEmpty(t *testing.T) {
	r := httptest.NewRequest("POST", "/tools/agents/sessions/s1/send", nil)
	c := tool.NewCtx(httptest.NewRecorder(), r, nil, tool.Tool{Key: "agents"}, nil, nil)

	ctx := withComposerSender(c, context.Background(), "s1")

	if got := agentchannels.CallerUserID(ctx); got != "" {
		t.Fatalf("CallerUserID = %q, want empty", got)
	}
}

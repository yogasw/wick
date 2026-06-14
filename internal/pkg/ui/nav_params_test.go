package ui

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestNavParamsFor_NoFn(t *testing.T) {
	navParamsFn = nil
	agentsAvailFn = func() bool { return true }
	params := NavParamsFor(context.Background(), &entity.User{ID: "u1"})
	if params.CanSeeAgents {
		t.Error("expected CanSeeAgents=false when no navParamsFn registered")
	}
}

func TestNavParamsFor_AgentsNotRunning(t *testing.T) {
	agentsAvailFn = func() bool { return false }
	navParamsFn = func(_ context.Context, _ *entity.User) NavParams {
		return NavParams{CanSeeAgents: true}
	}
	params := NavParamsFor(context.Background(), &entity.User{ID: "u1"})
	if params.CanSeeAgents {
		t.Error("expected CanSeeAgents=false when agents process not running")
	}
}

func TestNavParamsFor_AgentsRunning_FnGrants(t *testing.T) {
	agentsAvailFn = func() bool { return true }
	navParamsFn = func(_ context.Context, _ *entity.User) NavParams {
		return NavParams{CanSeeAgents: true}
	}
	params := NavParamsFor(context.Background(), &entity.User{ID: "u1"})
	if !params.CanSeeAgents {
		t.Error("expected CanSeeAgents=true")
	}
}

func TestNavParamsFor_NilUser(t *testing.T) {
	agentsAvailFn = func() bool { return true }
	navParamsFn = func(_ context.Context, _ *entity.User) NavParams {
		return NavParams{CanSeeAgents: true}
	}
	params := NavParamsFor(context.Background(), nil)
	if params.CanSeeAgents {
		t.Error("expected CanSeeAgents=false for nil user")
	}
}

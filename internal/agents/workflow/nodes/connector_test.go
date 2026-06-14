package nodes

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

// stubModule registers a single "whoami" op that surfaces the logged-in
// user (if any) the same way an identity-gated connector op would via
// login.GetUser(c.Context()).
func stubIdentityRegistry() (*connector.Registry, *string) {
	seen := new(string)
	reg := connector.NewRegistry(nil, nil)
	reg.Register(pkgconnector.Module{
		Meta: pkgconnector.Meta{Key: "stub", Name: "Stub"},
		Operations: []pkgconnector.Operation{
			{
				Key: "whoami",
				Execute: func(c *pkgconnector.Ctx) (any, error) {
					u := login.GetUser(c.Context())
					if u == nil {
						return nil, errors.New("not authenticated")
					}
					*seen = u.ID
					return map[string]any{"id": u.ID}, nil
				},
			},
		},
	})
	return reg, seen
}

func connRC(createdBy string) *workflow.RunContext {
	return &workflow.RunContext{
		Workflow: workflow.Workflow{ID: "wf-conn", CreatedBy: createdBy},
		RunID:    "run-1",
		Event:    workflow.Event{Type: "manual"},
	}
}

func whoamiNode() workflow.Node {
	return workflow.Node{ID: "call", Type: workflow.NodeConnector, Module: "stub", Op: "whoami"}
}

// With a resolver wired, the connector node runs as the workflow owner —
// the op sees login.GetUser instead of "not authenticated". This is the
// headless-run identity fix.
func TestConnector_StampsWorkflowOwner(t *testing.T) {
	reg, seen := stubIdentityRegistry()
	reg.SetUserResolver(func(ctx context.Context, id string) (*entity.User, []string, error) {
		if id != "owner-7" {
			return nil, nil, nil
		}
		return &entity.User{ID: "owner-7"}, []string{"tag-a"}, nil
	})

	exec := NewConnectorExecutor(reg)
	out, err := exec.Execute(context.Background(), whoamiNode(), connRC("owner-7"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if *seen != "owner-7" {
		t.Fatalf("op saw user %q, want owner-7", *seen)
	}
	if got := out.Fields["id"]; got != "owner-7" {
		t.Fatalf("result id = %v, want owner-7", got)
	}
}

// No resolver wired (legacy) → no identity stamped → the op's own gate
// rejects. Confirms we didn't paper over auth: the gate still fires.
func TestConnector_NoResolverStaysUnauthenticated(t *testing.T) {
	reg, _ := stubIdentityRegistry()
	exec := NewConnectorExecutor(reg)
	_, err := exec.Execute(context.Background(), whoamiNode(), connRC("owner-7"))
	if err == nil {
		t.Fatal("expected not-authenticated error, got nil")
	}
}

// Empty CreatedBy (no owner recorded) must not blow up the resolver — it
// just runs unauthenticated.
func TestConnector_EmptyOwnerSkipsStamp(t *testing.T) {
	reg, _ := stubIdentityRegistry()
	called := false
	reg.SetUserResolver(func(ctx context.Context, id string) (*entity.User, []string, error) {
		called = true
		return &entity.User{ID: id}, nil, nil
	})
	exec := NewConnectorExecutor(reg)
	_, err := exec.Execute(context.Background(), whoamiNode(), connRC(""))
	if err == nil {
		t.Fatal("expected not-authenticated error with empty owner")
	}
	if called {
		t.Fatal("resolver should not be called for empty CreatedBy")
	}
}

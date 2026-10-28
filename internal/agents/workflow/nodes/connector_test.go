package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

// stubRegistry registers a "stub" module with one "whoami" op and records
// every Call the node hands to the executor, plus the user on its ctx.
func stubRegistry(result string, execErr error) (*connector.Registry, *[]connector.Call, *string) {
	calls := &[]connector.Call{}
	ctxUser := new(string)
	reg := connector.NewRegistry(func(ctx context.Context, c connector.Call) (string, error) {
		*calls = append(*calls, c)
		if u := login.GetUser(ctx); u != nil {
			*ctxUser = u.ID
		}
		return result, execErr
	}, nil)
	reg.Register(pkgconnector.Module{
		Meta: pkgconnector.Meta{Key: "stub", Name: "Stub"},
		Operations: []pkgconnector.Category{
			pkgconnector.Cat("", "", pkgconnector.Operation{Key: "whoami"}),
		},
	})
	return reg, calls, ctxUser
}

func ownerResolver(want string) connector.UserResolverFn {
	return func(ctx context.Context, id string) (*entity.User, []string, error) {
		if id != want {
			return nil, nil, nil
		}
		return &entity.User{ID: id}, []string{"tag-a"}, nil
	}
}

func connRC(createdBy string) *workflow.RunContext {
	return &workflow.RunContext{
		Workflow: workflow.Workflow{ID: "wf-conn", CreatedBy: createdBy},
		RunID:    "run-1",
		Event:    workflow.Event{Type: "manual"},
	}
}

func whoamiNode() workflow.Node {
	return workflow.Node{ID: "call", Type: workflow.NodeConnector, Module: "stub", Op: "whoami", Row: "Instance B"}
}

// The node hands the op, the named instance and the workflow owner to the
// executor, with the owner on the ctx, and decodes the JSON it returns.
func TestConnector_RunsAsOwnerOnNamedInstance(t *testing.T) {
	reg, calls, ctxUser := stubRegistry(`{"id":"owner-7"}`, nil)
	reg.SetUserResolver(ownerResolver("owner-7"))

	out, err := NewConnectorExecutor(reg).Execute(context.Background(), whoamiNode(), connRC("owner-7"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("executor called %d times, want 1", len(*calls))
	}
	c := (*calls)[0]
	if c.Module != "stub" || c.Op != "whoami" || c.Instance != "Instance B" {
		t.Errorf("call = %s.%s on %q, want stub.whoami on Instance B", c.Module, c.Op, c.Instance)
	}
	if c.Owner == nil || c.Owner.ID != "owner-7" || len(c.OwnerTags) != 1 {
		t.Errorf("owner = %+v tags %v, want owner-7 with its tags", c.Owner, c.OwnerTags)
	}
	if *ctxUser != "owner-7" {
		t.Errorf("ctx user = %q, want owner-7", *ctxUser)
	}
	if got := out.Fields["id"]; got != "owner-7" {
		t.Errorf("result id = %v, want owner-7", got)
	}
}

// No owner to run as — unresolvable, or none recorded — fails the node
// before anything reaches the connector.
func TestConnector_NoOwnerFailsBeforeExecute(t *testing.T) {
	for _, tc := range []struct {
		name      string
		createdBy string
	}{{"unresolved", "someone-else"}, {"empty", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			reg, calls, _ := stubRegistry(`{}`, nil)
			reg.SetUserResolver(ownerResolver("owner-7"))
			_, err := NewConnectorExecutor(reg).Execute(context.Background(), whoamiNode(), connRC(tc.createdBy))
			if err == nil || !strings.Contains(err.Error(), "no owner") {
				t.Fatalf("want a no-owner error, got %v", err)
			}
			if len(*calls) != 0 {
				t.Fatalf("executor must not be called, got %d calls", len(*calls))
			}
		})
	}
}

// An executor error surfaces as the node's error, prefixed with the op.
func TestConnector_ExecuteErrorFailsNode(t *testing.T) {
	reg, _, _ := stubRegistry("", errors.New("the workflow owner cannot use stub instance"))
	reg.SetUserResolver(ownerResolver("owner-7"))
	_, err := NewConnectorExecutor(reg).Execute(context.Background(), whoamiNode(), connRC("owner-7"))
	if err == nil || !strings.Contains(err.Error(), "stub.whoami: the workflow owner cannot use") {
		t.Fatalf("want the executor error, got %v", err)
	}
}

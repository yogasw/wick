package setup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow/connector"
	connectorsvc "github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// userLookup is the subset of *login.Service the resolver needs. Kept as
// an interface so setup doesn't import login concretely (and so tests can
// stub it).
type userLookup interface {
	GetUserByID(ctx context.Context, id string) (*entity.User, error)
	GetUserFilterTagIDs(ctx context.Context, userID string) []string
}

// UserResolverAdapter wraps the login service into a connector.UserResolverFn
// so the workflow connector executor can run identity-gated ops as the
// workflow owner (Workflow.CreatedBy). Unknown ids resolve to a nil user
// (no error) so the run proceeds unauthenticated and the op's own gate
// decides whether to reject.
func UserResolverAdapter(svc userLookup) connector.UserResolverFn {
	return func(ctx context.Context, userID string) (*entity.User, []string, error) {
		u, err := svc.GetUserByID(ctx, userID)
		if err != nil || u == nil {
			return nil, nil, nil
		}
		return u, svc.GetUserFilterTagIDs(ctx, userID), nil
	}
}

// ConnectorsExecutor runs workflow connector nodes through
// connectors.Service.Execute as the workflow owner — the path a direct
// MCP call takes, so decryption, op state, admin-only ops, masking and the
// connector run history are the same for both.
//
// The instance is named by ID or label (case-insensitive). Empty picks the
// newest instance of the module, the rule nodes without an instance have
// always had, so a workflow keeps the instance it ran against. The owner
// must be able to see the instance, as for a direct call.
func ConnectorsExecutor(svc *connectorsvc.Service) connector.ExecuteFn {
	return func(ctx context.Context, c connector.Call) (string, error) {
		if c.Owner == nil {
			return "", errors.New("no workflow owner to run as")
		}
		row, err := resolveInstance(ctx, svc, c.Module, c.Instance)
		if err != nil {
			return "", err
		}
		visible, err := svc.IsVisibleTo(ctx, row.ID, c.Owner.ID, c.OwnerTags, c.Owner.IsAdmin())
		if err != nil {
			return "", fmt.Errorf("check access to %s instance %q: %w", c.Module, row.Label, err)
		}
		if !visible {
			return "", fmt.Errorf("the workflow owner cannot use %s instance %q — share it with them or pick another instance on the node", c.Module, row.Label)
		}
		res, execErr := svc.Execute(ctx, connectorsvc.ExecuteParams{
			ConnectorID:  row.ID,
			OperationKey: c.Op,
			Input:        c.Input,
			RawInput:     c.RawInput,
			Source:       entity.ConnectorRunSourceWorkflow,
			UserID:       c.Owner.ID,
			IsAdmin:      c.Owner.IsAdmin(),
			TagIDs:       c.OwnerTags,
			AccountID:    strings.TrimSpace(c.AccountID),
		})
		if execErr != nil {
			// Same precedence as wick_execute: the masked response, then
			// the masked error message, then the raw error.
			msg := execErr.Error()
			if res != nil && res.ErrorMessage != "" {
				msg = res.ErrorMessage
			}
			if res != nil && res.ResponseJSON != "" {
				msg = res.ResponseJSON
			}
			return "", errors.New(msg)
		}
		return res.ResponseJSON, nil
	}
}

// resolveInstance picks the instance of module a node names by ID or
// label; empty name → the newest instance.
func resolveInstance(ctx context.Context, svc *connectorsvc.Service, module, name string) (*entity.Connector, error) {
	rows, err := svc.ListByKey(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("list %s instances: %w", module, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no %s instance is configured", module)
	}
	want := strings.TrimSpace(name)
	if want == "" {
		return &rows[0], nil
	}
	for i := range rows {
		if rows[i].ID == want || strings.EqualFold(rows[i].Label, want) {
			return &rows[i], nil
		}
	}
	return nil, fmt.Errorf("no %s instance named %q", module, want)
}

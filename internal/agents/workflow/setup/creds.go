package setup

import (
	"context"
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

// ConnectorsCredsAdapter wraps the connectors service to expose a
// connector.RowCredsFn for the workflow registry. Lookup is by
// (module key, row label) — first matching label wins; empty row
// falls back to the first instance for that Key.
func ConnectorsCredsAdapter(svc *connectorsvc.Service) func(module, row string) (map[string]string, error) {
	return func(module, row string) (map[string]string, error) {
		rows, err := svc.ListByKey(context.Background(), module)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return map[string]string{}, nil
		}
		want := strings.TrimSpace(row)
		for _, r := range rows {
			if want == "" || strings.EqualFold(r.Label, want) || r.ID == want {
				return svc.LoadConfigs(r), nil
			}
		}
		// No label match — fall back to first row so YAML stays usable.
		return svc.LoadConfigs(rows[0]), nil
	}
}

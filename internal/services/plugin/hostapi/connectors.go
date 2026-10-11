package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp/handlers"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// Connector op deadlines: the default when the call carries none, and the
// most a call may ask for.
const (
	ConnectorDefaultTimeout = 3 * time.Minute
	ConnectorMaxTimeout     = 5 * time.Minute
)

// principal loads the person a user-level call runs as (resolveUser already
// checked they exist and are approved).
func (s *Server) principal(ctx context.Context, c *call, rpc string) (Principal, error) {
	if s.d.Principals == nil {
		return Principal{}, unavailable(rpc)
	}
	u, err := s.d.Principals.GetUserByID(ctx, c.userID)
	if err != nil || u == nil {
		return Principal{}, status.Error(codes.FailedPrecondition, "acting user ("+c.source+") could not be loaded")
	}
	return Principal{User: u, TagIDs: s.d.Principals.GetUserFilterTagIDs(ctx, u.ID)}, nil
}

// ListConnectorInstances lists the connector instances the acting user can
// see: id, module key, label, status, ops, owner and the ids of the linked
// accounts that user may run as. Never a credential.
func (s *Server) ListConnectorInstances(ctx context.Context, in *hostpb.ListConnectorInstancesRequest) (*hostpb.ListConnectorInstancesResponse, error) {
	var out *hostpb.ListConnectorInstancesResponse
	err := s.run(ctx, "ListConnectorInstances", wickplugin.ScopeHostConnectorsRead, "", func(c *call) error {
		if s.d.Connectors == nil {
			return unavailable("ListConnectorInstances")
		}
		p, err := s.principal(ctx, c, "ListConnectorInstances")
		if err != nil {
			return err
		}
		list, err := s.d.Connectors.ListInstances(ctx, p, in.GetConnector())
		if err != nil {
			return status.Errorf(codes.Unavailable, "list connector instances: %v", err)
		}
		out = &hostpb.ListConnectorInstancesResponse{Instances: list}
		return nil
	})
	return out, err
}

// ExecuteConnector runs a declared connector op as the acting user. The item
// is "connector.op": it must be declared in host_resources.connectors and not
// blocked by an admin. Instance ACL, per-op gates, account ownership, rate
// limits and masking are connectors.Service's, exactly as for wick_execute.
func (s *Server) ExecuteConnector(ctx context.Context, in *hostpb.ExecuteConnectorRequest) (*hostpb.ExecuteConnectorResponse, error) {
	var out *hostpb.ExecuteConnectorResponse
	g := gate{rpc: "ExecuteConnector", scope: wickplugin.ScopeHostConnectorsExec,
		item: wickplugin.ConnectorItem(in.GetConnector(), in.GetOp())}
	err := s.runGate(ctx, g, func(c *call) error {
		if s.d.Connectors == nil {
			return unavailable("ExecuteConnector")
		}
		destructive, found := s.d.Connectors.Op(in.GetConnector(), in.GetOp())
		if !found {
			return status.Errorf(codes.NotFound, "connector %q has no operation %q", in.GetConnector(), in.GetOp())
		}
		if destructive {
			c.addNote("destructive")
		}
		args := map[string]any{}
		if raw := in.GetArgsJson(); len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return status.Error(codes.InvalidArgument, "args must be a JSON object")
			}
		}
		p, err := s.principal(ctx, c, "ExecuteConnector")
		if err != nil {
			return err
		}
		id := in.GetInstanceId()
		if id == "" {
			list, err := s.d.Connectors.ListInstances(ctx, p, in.GetConnector())
			if err != nil {
				return status.Errorf(codes.Unavailable, "list connector instances: %v", err)
			}
			switch len(list) {
			case 0:
				return status.Errorf(codes.NotFound, "no %q instance is visible to the acting user", in.GetConnector())
			case 1:
				id = list[0].GetId()
			default:
				return status.Errorf(codes.FailedPrecondition, "%d %q instances are visible to the acting user; pass instance_id", len(list), in.GetConnector())
			}
		}
		c.addNote("instance=" + id)
		ctx, cancel := connectorDeadline(ctx)
		defer cancel()
		res, err := s.d.Connectors.Execute(ctx, p, ConnectorCall{Connector: in.GetConnector(), InstanceID: id,
			Op: in.GetOp(), Args: args, AccountID: in.GetAccountId(), PluginKey: c.plugin.Key})
		if err != nil {
			return err
		}
		result := []byte(res.ResultJSON)
		if !json.Valid(result) {
			result, _ = json.Marshal(res.ResultJSON)
		}
		out = &hostpb.ExecuteConnectorResponse{ResultJson: result,
			Meta: map[string]string{"instance_id": id, "run_id": res.RunID, "status": res.Status}}
		return nil
	})
	return out, err
}

// connectorDeadline applies the default op deadline and caps a longer one.
func connectorDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	d, ok := ctx.Deadline()
	switch {
	case !ok:
		return context.WithTimeout(ctx, ConnectorDefaultTimeout)
	case time.Until(d) > ConnectorMaxTimeout:
		return context.WithTimeout(ctx, ConnectorMaxTimeout)
	}
	return context.WithCancel(ctx)
}

// connectorRunner adapts connectors.Service to ConnectorRunner along the
// wick_execute path (handlers.ExecuteAs): the principal is stamped on the
// context the way the auth middleware does, the instance must be visible to
// it, then Service.Execute applies op gates, account ownership, rate limits
// and masking and records the run.
type connectorRunner struct{ svc *connectors.Service }

// NewConnectorRunner wraps svc.
func NewConnectorRunner(svc *connectors.Service) ConnectorRunner { return connectorRunner{svc: svc} }

func (r connectorRunner) ListInstances(ctx context.Context, p Principal, key string) ([]*hostpb.ConnectorInstance, error) {
	ctx = login.WithUser(ctx, p.User, p.TagIDs)
	rows, err := r.svc.ListVisibleTo(ctx, p.User.ID, p.TagIDs, p.User.IsAdmin())
	if err != nil {
		return nil, err
	}
	out := []*hostpb.ConnectorInstance{}
	for _, row := range rows {
		if row.Disabled || (key != "" && row.Key != key) || !r.svc.TypeEnabled(row.Key) {
			continue
		}
		inst := &hostpb.ConnectorInstance{Id: row.ID, Connector: row.Key, Label: row.Label, Status: "enabled", OwnerUserId: row.CreatedBy}
		// Only the accounts the acting user may run as, ids only.
		accs, err := r.svc.ListAccountsVisibleTo(ctx, row, r.svc.AccountAccessFor(row, p.User.ID, p.User.IsAdmin(), p.TagIDs))
		if err != nil {
			return nil, err
		}
		for _, a := range accs {
			inst.AccountIds = append(inst.AccountIds, a.ID)
		}
		if mod, ok := r.svc.Module(row.Key); ok {
			for _, op := range mod.AllOps() {
				if !op.ConfigOnly {
					inst.Ops = append(inst.Ops, op.Key)
				}
			}
		}
		out = append(out, inst)
	}
	return out, nil
}

func (r connectorRunner) Op(key, op string) (bool, bool) {
	mod, ok := r.svc.Module(key)
	if !ok {
		return false, false
	}
	for _, o := range mod.AllOps() {
		if o.Key == op && !o.ConfigOnly {
			return o.Destructive, true
		}
	}
	return false, false
}

var errInstanceNotFound = status.Error(codes.NotFound, "connector instance not found or not accessible")

func (r connectorRunner) Execute(ctx context.Context, p Principal, in ConnectorCall) (*ConnectorResult, error) {
	ctx = login.WithUser(ctx, p.User, p.TagIDs)
	row, err := r.svc.Get(ctx, in.InstanceID)
	// A named instance gets the same checks as an auto-picked one: right
	// module, enabled, its type enabled, visible to the acting user.
	if err != nil || row == nil || row.Key != in.Connector || row.Disabled || !r.svc.TypeEnabled(row.Key) {
		return nil, errInstanceNotFound
	}
	if ok, err := r.svc.IsVisibleTo(ctx, row.ID, p.User.ID, p.TagIDs, p.User.IsAdmin()); err != nil || !ok {
		return nil, errInstanceNotFound
	}
	res, err := r.svc.Execute(ctx, connectors.ExecuteParams{
		ConnectorID:  row.ID,
		OperationKey: in.Op,
		Input:        handlers.StringifyArgs(in.Args),
		RawInput:     in.Args,
		Source:       entity.ConnectorRunSourcePlugin,
		UserID:       p.User.ID,
		IsAdmin:      p.User.IsAdmin(),
		TagIDs:       p.TagIDs,
		UserAgent:    "wick-plugin/" + in.PluginKey,
		AccountID:    in.AccountID,
	})
	if err != nil {
		// Same precedence as wick_execute: the masked response, then the
		// masked error message, then the raw error.
		msg := err.Error()
		if res != nil && res.ErrorMessage != "" {
			msg = res.ErrorMessage
		}
		if res != nil && res.ResponseJSON != "" {
			msg = res.ResponseJSON
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, msg)
		}
		return nil, status.Error(codes.Aborted, msg)
	}
	return &ConnectorResult{RunID: res.RunID, Status: string(res.Status), ResultJSON: res.ResponseJSON}, nil
}

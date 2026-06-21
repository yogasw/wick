package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// ErrPluginOp wraps an operation-level error returned by the plugin (vs a
// transport error). Callers can errors.Is against it.
var ErrPluginOp = errors.New("plugin operation error")

// ExecCall is the host-side call descriptor the adapter closure fills from
// a *connector.Ctx (operation + plaintext input + plaintext creds).
type ExecCall struct {
	Operation string
	Input     map[string]string
	Creds     map[string]string
	RequestID string
	SessionID string
}

// GRPCConn is the host-facing surface of a connector plugin client. The
// manager hands this to the adapter closure; *grpcClient implements it.
type GRPCConn interface {
	Execute(ctx context.Context, call ExecCall) ([]byte, error)
	Schema(ctx context.Context) ([]byte, error)
}

// grpcClient is the host's handle to one connector plugin's gRPC service.
// It is what GRPCClient() dispenses; the manager keeps it alive per plugin.
type grpcClient struct {
	inner pb.ConnectorClient
}

// Execute runs one operation in the plugin and returns the raw result JSON.
func (c *grpcClient) Execute(ctx context.Context, call ExecCall) ([]byte, error) {
	args, err := json.Marshal(call.Input)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}
	resp, err := c.inner.Execute(ctx, &pb.ExecuteRequest{
		Operation: call.Operation,
		ArgsJson:  args,
		Creds:     call.Creds,
		RequestId: call.RequestID,
		SessionId: call.SessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("plugin transport: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%w: [%s] %s", ErrPluginOp, resp.Error.Code, resp.Error.Message)
	}
	return resp.ResultJson, nil
}

// Schema fetches the manifest JSON from a live plugin (used by the loader
// when no plugin.json is present on disk — Phase 2 hot path).
func (c *grpcClient) Schema(ctx context.Context) ([]byte, error) {
	resp, err := c.inner.Schema(ctx, &pb.SchemaRequest{})
	if err != nil {
		return nil, err
	}
	return resp.ManifestJson, nil
}

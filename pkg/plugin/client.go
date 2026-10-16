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
	// InstanceID / CallerUserID mirror the host Ctx so the plugin-side Ctx
	// answers c.InstanceID() and c.CallerUserID() the same way.
	InstanceID   string
	CallerUserID string
	// AccountID / OwnerBotID mirror c.AccountID() and c.OwnerBotID().
	AccountID  string
	OwnerBotID string
	// RawInput mirrors c.RawInput() (the untyped arguments); nil = none.
	RawInput map[string]any
	// HealthCheck runs the module's HealthCheck hook instead of Operation;
	// the result JSON is the []connector.OpHealth report.
	HealthCheck bool
	// Progress receives the op's c.ReportProgress calls. Only ExecuteStream
	// delivers them; the unary Execute has no channel back and drops them.
	Progress func(progress, total int, message string)
}

// ExecResult is what one plugin operation returns: the result JSON plus the
// values the operation passed to c.Mask / c.MaskIgnoreCase. The plugin has no
// encryption key, so those values arrive in the clear and the host masks them.
// On an operation error (ErrPluginOp) the mask lists are still filled, so the
// host can scrub the error message as well.
type ExecResult struct {
	JSON           []byte
	Mask           []string
	MaskIgnoreCase []string
}

// GRPCConn is the host-facing surface of a connector plugin client. The
// manager hands this to the adapter closure; *grpcClient implements it.
type GRPCConn interface {
	Execute(ctx context.Context, call ExecCall) (ExecResult, error)
	ExecuteStream(ctx context.Context, call ExecCall) (ExecResult, error)
	Schema(ctx context.Context) ([]byte, error)
	ResolveIdentity(ctx context.Context, accessToken string) (userID, displayName string, err error)
}

// grpcClient is the host's handle to one connector plugin's gRPC service.
// It is what GRPCClient() dispenses; the manager keeps it alive per plugin.
type grpcClient struct {
	inner pb.ConnectorClient
	key   string // plugin key for Host API call ids ("" = none)
}

// Execute runs one operation in the plugin and returns the raw result JSON.
func (c *grpcClient) Execute(ctx context.Context, call ExecCall) (ExecResult, error) {
	req, err := call.request()
	if err != nil {
		return ExecResult{}, err
	}
	ctx, end := beginHostCallFor(ctx, c.key, KindConnector, call.CallerUserID, call.SessionID, call.InstanceID)
	defer end()
	resp, err := c.inner.Execute(ctx, req)
	if err != nil {
		return ExecResult{}, fmt.Errorf("plugin transport: %w", err)
	}
	if resp.Error != nil {
		return ExecResult{Mask: resp.Mask, MaskIgnoreCase: resp.MaskIgnoreCase},
			fmt.Errorf("%w: [%s] %s", ErrPluginOp, resp.Error.Code, resp.Error.Message)
	}
	return ExecResult{JSON: resp.ResultJson, Mask: resp.Mask, MaskIgnoreCase: resp.MaskIgnoreCase}, nil
}

// ExecuteStream runs one operation and reassembles the streamed result. It
// removes the default gRPC message ceiling for large results.
func (c *grpcClient) ExecuteStream(ctx context.Context, call ExecCall) (ExecResult, error) {
	req, err := call.request()
	if err != nil {
		return ExecResult{}, err
	}
	ctx, end := beginHostCallFor(ctx, c.key, KindConnector, call.CallerUserID, call.SessionID, call.InstanceID)
	defer end()
	stream, err := c.inner.ExecuteStream(ctx, req)
	if err != nil {
		return ExecResult{}, fmt.Errorf("plugin transport: %w", err)
	}
	var out []byte
	for {
		chunk, err := stream.Recv()
		if err != nil {
			return ExecResult{}, fmt.Errorf("plugin transport: %w", err)
		}
		if chunk.Error != nil {
			return ExecResult{Mask: chunk.Mask, MaskIgnoreCase: chunk.MaskIgnoreCase},
				fmt.Errorf("%w: [%s] %s", ErrPluginOp, chunk.Error.Code, chunk.Error.Message)
		}
		if len(chunk.Data) > 0 {
			out = append(out, chunk.Data...)
		} else if !chunk.Eof && call.Progress != nil && (chunk.Progress != 0 || chunk.Total != 0 || chunk.Message != "") {
			call.Progress(int(chunk.Progress), int(chunk.Total), chunk.Message)
		}
		if chunk.Eof {
			return ExecResult{JSON: out, Mask: chunk.Mask, MaskIgnoreCase: chunk.MaskIgnoreCase}, nil
		}
	}
}

// request builds the wire request for call.
func (call ExecCall) request() (*pb.ExecuteRequest, error) {
	args, err := json.Marshal(call.Input)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}
	var raw []byte
	if call.RawInput != nil {
		if raw, err = json.Marshal(call.RawInput); err != nil {
			return nil, fmt.Errorf("marshal raw input: %w", err)
		}
	}
	return &pb.ExecuteRequest{
		Operation:    call.Operation,
		ArgsJson:     args,
		Creds:        call.Creds,
		RequestId:    call.RequestID,
		SessionId:    call.SessionID,
		InstanceId:   call.InstanceID,
		CallerUserId: call.CallerUserID,
		AccountId:    call.AccountID,
		OwnerBotId:   call.OwnerBotID,
		RawInputJson: raw,
		HealthCheck:  call.HealthCheck,
	}, nil
}

// ResolveIdentity asks the plugin who an OAuth access token belongs to.
func (c *grpcClient) ResolveIdentity(ctx context.Context, accessToken string) (userID, displayName string, err error) {
	resp, err := c.inner.ResolveIdentity(ctx, &pb.IdentityRequest{AccessToken: accessToken})
	if err != nil {
		return "", "", fmt.Errorf("plugin transport: %w", err)
	}
	if resp.Error != nil {
		return "", "", fmt.Errorf("%w: [%s] %s", ErrPluginOp, resp.Error.Code, resp.Error.Message)
	}
	return resp.UserId, resp.DisplayName, nil
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

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/yogasw/wick/pkg/connector"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// streamChunkSize bounds each ExecuteStream Chunk so large results clear the
// default gRPC message ceiling.
const streamChunkSize = 1 << 20 // 1 MiB

// grpcServer adapts a connector.Module to the generated proto service. It
// runs INSIDE the plugin subprocess; the host never sees it.
type grpcServer struct {
	pb.UnimplementedConnectorServer
	mod       connector.Module
	ops       map[string]connector.Operation
	schema    []byte
	schemaErr error
}

// NewServer builds the plugin-side service for one connector module.
func NewServer(mod connector.Module) pb.ConnectorServer {
	ops := make(map[string]connector.Operation)
	for _, op := range mod.AllOps() {
		ops[op.Key] = op
	}
	b, err := json.Marshal(manifestModule(mod))
	return &grpcServer{mod: mod, ops: ops, schema: b, schemaErr: err}
}

func (s *grpcServer) Schema(_ context.Context, _ *pb.SchemaRequest) (*pb.SchemaResponse, error) {
	if s.schemaErr != nil {
		return nil, fmt.Errorf("marshal manifest: %w", s.schemaErr)
	}
	return &pb.SchemaResponse{ManifestJson: s.schema}, nil
}

// Execute is the unary path: it has no channel back to the host before the
// result, so c.ReportProgress calls are dropped (ExecuteStream relays them).
func (s *grpcServer) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	result, masks, opErr := s.runOp(ctx, req, nil)
	if opErr != nil {
		return &pb.ExecuteResponse{Error: opErr, Mask: masks.exact, MaskIgnoreCase: masks.folded}, nil
	}
	return &pb.ExecuteResponse{ResultJson: result, Mask: masks.exact, MaskIgnoreCase: masks.folded}, nil
}

// runOp executes one operation and returns the marshalled result, or a
// proto Error. Shared by Execute and ExecuteStream so the two cannot drift.
func (s *grpcServer) runOp(ctx context.Context, req *pb.ExecuteRequest, progress connector.ProgressReporter) ([]byte, *maskRecorder, *pb.Error) {
	masks := &maskRecorder{}
	if req.GetHealthCheck() {
		return s.runHealthCheck(ctx, req, masks)
	}
	op, ok := s.ops[req.Operation]
	if !ok {
		return nil, masks, &pb.Error{Code: "unknown_operation", Message: req.Operation}
	}
	if op.Execute == nil {
		return nil, masks, &pb.Error{Code: "no_handler", Message: req.Operation}
	}
	var input map[string]string
	if len(req.ArgsJson) > 0 {
		if err := json.Unmarshal(req.ArgsJson, &input); err != nil {
			return nil, masks, &pb.Error{Code: "bad_args", Message: err.Error()}
		}
	}
	cctx := connector.NewCtx(ctx, req.GetInstanceId(), req.Creds, input, nil, progress, masks)
	if raw := req.GetRawInputJson(); len(raw) > 0 {
		var rawInput map[string]any
		if err := json.Unmarshal(raw, &rawInput); err != nil {
			return nil, masks, &pb.Error{Code: "bad_args", Message: "raw input: " + err.Error()}
		}
		cctx.SetRawInput(rawInput)
	}
	cctx.SetRequestID(req.GetRequestId())
	cctx.SetUserNameResolver(hostUserName(ctx))
	if uid := req.GetCallerUserId(); uid != "" {
		cctx.SetCallerUserID(uid)
	}
	cctx.SetSessionID(req.GetSessionId())
	cctx.SetAccountID(req.GetAccountId())
	cctx.SetOwnerBotID(req.GetOwnerBotId())
	cctx.SetAccountsLister(hostAccounts(ctx, req.GetInstanceId()))
	value, execErr := op.Execute(cctx)
	if execErr != nil {
		return nil, masks, &pb.Error{Code: "exec_error", Message: execErr.Error()}
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, masks, &pb.Error{Code: "marshal_error", Message: err.Error()}
	}
	return b, masks, nil
}

// runHealthCheck runs the module's HealthCheck hook against the request's
// creds and returns the []connector.OpHealth report as the result JSON.
func (s *grpcServer) runHealthCheck(ctx context.Context, req *pb.ExecuteRequest, masks *maskRecorder) ([]byte, *maskRecorder, *pb.Error) {
	if s.mod.HealthCheck == nil {
		return nil, masks, &pb.Error{Code: CodeNoHealthCheck, Message: "connector has no health check"}
	}
	cctx := connector.NewCtx(ctx, req.GetInstanceId(), req.Creds, nil, nil, nil, masks)
	report, err := s.mod.HealthCheck(cctx)
	if err != nil {
		return nil, masks, &pb.Error{Code: "exec_error", Message: err.Error()}
	}
	b, err := json.Marshal(report)
	if err != nil {
		return nil, masks, &pb.Error{Code: "marshal_error", Message: err.Error()}
	}
	return b, masks, nil
}

// CodeNoHealthCheck is the Error code a plugin answers a health_check
// request with when its module registers no HealthCheck hook.
const CodeNoHealthCheck = "no_health_check"

// manifestModule is mod as it goes into the manifest: HasHealthCheck tells
// the host a HealthCheck hook exists, since the func itself cannot travel.
func manifestModule(mod connector.Module) connector.Module {
	mod.HasHealthCheck = mod.HealthCheck != nil
	return mod
}

// streamProgress relays c.ReportProgress to the host as progress chunks.
// Ops may report from several goroutines; a stream allows one Send at a time.
type streamProgress struct {
	mu     sync.Mutex
	stream pb.Connector_ExecuteStreamServer
}

func (p *streamProgress) Report(progress, total int, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.stream.Send(&pb.Chunk{Progress: int32(progress), Total: int32(total), Message: message})
}

// maskRecorder is the plugin-side Masker. The plugin holds no encryption
// key, so c.Mask / c.MaskIgnoreCase cannot tokenize here: the recorder
// keeps the values, leaves the data as it is, and the values travel back
// with the result for the host to mask before anything leaves wick.
type maskRecorder struct {
	mu     sync.Mutex
	exact  []string
	folded []string
}

func (m *maskRecorder) Mask(data string, values []string, caseInsensitive bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range values {
		if v == "" {
			continue
		}
		if caseInsensitive {
			m.folded = append(m.folded, v)
		} else {
			m.exact = append(m.exact, v)
		}
	}
	return data
}

// ExecuteStream runs the operation and streams the result in bounded chunks,
// then a final Eof chunk. Op errors are delivered as a single Chunk{Error}.
func (s *grpcServer) ExecuteStream(req *pb.ExecuteRequest, stream pb.Connector_ExecuteStreamServer) error {
	progress := &streamProgress{stream: stream}
	result, masks, opErr := s.runOp(stream.Context(), req, progress)
	// Hold the lock for the rest of the call: a straggling progress report
	// from an op goroutine must not interleave with the result chunks.
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if opErr != nil {
		return stream.Send(&pb.Chunk{Error: opErr, Mask: masks.exact, MaskIgnoreCase: masks.folded})
	}
	for off := 0; off < len(result); off += streamChunkSize {
		end := off + streamChunkSize
		if end > len(result) {
			end = len(result)
		}
		if err := stream.Send(&pb.Chunk{Data: result[off:end]}); err != nil {
			return err
		}
	}
	return stream.Send(&pb.Chunk{Eof: true, Mask: masks.exact, MaskIgnoreCase: masks.folded})
}

// ResolveIdentity resolves which provider user an OAuth access token belongs
// to by calling the module's OAuth.GetUserIdentity. The host invokes this
// across the process boundary because GetUserIdentity (a func) cannot be
// marshalled in the manifest.
func (s *grpcServer) ResolveIdentity(ctx context.Context, req *pb.IdentityRequest) (*pb.IdentityResponse, error) {
	if s.mod.OAuth == nil || s.mod.OAuth.GetUserIdentity == nil {
		return &pb.IdentityResponse{Error: &pb.Error{Code: "no_oauth", Message: "connector has no OAuth identity resolver"}}, nil
	}
	uid, name, err := s.mod.OAuth.GetUserIdentity(ctx, req.AccessToken)
	if err != nil {
		return &pb.IdentityResponse{Error: &pb.Error{Code: "identity_error", Message: err.Error()}}, nil
	}
	return &pb.IdentityResponse{UserId: uid, DisplayName: name}, nil
}

func (s *grpcServer) Health(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Healthy: true}, nil
}

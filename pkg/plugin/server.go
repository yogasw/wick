package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yogasw/wick/pkg/connector"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// grpcServer adapts a connector.Module to the generated proto service. It
// runs INSIDE the plugin subprocess; the host never sees it.
type grpcServer struct {
	pb.UnimplementedConnectorServer
	mod connector.Module
	ops map[string]connector.Operation
}

// NewServer builds the plugin-side service for one connector module.
func NewServer(mod connector.Module) pb.ConnectorServer {
	ops := make(map[string]connector.Operation)
	for _, op := range mod.AllOps() {
		ops[op.Key] = op
	}
	return &grpcServer{mod: mod, ops: ops}
}

func (s *grpcServer) Schema(_ context.Context, _ *pb.SchemaRequest) (*pb.SchemaResponse, error) {
	b, err := json.Marshal(s.mod)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return &pb.SchemaResponse{ManifestJson: b}, nil
}

func (s *grpcServer) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	op, ok := s.ops[req.Operation]
	if !ok {
		return &pb.ExecuteResponse{Error: &pb.Error{Code: "unknown_operation", Message: req.Operation}}, nil
	}
	var input map[string]string
	if len(req.ArgsJson) > 0 {
		if err := json.Unmarshal(req.ArgsJson, &input); err != nil {
			return &pb.ExecuteResponse{Error: &pb.Error{Code: "bad_args", Message: err.Error()}}, nil
		}
	}
	cctx := connector.NewPluginCtx(ctx, req.Creds, input)
	value, execErr := op.Execute(cctx)
	if execErr != nil {
		return &pb.ExecuteResponse{Error: &pb.Error{Code: "exec_error", Message: execErr.Error()}}, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return &pb.ExecuteResponse{Error: &pb.Error{Code: "marshal_error", Message: err.Error()}}, nil
	}
	return &pb.ExecuteResponse{ResultJson: b}, nil
}

func (s *grpcServer) Health(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Healthy: true}, nil
}

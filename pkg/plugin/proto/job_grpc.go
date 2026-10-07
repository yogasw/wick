// Hand-written gRPC bindings for job.proto. job.proto declares no messages of
// its own (it reuses connector.proto's), so the only generated code would be
// these bindings; they follow protoc-gen-go-grpc's layout so a later
// `go generate` (with job.proto added to gen.go) can replace this file 1:1.

package proto

import (
	"context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

const (
	Job_Schema_FullMethodName = "/wick.job.v1.Job/Schema"
	Job_Run_FullMethodName    = "/wick.job.v1.Job/Run"
	Job_Health_FullMethodName = "/wick.job.v1.Job/Health"
)

// JobClient is the client API for Job service.
type JobClient interface {
	Schema(ctx context.Context, in *SchemaRequest, opts ...grpc.CallOption) (*SchemaResponse, error)
	Run(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[Chunk], error)
	Health(ctx context.Context, in *HealthRequest, opts ...grpc.CallOption) (*HealthResponse, error)
}

type jobClient struct {
	cc grpc.ClientConnInterface
}

func NewJobClient(cc grpc.ClientConnInterface) JobClient {
	return &jobClient{cc}
}

func (c *jobClient) Schema(ctx context.Context, in *SchemaRequest, opts ...grpc.CallOption) (*SchemaResponse, error) {
	out := new(SchemaResponse)
	if err := c.cc.Invoke(ctx, Job_Schema_FullMethodName, in, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *jobClient) Run(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[Chunk], error) {
	stream, err := c.cc.NewStream(ctx, &Job_ServiceDesc.Streams[0], Job_Run_FullMethodName, opts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[ExecuteRequest, Chunk]{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

func (c *jobClient) Health(ctx context.Context, in *HealthRequest, opts ...grpc.CallOption) (*HealthResponse, error) {
	out := new(HealthResponse)
	if err := c.cc.Invoke(ctx, Job_Health_FullMethodName, in, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

// JobServer is the server API for Job service. Implementations must embed
// UnimplementedJobServer for forward compatibility.
type JobServer interface {
	Schema(context.Context, *SchemaRequest) (*SchemaResponse, error)
	Run(*ExecuteRequest, grpc.ServerStreamingServer[Chunk]) error
	Health(context.Context, *HealthRequest) (*HealthResponse, error)
	mustEmbedUnimplementedJobServer()
}

// UnimplementedJobServer must be embedded to have forward compatible implementations.
type UnimplementedJobServer struct{}

func (UnimplementedJobServer) Schema(context.Context, *SchemaRequest) (*SchemaResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Schema not implemented")
}
func (UnimplementedJobServer) Run(*ExecuteRequest, grpc.ServerStreamingServer[Chunk]) error {
	return status.Error(codes.Unimplemented, "method Run not implemented")
}
func (UnimplementedJobServer) Health(context.Context, *HealthRequest) (*HealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Health not implemented")
}
func (UnimplementedJobServer) mustEmbedUnimplementedJobServer() {}

func RegisterJobServer(s grpc.ServiceRegistrar, srv JobServer) {
	s.RegisterService(&Job_ServiceDesc, srv)
}

func _Job_Schema_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SchemaRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobServer).Schema(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Job_Schema_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobServer).Schema(ctx, req.(*SchemaRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Job_Run_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(ExecuteRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(JobServer).Run(m, &grpc.GenericServerStream[ExecuteRequest, Chunk]{ServerStream: stream})
}

func _Job_Health_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(HealthRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobServer).Health(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Job_Health_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobServer).Health(ctx, req.(*HealthRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Job_ServiceDesc is the grpc.ServiceDesc for Job service.
var Job_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "wick.job.v1.Job",
	HandlerType: (*JobServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Schema", Handler: _Job_Schema_Handler},
		{MethodName: "Health", Handler: _Job_Health_Handler},
	},
	Streams: []grpc.StreamDesc{
		{StreamName: "Run", Handler: _Job_Run_Handler, ServerStreams: true},
	},
	Metadata: "job.proto",
}

// Hand-written gRPC bindings for tool.proto. tool.proto declares no messages
// of its own (it reuses connector.proto's), so the only generated code would
// be these bindings; they follow protoc-gen-go-grpc's layout so a later
// `go generate` (with tool.proto added to gen.go) can replace this file 1:1.

package proto

import (
	"context"

	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

const (
	Tool_Schema_FullMethodName    = "/wick.tool.v1.Tool/Schema"
	Tool_Configure_FullMethodName = "/wick.tool.v1.Tool/Configure"
	Tool_Health_FullMethodName    = "/wick.tool.v1.Tool/Health"
)

// ToolClient is the client API for Tool service.
type ToolClient interface {
	Schema(ctx context.Context, in *SchemaRequest, opts ...grpc.CallOption) (*SchemaResponse, error)
	Configure(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (*HealthResponse, error)
	Health(ctx context.Context, in *HealthRequest, opts ...grpc.CallOption) (*HealthResponse, error)
}

type toolClient struct {
	cc grpc.ClientConnInterface
}

func NewToolClient(cc grpc.ClientConnInterface) ToolClient {
	return &toolClient{cc}
}

func (c *toolClient) Schema(ctx context.Context, in *SchemaRequest, opts ...grpc.CallOption) (*SchemaResponse, error) {
	out := new(SchemaResponse)
	if err := c.cc.Invoke(ctx, Tool_Schema_FullMethodName, in, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *toolClient) Configure(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (*HealthResponse, error) {
	out := new(HealthResponse)
	if err := c.cc.Invoke(ctx, Tool_Configure_FullMethodName, in, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *toolClient) Health(ctx context.Context, in *HealthRequest, opts ...grpc.CallOption) (*HealthResponse, error) {
	out := new(HealthResponse)
	if err := c.cc.Invoke(ctx, Tool_Health_FullMethodName, in, out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}

// ToolServer is the server API for Tool service. Implementations must embed
// UnimplementedToolServer for forward compatibility.
type ToolServer interface {
	Schema(context.Context, *SchemaRequest) (*SchemaResponse, error)
	Configure(context.Context, *ExecuteRequest) (*HealthResponse, error)
	Health(context.Context, *HealthRequest) (*HealthResponse, error)
	mustEmbedUnimplementedToolServer()
}

// UnimplementedToolServer must be embedded to have forward compatible implementations.
type UnimplementedToolServer struct{}

func (UnimplementedToolServer) Schema(context.Context, *SchemaRequest) (*SchemaResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Schema not implemented")
}
func (UnimplementedToolServer) Configure(context.Context, *ExecuteRequest) (*HealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Configure not implemented")
}
func (UnimplementedToolServer) Health(context.Context, *HealthRequest) (*HealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method Health not implemented")
}
func (UnimplementedToolServer) mustEmbedUnimplementedToolServer() {}

func RegisterToolServer(s grpc.ServiceRegistrar, srv ToolServer) {
	s.RegisterService(&Tool_ServiceDesc, srv)
}

func _Tool_Schema_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SchemaRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ToolServer).Schema(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Tool_Schema_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ToolServer).Schema(ctx, req.(*SchemaRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Tool_Configure_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ExecuteRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ToolServer).Configure(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Tool_Configure_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ToolServer).Configure(ctx, req.(*ExecuteRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Tool_Health_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(HealthRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ToolServer).Health(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Tool_Health_FullMethodName}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ToolServer).Health(ctx, req.(*HealthRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Tool_ServiceDesc is the grpc.ServiceDesc for Tool service.
var Tool_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "wick.tool.v1.Tool",
	HandlerType: (*ToolServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Schema", Handler: _Tool_Schema_Handler},
		{MethodName: "Configure", Handler: _Tool_Configure_Handler},
		{MethodName: "Health", Handler: _Tool_Health_Handler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "tool.proto",
}

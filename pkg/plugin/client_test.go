package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
	"github.com/yogasw/wick/pkg/wickdocs"
	"google.golang.org/grpc"
)

// fakeStreamClient implements grpc.ServerStreamingClient[pb.Chunk].
type fakeStreamClient struct {
	grpc.ClientStream
	chunks []*pb.Chunk
	i      int
}

func (f *fakeStreamClient) Recv() (*pb.Chunk, error) {
	if f.i >= len(f.chunks) {
		return nil, context.Canceled // not reached: EOF chunk ends the loop
	}
	c := f.chunks[f.i]
	f.i++
	return c, nil
}

type fakeConnClient struct {
	pb.ConnectorClient
	lastReq *pb.ExecuteRequest
	resp    *pb.ExecuteResponse
	stream  *fakeStreamClient
}

func (f *fakeConnClient) Execute(_ context.Context, in *pb.ExecuteRequest, _ ...grpc.CallOption) (*pb.ExecuteResponse, error) {
	f.lastReq = in
	return f.resp, nil
}

func (f *fakeConnClient) ExecuteStream(_ context.Context, _ *pb.ExecuteRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.Chunk], error) {
	return f.stream, nil
}

func TestClientExecuteMapsArgsAndCreds(t *testing.T) {
	res, _ := json.Marshal(map[string]string{"ok": "yes"})
	fc := &fakeConnClient{resp: &pb.ExecuteResponse{ResultJson: res}}
	cl := &grpcClient{inner: fc}
	out, err := cl.Execute(context.Background(), ExecCall{
		Operation: "say",
		Input:     map[string]string{"text": "hi"},
		Creds:     map[string]string{"token": "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fc.lastReq.Operation != "say" || fc.lastReq.Creds["token"] != "abc" {
		t.Fatalf("request not mapped: %+v", fc.lastReq)
	}
	var got map[string]string
	if err := json.Unmarshal(out.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["ok"] != "yes" {
		t.Fatalf("result not returned: %v", got)
	}
}

func TestClientExecutePropagatesOpError(t *testing.T) {
	fc := &fakeConnClient{resp: &pb.ExecuteResponse{Error: &pb.Error{Code: "exec_error", Message: "boom"}}}
	cl := &grpcClient{inner: fc}
	_, err := cl.Execute(context.Background(), ExecCall{Operation: "say"})
	if err == nil || !errors.Is(err, ErrPluginOp) {
		t.Fatalf("expected ErrPluginOp, got %v", err)
	}
}

func TestClientExecuteStreamReassembles(t *testing.T) {
	big := strings.Repeat("y", 1500)
	stream := &fakeStreamClient{chunks: []*pb.Chunk{
		{Data: []byte(big[:1000])},
		{Data: []byte(big[1000:])},
		{Eof: true, Mask: []string{"a"}, MaskIgnoreCase: []string{"b"}},
	}}
	c := &grpcClient{inner: &fakeConnClient{stream: stream}}
	out, err := c.ExecuteStream(context.Background(), ExecCall{Operation: "say"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.JSON) != big {
		t.Fatalf("reassembled %d bytes, want %d", len(out.JSON), len(big))
	}
	if len(out.Mask) != 1 || out.Mask[0] != "a" || len(out.MaskIgnoreCase) != 1 || out.MaskIgnoreCase[0] != "b" {
		t.Fatalf("mask values from the Eof chunk not returned: %+v %+v", out.Mask, out.MaskIgnoreCase)
	}
}

func TestClientExecuteStreamSurfacesError(t *testing.T) {
	stream := &fakeStreamClient{chunks: []*pb.Chunk{
		{Error: &pb.Error{Code: "exec_error", Message: "boom"}},
	}}
	c := &grpcClient{inner: &fakeConnClient{stream: stream}}
	if _, err := c.ExecuteStream(context.Background(), ExecCall{Operation: "say"}); err == nil {
		t.Fatal("Chunk.Error must surface as an error")
	}
}

// bridgeConnClient forwards Execute straight to an in-process server, so a
// test covers the full host ExecCall → ExecuteRequest → plugin Ctx path.
type bridgeConnClient struct {
	pb.ConnectorClient
	srv     pb.ConnectorServer
	lastReq *pb.ExecuteRequest
}

func (b *bridgeConnClient) Execute(ctx context.Context, in *pb.ExecuteRequest, _ ...grpc.CallOption) (*pb.ExecuteResponse, error) {
	return b.srv.Execute(ctx, in)
}

func (b *bridgeConnClient) ExecuteStream(_ context.Context, in *pb.ExecuteRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.Chunk], error) {
	b.lastReq = in
	return &fakeStreamClient{chunks: []*pb.Chunk{{Data: []byte(`{}`), Eof: true}}}, nil
}

func identityModule() connector.Module {
	who := func(c *connector.Ctx) (any, error) {
		return map[string]string{"instance": c.InstanceID(), "caller": c.CallerUserID()}, nil
	}
	return connector.Module{
		Meta:    connector.Meta{Key: "who", Name: "Who"},
		Configs: entity.StructToConfigs(struct{}{}),
		Operations: []connector.Category{
			connector.Cat("Main", "",
				connector.Op("who", "Who", "reports instance and caller",
					struct{}{}, who, wickdocs.Docs{})),
		},
	}
}

func TestClientExecuteCarriesInstanceAndCallerToOp(t *testing.T) {
	cl := &grpcClient{inner: &bridgeConnClient{srv: NewServer(identityModule())}}
	out, err := cl.Execute(context.Background(), ExecCall{
		Operation:    "who",
		InstanceID:   "inst-1",
		CallerUserID: "user-9",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["instance"] != "inst-1" || got["caller"] != "user-9" {
		t.Fatalf("op saw instance=%q caller=%q", got["instance"], got["caller"])
	}
}

func TestClientExecuteWithoutInstanceLeavesCtxEmpty(t *testing.T) {
	cl := &grpcClient{inner: &bridgeConnClient{srv: NewServer(identityModule())}}
	out, err := cl.Execute(context.Background(), ExecCall{Operation: "who"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.JSON) != `{"caller":"","instance":""}` {
		t.Fatalf("unexpected result %s", out.JSON)
	}
}

func TestClientExecuteStreamSendsInstanceAndCaller(t *testing.T) {
	bc := &bridgeConnClient{}
	cl := &grpcClient{inner: bc}
	if _, err := cl.ExecuteStream(context.Background(), ExecCall{
		Operation:    "who",
		InstanceID:   "inst-1",
		CallerUserID: "user-9",
	}); err != nil {
		t.Fatal(err)
	}
	if bc.lastReq.GetInstanceId() != "inst-1" || bc.lastReq.GetCallerUserId() != "user-9" {
		t.Fatalf("request not mapped: %+v", bc.lastReq)
	}
}

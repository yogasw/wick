package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	pb "github.com/yogasw/wick/pkg/plugin/proto"
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
	if err := json.Unmarshal(out, &got); err != nil {
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
		{Eof: true},
	}}
	c := &grpcClient{inner: &fakeConnClient{stream: stream}}
	out, err := c.ExecuteStream(context.Background(), ExecCall{Operation: "say"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != big {
		t.Fatalf("reassembled %d bytes, want %d", len(out), len(big))
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

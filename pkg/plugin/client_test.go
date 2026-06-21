package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pb "github.com/yogasw/wick/pkg/plugin/proto"
	"google.golang.org/grpc"
)

type fakeConnClient struct {
	pb.ConnectorClient
	lastReq *pb.ExecuteRequest
	resp    *pb.ExecuteResponse
}

func (f *fakeConnClient) Execute(_ context.Context, in *pb.ExecuteRequest, _ ...grpc.CallOption) (*pb.ExecuteResponse, error) {
	f.lastReq = in
	return f.resp, nil
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

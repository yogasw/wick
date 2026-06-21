package plugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type fakeConn struct {
	lastCall wickplugin.ExecCall
	streamed bool
}

func (f *fakeConn) Execute(_ context.Context, call wickplugin.ExecCall) ([]byte, error) {
	f.lastCall = call
	return json.Marshal(map[string]string{"echo": call.Input["text"]})
}
func (f *fakeConn) ExecuteStream(ctx context.Context, call wickplugin.ExecCall) ([]byte, error) {
	f.streamed = true
	return f.Execute(ctx, call)
}
func (f *fakeConn) Schema(context.Context) ([]byte, error) { return nil, nil }
func (f *fakeConn) ResolveIdentity(context.Context, string) (string, string, error) {
	return "", "", nil
}

func manifestJSON(t *testing.T) []byte {
	t.Helper()
	mod := connector.Module{
		Meta: connector.Meta{Key: "demo", Name: "Demo"},
		Operations: []connector.Category{
			{Title: "Main", Ops: []connector.Operation{
				{Key: "say", Name: "Say", Description: "echo"},
			}},
		},
	}
	b, _ := json.Marshal(mod)
	return b
}

func TestAdapterBuildsModuleThatDispatchesOverGRPC(t *testing.T) {
	fc := &fakeConn{}
	getConn := func(key string) (*Lease, error) { return &Lease{Conn: fc}, nil }

	var mod connector.Module
	if err := json.Unmarshal(manifestJSON(t), &mod); err != nil {
		t.Fatal(err)
	}
	built := BuildModule(mod, getConn)
	if built.Meta.Key != "demo" {
		t.Fatalf("meta not parsed: %+v", built.Meta)
	}
	ops := built.AllOps()
	if len(ops) != 1 || ops[0].Key != "say" {
		t.Fatalf("ops not parsed: %+v", ops)
	}

	cctx := connector.NewPluginCtx(context.Background(), nil, map[string]string{"text": "hi"})
	out, err := ops[0].Execute(cctx)
	if err != nil {
		t.Fatal(err)
	}
	if fc.lastCall.Operation != "say" || fc.lastCall.Input["text"] != "hi" {
		t.Fatalf("closure did not forward call: %+v", fc.lastCall)
	}
	got := out.(json.RawMessage)
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["echo"] != "hi" {
		t.Fatalf("result not returned: %v", m)
	}
}

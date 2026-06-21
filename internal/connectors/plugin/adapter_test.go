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
}

func (f *fakeConn) Execute(_ context.Context, call wickplugin.ExecCall) ([]byte, error) {
	f.lastCall = call
	return json.Marshal(map[string]string{"echo": call.Input["text"]})
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
	getConn := func(key string) (wickplugin.GRPCConn, error) { return fc, nil }

	mod, err := BuildModule(manifestJSON(t), getConn)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Meta.Key != "demo" {
		t.Fatalf("meta not parsed: %+v", mod.Meta)
	}
	ops := mod.AllOps()
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

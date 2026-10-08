package plugin

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type fakeConn struct {
	lastCall       wickplugin.ExecCall
	streamed       bool
	mask           []string
	maskIgnoreCase []string
}

func (f *fakeConn) Execute(_ context.Context, call wickplugin.ExecCall) (wickplugin.ExecResult, error) {
	f.lastCall = call
	b, err := json.Marshal(map[string]string{"echo": call.Input["text"]})
	return wickplugin.ExecResult{JSON: b, Mask: f.mask, MaskIgnoreCase: f.maskIgnoreCase}, err
}
func (f *fakeConn) ExecuteStream(ctx context.Context, call wickplugin.ExecCall) (wickplugin.ExecResult, error) {
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
	if !fc.streamed {
		t.Fatal("adapter should dispatch via ExecuteStream")
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

// recMasker stands in for the host masker: it swaps every value for a
// marker so the test can see which values were masked and how.
type recMasker struct{}

func (recMasker) Mask(data string, values []string, caseInsensitive bool) string {
	for _, v := range values {
		if caseInsensitive {
			data = regexp.MustCompile("(?i)"+regexp.QuoteMeta(v)).ReplaceAllLiteralString(data, "<enci:"+v+">")
			continue
		}
		data = strings.ReplaceAll(data, v, "<enc:"+v+">")
	}
	return data
}

func TestAdapterMasksValuesThePluginReported(t *testing.T) {
	fc := &fakeConn{mask: []string{`s"cr\et`}, maskIgnoreCase: []string{"word"}}
	getConn := func(key string) (*Lease, error) { return &Lease{Conn: fc}, nil }
	var mod connector.Module
	if err := json.Unmarshal(manifestJSON(t), &mod); err != nil {
		t.Fatal(err)
	}
	op := BuildModule(mod, getConn).AllOps()[0]

	cctx := connector.NewCtx(context.Background(), "", nil, map[string]string{"text": `key s"cr\et WORD`}, nil, nil, recMasker{})
	out, err := op.Execute(cctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(out.(json.RawMessage), &m); err != nil {
		t.Fatalf("result is not valid JSON after masking: %v", err)
	}
	want := `key <enc:s"cr\et> <enci:word>`
	if m["echo"] != want {
		t.Fatalf("echo = %q, want %q", m["echo"], want)
	}
}

func TestAdapterLeavesResultAloneWithoutMaskValues(t *testing.T) {
	fc := &fakeConn{}
	getConn := func(key string) (*Lease, error) { return &Lease{Conn: fc}, nil }
	var mod connector.Module
	if err := json.Unmarshal(manifestJSON(t), &mod); err != nil {
		t.Fatal(err)
	}
	op := BuildModule(mod, getConn).AllOps()[0]
	cctx := connector.NewCtx(context.Background(), "", nil, map[string]string{"text": "plain"}, nil, nil, recMasker{})
	out, err := op.Execute(cctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(out.(json.RawMessage)) != `{"echo":"plain"}` {
		t.Fatalf("unexpected result %s", out)
	}
}

func TestAdapterForwardsInstanceAndCaller(t *testing.T) {
	fc := &fakeConn{}
	getConn := func(key string) (*Lease, error) { return &Lease{Conn: fc}, nil }
	var mod connector.Module
	if err := json.Unmarshal(manifestJSON(t), &mod); err != nil {
		t.Fatal(err)
	}
	op := BuildModule(mod, getConn).AllOps()[0]
	cctx := connector.NewCtx(context.Background(), "inst-1", nil, map[string]string{"text": "hi"}, nil, nil, recMasker{})
	cctx.SetCallerUserID("user-9")
	if _, err := op.Execute(cctx); err != nil {
		t.Fatal(err)
	}
	if fc.lastCall.InstanceID != "inst-1" || fc.lastCall.CallerUserID != "user-9" {
		t.Fatalf("closure did not forward identity: %+v", fc.lastCall)
	}
}

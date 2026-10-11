package main

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// fakeHost answers the two RPCs whoami_host uses; any other panics.
type fakeHost struct {
	host.Host
	gotID string
}

func (f *fakeHost) Whoami(context.Context) (*hostpb.WhoamiResponse, error) {
	return &hostpb.WhoamiResponse{PluginKey: "httpbin", ScopesAllowed: []string{"users.read"}, HostApiVersion: 1}, nil
}

func (f *fakeHost) GetUser(_ context.Context, in *hostpb.GetUserRequest) (*hostpb.User, error) {
	f.gotID = in.GetId()
	return &hostpb.User{Id: in.GetId(), Name: "Ada", Email: "ada@example.com"}, nil
}

func withHost(t *testing.T, h host.Host, err error) {
	t.Helper()
	old := hostFor
	hostFor = func(context.Context) (host.Host, error) { return h, err }
	t.Cleanup(func() { hostFor = old })
}

func TestWhoamiHost(t *testing.T) {
	f := &fakeHost{}
	withHost(t, f, nil)
	c := connector.NewPluginCtx(context.Background(), nil, nil)
	c.SetCallerUserID("u1")
	got, err := doWhoamiHost(c)
	if err != nil {
		t.Fatal(err)
	}
	r := got.(whoamiHostResult)
	if !r.Available || r.Plugin != "httpbin" || r.CallerName != "Ada" || r.CallerEmail != "ada@example.com" || f.gotID != "u1" {
		t.Fatalf("result = %+v (GetUser id %q)", r, f.gotID)
	}
}

func TestWhoamiHostOlderWick(t *testing.T) {
	withHost(t, nil, host.ErrHostAPIUnavailable)
	got, err := doWhoamiHost(connector.NewPluginCtx(context.Background(), nil, nil))
	if err != nil || got.(whoamiHostResult).Available {
		t.Fatalf("older wick: %+v %v", got, err)
	}
	withHost(t, nil, errors.New("boom"))
	if _, err := doWhoamiHost(connector.NewPluginCtx(context.Background(), nil, nil)); err == nil {
		t.Fatal("other errors must surface")
	}
}

// TestHostScopesInManifest: what main declares reaches the manifest
// `wick plugin build` writes.
func TestHostScopesInManifest(t *testing.T) {
	wickplugin.DeclareHostAccess(hostScopes, nil)
	t.Cleanup(func() { wickplugin.DeclareHostAccess(nil, nil) })
	m, err := wickplugin.BuildSelfManifest(Module(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.HostScopes) != 1 || m.HostScopes[0] != wickplugin.ScopeHostUsersRead {
		t.Fatalf("host_scopes = %v", m.HostScopes)
	}
}

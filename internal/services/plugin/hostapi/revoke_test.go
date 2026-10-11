package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// SetRevoked is the admin's "Revoke host tokens": persistent, every kind,
// both doors, until restored.
func TestBridgeSetRevokedDoor(t *testing.T) {
	svc := &fakeServiceTokens{revoked: map[string]bool{}}
	b := NewBridge(newFixture(t).srv, svc)
	env, _ := b.ProcessEnv("sync", wickplugin.KindJob)
	tok := envValue(env, host.EnvPluginToken)
	dropped, changed, err := b.SetRevoked("sync", true)
	if err != nil || dropped != 1 || !changed || !b.Revoked("sync") {
		t.Fatalf("revoke = %d %v %v", dropped, changed, err)
	}
	if _, _, _, ok := b.authenticate(tok); ok {
		t.Fatal("token accepted after revoke")
	}
	if env, _ := b.ProcessEnv("sync", wickplugin.KindJob); envValue(env, host.EnvPluginToken) != "" {
		t.Fatal("revoked plugin got a token for its next process")
	}
	if d, c, _ := b.SetRevoked("sync", true); d != 0 || c {
		t.Fatalf("second revoke = %d %v, want nothing to do", d, c)
	}
	if _, c, err := b.SetRevoked("sync", false); err != nil || !c || b.Revoked("sync") {
		t.Fatalf("restore = %v %v", c, err)
	}
	env, _ = b.ProcessEnv("sync", wickplugin.KindJob)
	if _, _, _, ok := b.authenticate(envValue(env, host.EnvPluginToken)); !ok {
		t.Fatal("no working token after restore")
	}
	if _, _, err := NewBridge(newFixture(t).srv, nil).SetRevoked("sync", true); !errors.Is(err, ErrRevocationUnavailable) {
		t.Fatalf("no store: %v", err)
	}
}

func TestBridgeSetRevokedBroker(t *testing.T) {
	f := newFixture(t)
	svc := &fakeServiceTokens{revoked: map[string]bool{}}
	b := NewBridge(f.srv, svc)
	wickplugin.SetHostBridge(b)
	t.Cleanup(func() { wickplugin.SetHostBridge(nil) })
	t.Setenv(wickplugin.EnvHostBroker, "1")
	mod := connector.Module{Meta: connector.Meta{Key: "sync", Name: "Sync"}, Operations: []connector.Category{{Ops: []connector.Operation{
		{Key: "who", Execute: func(c *connector.Ctx) (any, error) {
			h, err := wickplugin.Host(c.Context())
			if err != nil {
				return nil, err
			}
			_, err = h.ListUsers(c.Context(), &hostpb.ListUsersRequest{})
			return map[string]string{"code": status.Code(err).String()}, nil
		}},
	}}}}
	client, _ := goplugin.TestPluginGRPCConn(t, false, map[string]goplugin.Plugin{
		wickplugin.PluginName: &wickplugin.ConnectorGRPCPlugin{Impl: wickplugin.NewServer(mod), HostKey: "sync"},
	})
	t.Cleanup(func() { _ = client.Close() })
	raw, err := client.Dispense(wickplugin.PluginName)
	if err != nil {
		t.Fatal(err)
	}
	conn := raw.(wickplugin.GRPCConn)
	code := func() string {
		t.Helper()
		res, err := conn.Execute(context.Background(), wickplugin.ExecCall{Operation: "who", CallerUserID: "u1"})
		if err != nil {
			t.Fatal(err)
		}
		var out struct{ Code string }
		_ = json.Unmarshal(res.JSON, &out)
		return out.Code
	}
	if c := code(); c != "OK" {
		t.Fatalf("before revoke: %s", c)
	}
	if _, _, err := b.SetRevoked("sync", true); err != nil {
		t.Fatal(err)
	}
	if c := code(); c != "Unauthenticated" {
		t.Fatalf("broker call after revoke: %s, want Unauthenticated", c)
	}
	if _, _, err := b.SetRevoked("sync", false); err != nil {
		t.Fatal(err)
	}
	if c := code(); c != "OK" {
		t.Fatalf("after restore: %s", c)
	}
}

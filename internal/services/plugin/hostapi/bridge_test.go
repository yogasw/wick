package hostapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

func envValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// TestBridgeBrokerEndToEnd runs a connector plugin over a real go-plugin
// connection: the op calls back into wick through the broker, and the call
// runs for the person behind the Execute.
func TestBridgeBrokerEndToEnd(t *testing.T) {
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
			users, err := h.ListUsers(c.Context(), &hostpb.ListUsersRequest{})
			if err != nil {
				return nil, err
			}
			_, terr := h.GetTicket(c.Context(), &hostpb.GetTicketRequest{Id: "T-1"})
			return map[string]any{"users": len(users.GetUsers()), "ticket_err": terr != nil, "name": c.UserName("u1")}, nil
		}},
		{Key: "plain", Execute: func(*connector.Ctx) (any, error) { return "ok", nil }},
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

	res, err := conn.Execute(context.Background(), wickplugin.ExecCall{Operation: "who", CallerUserID: "u2", SessionID: "s-1"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Users     int    `json:"users"`
		TicketErr bool   `json:"ticket_err"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		t.Fatalf("%v: %s", err, res.JSON)
	}
	if out.Users == 0 || out.TicketErr || out.Name != "Ada" {
		t.Fatalf("plugin saw %+v, want users, the ticket and UserName(u1)=Ada", out)
	}
	if f.tickets.lastUser != "u2" {
		t.Fatalf("ticket read as %q, want the Execute caller u2", f.tickets.lastUser)
	}
	// ExecuteStream carries the call id the same way.
	f.tickets.lastUser = ""
	if _, err := conn.ExecuteStream(context.Background(), wickplugin.ExecCall{Operation: "who", CallerUserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if f.tickets.lastUser != "u1" {
		t.Fatalf("stream ticket read as %q, want u1", f.tickets.lastUser)
	}
	// A plugin that never touches the Host API runs exactly as before.
	if res, err := conn.Execute(context.Background(), wickplugin.ExecCall{Operation: "plain"}); err != nil || string(res.JSON) != `"ok"` {
		t.Fatalf("plain op: %s, %v", res.JSON, err)
	}
	// Revoking callback access cuts the broker too (the process keeps
	// running; its calls are refused).
	svc.revoked["sync"] = true
	if _, err := conn.Execute(context.Background(), wickplugin.ExecCall{Operation: "who", CallerUserID: "u2"}); err == nil {
		t.Fatal("broker call succeeded after revoke")
	}
	svc.revoked["sync"] = false
	f.aud.Start(context.Background())
	f.aud.Close()
	var listed bool
	for _, c := range f.audit.snapshot() {
		if c.RPC == "ListUsers" && c.PluginKey == "sync" && c.UserID == "u2" && c.OK {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("no ListUsers audit row for sync as u2: %+v", f.audit.snapshot())
	}
	if len(b.calls) != 0 {
		t.Fatalf("call ids outlived their Execute: %d", len(b.calls))
	}
}

func TestBridgeHTTPDoor(t *testing.T) {
	f := newFixture(t)
	b := NewBridge(f.srv, nil)
	if err := b.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	env, _ := b.ProcessEnv("sync", wickplugin.KindJob)
	url, tok := envValue(env, host.EnvHostAPIURL), envValue(env, host.EnvPluginToken)
	if !strings.HasPrefix(url, "http://127.0.0.1:") || tok == "" || envValue(env, wickplugin.EnvHostBroker) != "1" {
		t.Fatalf("process env = %v", env)
	}

	get := func(path, bearer, callID string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodGet, url+path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		if callID != "" {
			req.Header.Set(host.HeaderCallID, callID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}

	// The original whoami path answers with the superset body.
	resp, body := get("/x/-/api/whoami", tok, "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"plugin":"sync"`) || !strings.Contains(body, `"host_api_version":1`) {
		t.Fatalf("whoami = %d %s", resp.StatusCode, body)
	}
	resp, _ = get("/x/-/api/whoami", "wick_hst_bogus", "")
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get(host.HeaderHostAPI) == "" {
		t.Fatalf("bad token = %d (marker %q)", resp.StatusCode, resp.Header.Get(host.HeaderHostAPI))
	}

	// A call id issued for this plugin runs the call as its person.
	id, end := b.BeginCall("sync", wickplugin.KindJob, "u2", "s-1")
	if resp, body := get("/x/-/api/tickets/T-1", tok, id); resp.StatusCode != 200 || f.tickets.lastUser != "u2" {
		t.Fatalf("ticket via call id = %d %s as %q", resp.StatusCode, body, f.tickets.lastUser)
	}
	end()
	// Another plugin's call id, or an ended one, is refused outright — never
	// downgraded to the plugin's wick_pat.
	f.tickets.lastUser = ""
	other, endOther := b.BeginCall("gen", wickplugin.KindTool, "u1", "")
	defer endOther()
	if resp, _ := get("/x/-/api/tickets/T-1", tok, other); resp.StatusCode != http.StatusUnauthorized || f.tickets.lastUser != "" {
		t.Fatalf("foreign call id: %d as %q, want 401", resp.StatusCode, f.tickets.lastUser)
	}
	if resp, _ := get("/x/-/api/whoami", tok, id); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ended call id: %d, want 401", resp.StatusCode)
	}

	// The SDK client speaks the same door.
	h, err := host.FromHTTP(url, tok, nil)
	if err != nil {
		t.Fatal(err)
	}
	who, err := h.Whoami(context.Background())
	if err != nil || who.GetPluginKey() != "sync" {
		t.Fatalf("sdk whoami = %v, %v", who, err)
	}
}

func TestBridgeTokensPerKeyCapped(t *testing.T) {
	b := NewBridge(newFixture(t).srv, nil)
	env, _ := b.ProcessEnv("sync", wickplugin.KindJob)
	first := envValue(env, host.EnvPluginToken)
	for range tokensPerKey {
		b.ProcessEnv("sync", wickplugin.KindJob)
	}
	if _, _, _, ok := b.authenticate(first); ok {
		t.Fatal("oldest token still valid after tokensPerKey newer processes")
	}
	if len(b.tokens) != tokensPerKey {
		t.Fatalf("tokens kept = %d", len(b.tokens))
	}
}

type fakeServiceTokens struct{ revoked map[string]bool }

func (f *fakeServiceTokens) LookupCallback(string) (string, []string, bool) { return "", nil, false }
func (f *fakeServiceTokens) CallbackRevoked(key string) bool                { return f.revoked[key] }
func (f *fakeServiceTokens) SetCallbackRevoked(key string, revoked bool) error {
	f.revoked[key] = revoked
	return nil
}

type toolStub struct{ pb.UnimplementedToolServer }
type jobStub struct{ pb.UnimplementedJobServer }

// Tool and job processes get the same broker door: a tool request's call id
// runs as its person, a job (no call id) runs as the plugin alone.
func TestBridgeBrokerToolAndJob(t *testing.T) {
	f := newFixture(t)
	b := NewBridge(f.srv, nil)
	wickplugin.SetHostBridge(b)
	t.Cleanup(func() { wickplugin.SetHostBridge(nil) })
	t.Setenv(wickplugin.EnvHostBroker, "1")

	tc, _ := goplugin.TestPluginGRPCConn(t, false, map[string]goplugin.Plugin{
		wickplugin.ToolPluginName: &wickplugin.ToolGRPCPlugin{Impl: toolStub{}, HostKey: "sync", HostKind: wickplugin.KindTool},
	})
	t.Cleanup(func() { _ = tc.Close() })
	if _, err := tc.Dispense(wickplugin.ToolPluginName); err != nil {
		t.Fatal(err)
	}
	id, end := b.BeginCall("sync", wickplugin.KindTool, "u2", "")
	ctx := host.WithCallID(context.Background(), id)
	h, err := wickplugin.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetTicket(ctx, &hostpb.GetTicketRequest{Id: "T-1"}); err != nil || f.tickets.lastUser != "u2" {
		t.Fatalf("tool ticket: %v as %q", err, f.tickets.lastUser)
	}
	end()
	if _, err := h.GetTicket(ctx, &hostpb.GetTicketRequest{Id: "T-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expired tool call id: %v, want Unauthenticated", err)
	}

	jc, _ := goplugin.TestPluginGRPCConn(t, false, map[string]goplugin.Plugin{
		wickplugin.JobPluginName: &wickplugin.JobGRPCPlugin{Impl: jobStub{}, HostKey: "sync"},
	})
	t.Cleanup(func() { _ = jc.Close() })
	if _, err := jc.Dispense(wickplugin.JobPluginName); err != nil {
		t.Fatal(err)
	}
	h, err = wickplugin.Host(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.ListUsers(context.Background(), nil); err != nil {
		t.Fatalf("job ListUsers: %v", err)
	}
	f.tickets.lastUser = ""
	if _, err := h.GetTicket(context.Background(), &hostpb.GetTicketRequest{Id: "T-1"}); status.Code(err) != codes.FailedPrecondition || f.tickets.lastUser != "" {
		t.Fatalf("job ticket without person or PAT: %v as %q, want FailedPrecondition", err, f.tickets.lastUser)
	}
}

func TestBridgeReleaseAndRevokeDoor(t *testing.T) {
	svc := &fakeServiceTokens{revoked: map[string]bool{}}
	b := NewBridge(newFixture(t).srv, svc)
	env, release := b.ProcessEnv("sync", wickplugin.KindJob)
	tok := envValue(env, host.EnvPluginToken)
	if _, _, _, ok := b.authenticate(tok); !ok {
		t.Fatal("fresh token rejected")
	}
	svc.revoked["sync"] = true
	if _, _, _, ok := b.authenticate(tok); ok {
		t.Fatal("token accepted after revoke")
	}
	if env, _ := b.ProcessEnv("sync", wickplugin.KindJob); envValue(env, host.EnvPluginToken) != "" {
		t.Fatal("revoked plugin got a new token")
	}
	svc.revoked["sync"] = false
	release()
	if _, _, _, ok := b.authenticate(tok); ok || len(b.tokens) != 0 || len(b.issued) != 0 {
		t.Fatalf("token survived its process: tokens=%d issued=%d", len(b.tokens), len(b.issued))
	}
	env2, _ := b.ProcessEnv("sync", wickplugin.KindJob)
	b.RevokeKey("sync")
	if _, _, _, ok := b.authenticate(envValue(env2, host.EnvPluginToken)); ok {
		t.Fatal("RevokeKey left a token")
	}
}

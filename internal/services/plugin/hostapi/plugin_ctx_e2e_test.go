package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
	"github.com/yogasw/wick/pkg/wickdocs"
)

type fakeAccounts map[string][]*hostpb.AccountRef

func (f fakeAccounts) ConnectedAccounts(_ context.Context, instanceID string) ([]*hostpb.AccountRef, error) {
	return f[instanceID], nil
}

const (
	gapArgPlain   = "arg-plain-4711"
	gapCredPlain  = "cred-plain-0815"
	gapUpstream   = "upstream-secret-9"
	gapErrorValue = "leak-in-error-77"
	gapFoldValue  = "fold.me@example.com"
)

// progressLog records the progress a run reported.
type progressLog struct {
	mu  sync.Mutex
	got []string
}

func (p *progressLog) Report(progress, total int, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, fmt.Sprintf("%d/%d %s", progress, total, message))
}

// hostCode is the gRPC code of a Host API call from inside the plugin.
func hostCode(err error) string { return status.Code(err).String() }

// gapModule is the plugin side of TestPluginCtxParity: every op reports what
// its connector.Ctx saw, as booleans and ids, so the host's own sweep cannot
// hide a plaintext that should not have arrived.
func gapModule() connector.Module {
	type In struct {
		Q string `wick:"q"`
	}
	probe := func(c *connector.Ctx) (any, error) {
		c.ReportProgress(1, 2, "half")
		accs := c.ConnectedAccounts()
		// The Host API holds ConnectedAccounts to the instance being run:
		// another instance id, or a call without the Execute's call id, is refused.
		otherCode, noCallCode := "", ""
		if h, err := wickplugin.Host(c.Context()); err == nil {
			_, err = h.ConnectedAccounts(c.Context(), &hostpb.ConnectedAccountsRequest{InstanceId: "other-instance"})
			otherCode = hostCode(err)
			_, err = h.ConnectedAccounts(context.Background(), &hostpb.ConnectedAccountsRequest{InstanceId: c.InstanceID()})
			noCallCode = hostCode(err)
		}
		rawN, _ := c.RawInputValue("n")
		first := ""
		if len(accs) > 0 {
			first = accs[0].ID + "/" + accs[0].DisplayName + "/" + accs[0].WickUserID
		}
		return map[string]any{
			"arg_plain":  c.Input("q") == gapArgPlain,
			"cred_plain": c.Cfg("token") == gapCredPlain,
			"dyn":        c.Mask("dyn: "+gapUpstream, []string{gapUpstream}),
			"caller":     c.CallerUserID(),
			"name":       c.UserName(c.CallerUserID()),
			"session":    c.SessionID(),
			"account":    c.AccountID(),
			"owner_bot":  c.OwnerBotID(),
			"instance":   c.InstanceID(),
			"accounts":   first,
			"request":    c.RequestID(),
			"raw_n":      rawN,
			"other_code": otherCode,
			"nocall":     noCallCode,
		}, nil
	}
	fail := func(c *connector.Ctx) (any, error) {
		c.Mask("", []string{gapErrorValue})
		c.MaskIgnoreCase("", []string{gapFoldValue})
		return nil, errors.New("upstream rejected " + gapErrorValue + " " + strings.ToUpper(gapFoldValue))
	}
	return connector.Module{
		Meta:    connector.Meta{Key: "gap", Name: "Gap", Description: "test", Fixed: true},
		Configs: []entity.Config{{Key: "token", Type: "text", IsSecret: true, Required: true}},
		HealthCheck: func(c *connector.Ctx) ([]connector.OpHealth, error) {
			return []connector.OpHealth{{Key: "probe", OK: c.Cfg("token") == gapCredPlain, Reason: "token"}}, nil
		},
		Operations: []connector.Category{connector.Cat("", "",
			connector.Op("probe", "Probe", "report the ctx", In{}, probe, wickdocs.Docs{}),
			connector.Op("fail", "Fail", "fail with a masked value", In{}, fail, wickdocs.Docs{}),
		)},
	}
}

// TestPluginCtxParity runs a connector plugin over a real go-plugin
// connection behind a real connectors.Service: wick_enc_ args and creds
// arrive in the plugin as plaintext, c.Mask comes back as a wick_enc_ token
// the caller can decrypt, a failed op's masked values never leak through the
// error, and the caller's identity (user, name, session, account, owner bot,
// connected accounts) reaches the plugin's Ctx.
func TestPluginCtxParity(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	cfgs := configs.NewService(db)
	if err := cfgs.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	encSvc, err := enc.New("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}

	// Plugin process side, reached through the broker like production.
	users := &fakeUsers{users: []*entity.User{{ID: "u1", Name: "Ada", Approved: true, Role: entity.RoleAdmin}}}
	srv := NewServer(Deps{
		Plugins:    fakePlugins{"gap": {Key: "gap", Kind: "connector", Scopes: []string{"users.read", "accounts.read"}}},
		Users:      users,
		Principals: fakePrincipals{users},
	})
	b := NewBridge(srv, &fakeServiceTokens{revoked: map[string]bool{}})
	wickplugin.SetHostBridge(b)
	t.Cleanup(func() { wickplugin.SetHostBridge(nil) })
	t.Setenv(wickplugin.EnvHostBroker, "1")
	client, _ := goplugin.TestPluginGRPCConn(t, false, map[string]goplugin.Plugin{
		wickplugin.PluginName: &wickplugin.ConnectorGRPCPlugin{Impl: wickplugin.NewServer(gapModule()), HostKey: "gap"},
	})
	t.Cleanup(func() { _ = client.Close() })
	raw, err := client.Dispense(wickplugin.PluginName)
	if err != nil {
		t.Fatal(err)
	}
	conn := raw.(wickplugin.GRPCConn)

	// Host side: the manifest round trip strips the Execute funcs, exactly
	// what the loader parses from plugin.json, then BuildModule rewires them.
	manifest, err := wickplugin.DumpManifest(gapModule())
	if err != nil {
		t.Fatal(err)
	}
	var hostMod connector.Module
	if err := json.Unmarshal(manifest, &hostMod); err != nil {
		t.Fatal(err)
	}
	hostMod = connplugin.BuildModule(hostMod, func(string) (*connplugin.Lease, error) {
		return &connplugin.Lease{Conn: conn}, nil
	})

	svc := connectors.NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgs)
	svc.SetSessionOwnerBotResolver(func(sid string) (string, bool) { return "B-" + sid, sid != "" })
	if err := svc.Bootstrap(ctx, []connector.Module{hostMod}); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.List(ctx)
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	id := rows[0].ID
	srv.d.Accounts = fakeAccounts{id: {{Id: "acc-1", DisplayName: "Ada Slack", WickUserId: "u1"}}}

	// Configure with an already-encrypted secret, then pass an encrypted arg.
	credTok, err := encSvc.EncryptValue(gapCredPlain, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Update(ctx, id, "Gap", map[string]string{"token": credTok}, false); err != nil {
		t.Fatalf("configure with encrypted secret: %v", err)
	}
	argTok, err := encSvc.EncryptValue(gapArgPlain, "u1")
	if err != nil {
		t.Fatal(err)
	}

	progress := &progressLog{}
	res, err := svc.Execute(ctx, connectors.ExecuteParams{ConnectorID: id, OperationKey: "probe",
		Input: map[string]string{"q": argTok, "n": "7"}, RawInput: map[string]any{"q": argTok, "n": 7},
		Progress: progress, Source: entity.ConnectorRunSourceMCP, UserID: "u1", SessionID: "s-1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res.ResponseJSON), &got); err != nil {
		t.Fatalf("%v: %s", err, res.ResponseJSON)
	}
	if got["arg_plain"] != true || got["cred_plain"] != true {
		t.Fatalf("plugin did not get plaintext: %s", res.ResponseJSON)
	}
	for k, want := range map[string]string{"caller": "u1", "name": "Ada", "session": "s-1", "owner_bot": "B-s-1",
		"instance": id, "accounts": "acc-1/Ada Slack/u1", "request": res.RunID,
		"other_code": codes.PermissionDenied.String(), "nocall": codes.FailedPrecondition.String()} {
		if got[k] != want {
			t.Errorf("plugin %s = %v, want %q", k, got[k], want)
		}
	}
	if got["raw_n"] != float64(7) {
		t.Errorf("plugin RawInputValue(n) = %#v, want the number 7", got["raw_n"])
	}
	if len(progress.got) != 1 || progress.got[0] != "1/2 half" {
		t.Errorf("progress = %v, want [1/2 half]", progress.got)
	}
	if strings.Contains(res.ResponseJSON, gapUpstream) {
		t.Fatalf("masked value leaked: %s", res.ResponseJSON)
	}
	tok := regexp.MustCompile(`wick_enc_[A-Za-z0-9_\-]+`).FindString(got["dyn"].(string))
	if tok == "" {
		t.Fatalf("c.Mask did not come back as wick_enc_: %v", got["dyn"])
	}
	if plain, err := encSvc.DecryptValue(tok, "u1"); err != nil || plain != gapUpstream {
		t.Fatalf("mask token decrypts to %q, %v", plain, err)
	}

	// A failed op's masked values are scrubbed from the error message the
	// callers surface (res.ErrorMessage; the raw err stays host-internal,
	// as for in-process connectors).
	res, err = svc.Execute(ctx, connectors.ExecuteParams{ConnectorID: id, OperationKey: "fail",
		Source: entity.ConnectorRunSourceMCP, UserID: "u1"})
	if err == nil || res == nil {
		t.Fatalf("fail op = %+v, %v", res, err)
	}
	if msg := res.ErrorMessage; !strings.Contains(msg, "upstream rejected wick_enc_") || strings.Contains(msg, gapErrorValue) ||
		strings.Contains(strings.ToLower(msg), gapFoldValue) {
		t.Fatalf("error message = %q, want it masked", msg)
	}

	// The unary Execute returns the masks with the op error too.
	ures, uerr := conn.Execute(ctx, wickplugin.ExecCall{Operation: "fail", InstanceID: id})
	if !errors.Is(uerr, wickplugin.ErrPluginOp) || len(ures.Mask) != 1 || ures.Mask[0] != gapErrorValue ||
		len(ures.MaskIgnoreCase) != 1 || ures.MaskIgnoreCase[0] != gapFoldValue {
		t.Fatalf("unary fail = %+v, %v", ures, uerr)
	}

	// "Test setup" runs the plugin module's HealthCheck. (RunHealthCheck
	// decrypts under the empty-user key like for in-process modules, so the
	// u1-bound token above stays opaque: only the report's arrival is checked
	// here; the plaintext path is checked through the hook directly below.)
	if hostMod.HealthCheck == nil {
		t.Fatal("manifest did not carry has_health_check")
	}
	hc, err := svc.RunHealthCheck(ctx, id)
	if err != nil || len(hc.Ops) != 1 || hc.Ops[0].Key != "probe" || hc.Ops[0].Reason != "token" {
		t.Fatalf("health check = %+v, %v", hc, err)
	}
	report, err := hostMod.HealthCheck(connector.NewCtx(ctx, id, map[string]string{"token": gapCredPlain}, nil, nil, nil, nil))
	if err != nil || len(report) != 1 || !report[0].OK {
		t.Fatalf("health hook with plaintext creds = %+v, %v", report, err)
	}
	// A plugin that answers no_health_check maps to ErrNoHealthCheck, so the
	// caller falls back to running an operation.
	bare := gapModule()
	bare.HealthCheck = nil
	bareClient, _ := goplugin.TestPluginGRPCConn(t, false, map[string]goplugin.Plugin{
		wickplugin.PluginName: &wickplugin.ConnectorGRPCPlugin{Impl: wickplugin.NewServer(bare), HostKey: "gap"},
	})
	t.Cleanup(func() { _ = bareClient.Close() })
	bareRaw, err := bareClient.Dispense(wickplugin.PluginName)
	if err != nil {
		t.Fatal(err)
	}
	staleMod := connplugin.BuildModule(connector.Module{Meta: hostMod.Meta, HasHealthCheck: true}, func(string) (*connplugin.Lease, error) {
		return &connplugin.Lease{Conn: bareRaw.(wickplugin.GRPCConn)}, nil
	})
	if _, err := staleMod.HealthCheck(connector.NewPluginCtx(ctx, nil, nil)); !errors.Is(err, connectors.ErrNoHealthCheck) {
		t.Fatalf("no hook err = %v, want ErrNoHealthCheck", err)
	}

	// AccountID rides the same request (the Service only sets it for a
	// resolved OAuth account, so drive the adapter with a host Ctx here).
	var probe connector.Operation
	for _, op := range hostMod.AllOps() {
		if op.Key == "probe" {
			probe = op
		}
	}
	cctx := connector.NewCtx(ctx, id, map[string]string{"token": gapCredPlain}, map[string]string{"q": gapArgPlain}, nil, nil, nil)
	cctx.SetCallerUserID("u1")
	cctx.SetAccountID("acc-1")
	out, err := probe.Execute(cctx)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := json.Unmarshal(out.(json.RawMessage), &got); err != nil {
		t.Fatal(err)
	}
	if got["account"] != "acc-1" {
		t.Fatalf("plugin AccountID = %v, want acc-1", got["account"])
	}
}

// TestConnectedAccountsBoundToExecutedInstance: only a connector plugin, inside
// the Execute wick started for that instance, may list its accounts.
func TestConnectedAccountsBoundToExecutedInstance(t *testing.T) {
	srv := NewServer(Deps{
		Plugins: fakePlugins{
			"gap":  {Key: "gap", Kind: "connector", Scopes: []string{"accounts.read"}},
			"tl":   {Key: "tl", Kind: "tool", Scopes: []string{"accounts.read"}},
			"jb":   {Key: "jb", Kind: "job", Scopes: []string{"accounts.read"}},
			"sv":   {Key: "sv", Kind: "service", Scopes: []string{"accounts.read"}},
			"conn": {Key: "conn", Kind: "connector", Scopes: []string{"accounts.read"}},
		},
		Accounts: fakeAccounts{"inst-a": {{Id: "acc-1"}}, "inst-b": {{Id: "acc-2"}}, "inst-x": {{Id: "acc-x"}}},
	})
	ask := func(c Caller, instance string) (*hostpb.ConnectedAccountsResponse, error) {
		return srv.ConnectedAccounts(as(c), &hostpb.ConnectedAccountsRequest{InstanceId: instance})
	}
	if out, err := ask(Caller{PluginKey: "gap", Kind: "connector", UserID: "u1", InstanceID: "inst-a"}, "inst-a"); err != nil ||
		len(out.Accounts) != 1 || out.Accounts[0].Id != "acc-1" {
		t.Fatalf("own instance = %+v, %v", out, err)
	}
	for _, tc := range []struct {
		name     string
		caller   Caller
		instance string
		want     codes.Code
	}{
		{"tool", Caller{PluginKey: "tl", Kind: "tool", UserID: "u1", InstanceID: "inst-a"}, "inst-a", codes.PermissionDenied},
		{"job", Caller{PluginKey: "jb", Kind: "job", UserID: "u1", InstanceID: "inst-a"}, "inst-a", codes.PermissionDenied},
		{"service", Caller{PluginKey: "sv", Kind: "service", UserID: "u1", InstanceID: "inst-a"}, "inst-a", codes.PermissionDenied},
		{"no call id", Caller{PluginKey: "gap", Kind: "connector"}, "inst-a", codes.FailedPrecondition},
		{"other connector's instance", Caller{PluginKey: "gap", Kind: "connector", UserID: "u1", InstanceID: "inst-a"}, "inst-x", codes.PermissionDenied},
		{"same connector, other instance", Caller{PluginKey: "gap", Kind: "connector", UserID: "u1", InstanceID: "inst-a"}, "inst-b", codes.PermissionDenied},
	} {
		if _, err := ask(tc.caller, tc.instance); status.Code(err) != tc.want {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		}
	}
}

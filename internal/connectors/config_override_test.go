package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// overrideModule: "write" only runs with mode = write; mode is a choice
// field (read by default), host is free text and token a secret.
func overrideModule() connector.Module {
	type Creds struct {
		Mode  string `wick:"dropdown=read|write;default=read"`
		Host  string `wick:"default=api.example.com"`
		Token string `wick:"secret"`
	}
	exec := func(c *connector.Ctx) (any, error) { return map[string]string{"ok": "1"}, nil }
	w := connector.Op("write", "Write", "change a thing", struct{}{}, exec, wickdocs.Docs{})
	w.VisibleWhen = "mode:write"
	return connector.Module{
		Meta:       connector.Meta{Key: "ovstub", Name: "OV Stub", Description: "test", Fixed: true},
		Configs:    entity.StructToConfigs(Creds{}),
		Operations: []connector.Category{connector.Cat("", "", connector.Op("read", "Read", "read", struct{}{}, exec, wickdocs.Docs{}), w)},
	}
}

func TestConfigOverridePerCall(t *testing.T) {
	ctx := context.Background()
	svc, id := newSvcWithModule(t, nil, overrideModule())
	svc.SetIdentityLookup(func(_ context.Context, uid string) (*entity.User, []string, error) {
		if uid == "admin-1" {
			return &entity.User{ID: uid, Role: entity.RoleAdmin}, nil, nil
		}
		return &entity.User{ID: uid}, nil, nil
	})
	if got := svc.OverridableCandidates("ovstub"); len(got) != 1 || got[0] != "mode" {
		t.Fatalf("candidates = %v, want only mode (host is free text, token a secret)", got)
	}
	run := func(user string, admin bool, ov map[string]string) error {
		_, err := svc.Execute(ctx, ExecuteParams{ConnectorID: id, OperationKey: "write", Source: entity.ConnectorRunSourceMCP,
			UserID: user, IsAdmin: admin, ConfigOverride: ov})
		return err
	}
	// Nothing listed: refused, and the reason does not offer an override.
	if err := run("admin-1", true, map[string]string{"mode": "write"}); err == nil || !strings.Contains(err.Error(), "does not allow a config override") {
		t.Fatalf("unlisted override = %v", err)
	}
	if _, err := svc.SetOverridableConfig(ctx, id, []string{"host"}); err == nil {
		t.Fatal("a free-text key was accepted as overridable")
	}
	if _, err := svc.SetOverridableConfig(ctx, id, []string{"mode"}); err != nil {
		t.Fatal(err)
	}
	if err := run("admin-1", true, nil); err == nil || !strings.Contains(err.Error(), "config_override") {
		t.Fatalf("config-off reason must point at the override: %v", err)
	}
	if err := run("admin-1", true, map[string]string{"mode": "delete"}); err == nil || !strings.Contains(err.Error(), "takes one of") {
		t.Fatalf("value outside the choices = %v", err)
	}
	if err := run("admin-1", true, map[string]string{"host": "evil.example"}); err == nil {
		t.Fatal("an unlisted key was overridden")
	}
	if err := run("user-1", false, map[string]string{"mode": "write"}); err == nil || !strings.Contains(err.Error(), "may not configure") {
		t.Fatalf("non-configurer override = %v", err)
	}
	if err := run("admin-1", true, map[string]string{"mode": "write"}); err != nil {
		t.Fatalf("configurer override: %v", err)
	}
	// The saved config is untouched.
	if cfg := svc.LoadConfigs(entity.Connector{ID: id}); cfg["mode"] == "write" {
		t.Fatal("the override was saved to the instance")
	}
	// Access requests see the override.
	if _, err := svc.DescribeOpWith(ctx, id, "write", map[string]string{"mode": "write"}); err != nil {
		t.Fatalf("describe with override: %v", err)
	}
	if ok, why := svc.CanRunOwnWith(ctx, "user-1", id, "write", map[string]string{"mode": "write"}); ok || why == "" {
		t.Fatalf("non-configurer may approve an override: %v %q", ok, why)
	}
	if ok, why := svc.CanRunOwnWith(ctx, "admin-1", id, "write", map[string]string{"mode": "write"}); !ok {
		t.Fatalf("configurer cannot approve: %q", why)
	}
	// A session connector sets its own config instead.
	_, err := svc.Execute(ctx, ExecuteParams{ConnectorID: "sw_x", OperationKey: "write", Source: entity.ConnectorRunSourceMCP,
		ConfigOverride:  map[string]string{"mode": "write"},
		SessionInstance: &SessionInstanceTarget{BaseKey: "ovstub", Label: "sw"}})
	if err == nil || !strings.Contains(err.Error(), "set_config") {
		t.Fatalf("session override = %v", err)
	}
}

func TestOverrideFromParams(t *testing.T) {
	ov, rest, err := OverrideFromParams(map[string]any{"id": "1", ConfigOverrideParam: map[string]any{"mode": "write"}})
	if err != nil || ov["mode"] != "write" || len(rest) != 1 {
		t.Fatalf("object: %v %v %v", ov, rest, err)
	}
	ov, _, err = OverrideFromParams(map[string]any{ConfigOverrideParam: `{"mode":"write"}`})
	if err != nil || ov["mode"] != "write" {
		t.Fatalf("json string: %v %v", ov, err)
	}
	if _, _, err := OverrideFromParams(map[string]any{ConfigOverrideParam: 5}); err == nil {
		t.Fatal("a number was accepted")
	}
}

type openScope struct{}

func (openScope) AllowConnector(string) bool        { return true }
func (openScope) AllowAccount(string, string) bool  { return true }
func (openScope) AllowOp(string, string, bool) bool { return true }

// An agent working for someone who may configure the instance runs the
// override at once; for anyone else it needs approval.
func TestAgentOverrideNeedsApprovalOnlyForNonConfigurers(t *testing.T) {
	bg := context.Background()
	svc, id := newSvcWithModule(t, nil, overrideModule())
	svc.SetIdentityLookup(func(_ context.Context, uid string) (*entity.User, []string, error) {
		if uid == "admin-1" {
			return &entity.User{ID: uid, Role: entity.RoleAdmin}, nil, nil
		}
		return &entity.User{ID: uid}, nil, nil
	})
	if _, err := svc.SetOverridableConfig(bg, id, []string{"mode"}); err != nil {
		t.Fatal(err)
	}
	ctx := WithAgentScope(bg, openScope{})
	run := func(user string, admin bool) error {
		_, err := svc.Execute(ctx, ExecuteParams{ConnectorID: id, OperationKey: "write", Source: entity.ConnectorRunSourceMCP,
			UserID: user, IsAdmin: admin, ConfigOverride: map[string]string{"mode": "write"}})
		return err
	}
	if err := run("admin-1", true); err != nil {
		t.Fatalf("configurer's agent was stopped: %v", err)
	}
	if err := run("user-1", false); err == nil || !strings.Contains(err.Error(), "needs approval") {
		t.Fatalf("non-configurer's agent override = %v", err)
	}
}

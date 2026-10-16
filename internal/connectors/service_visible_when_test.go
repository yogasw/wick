package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// visibleWhenModule has one always-visible op and one ("pdf") that only
// the moli engine supports. browser defaults to chromium.
func visibleWhenModule() connector.Module {
	type Creds struct {
		Browser string `wick:"default=chromium"`
	}
	exec := func(c *connector.Ctx) (any, error) { return map[string]string{"ok": "1"}, nil }
	pdf := connector.Op("pdf", "PDF", "render pdf", struct{}{}, exec, wickdocs.Docs{})
	pdf.VisibleWhen = "browser:moli"
	return connector.Module{
		Meta:    connector.Meta{Key: "vwstub", Name: "VW Stub", Description: "test", Fixed: true},
		Configs: entity.StructToConfigs(Creds{}),
		Operations: []connector.Category{connector.Cat("", "",
			connector.Op("open", "Open", "open a page", struct{}{}, exec, wickdocs.Docs{}),
			pdf,
		)},
	}
}

func TestVisibleWhenOperationStates(t *testing.T) {
	ctx := context.Background()
	svc, id := newSvcWithModule(t, nil, visibleWhenModule())

	states, err := svc.OperationStates(ctx, id, "vwstub")
	if err != nil {
		t.Fatal(err)
	}
	if !states["open"] || states["pdf"] {
		t.Fatalf("default chromium: want open on, pdf off; got %v", states)
	}
	full, _ := svc.OperationStatesFull(ctx, id, "vwstub")
	if st := full["pdf"]; !st.Enabled || !st.HiddenByConfig || st.VisibleWhen != "browser:moli" {
		t.Fatalf("full state must keep the toggle and flag the predicate: %+v", st)
	}

	if err := svc.Update(ctx, id, "VW", map[string]string{"browser": "moli"}, false); err != nil {
		t.Fatal(err)
	}
	states, _ = svc.OperationStates(ctx, id, "vwstub")
	if !states["pdf"] {
		t.Fatalf("browser=moli: pdf must be on; got %v", states)
	}
}

func TestVisibleWhenCatalog(t *testing.T) {
	ctx := context.Background()
	svc, id := newSvcWithModule(t, nil, visibleWhenModule())
	opKeys := func() []string {
		entries, err := svc.VisibleCatalog(ctx, "admin", nil, true)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, e := range entries {
			if e.Row.ID != id {
				continue
			}
			for _, op := range e.Ops {
				keys = append(keys, op.Key)
			}
		}
		return keys
	}
	if got := strings.Join(opKeys(), ","); got != "open" {
		t.Fatalf("catalog ops = %q, want open", got)
	}
	if err := svc.Update(ctx, id, "VW", map[string]string{"browser": "moli"}, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(opKeys(), ","); got != "open,pdf" {
		t.Fatalf("catalog ops = %q, want open,pdf", got)
	}
}

func TestVisibleWhenExecuteGuard(t *testing.T) {
	ctx := context.Background()
	svc, id := newSvcWithModule(t, nil, visibleWhenModule())
	run := func(src entity.ConnectorRunSource) error {
		_, err := svc.Execute(ctx, ExecuteParams{ConnectorID: id, OperationKey: "pdf", Source: src, IsAdmin: true})
		return err
	}
	for _, src := range []entity.ConnectorRunSource{entity.ConnectorRunSourceMCP, entity.ConnectorRunSourcePlugin, entity.ConnectorRunSourceSchedule} {
		if err := run(src); err == nil || !strings.Contains(err.Error(), "requires browser:moli") {
			t.Fatalf("source %s: want visible_when refusal, got %v", src, err)
		}
	}
	for _, src := range []entity.ConnectorRunSource{entity.ConnectorRunSourceTest, entity.ConnectorRunSourcePluginTest} {
		if err := run(src); err != nil {
			t.Fatalf("source %s must pass: %v", src, err)
		}
	}
	// The ungated op is unaffected.
	if _, err := svc.Execute(ctx, ExecuteParams{ConnectorID: id, OperationKey: "open", Source: entity.ConnectorRunSourceMCP, IsAdmin: true}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := svc.Update(ctx, id, "VW", map[string]string{"browser": "moli"}, false); err != nil {
		t.Fatal(err)
	}
	if err := run(entity.ConnectorRunSourceMCP); err != nil {
		t.Fatalf("browser=moli: mcp must pass: %v", err)
	}
}

func TestVisibleWhenExecuteSessionInstance(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvcWithModule(t, nil, visibleWhenModule())
	run := func(cfg map[string]string) error {
		_, err := svc.Execute(ctx, ExecuteParams{
			ConnectorID:     "sw_test",
			OperationKey:    "pdf",
			Source:          entity.ConnectorRunSourceMCP,
			SessionInstance: &SessionInstanceTarget{BaseKey: "vwstub", Label: "sw", Config: cfg},
		})
		return err
	}
	// No browser saved on the session instance: the module default applies.
	if err := run(map[string]string{}); err == nil || !strings.Contains(err.Error(), "not available on this instance") {
		t.Fatalf("want refusal, got %v", err)
	}
	if err := run(map[string]string{"browser": "moli"}); err != nil {
		t.Fatalf("browser=moli: %v", err)
	}
}

// TestBuiltinModulesValidate keeps every built-in connector passing the
// same ValidateModule check plugins get at --dump-manifest and install.
func TestBuiltinModulesValidate(t *testing.T) {
	for _, mod := range builtinModules() {
		if err := connector.ValidateModule(mod); err != nil {
			t.Errorf("%s: %v", mod.Meta.Key, err)
		}
	}
}

package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// vwSessionModule: "open" always, "pdf" only on browser=moli, "picker" a
// config-only helper. browser defaults to chromium.
func vwSessionModule() connector.Module {
	exec := func(c *connector.Ctx) (any, error) { return nil, nil }
	pdf := connector.Op("pdf", "PDF", "render pdf", struct{}{}, exec, wickdocs.Docs{})
	pdf.VisibleWhen = "browser:moli"
	return connector.Module{
		Meta:    connector.Meta{Key: "vwsess", Name: "VW Session", Description: "test"},
		Configs: []entity.Config{{Key: "browser", Type: "text", Value: "chromium"}},
		Operations: []connector.Category{connector.Cat("Main", "",
			connector.Op("open", "Open", "open page", struct{}{}, exec, wickdocs.Docs{}),
			pdf,
			connector.OpConfigOnly("picker", "Picker", "picker options", struct{}{}, exec, wickdocs.Docs{}),
		)},
	}
}

func newVWSessionSvc(t *testing.T) *connectors.Service {
	t.Helper()
	return newVWSessionSvcWith(t, vwSessionModule())
}

func newVWSessionSvcWith(t *testing.T, mod connector.Module) *connectors.Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	cfgs := configs.NewService(db)
	if err := cfgs.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := connectors.NewServiceFromDB(db)
	svc.SetConfigs(cfgs)
	if err := svc.Bootstrap(context.Background(), []connector.Module{mod}); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestSessionInstanceVisibleWhen(t *testing.T) {
	svc := newVWSessionSvc(t)
	layout, sid := newTestLayout(t)
	inst, err := sessionworkspace.Add(layout, sid, sessionworkspace.Instance{BaseKey: "vwsess", Label: "S", Config: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}

	// Default chromium: only "open" (pdf gated, picker config-only).
	if _, total := sessionInstanceSummaries(svc, layout, sid); total != 1 {
		t.Fatalf("summary total = %d, want 1", total)
	}
	if _, total := sessionInstanceSearch(svc, layout, sid, "p"); total != 1 {
		t.Fatalf("search total = %d, want 1 (open)", total)
	}
	target := &connectors.SessionInstanceTarget{BaseKey: "vwsess", Label: "S", Config: inst.Config}
	// pdf is switched off by config: listed with a reason, no tool_id or schema.
	off, _, err := sessionInstanceDetail(svc, target, inst.ID, "pdf")
	if err != nil {
		t.Fatalf("wick_get pdf: %v", err)
	}
	if len(off.Tools) != 1 || off.Tools[0].ToolID != "" || off.Tools[0].InputSchema != nil ||
		!strings.Contains(off.Tools[0].Unavailable, "browser = moli") {
		t.Fatalf("pdf on chromium must read as unavailable: %+v", off.Tools)
	}
	if _, _, err := sessionInstanceDetail(svc, target, inst.ID, "picker"); err == nil {
		t.Fatal("wick_get must not expose a config-only op")
	}
	detail, _, err := sessionInstanceDetail(svc, target, inst.ID, "Main")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, tool := range detail.Tools {
		keys = append(keys, tool.Name)
	}
	if got := strings.Join(keys, ","); got != "Open,PDF" {
		t.Fatalf("category ops = %q, want Open,PDF (PDF unavailable)", got)
	}

	// browser=moli: pdf joins.
	target.Config = map[string]string{"browser": "moli"}
	if _, _, err := sessionInstanceDetail(svc, target, inst.ID, "pdf"); err != nil {
		t.Fatalf("wick_get pdf on moli: %v", err)
	}
	inst2, err := sessionworkspace.Add(layout, sid, sessionworkspace.Instance{BaseKey: "vwsess", Label: "M", Config: map[string]string{"browser": "moli"}})
	if err != nil || inst2.ID == "" {
		t.Fatal(err)
	}
	if _, total := sessionInstanceSummaries(svc, layout, sid); total != 3 {
		t.Fatalf("summary total = %d, want 3 (1 + 2)", total)
	}
}

// A session-locked field is announced up front (locked_keys), refused on
// write, and ignored at runtime even when a value was stored anyway.
func TestSessionLockedConfig(t *testing.T) {
	mod := vwSessionModule()
	mod.Configs[0].SessionLocked = true
	svc := newVWSessionSvcWith(t, mod)

	vm := sessionWorkspaceVM(svc, sessionworkspace.Instance{ID: "sw_x", BaseKey: "vwsess"})
	if strings.Join(vm.Locked, ",") != "browser" {
		t.Fatalf("locked_keys = %v, want [browser]", vm.Locked)
	}
	specs := map[string]entity.Config{"browser": mod.Configs[0]}
	if msg := sessionConfigKeyError(specs, "browser"); !strings.Contains(msg, "locked") {
		t.Fatalf("want locked refusal, got %q", msg)
	}
	if msg := sessionConfigKeyError(specs, "nope"); !strings.Contains(msg, "unknown config key") {
		t.Fatalf("want unknown key, got %q", msg)
	}
	visible := sessionOpVisible(mod, svc.ResolveSessionConfig(mod, "sw_x", map[string]string{"browser": "moli"}, nil))
	for _, op := range mod.AllOps() {
		if op.Key == "pdf" && visible(op) {
			t.Fatal("stored override of a locked field must not switch pdf on")
		}
	}
}

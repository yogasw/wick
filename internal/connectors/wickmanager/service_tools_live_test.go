package wickmanager

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

// A tool plugin loaded while wick runs is visible to tool_list / tool_get /
// tool_set_config at once, and gone once it is unloaded: the ops read
// Deps.ToolsFn per call, not a boot snapshot.
func TestToolOpsReadLiveRegistry(t *testing.T) {
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
	var mu sync.Mutex
	live := []tool.Tool{{Key: "builtin", Name: "Builtin", Path: "/tools/builtin", DefaultVisibility: entity.VisibilityPrivate}}
	h := newHandlers(Deps{Configs: cfgs, Login: login.NewService(db, ""), Tools: live[:1], ToolsFn: func() []tool.Tool {
		mu.Lock()
		defer mu.Unlock()
		return append([]tool.Tool(nil), live...)
	}})
	admin := login.WithUser(context.Background(), &entity.User{ID: "u-admin", Role: entity.RoleAdmin, Approved: true}, nil)
	ctx := func(in map[string]string) *connector.Ctx { return connector.NewCtx(admin, "", nil, in, nil, nil, nil) }

	if _, err := h.toolGet(ctx(map[string]string{"key": "hot"})); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("before hot-load: %v", err)
	}

	// Hot-load: the sink seeds config rows and registers the tool.
	if err := cfgs.EnsureOwned(context.Background(), "hot", entity.Config{Key: "token", Type: "text"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	live = append(live, tool.Tool{Key: "hot", Name: "Hot", Path: "/tools/hot", DefaultVisibility: entity.VisibilityPrivate})
	mu.Unlock()

	out, err := h.toolGet(ctx(map[string]string{"key": "hot"}))
	if err != nil {
		t.Fatalf("tool_get on a hot-loaded tool: %v", err)
	}
	if rows, _ := out.(map[string]any)["configs"].([]map[string]any); len(rows) != 1 {
		t.Fatalf("configs %#v", out.(map[string]any)["configs"])
	}
	list, err := h.toolList(ctx(nil))
	if err != nil || len(list.([]map[string]any)) != 2 {
		t.Fatalf("tool_list: %v %v", list, err)
	}
	if _, err := h.toolSetConfig(ctx(map[string]string{"key": "hot", "config_key": "token", "value": "v"})); err != nil {
		t.Fatalf("tool_set_config: %v", err)
	}

	// Disable: gone again.
	mu.Lock()
	live = live[:1]
	mu.Unlock()
	if _, err := h.toolGet(ctx(map[string]string{"key": "hot"})); err == nil {
		t.Fatal("tool_get still finds a disabled tool")
	}
	if list, _ := h.toolList(ctx(nil)); len(list.([]map[string]any)) != 1 {
		t.Fatalf("tool_list after disable: %v", list)
	}
}

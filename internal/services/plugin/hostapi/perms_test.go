package hostapi

import (
	"context"
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func dbPlugin() PluginInfo {
	return PluginInfo{Key: "p", Scopes: []string{"users.read", "db.read"},
		Resources: &wickplugin.HostResources{DB: []wickplugin.HostDBResource{{Table: "users", Mode: "read"}, {Table: "tickets", Mode: "read"}}}}
}

func TestDecideDefaults(t *testing.T) {
	ctx := context.Background()
	p := NewPermissions(NewMemoryPermissionStore())
	pl := dbPlugin()
	if d := p.Decide(ctx, pl, "users.read", ""); !d.Allowed || d.Status != StatusAllowed {
		t.Fatalf("users.read default = %+v, want allowed", d)
	}
	if d := p.Decide(ctx, pl, "db.read", "users"); d.Allowed || d.Status != StatusOffByDefault {
		t.Fatalf("db.read default = %+v, want off-by-default", d)
	}
	if d := p.Decide(ctx, pl, "whoami", ""); !d.Allowed {
		t.Fatal("whoami is always allowed")
	}
	if d := p.Decide(ctx, pl, "nope.read", ""); d.Allowed {
		t.Fatal("unknown scope allowed")
	}
}

func TestDecideUndeclaredStillAllowed(t *testing.T) {
	p := NewPermissions(nil)
	d := p.Decide(context.Background(), dbPlugin(), "tickets.read", "")
	if !d.Allowed || d.Status != StatusUndeclared {
		t.Fatalf("undeclared tickets.read = %+v, want allowed + undeclared", d)
	}
	// Resource scopes are the exception: no declaration, no access.
	d = p.Decide(context.Background(), dbPlugin(), "connectors.execute", "slack.send_message")
	if d.Allowed {
		t.Fatalf("undeclared connectors.execute = %+v, want denied", d)
	}
}

func TestDecideOverlayScopeAndItem(t *testing.T) {
	ctx := context.Background()
	p := NewPermissions(NewMemoryPermissionStore())
	pl := dbPlugin()
	if err := p.Set(ctx, "p", "db.read", "", true, "admin"); err != nil {
		t.Fatal(err)
	}
	if d := p.Decide(ctx, pl, "db.read", "tickets"); !d.Allowed {
		t.Fatalf("db.read after allow = %+v", d)
	}
	if d := p.Decide(ctx, pl, "db.read", "sessions"); d.Allowed {
		t.Fatal("a table outside host_resources was allowed")
	}
	_ = p.Set(ctx, "p", "db.read", "tickets", false, "admin")
	if d := p.Decide(ctx, pl, "db.read", "tickets"); d.Allowed || d.Status != StatusBlocked {
		t.Fatalf("blocked item = %+v", d)
	}
	if d := p.Decide(ctx, pl, "db.read", "users"); !d.Allowed {
		t.Fatal("blocking one item blocked another")
	}
	_ = p.Set(ctx, "p", "users.read", "", false, "admin")
	if d := p.Decide(ctx, pl, "users.read", ""); d.Allowed || d.Status != StatusBlocked {
		t.Fatalf("blocked scope = %+v", d)
	}
	_ = p.Clear(ctx, "p", "users.read", "")
	if d := p.Decide(ctx, pl, "users.read", ""); !d.Allowed {
		t.Fatal("clearing the overlay did not restore the default")
	}
}

func TestDecideBlockAll(t *testing.T) {
	ctx := context.Background()
	p := NewPermissions(NewMemoryPermissionStore())
	_ = p.Set(ctx, "p", "db.read", "", true, "admin")
	_ = p.Set(ctx, "p", BlockAll, BlockAll, false, "admin")
	for _, sc := range []string{"users.read", "tickets.read", "db.read"} {
		if d := p.Decide(ctx, dbPlugin(), sc, ""); d.Allowed {
			t.Errorf("%s allowed under Block all", sc)
		}
	}
	if d := p.Decide(ctx, dbPlugin(), "whoami", ""); !d.Allowed {
		t.Error("whoami must survive Block all")
	}
	// Other plugins are untouched.
	if d := p.Decide(ctx, PluginInfo{Key: "q"}, "users.read", ""); !d.Allowed {
		t.Error("Block all leaked to another plugin")
	}
}

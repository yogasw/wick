package api

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/services/plugin/hostapi"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// Plugin processes spawned before the Host API exists never get its env or
// broker, so keep_warm tools and the connector warm-up must start after it.
func TestHostAPIStartsBeforePluginSpawns(t *testing.T) {
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	host := strings.Index(src, "startHostAPI(")
	if host < 0 {
		t.Fatal("startHostAPI is not called")
	}
	for _, spawn := range []string{"toolPlugins.Start()", "pluginMgr.WarmUp()", "servicePlugins.Start()"} {
		i := strings.Index(src, spawn)
		if i < 0 || i < host {
			t.Errorf("%s at %d must come after startHostAPI at %d", spawn, i, host)
		}
	}
}

type fakeConfigRows map[string]entity.Config

func (f fakeConfigRows) Lookup(owner, key string) (entity.Config, bool) {
	r, ok := f[owner+"/"+key]
	return r, ok
}

type onePlugin map[string]hostapi.PluginInfo

func (o onePlugin) Plugin(key string) (hostapi.PluginInfo, bool) { p, ok := o[key]; return p, ok }

// The setup tool reads the notion-ticket-sync job's config: plain values
// with config.read_other, a secret only when declared and allowed (an
// undeclared secret stays empty).
func TestOwnedConfigsUseRealSecretFlag(t *testing.T) {
	rows := fakeConfigRows{
		"notion-ticket-sync/database_id": {Value: "db-1"},
		"notion-ticket-sync/token":       {Value: "secret-token", IsSecret: true},
		"notion-ticket-sync/write_token": {Value: "secret-write", IsSecret: true},
	}
	// A secret also needs the admin to allow config.read_secret.
	perms := hostapi.NewPermissions(hostapi.NewMemoryPermissionStore())
	if err := perms.Set(context.Background(), "notion-ticket-sync-setup", wickplugin.ScopeHostConfigReadSecret,
		"", true, "admin"); err != nil {
		t.Fatal(err)
	}
	srv := hostapi.NewServer(hostapi.Deps{
		Plugins: onePlugin{"notion-ticket-sync-setup": {Key: "notion-ticket-sync-setup", Kind: wickplugin.KindTool,
			ConfigOwner: "notion-ticket-sync-setup",
			Scopes:      []string{wickplugin.ScopeHostConfigReadOther, wickplugin.ScopeHostConfigReadSecret},
			Resources:   &wickplugin.HostResources{Config: []wickplugin.HostConfigResource{{Owner: "notion-ticket-sync", Key: "token"}}}}},
		Configs: ownedConfigs{c: rows},
		Perms:   perms,
	})
	ctx := hostapi.WithCaller(context.Background(), hostapi.Caller{PluginKey: "notion-ticket-sync-setup", Kind: wickplugin.KindTool})
	get := func(key string) *hostpb.GetConfigResponse {
		out, err := srv.GetConfig(ctx, &hostpb.GetConfigRequest{Owner: "notion-ticket-sync", Key: key})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return out
	}
	if r := get("database_id"); r.GetValue() != "db-1" || r.GetSecret() {
		t.Fatalf("plain value = %+v", r)
	}
	if r := get("token"); r.GetValue() != "secret-token" || !r.GetSecret() {
		t.Fatalf("declared secret = %+v", r)
	}
	if r := get("write_token"); r.GetValue() != "" || !r.GetSecret() {
		t.Fatalf("undeclared secret leaked: %+v", r)
	}
	if r := get("missing"); r.GetFound() {
		t.Fatalf("missing row found: %+v", r)
	}
}

func TestUserTagsOneQuery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE user_tags (user_id TEXT, tag_id TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO user_tags VALUES ('u1','t2'),('u1','t1'),('u2','t3'),('u9','t9')")
	got, err := userTags(db)(context.Background(), []string{"u1", "u2", "u3"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got["u1"], ",") != "t1,t2" || strings.Join(got["u2"], ",") != "t3" || len(got["u3"]) != 0 || got["u9"] != nil {
		t.Fatalf("tags = %v", got)
	}
}

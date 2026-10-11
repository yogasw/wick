package hostapi

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// secretModule's op echoes its secret config back, the way a careless
// connector would; wick must mask it before the plugin sees it.
func secretModule() connector.Module {
	type In struct {
		Q string `wick:"required"`
	}
	return connector.Module{
		Meta:    connector.Meta{Key: "stub", Name: "Stub", Description: "test", Fixed: true},
		Configs: []entity.Config{{Key: "token", Type: "text", IsSecret: true, Required: true}},
		Operations: []connector.Category{connector.Cat("", "",
			connector.Op("echo", "Echo", "echo the token", In{}, func(c *connector.Ctx) (any, error) {
				return map[string]string{"token": c.Cfg("token"), "q": c.Input("q")}, nil
			}, wickdocs.Docs{}),
		)},
	}
}

// TestExecuteConnectorRealService runs ExecuteConnector through
// NewConnectorRunner over a real connectors.Service: visibility, the module
// check, run source and masking are wick's own.
func TestExecuteConnectorRealService(t *testing.T) {
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
	svc := connectors.NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgs)
	if err := svc.Bootstrap(ctx, []connector.Module{secretModule()}); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.List(ctx)
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	id := rows[0].ID
	const secret = "super-secret-token-12345"
	if err := svc.Update(ctx, id, "Stub", map[string]string{"token": secret}, false); err != nil {
		t.Fatal(err)
	}

	users := &fakeUsers{users: []*entity.User{
		{ID: "admin", Name: "Admin", Approved: true, Role: entity.RoleAdmin},
		{ID: "plain", Name: "Plain", Approved: true},
	}}
	srv := NewServer(Deps{
		Plugins: fakePlugins{"p": {Key: "p", Kind: "connector", Scopes: []string{"connectors.read", "connectors.execute"},
			Resources: &wickplugin.HostResources{Connectors: []wickplugin.HostConnectorResource{
				{Connector: "stub", Ops: []string{"echo"}}, {Connector: "other", Ops: []string{"echo"}}}}}},
		Users:      users,
		Principals: fakePrincipals{users},
		Connectors: NewConnectorRunner(svc),
	})
	asUser := func(uid string) context.Context { return as(Caller{PluginKey: "p", UserID: uid, Source: SourceCaller}) }

	list, err := srv.ListConnectorInstances(asUser("admin"), &hostpb.ListConnectorInstancesRequest{Connector: "stub"})
	if err != nil || len(list.Instances) != 1 || list.Instances[0].Id != id || !strings.Contains(strings.Join(list.Instances[0].Ops, ","), "echo") {
		t.Fatalf("list = %+v %v", list, err)
	}

	out, err := srv.ExecuteConnector(asUser("admin"), &hostpb.ExecuteConnectorRequest{Connector: "stub", Op: "echo", ArgsJson: []byte(`{"q":"hi"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.ResultJson), secret) || !strings.Contains(string(out.ResultJson), "wick_enc_") {
		t.Fatalf("secret not masked: %s", out.ResultJson)
	}
	runs, err := svc.ListRuns(ctx, id, 1)
	if err != nil || len(runs) != 1 || runs[0].Source != entity.ConnectorRunSourcePlugin {
		t.Fatalf("run = %+v %v", runs, err)
	}

	// Restrict the instance to a filter tag "plain" does not carry: a user
	// who cannot see it gets NotFound, named or not.
	if err := db.Create(&entity.Tag{ID: "tag-ops", Name: "ops", IsFilter: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entity.ToolTag{ToolPath: "/connectors/" + id, TagID: "tag-ops"}).Error; err != nil {
		t.Fatal(err)
	}
	if l, err := srv.ListConnectorInstances(asUser("plain"), &hostpb.ListConnectorInstancesRequest{Connector: "stub"}); err != nil || len(l.Instances) != 0 {
		t.Fatalf("plain sees %+v %v", l, err)
	}
	if _, err := srv.ExecuteConnector(asUser("plain"), &hostpb.ExecuteConnectorRequest{Connector: "stub", Op: "echo"}); status.Code(err) != codes.NotFound {
		t.Fatalf("auto-pick for plain err = %v", err)
	}
	if _, err := srv.ExecuteConnector(asUser("plain"), &hostpb.ExecuteConnectorRequest{Connector: "stub", Op: "echo", InstanceId: id}); status.Code(err) != codes.NotFound {
		t.Fatalf("invisible instance err = %v", err)
	}
	// A declared op on another module cannot borrow this instance.
	if _, err := srv.ExecuteConnector(asUser("admin"), &hostpb.ExecuteConnectorRequest{Connector: "other", Op: "echo", InstanceId: id}); status.Code(err) != codes.NotFound {
		t.Fatalf("other module err = %v", err)
	}
}

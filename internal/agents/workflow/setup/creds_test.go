package setup

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	connectorsvc "github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/pkg/postgres"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

func credsTestSvc(t *testing.T) (*connectorsvc.Service, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: postgres.NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)

	cfgs := configs.NewService(db)
	if err := cfgs.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	svc := connectorsvc.NewServiceFromDB(db)
	svc.SetConfigs(cfgs)
	mod := pkgconnector.Module{
		Meta: pkgconnector.Meta{Key: "stub", Name: "Stub"},
		Operations: []pkgconnector.Category{
			pkgconnector.Cat("", "", pkgconnector.Operation{
				Key: "echo", Name: "Echo",
				Execute: func(*pkgconnector.Ctx) (any, error) { return nil, nil },
			}),
		},
	}
	if err := svc.Bootstrap(context.Background(), []pkgconnector.Module{mod}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	row, err := svc.Create(context.Background(), "stub", "Stub", map[string]string{}, "u-owner")
	if err != nil {
		t.Fatalf("create row: %v", err)
	}
	return svc, row.ID
}

// The adapter injects the selected account's token as user_token, and
// rejects an account that belongs to a different instance. Access itself
// is enforced upstream (tags); this only asserts the token plumbing +
// data-integrity check.
func TestConnectorsCredsAdapter_AccountToken(t *testing.T) {
	ctx := context.Background()
	svc, rowID := credsTestSvc(t)
	adapter := ConnectorsCredsAdapter(svc)

	// No account → plain row creds (no user_token).
	creds, err := adapter("stub", rowID, "")
	if err != nil {
		t.Fatalf("adapter(no account): %v", err)
	}
	if _, ok := creds["user_token"]; ok {
		t.Errorf("did not expect user_token without account, got %v", creds)
	}

	// Save an account, then bind it → its token is injected.
	if err := svc.SaveAccount(ctx, rowID, "u-1", "ext-1", "yoga@example.com", "tok-abc"); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	accs, err := svc.ListAccounts(ctx, rowID)
	if err != nil || len(accs) != 1 {
		t.Fatalf("ListAccounts: %v (n=%d)", err, len(accs))
	}
	creds, err = adapter("stub", rowID, accs[0].ID)
	if err != nil {
		t.Fatalf("adapter(with account): %v", err)
	}
	if creds["user_token"] != "tok-abc" {
		t.Errorf("user_token = %q, want tok-abc", creds["user_token"])
	}

	// An account id that doesn't exist → error (integrity check).
	if _, err := adapter("stub", rowID, "does-not-exist"); err == nil {
		t.Error("expected error for unknown account id")
	}
}

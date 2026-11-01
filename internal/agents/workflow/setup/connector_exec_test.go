package setup

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/configs"
	connectorsvc "github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

func execTestSvc(t *testing.T) (*connectorsvc.Service, string) {
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
		Meta:    pkgconnector.Meta{Key: "stub", Name: "Stub"},
		Configs: []entity.Config{{Key: "api_token", Type: "text", IsSecret: true}},
		Operations: []pkgconnector.Category{
			pkgconnector.Cat("", "", pkgconnector.Operation{
				Key: "echo", Name: "Echo",
				// Reports what the op saw without echoing a secret back
				// (the response is masked).
				Execute: func(c *pkgconnector.Ctx) (any, error) {
					return map[string]any{
						"token_ok":      c.Cfg("api_token") == "plain-api-key",
						"user_token_ok": c.Cfg("user_token") == "tok-abc",
					}, nil
				},
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

func runEcho(t *testing.T, exec connector.ExecuteFn, ownerID, instance, accountID string) (map[string]any, error) {
	t.Helper()
	raw, err := exec(context.Background(), connector.Call{
		Module: "stub", Op: "echo", Instance: instance, AccountID: accountID,
		Owner: &entity.User{ID: ownerID},
	})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return out, nil
}

// A secret saved as a wick_enc_ token reaches the op as plaintext when the
// workflow owner is the one it was encrypted for — the node runs through
// connectors.Service.Execute, exactly like a direct call. Another user's
// key cannot open it, and that is an error, not a ciphertext sent upstream.
func TestConnectorsExecutor_SecretOpensUnderOwner(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	ctx := context.Background()
	encSvc, err := enc.New("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, rowID := execTestSvc(t)
	svc.SetEnc(encSvc)
	tok, err := encSvc.EncryptValue("plain-api-key", "u-owner")
	if err != nil {
		t.Fatalf("EncryptValue: %v", err)
	}
	if err := svc.Update(ctx, rowID, "Stub", map[string]string{"api_token": tok}, false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	exec := ConnectorsExecutor(svc)

	out, err := runEcho(t, exec, "u-owner", "", "")
	if err != nil {
		t.Fatalf("owner run: %v", err)
	}
	if out["token_ok"] != true {
		t.Errorf("op did not get the decrypted token: %v", out)
	}
	if _, err := runEcho(t, exec, "u-other", "", ""); err == nil {
		t.Error("expected an error when the owner's key cannot open the token")
	}
}

// The instance is picked by ID or label (case-insensitive); empty picks the
// newest one; a name matching none is an error that names it.
func TestConnectorsExecutor_PicksInstance(t *testing.T) {
	ctx := context.Background()
	svc, firstID := execTestSvc(t)
	second, err := svc.Create(ctx, "stub", "Second", map[string]string{}, "u-owner")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	exec := ConnectorsExecutor(svc)

	for _, name := range []string{firstID, "stub", "second", second.ID, ""} {
		if _, err := runEcho(t, exec, "u-owner", name, ""); err != nil {
			t.Errorf("instance %q: %v", name, err)
		}
	}
	if _, err := runEcho(t, exec, "u-owner", "no-such-instance", ""); err == nil || !strings.Contains(err.Error(), "no-such-instance") {
		t.Errorf("unknown instance: want an error naming it, got %v", err)
	}
	row, err := resolveInstance(ctx, svc, "stub", "")
	if err != nil || row.ID != second.ID {
		t.Errorf("empty instance resolved to %v (%v), want the newest %s", row, err, second.ID)
	}
}

// A connected account the node names runs the op with that account's token.
func TestConnectorsExecutor_AccountToken(t *testing.T) {
	ctx := context.Background()
	svc, rowID := execTestSvc(t)
	if err := svc.SaveAccount(ctx, rowID, "u-1", "ext-1", "dev@example.com", "tok-abc"); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	accs, err := svc.ListAccounts(ctx, rowID)
	if err != nil || len(accs) != 1 {
		t.Fatalf("ListAccounts: %v (n=%d)", err, len(accs))
	}
	exec := ConnectorsExecutor(svc)

	out, err := runEcho(t, exec, "u-1", rowID, accs[0].ID)
	if err != nil {
		t.Fatalf("run as account: %v", err)
	}
	if out["user_token_ok"] != true {
		t.Errorf("op did not get the account token: %v", out)
	}
	out, err = runEcho(t, exec, "u-1", rowID, "")
	if err != nil {
		t.Fatalf("run without account: %v", err)
	}
	if out["user_token_ok"] == true {
		t.Error("user_token must not be set without an account")
	}
}

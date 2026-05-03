package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
)

const testEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newSQLite(t *testing.T) *gorm.DB {
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
	return db
}

// echoModule returns a connector with one Configs field and one
// Input field both tagged `secret`. The op echoes configs.token +
// input.password back so we can verify masking covers both surfaces.
func echoModule() connector.Module {
	type Creds struct {
		Token string `wick:"required;secret"`
	}
	type EchoInput struct {
		Password string `wick:"required;secret"`
	}
	return connector.Module{
		Meta: connector.Meta{Key: "stub", Name: "Stub", Description: "test"},
		Configs: []entity.Config{
			{Key: "token", Type: "text", IsSecret: true, Required: true},
		},
		Operations: []connector.Operation{
			connector.Op("echo", "Echo", "echo input + cfg",
				EchoInput{},
				func(c *connector.Ctx) (any, error) {
					return map[string]string{
						"echoed_token":    c.Cfg("token"),
						"echoed_password": c.Input("password"),
					}, nil
				},
			),
		},
	}
}

func newSvcWithStub(t *testing.T, encSvc *enc.Service) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	svc := NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	if err := svc.Bootstrap(context.Background(), []connector.Module{echoModule()}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, _ := svc.List(context.Background())
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	return svc, rows[0].ID
}

func TestExecuteMasksSensitiveCredsInResponse(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, connID := newSvcWithStub(t, encSvc)

	plaintextToken := "super-secret-token-12345"
	if err := svc.Update(context.Background(), connID, "Stub",
		map[string]string{"token": plaintextToken}, false); err != nil {
		t.Fatalf("seed configs: %v", err)
	}

	res, err := svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  connID,
		OperationKey: "echo",
		Input:        map[string]string{"password": "hunter2-long-pass"},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-A",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.ResponseJSON, plaintextToken) {
		t.Fatalf("plaintext token leaked in response: %s", res.ResponseJSON)
	}
	if strings.Contains(res.ResponseJSON, "hunter2-long-pass") {
		t.Fatalf("plaintext password leaked: %s", res.ResponseJSON)
	}
	if !strings.Contains(res.ResponseJSON, "wick_enc_") {
		t.Fatalf("expected wick_enc_ token in response: %s", res.ResponseJSON)
	}
}

func TestExecuteDecryptsTokenInputBeforeCallingConnector(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, connID := newSvcWithStub(t, encSvc)

	plaintextPwd := "user-A-real-password"
	token, err := encSvc.EncryptValue(plaintextPwd, "user-A")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if err := svc.Update(context.Background(), connID, "Stub",
		map[string]string{"token": "long-cfg-token-aaa"}, false); err != nil {
		t.Fatalf("seed configs: %v", err)
	}

	res, err := svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  connID,
		OperationKey: "echo",
		Input:        map[string]string{"password": token},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-A",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Response should NOT contain the plaintext (it was masked back),
	// but the connector itself should have received plaintext — the
	// echo response shows that by carrying the wick_enc_ token (since
	// we encrypt the plaintext output).
	if strings.Contains(res.ResponseJSON, plaintextPwd) {
		t.Fatalf("plaintext leaked through: %s", res.ResponseJSON)
	}
	if !strings.Contains(res.ResponseJSON, "wick_enc_") {
		t.Fatalf("response should carry token: %s", res.ResponseJSON)
	}

	// Audit row stores the pre-decrypt request — must contain the
	// original wick_enc_ token, NOT plaintext.
	runs, err := svc.ListRuns(context.Background(), connID, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("list runs: %v len=%d", err, len(runs))
	}
	if !strings.Contains(runs[0].RequestJSON, "wick_enc_") {
		t.Fatalf("audit request should carry token: %s", runs[0].RequestJSON)
	}
	if strings.Contains(runs[0].RequestJSON, plaintextPwd) {
		t.Fatalf("audit request leaked plaintext: %s", runs[0].RequestJSON)
	}
}

func TestExecuteCrossUserTokenFails(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, connID := newSvcWithStub(t, encSvc)

	tokenA, _ := encSvc.EncryptValue("user-A-pass-12345", "user-A")
	if err := svc.Update(context.Background(), connID, "Stub",
		map[string]string{"token": "long-cfg-token"}, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err = svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  connID,
		OperationKey: "echo",
		Input:        map[string]string{"password": tokenA},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-B",
	})
	if err == nil {
		t.Fatal("expected cross-user decrypt to fail")
	}
}

func TestExecuteDisabledEncIsPassthrough(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "true")
	encSvc, err := enc.New("")
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, connID := newSvcWithStub(t, encSvc)

	plaintext := "still-plain-hello-12345"
	if err := svc.Update(context.Background(), connID, "Stub",
		map[string]string{"token": plaintext}, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  connID,
		OperationKey: "echo",
		Input:        map[string]string{"password": "another-plain-67890"},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-A",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.ResponseJSON, plaintext) {
		t.Fatalf("disabled enc should keep plaintext: %s", res.ResponseJSON)
	}
	if strings.Contains(res.ResponseJSON, "wick_enc_") {
		t.Fatalf("disabled enc should NOT mint tokens: %s", res.ResponseJSON)
	}
}

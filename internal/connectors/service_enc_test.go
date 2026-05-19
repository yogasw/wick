package connectors

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
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
				}, wickdocs.Docs{},
			),
		},
	}
}

func newSvcWithStub(t *testing.T, encSvc *enc.Service) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	svc := NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgsSvc)
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

// echoNonSecretModule has an Input field that is NOT tagged secret.
// Used to verify that a wick_enc_ token the LLM passes into a plain
// field still round-trips back as a token in the response — the
// auto-mask layer must not depend on tag discipline alone.
func echoNonSecretModule() connector.Module {
	type Creds struct {
		Token string `wick:"required;secret"`
	}
	type EchoInput struct {
		Note string `wick:"required"` // ← intentionally NOT secret
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
						"echoed_token": c.Cfg("token"),
						"echoed_note":  c.Input("note"),
					}, nil
				}, wickdocs.Docs{},
			),
		},
	}
}

func newSvcWithModule(t *testing.T, encSvc *enc.Service, mod connector.Module) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	svc := NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(context.Background(), []connector.Module{mod}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, _ := svc.List(context.Background())
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	return svc, rows[0].ID
}

// LLM passes a wick_enc_ token into an Input field that carries NO
// `secret` tag (the LLM treats wick_enc_ as opaque per the contract,
// so it may show up in any field). The connector echoes the field
// back in its response. The plaintext MUST NOT leak — the framework
// remembers every plaintext produced by decrypt and re-masks the
// response automatically.
func TestExecuteRoundTripsTokenInNonSecretInput(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, connID := newSvcWithModule(t, encSvc, echoNonSecretModule())

	plaintext := "carried-across-call-abc-123"
	token, err := encSvc.EncryptValue(plaintext, "user-A")
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
		Input:        map[string]string{"note": token},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-A",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.ResponseJSON, plaintext) {
		t.Fatalf("plaintext leaked through non-secret field: %s", res.ResponseJSON)
	}
	if !strings.Contains(res.ResponseJSON, "wick_enc_") {
		t.Fatalf("response should carry token: %s", res.ResponseJSON)
	}
}

// echoMaskModule has the connector itself call c.Mask on a dynamic
// value pulled mid-call, then echo that same value in a different
// response field. Auto-mask must catch the second occurrence — the
// values passed to c.Mask are remembered for the post-Execute sweep.
func echoMaskModule() connector.Module {
	type Creds struct {
		Token string `wick:"required;secret"`
	}
	type EchoInput struct {
		Note string `wick:"required"`
	}
	return connector.Module{
		Meta: connector.Meta{Key: "stub", Name: "Stub", Description: "test"},
		Configs: []entity.Config{
			{Key: "token", Type: "text", IsSecret: true, Required: true},
		},
		Operations: []connector.Operation{
			connector.Op("echo", "Echo", "echo input + dynamic mask",
				EchoInput{},
				func(c *connector.Ctx) (any, error) {
					dyn := "dynamic-session-cookie-9999"
					_ = c.Mask("ignored", []string{dyn}) // returns string is dropped — only side-effect (record) matters
					return map[string]string{
						"raw_field": dyn, // intentionally raw — middleware must catch it
					}, nil
				}, wickdocs.Docs{},
			),
		},
	}
}

func TestExecuteAutoMasksValuesPassedToCMask(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, connID := newSvcWithModule(t, encSvc, echoMaskModule())

	if err := svc.Update(context.Background(), connID, "Stub",
		map[string]string{"token": "long-cfg-token-aaa"}, false); err != nil {
		t.Fatalf("seed configs: %v", err)
	}

	res, err := svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  connID,
		OperationKey: "echo",
		Input:        map[string]string{"note": "hello"},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-A",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.ResponseJSON, "dynamic-session-cookie-9999") {
		t.Fatalf("c.Mask value leaked in raw_field: %s", res.ResponseJSON)
	}
	if !strings.Contains(res.ResponseJSON, "wick_enc_") {
		t.Fatalf("response should carry token: %s", res.ResponseJSON)
	}
}

// errLeakModule echoes the configs token inside an error message.
// Connector authors sometimes wrap upstream error bodies (or, worse,
// credentials themselves) into the error string — auto-mask must
// cover that path too, otherwise the audit log row's error_message
// and the LLM-visible error both leak plaintext.
func errLeakModule() connector.Module {
	type Creds struct {
		Token string `wick:"required;secret"`
	}
	type EchoInput struct {
		Note string `wick:"required"`
	}
	return connector.Module{
		Meta: connector.Meta{Key: "stub", Name: "Stub", Description: "test"},
		Configs: []entity.Config{
			{Key: "token", Type: "text", IsSecret: true, Required: true},
		},
		Operations: []connector.Operation{
			connector.Op("fail", "Fail", "always returns error containing token",
				EchoInput{},
				func(c *connector.Ctx) (any, error) {
					return nil, fmt.Errorf("upstream auth failed for token %q", c.Cfg("token"))
				}, wickdocs.Docs{},
			),
		},
	}
}

func TestExecuteMasksErrorMessageLeakingCreds(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, connID := newSvcWithModule(t, encSvc, errLeakModule())

	plaintextToken := "leak-via-error-token-abcdef"
	if err := svc.Update(context.Background(), connID, "Stub",
		map[string]string{"token": plaintextToken}, false); err != nil {
		t.Fatalf("seed configs: %v", err)
	}

	res, execErr := svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  connID,
		OperationKey: "fail",
		Input:        map[string]string{"note": "hello"},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "user-A",
	})
	if execErr == nil {
		t.Fatal("expected error from connector")
	}
	if res == nil {
		t.Fatal("expected ExecuteResult even on error")
	}
	if strings.Contains(res.ErrorMessage, plaintextToken) {
		t.Fatalf("plaintext leaked in ErrorMessage: %s", res.ErrorMessage)
	}
	if !strings.Contains(res.ErrorMessage, "wick_enc_") {
		t.Fatalf("error message should carry token: %s", res.ErrorMessage)
	}

	// audit row's error_message must also be masked.
	runs, err := svc.ListRuns(context.Background(), connID, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("list runs: %v len=%d", err, len(runs))
	}
	if strings.Contains(runs[0].ErrorMsg, plaintextToken) {
		t.Fatalf("audit error_message leaked: %s", runs[0].ErrorMsg)
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

package connectors

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
)

// TestExecuteSessionInstanceRunsBaseModuleWithDecryptedConfig verifies
// the virtual Execute path: a SessionInstanceTarget runs the base module
// with the instance's own config (no DB row), and wick_cenc_ master
// tokens in that config are decrypted before the connector sees them
// (then re-masked on the way out).
func TestExecuteSessionInstanceRunsBaseModuleWithDecryptedConfig(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, _ := newSvcWithStub(t, encSvc) // registers the "stub" base module

	plaintext := "staging-token-xyz-123"
	masterTok, err := encSvc.EncryptMaster(plaintext)
	if err != nil {
		t.Fatalf("encrypt master: %v", err)
	}

	res, err := svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  "sw_test-instance", // synthetic id, no DB row
		OperationKey: "echo",
		Input:        map[string]string{"password": "pw-aaaaaa"},
		Source:       entity.ConnectorRunSourceTest,
		UserID:       "user-A",
		SessionInstance: &SessionInstanceTarget{
			BaseKey: "stub",
			Label:   "Stub (session)",
			Config:  map[string]string{"token": masterTok},
		},
	})
	if err != nil {
		t.Fatalf("virtual execute failed (no DB row should be needed): %v", err)
	}
	// Op ran (echo returns echoed_token), proving the base module was
	// cloned and executed via the virtual path.
	if !strings.Contains(res.ResponseJSON, "echoed_token") {
		t.Fatalf("op did not run: %s", res.ResponseJSON)
	}
	// The connector received the DECRYPTED token (not the master token),
	// and the masker re-tokenized it on the way out — so neither the
	// plaintext nor the raw wick_cenc_ token appears in the response.
	if strings.Contains(res.ResponseJSON, plaintext) {
		t.Fatalf("plaintext config leaked: %s", res.ResponseJSON)
	}
	if strings.Contains(res.ResponseJSON, masterTok) {
		t.Fatalf("master token reached the connector unresolved: %s", res.ResponseJSON)
	}
}

// TestExecuteSessionInstanceUnknownBaseKey errors cleanly when the base
// module isn't registered (e.g. a def deleted mid-session).
func TestExecuteSessionInstanceUnknownBaseKey(t *testing.T) {
	encSvc, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc, _ := newSvcWithStub(t, encSvc)

	_, err = svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  "sw_gone",
		OperationKey: "echo",
		Input:        map[string]string{},
		Source:       entity.ConnectorRunSourceTest,
		UserID:       "u",
		SessionInstance: &SessionInstanceTarget{
			BaseKey: "no-such-module",
			Config:  map[string]string{},
		},
	})
	if err == nil {
		t.Fatal("expected error for unregistered base module")
	}
}

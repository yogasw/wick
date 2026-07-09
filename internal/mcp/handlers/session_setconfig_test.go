package handlers

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
)

// 32-byte (64 hex char) master key for the encrypt-path test.
const testMasterKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func specMap() map[string]entity.Config {
	return map[string]entity.Config{
		"base_url": {Key: "base_url", Required: true},
		"api_key":  {Key: "api_key", Required: true, IsSecret: true},
	}
}

func TestStoreSessionConfig_TokenPassthrough(t *testing.T) {
	layout, sid := newTestLayout(t)
	inst, err := sessionworkspace.Add(layout, sid, sessionworkspace.Instance{BaseKey: "httprest", Label: "T"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Enc is nil on this Service — a token must pass through WITHOUT ever
	// touching svc.Enc(), proving the automation path never needs plaintext.
	svc := connectors.NewService(nil)

	applied, err := storeSessionConfig(svc, layout, sid, inst.ID, specMap(), map[string]string{
		"base_url": "https://staging.example.com",
		"api_key":  "wick_cenc_sometoken",
	})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("applied = %v, want 2 keys", applied)
	}
	got, _, _ := sessionworkspace.Get(layout, sid, inst.ID)
	if got.Config["api_key"] != "wick_cenc_sometoken" {
		t.Fatalf("secret token not stored verbatim: %q", got.Config["api_key"])
	}
	if got.Config["base_url"] != "https://staging.example.com" {
		t.Fatalf("non-secret not stored: %q", got.Config["base_url"])
	}
}

func TestStoreSessionConfig_PlaintextSecretEncrypted(t *testing.T) {
	layout, sid := newTestLayout(t)
	inst, err := sessionworkspace.Add(layout, sid, sessionworkspace.Instance{BaseKey: "httprest", Label: "T"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	e, err := enc.New(testMasterKeyHex)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	svc := connectors.NewService(nil)
	svc.SetEnc(e)

	if _, err := storeSessionConfig(svc, layout, sid, inst.ID, specMap(), map[string]string{
		"api_key": "raw-secret-value",
	}); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, _, _ := sessionworkspace.Get(layout, sid, inst.ID)
	if !enc.IsMasterToken(got.Config["api_key"]) {
		t.Fatalf("plaintext secret was not master-encrypted: %q", got.Config["api_key"])
	}
	if got.Config["api_key"] == "raw-secret-value" {
		t.Fatal("plaintext secret stored verbatim — must never happen")
	}
}

func TestStoreSessionConfig_EmptyAndUnknownSkipped(t *testing.T) {
	layout, sid := newTestLayout(t)
	inst, err := sessionworkspace.Add(layout, sid, sessionworkspace.Instance{BaseKey: "httprest", Label: "T"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	svc := connectors.NewService(nil)

	// Empty value = skip; unknown key = skip (dispatcher rejects unknowns
	// before calling this, but the helper itself must not panic on one).
	applied, err := storeSessionConfig(svc, layout, sid, inst.ID, specMap(), map[string]string{
		"base_url": "",
		"nope":     "x",
	})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %v, want none", applied)
	}
}

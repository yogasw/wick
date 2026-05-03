package configs

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
)

const testEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newEncSvcForTest(t *testing.T) *enc.Service {
	t.Helper()
	t.Setenv("WICK_ENC_DISABLE", "")
	e, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	return e
}

func TestSetOwnedEncryptsSecretAtRest(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "url", Value: "http://abc.com"},
		entity.Config{Key: "token", Type: "secret", IsSecret: true},
	); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	svc.SetEncryptor(newEncSvcForTest(t))

	if err := svc.SetOwned(ctx, "connector:abc", "token", "salam123"); err != nil {
		t.Fatalf("set: %v", err)
	}

	// DB row stored as ciphertext.
	row, err := svc.repo.FindByOwnerKey(ctx, "connector:abc", "token")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !enc.IsMasterToken(row.Value) {
		t.Fatalf("expected wick_cenc_ token in DB, got %q", row.Value)
	}
	if strings.Contains(row.Value, "salam123") {
		t.Fatalf("plaintext leaked into DB: %q", row.Value)
	}
	// Cache returns plaintext.
	if got := svc.GetOwned("connector:abc", "token"); got != "salam123" {
		t.Fatalf("cache should hold plaintext, got %q", got)
	}
}

func TestSetOwnedNonSecretStaysPlaintext(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "url", Value: ""},
	); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	svc.SetEncryptor(newEncSvcForTest(t))

	if err := svc.SetOwned(ctx, "connector:abc", "url", "http://abc.com"); err != nil {
		t.Fatalf("set: %v", err)
	}
	row, err := svc.repo.FindByOwnerKey(ctx, "connector:abc", "url")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if row.Value != "http://abc.com" {
		t.Fatalf("non-secret should stay plaintext, got %q", row.Value)
	}
}

func TestSetOwnedSecretEmptyKeepsExisting(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "token", Type: "secret", IsSecret: true},
	); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	svc.SetEncryptor(newEncSvcForTest(t))
	if err := svc.SetOwned(ctx, "connector:abc", "token", "first-value"); err != nil {
		t.Fatalf("set 1: %v", err)
	}
	if err := svc.SetOwned(ctx, "connector:abc", "token", ""); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	if got := svc.GetOwned("connector:abc", "token"); got != "first-value" {
		t.Fatalf("empty submit should preserve, got %q", got)
	}
}

func TestSetEncryptorMigratesPlaintextLegacyRows(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	// Seed plaintext IsSecret row before encryptor is wired (legacy path).
	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "token", Type: "secret", IsSecret: true, Value: "legacy-plain"},
	); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	row, _ := svc.repo.FindByOwnerKey(ctx, "connector:abc", "token")
	if row.Value != "legacy-plain" {
		t.Fatalf("seed plaintext failed: %q", row.Value)
	}

	svc.SetEncryptor(newEncSvcForTest(t))

	row, _ = svc.repo.FindByOwnerKey(ctx, "connector:abc", "token")
	if !enc.IsMasterToken(row.Value) {
		t.Fatalf("legacy plaintext should be migrated to ciphertext, got %q", row.Value)
	}
	// Cache plaintext is unchanged either way (was already plaintext).
	if got := svc.GetOwned("connector:abc", "token"); got != "legacy-plain" {
		t.Fatalf("cache should still resolve to plaintext, got %q", got)
	}
}

func TestSetEncryptorDecryptsExistingCiphertextIntoCache(t *testing.T) {
	// Two-boot scenario: first boot encrypts, second boot starts cold.
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "token", Type: "secret", IsSecret: true},
	); err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	svc.SetEncryptor(newEncSvcForTest(t))
	if err := svc.SetOwned(ctx, "connector:abc", "token", "real-secret"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Sanity: DB ciphertext.
	row, _ := svc.repo.FindByOwnerKey(ctx, "connector:abc", "token")
	if !enc.IsMasterToken(row.Value) {
		t.Fatalf("DB not ciphertext: %q", row.Value)
	}

	// Simulate cold boot: new Service over the same DB.
	svc2 := NewService(svc.repo.db)
	if err := svc2.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "token", Type: "secret", IsSecret: true},
	); err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	// Before SetEncryptor — cache holds ciphertext.
	if got := svc2.GetOwned("connector:abc", "token"); !enc.IsMasterToken(got) {
		t.Fatalf("pre-wire cache should hold ciphertext, got %q", got)
	}
	svc2.SetEncryptor(newEncSvcForTest(t))
	if got := svc2.GetOwned("connector:abc", "token"); got != "real-secret" {
		t.Fatalf("post-wire cache should hold plaintext, got %q", got)
	}
}

func TestSetOwnedSkipsEncryptionKey(t *testing.T) {
	// The encryption_key row itself must never be encrypted (chicken
	// and egg). Bootstrap seeds it with a plaintext hex blob; Set on
	// it should leave the value as-is in the DB.
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	svc.SetEncryptor(newEncSvcForTest(t))

	newKey := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := svc.Set(ctx, KeyEncryptionKey, newKey); err != nil {
		t.Fatalf("set: %v", err)
	}
	row, err := svc.repo.FindByOwnerKey(ctx, "", KeyEncryptionKey)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if row.Value != newKey {
		t.Fatalf("encryption_key should stay plaintext, got %q", row.Value)
	}
}

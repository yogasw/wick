package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/enc"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

const testEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// ── channelSecretKeys ─────────────────────────────────────────────────────────

func TestChannelSecretKeys_Slack(t *testing.T) {
	keys := channelSecretKeys("slack")
	seed := agentconfig.SeedSlackChannelConfig()
	for _, r := range seed {
		if r.IsSecret && !keys[r.Key] {
			t.Errorf("slack secret key %q missing from channelSecretKeys", r.Key)
		}
		if !r.IsSecret && keys[r.Key] {
			t.Errorf("slack non-secret key %q should not be in channelSecretKeys", r.Key)
		}
	}
}

func TestChannelSecretKeys_Telegram(t *testing.T) {
	keys := channelSecretKeys("telegram")
	seed := agentconfig.SeedTelegramChannelConfig()
	for _, r := range seed {
		if r.IsSecret && !keys[r.Key] {
			t.Errorf("telegram secret key %q missing from channelSecretKeys", r.Key)
		}
		if !r.IsSecret && keys[r.Key] {
			t.Errorf("telegram non-secret key %q should not be in channelSecretKeys", r.Key)
		}
	}
}

func TestChannelSecretKeys_REST(t *testing.T) {
	keys := channelSecretKeys("rest")
	if len(keys) != 0 {
		t.Errorf("rest channel should have no secret keys, got %v", keys)
	}
}

func TestChannelSecretKeys_Unknown(t *testing.T) {
	keys := channelSecretKeys("unknown")
	if len(keys) != 0 {
		t.Errorf("unknown channel should have no secret keys, got %v", keys)
	}
}

// ── EncryptSecret / DecryptSecret round-trip ──────────────────────────────────

func newTestConfigs(t *testing.T) *testConfigsStub {
	t.Helper()
	e, err := enc.New(testEncKey)
	if err != nil {
		t.Fatalf("enc.New: %v", err)
	}
	return &testConfigsStub{enc: e}
}

// testConfigsStub mimics the EncryptSecret/DecryptSecret surface of
// *configs.Service without pulling in a DB.
type testConfigsStub struct{ enc *enc.Service }

func (s *testConfigsStub) EncryptSecret(plain string) (string, error) {
	if plain == "" || enc.IsToken(plain) || enc.IsMasterToken(plain) {
		return plain, nil
	}
	return s.enc.EncryptMaster(plain)
}

func (s *testConfigsStub) DecryptSecret(token string) (string, error) {
	if !enc.IsMasterToken(token) {
		return token, nil
	}
	return s.enc.DecryptMaster(token)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	svc := newTestConfigs(t)

	plain := "xoxb-secret-token"
	token, err := svc.EncryptSecret(plain)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if !enc.IsMasterToken(token) {
		t.Fatalf("expected wick_cenc_ token, got %q", token)
	}

	got, err := svc.DecryptSecret(token)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if got != plain {
		t.Errorf("round-trip mismatch: want %q, got %q", plain, got)
	}
}

func TestEncryptSecret_EmptyPassthrough(t *testing.T) {
	svc := newTestConfigs(t)
	got, err := svc.EncryptSecret("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
}

func TestEncryptSecret_AlreadyTokenPassthrough(t *testing.T) {
	svc := newTestConfigs(t)
	token, _ := svc.EncryptSecret("some-value")
	// encrypting an already-encrypted token should not double-encrypt
	again, err := svc.EncryptSecret(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if again != token {
		t.Errorf("already-token should pass through unchanged")
	}
}

func TestDecryptSecret_PlaintextPassthrough(t *testing.T) {
	svc := newTestConfigs(t)
	plain := "not-a-token"
	got, err := svc.DecryptSecret(plain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != plain {
		t.Errorf("plaintext should pass through unchanged, got %q", got)
	}
}

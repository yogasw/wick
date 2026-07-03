package provider

import "testing"

func TestRouter9BaseURL(t *testing.T) {
	t.Setenv("WICK_PORT", "9425")
	if got := Router9BaseURL(); got != "http://127.0.0.1:9425/9router/v1" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("WICK_PORT", "")
	if got := Router9BaseURL(); got != "" {
		t.Fatalf("empty WICK_PORT should give empty base, got %q", got)
	}
}

func TestRouter9AuthKeyDefault(t *testing.T) {
	// No custom key → default sk_9router.
	if got := Router9AuthKey(Instance{}); got != router9DefaultKey {
		t.Fatalf("default key: got %q want %q", got, router9DefaultKey)
	}
	// Custom key, no decrypter wired → passes through.
	SetSecretDecrypter(nil)
	if got := Router9AuthKey(Instance{Router9APIKey: "plainkey"}); got != "plainkey" {
		t.Fatalf("passthrough: got %q", got)
	}
	// Decrypter wired → unwraps.
	SetSecretDecrypter(func(s string) (string, error) { return "decrypted", nil })
	defer SetSecretDecrypter(nil)
	if got := Router9AuthKey(Instance{Router9APIKey: "wick_cenc_x"}); got != "decrypted" {
		t.Fatalf("decrypt: got %q", got)
	}
}

func TestRouter9Slots(t *testing.T) {
	if len(Router9Slots(TypeClaude)) != 3 {
		t.Error("claude should have 3 slots")
	}
	if len(Router9Slots(TypeCodex)) != 2 {
		t.Error("codex should have 2 slots")
	}
	if Router9Slots(TypeGemini) != nil {
		t.Error("gemini should have no slots")
	}
}

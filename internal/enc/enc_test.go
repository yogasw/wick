package enc

import (
	"strings"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("WICK_ENC_DISABLE", "")
	s, err := New(testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestRoundTrip(t *testing.T) {
	s := newTestService(t)
	plain := "s3cr3t-password"
	token, err := s.EncryptValue(plain, "user-1")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsToken(token) {
		t.Fatalf("not a token: %q", token)
	}
	got, err := s.DecryptValue(token, "user-1")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plain {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plain)
	}
}

func TestCrossUserDecryptFails(t *testing.T) {
	s := newTestService(t)
	token, err := s.EncryptValue("alpha-bravo-charlie", "user-A")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := s.DecryptValue(token, "user-B"); err == nil {
		t.Fatal("expected cross-user decrypt to fail")
	}
}

func TestEncryptShortValueRoundTrips(t *testing.T) {
	s := newTestService(t)
	plain := "abc"
	token, err := s.EncryptValue(plain, "user-1")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsToken(token) {
		t.Fatalf("short value should still mint a token, got %q", token)
	}
	got, err := s.DecryptValue(token, "user-1")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plain {
		t.Fatalf("round-trip: got %q want %q", got, plain)
	}
}

func TestMaskAndUnmask(t *testing.T) {
	s := newTestService(t)
	creds := []string{"super-secret-pass", "another-cred-value"}
	body := `{"username":"alice","password":"super-secret-pass","backup":"super-secret-pass","other":"another-cred-value"}`
	masked := s.Mask(body, creds, "u-1")
	if strings.Contains(masked, "super-secret-pass") {
		t.Fatalf("plaintext leaked: %s", masked)
	}
	if strings.Count(masked, "wick_enc_") != 3 {
		t.Fatalf("expected 3 tokens, got body=%s", masked)
	}
	// Same plaintext must mint the same token within one call.
	tokens := tokenRegex.FindAllString(masked, -1)
	first := ""
	for _, tok := range tokens {
		plain, err := s.DecryptValue(tok, "u-1")
		if err != nil {
			t.Fatalf("decrypt %q: %v", tok, err)
		}
		if plain == "super-secret-pass" {
			if first == "" {
				first = tok
				continue
			}
			if tok != first {
				t.Fatalf("identical plaintext got different tokens: %q vs %q", first, tok)
			}
		}
	}
	roundtrip, err := s.Unmask(masked, "u-1")
	if err != nil {
		t.Fatalf("unmask: %v", err)
	}
	if roundtrip != body {
		t.Fatalf("unmask round-trip mismatch:\n got %s\nwant %s", roundtrip, body)
	}
}

func TestUnmaskWrongUserErrors(t *testing.T) {
	s := newTestService(t)
	masked := s.Mask("password=super-secret-pass", []string{"super-secret-pass"}, "u-A")
	if _, err := s.Unmask(masked, "u-B"); err == nil {
		t.Fatal("expected unmask to fail for different user")
	}
}

func TestDisabledServicePassesThrough(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "true")
	s, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !s.Disabled() {
		t.Fatal("expected disabled")
	}
	body := `{"password":"super-secret-pass"}`
	masked := s.Mask(body, []string{"super-secret-pass"}, "u")
	if masked != body {
		t.Fatalf("disabled mask should be passthrough, got %q", masked)
	}
	out, err := s.Unmask(body, "u")
	if err != nil {
		t.Fatalf("disabled unmask: %v", err)
	}
	if out != body {
		t.Fatalf("disabled unmask should be passthrough, got %q", out)
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	if _, err := New(""); err == nil {
		t.Fatal("empty key should error")
	}
	if _, err := New("not-hex"); err == nil {
		t.Fatal("non-hex key should error")
	}
	if _, err := New("aabb"); err == nil {
		t.Fatal("short hex key should error")
	}
}

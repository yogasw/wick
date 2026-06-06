package enc

import (
	"strings"
	"testing"
)

// When a shorter secret is a prefix of a longer one and is listed first,
// the longer secret's tail must NOT leak. Pre-fix Mask replaced "abc"
// inside "abcZZTAIL" first, leaking "ZZTAIL".
func TestMask_OverlappingSecrets_NoLeak(t *testing.T) {
	s := newTestService(t)
	body := "key=abcZZTAIL!"
	masked := s.Mask(body, []string{"abc", "abcZZTAIL"}, "u-1")
	if strings.Contains(masked, "ZZTAIL") || strings.Contains(masked, "abcZZTAIL") {
		t.Fatalf("longer secret leaked: %q", masked)
	}
	if strings.Count(masked, "wick_enc_") != 1 {
		t.Fatalf("expected exactly 1 token, got %q", masked)
	}
}

// MaskIgnoreCase must mask every case variant and share one token across
// them (same lowercased key).
func TestMaskIgnoreCase_AllVariantsOneToken(t *testing.T) {
	s := newTestService(t)
	body := "A=Secret b=SECRET c=secret"
	masked := s.MaskIgnoreCase(body, []string{"secret"}, "u-1")
	for _, leak := range []string{"Secret", "SECRET", "secret"} {
		if strings.Contains(masked, leak) {
			t.Fatalf("case variant %q leaked in %q", leak, masked)
		}
	}
	toks := tokenRegex.FindAllString(masked, -1)
	if len(toks) != 3 {
		t.Fatalf("expected 3 tokens, got %q", masked)
	}
	for _, tk := range toks[1:] {
		if tk != toks[0] {
			t.Fatalf("case variants should share one token: %v", toks)
		}
	}
}

// Same overlap hazard for the case-insensitive path.
func TestMaskIgnoreCase_OverlappingNoLeak(t *testing.T) {
	s := newTestService(t)
	body := "v=PassZZTAIL!"
	masked := s.MaskIgnoreCase(body, []string{"pass", "passZZTAIL"}, "u-1")
	if strings.Contains(strings.ToLower(masked), "zztail") {
		t.Fatalf("longer keyword tail leaked: %q", masked)
	}
}

// Absent secrets must not be masked (and the present one still is).
func TestMask_AbsentSecretSkipped(t *testing.T) {
	s := newTestService(t)
	masked := s.Mask("only=present-secret", []string{"present-secret", "missing-secret"}, "u-1")
	if strings.Contains(masked, "present-secret") {
		t.Fatalf("present secret not masked: %q", masked)
	}
	if strings.Count(masked, "wick_enc_") != 1 {
		t.Fatalf("expected 1 token (absent skipped), got %q", masked)
	}
}

// Unmask round-trips after the case-sensitive Mask, including repeated
// occurrences sharing one token.
func TestMask_Unmask_RoundTripRepeated(t *testing.T) {
	s := newTestService(t)
	body := `a=cred-one b=cred-one c=cred-two`
	masked := s.Mask(body, []string{"cred-one", "cred-two"}, "u-1")
	if strings.Contains(masked, "cred-one") || strings.Contains(masked, "cred-two") {
		t.Fatalf("plaintext leaked: %q", masked)
	}
	back, err := s.Unmask(masked, "u-1")
	if err != nil {
		t.Fatalf("unmask: %v", err)
	}
	if back != body {
		t.Fatalf("round-trip mismatch:\n got %s\nwant %s", back, body)
	}
}

package wick

import (
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func TestParseHeadersPlainKV(t *testing.T) {
	got := ParseHeaders("X-Org-Id: abc123\nUser-Agent: RooCode/3.53.0")
	want := map[string]string{"X-Org-Id": "abc123", "User-Agent": "RooCode/3.53.0"}
	assertHeaders(t, got, want)
}

// A pasted curl block is the primary input shape this field is designed
// for — flags, quotes and trailing continuations must all survive.
func TestParseHeadersPastedCurlBlock(t *testing.T) {
	raw := `--header 'Authorization: Bearer xxx' \
--header 'X-Stainless-OS: Linux' \
--header 'X-Stainless-Arch: x64' \
--header 'X-Stainless-Lang: js' \
--header 'X-Stainless-Runtime: node' \
--header 'X-Stainless-Runtime-Version: v22.22.1' \
--header 'User-Agent: RooCode/3.53.0' \`
	got := ParseHeaders(raw)
	want := map[string]string{
		"Authorization":               "Bearer xxx",
		"X-Stainless-OS":              "Linux",
		"X-Stainless-Arch":            "x64",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Runtime-Version": "v22.22.1",
		"User-Agent":                  "RooCode/3.53.0",
	}
	assertHeaders(t, got, want)
}

func TestParseHeadersFlagSpellings(t *testing.T) {
	raw := `-H "A: 1"
-H=B: 2
--header=C: 3
--header 'D: 4'
E: 5`
	got := ParseHeaders(raw)
	want := map[string]string{"A": "1", "B": "2", "C": "3", "D": "4", "E": "5"}
	assertHeaders(t, got, want)
}

// A value may itself contain a colon (URLs are the common case) — only the
// FIRST colon separates key from value.
func TestParseHeadersValueWithColon(t *testing.T) {
	got := ParseHeaders("HTTP-Referer: https://abc.com/x")
	assertHeaders(t, got, map[string]string{"HTTP-Referer": "https://abc.com/x"})
}

func TestParseHeadersSkipsJunkKeepsRest(t *testing.T) {
	raw := `# a comment

not-a-header-line
Good: yes`
	assertHeaders(t, ParseHeaders(raw), map[string]string{"Good": "yes"})
}

func TestParseHeadersEmptyIsNil(t *testing.T) {
	if got := ParseHeaders("   \n\n  "); got != nil {
		t.Errorf("blank input: want nil, got %v", got)
	}
	if got := ParseHeaders("# only a comment"); got != nil {
		t.Errorf("comment-only input: want nil, got %v", got)
	}
}

func TestParseHeadersLastDuplicateWins(t *testing.T) {
	assertHeaders(t, ParseHeaders("X: first\nX: second"), map[string]string{"X": "second"})
}

func TestParseHeadersAllowsEmptyValue(t *testing.T) {
	assertHeaders(t, ParseHeaders("X-Blank:"), map[string]string{"X-Blank": ""})
}

// Custom headers are applied last and deliberately CAN override auth —
// a proxy fronting the vendor may use its own scheme.
func TestApplyCustomHeadersOverridesAuth(t *testing.T) {
	base := map[string]string{"Authorization": "Bearer real-key", "Content-Type": "application/json"}
	got := applyCustomHeaders(base, provider.WickModel{Headers: "Authorization: Bearer proxy-token"})
	assertHeaders(t, got, map[string]string{
		"Authorization": "Bearer proxy-token",
		"Content-Type":  "application/json",
	})
}

func TestApplyCustomHeadersNoneLeavesBaseAlone(t *testing.T) {
	base := map[string]string{"Authorization": "Bearer k"}
	got := applyCustomHeaders(base, provider.WickModel{})
	assertHeaders(t, got, map[string]string{"Authorization": "Bearer k"})
}

func TestApplyCustomHeadersNilBase(t *testing.T) {
	got := applyCustomHeaders(nil, provider.WickModel{Headers: "X: 1"})
	assertHeaders(t, got, map[string]string{"X": "1"})
}

func TestCustomHTTPHeader(t *testing.T) {
	h := customHTTPHeader(provider.WickModel{Headers: "X-Stainless-OS: Linux"})
	if h == nil {
		t.Fatal("want header, got nil")
	}
	if got := h.Get("X-Stainless-OS"); got != "Linux" {
		t.Errorf("X-Stainless-OS = %q, want %q", got, "Linux")
	}
	if customHTTPHeader(provider.WickModel{}) != nil {
		t.Error("no custom headers: want nil http.Header")
	}
}

func assertHeaders(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("header count = %d, want %d (got %v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("header %q = %q, want %q", k, got[k], v)
		}
	}
}

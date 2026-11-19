package strutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLimitTextKeepsUTF8Valid(t *testing.T) {
	// "—" and "…" are 3 bytes each; cut at every offset inside them.
	s := strings.Repeat("a — b … c ", 50)
	for max := 1; max < len(s); max++ {
		got := LimitText(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d produced invalid UTF-8: %q", max, got)
		}
		if body := strings.TrimSuffix(got, " ...[truncated]"); len(body) > max {
			t.Fatalf("max=%d body is %d bytes", max, len(body))
		}
	}
}

func TestLimitTextShortUnchanged(t *testing.T) {
	if got := LimitText("héllo", 100); got != "héllo" {
		t.Fatalf("got %q", got)
	}
}

func TestCutBytes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"abc", 0, ""},
		{"abc", 2, "ab"},
		{"a—", 2, "a"},
		{"a—", 3, "a"},
		{"a—", 4, "a—"},
		{"😀x", 3, ""},
		{"😀x", 4, "😀"},
	}
	for _, c := range cases {
		if got := CutBytes(c.in, c.max); got != c.want {
			t.Errorf("CutBytes(%q,%d)=%q want %q", c.in, c.max, got, c.want)
		}
	}
}

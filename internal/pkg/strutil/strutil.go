package strutil

import "unicode/utf8"

const DefaultLimit = 10_000

// LimitText truncates s to at most max bytes, appending a marker when cut.
// The cut never splits a multi-byte UTF-8 character, so the result stays
// valid UTF-8 (Postgres rejects text with a dangling lead byte).
func LimitText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return CutBytes(s, max) + " ...[truncated]"
}

// CutBytes returns the longest prefix of s that is at most max bytes and
// does not end in the middle of a UTF-8 character.
func CutBytes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max
	// Step back over continuation bytes (at most 3) to a rune start.
	for i := 0; i < utf8.UTFMax && cut > 0 && !utf8.RuneStart(s[cut]); i++ {
		cut--
	}
	return s[:cut]
}

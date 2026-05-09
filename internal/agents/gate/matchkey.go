package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// MatchKey hashes (tool, normalized cmd) into a stable identifier the
// daemon and the gate binary both compute the same way. Used to look up
// "allow this session" / "always allow" decisions.
//
// Normalization is intentionally minimal for the MVP: trim outer
// whitespace + lowercase the tool name. Per-arg pattern stripping
// (e.g. dropping file paths so `git add a` and `git add b` collapse
// to one key) is deferred — exact-string match keeps the surprise
// budget at zero. Future work may take a smarter normalizer that
// users can preview in the UI.
//
// Output is hex(sha256(tool + "\x00" + cmd)), 64 chars. Stable
// across builds; safe to store + transmit.
func MatchKey(tool, cmd string) string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	cmd = strings.TrimSpace(cmd)
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write([]byte(cmd))
	return hex.EncodeToString(h.Sum(nil))
}

// IsAutoApproved reports whether key is in the spec's AutoApproved
// list. Linear scan — the list is bounded by user clicks, so O(n)
// is fine.
func IsAutoApproved(spec Spec, key string) bool {
	for _, k := range spec.AutoApproved {
		if k == key {
			return true
		}
	}
	return false
}

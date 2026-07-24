package wick

import "context"

// gate.go holds the injection seams the server wires at boot so the
// wick package stays free of heavy dependencies (configs, gate spec,
// connector service) and avoids import cycles.

// secretDecryptor decrypts a wick_cenc_ token to plaintext. Defaults to
// identity so plaintext keys and tests work without wiring; the server
// overrides it with the configs service decryptor via SetSecretDecryptor.
var secretDecryptor = func(s string) string { return s }

// SetSecretDecryptor wires the real secret decryptor (configs service).
// nil is ignored so a mis-wire can't disable decryption silently.
func SetSecretDecryptor(fn func(string) string) {
	if fn != nil {
		secretDecryptor = fn
	}
}

// gateChecker decides whether a tool call is allowed. Returns deny=true
// with a reason to block. Defaults to allow-all (fail-open) until the
// server wires the real gate; the command gate for wick is enforced
// here, in-process, rather than via a subprocess PreToolUse hook — every
// tool goes through it (not just "shell"), same coverage the CLI
// providers get via their catch-all `.*` PreToolUse matcher. ctx lets an
// approval-required call block synchronously on the daemon's
// ApprovalManager (same modal/SSE flow a CLI provider's gate binary
// drives) — cancelling ctx (session killed mid-wait) resolves as deny.
//
// gateCheckerFn is a package-level seam so wiring stays out of the
// spawn hot path; a per-spawn checker can be introduced later if the
// gate spec becomes session-scoped.
var gateCheckerFn func(ctx context.Context, sessionID, tool string, args map[string]any) (deny bool, reason string)

// SetGateChecker wires the in-process gate checker.
func SetGateChecker(fn func(ctx context.Context, sessionID, tool string, args map[string]any) (deny bool, reason string)) {
	gateCheckerFn = fn
}

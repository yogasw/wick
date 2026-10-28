package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathGuardFlag is the gate binary's argument that switches it into the
// path guard: a PreToolUse hook that refuses any tool call touching
// another agent's project or session folder. It runs whatever the
// permission mode is (an exit code 2 blocks the call even under
// bypassPermissions), and it is installed through `--settings` from the
// session dir, outside the workspace the agent can edit.
const PathGuardFlag = "--path-guard"

// PathGuardSpec is what one spawn's guard checks against.
type PathGuardSpec struct {
	// Guarded are the roots no agent may enter except for the parts
	// listed in Allow: the projects root and the sessions root.
	Guarded []string `json:"guarded"`
	// Allow are the real paths this spawn may touch under Guarded: its
	// own project and session, plus the operator's exceptions.
	Allow []string `json:"allow"`
	// Home expands a leading "~" in a command or path.
	Home string `json:"home,omitempty"`
}

// PathGuardInput is the part of a PreToolUse payload the guard reads.
type PathGuardInput struct {
	CWD       string
	ToolInput map[string]any
}

// pathKeys are the tool_input fields that name a file or directory.
var pathKeys = []string{"file_path", "path", "notebook_path", "pattern"}

// CheckPathGuard returns the first path in.ToolInput reaches into a
// guarded root outside spec.Allow, or "" when the call may go ahead.
// Commands (Bash and friends) are scanned token by token; it is a
// guardrail for the common case (`cat ../other/x`, `ls ~/…/projects`),
// not a sandbox.
func CheckPathGuard(spec PathGuardSpec, in PathGuardInput) string {
	var cands []string
	for _, k := range pathKeys {
		if s, ok := in.ToolInput[k].(string); ok && strings.TrimSpace(s) != "" {
			cands = append(cands, s)
		}
	}
	for _, k := range commandKeys {
		if s, ok := in.ToolInput[k].(string); ok {
			cands = append(cands, commandPaths(s)...)
		}
	}
	for _, c := range cands {
		p := resolveGuardPath(c, in.CWD, spec.Home)
		if p == "" {
			continue
		}
		if guardedOutsideAllow(spec, p) {
			return p
		}
	}
	return ""
}

// commandKeys mirrors cmd/gate's list of fields carrying shell text.
var commandKeys = []string{"command", "script", "cmd", "code"}

// commandPaths picks the path-looking tokens out of a shell command:
// anything with a slash, or starting with "~" or "..".
func commandPaths(cmd string) []string {
	split := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\'', '"', ';', '|', '&', '<', '>', '(', ')', '`', '=', ',', '{', '}':
			return true
		}
		return false
	}
	var out []string
	for _, tok := range strings.FieldsFunc(cmd, split) {
		if strings.Contains(tok, "/") || strings.HasPrefix(tok, "~") || strings.HasPrefix(tok, "..") {
			out = append(out, tok)
		}
	}
	return out
}

// resolveGuardPath makes p absolute (against cwd, "~" against home),
// cuts it at the first glob character, and resolves symlinks on the
// longest part that exists, so `../x`, `~/…` and a symlink pointing
// into another project all land on the real target.
func resolveGuardPath(p, cwd, home string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "://") {
		return ""
	}
	if strings.HasPrefix(p, "~") && home != "" {
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		p = p[:i]
	}
	if !filepath.IsAbs(p) {
		if cwd == "" {
			return ""
		}
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	return realPrefix(p)
}

// realPrefix resolves symlinks on the longest existing prefix of p and
// re-appends the rest.
func realPrefix(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func guardedOutsideAllow(spec PathGuardSpec, p string) bool {
	under := func(root string) bool {
		root = filepath.Clean(root)
		return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
	}
	guarded := false
	for _, g := range spec.Guarded {
		if g = strings.TrimSpace(g); g != "" && under(g) {
			guarded = true
			break
		}
	}
	if !guarded {
		return false
	}
	for _, a := range spec.Allow {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		if strings.ContainsAny(a, "*?[") {
			if underPattern(a, p) {
				return false
			}
			continue
		}
		if under(a) {
			return false
		}
	}
	// Listing the guarded root itself (`ls projects/`) reveals the other
	// projects too, so it is refused like entering one.
	return true
}

// underPattern reports whether p is at or under a path matching pattern,
// whose segments may hold globs: "/x/projects/*/files/knowledge" lets in
// every project's knowledge folder and nothing else of those projects.
// A "*" never spans a "/".
func underPattern(pattern, p string) bool {
	pat := strings.Split(filepath.Clean(pattern), string(filepath.Separator))
	got := strings.Split(p, string(filepath.Separator))
	if len(got) < len(pat) {
		return false
	}
	for i, seg := range pat {
		if ok, err := filepath.Match(seg, got[i]); err != nil || !ok {
			return false
		}
	}
	return true
}

// PathGuardMessage is what the agent reads when a call is refused.
func PathGuardMessage(path string) string {
	return fmt.Sprintf("path guard: %s belongs to another agent's project or session, which this agent may not read or change. Stop here: do not look for another route. If the task needs it, ask the person to have the admin add it to path_guard_allow.", path)
}

// WritePathGuard writes the spec and the claude settings that install
// the guard hook into dir (the session dir) and returns the settings
// path to pass as `--settings`.
func WritePathGuard(dir, gateBin string, spec PathGuardSpec) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	specPath := filepath.Join(dir, "pathguard-spec.json")
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		return "", err
	}
	cmd := strings.ReplaceAll(gateBin, "\\", "/") + " " + PathGuardFlag + " " + shellQuote(strings.ReplaceAll(specPath, "\\", "/"))
	cfg := claudeHookConfig{Hooks: claudeHooks{PreToolUse: []claudeHookGroup{
		{Matcher: ".*", Hooks: []claudeHookEntry{{Type: "command", Command: cmd, Timeout: 30}}},
	}}}
	sdata, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(dir, "pathguard-settings.json")
	if err := os.WriteFile(settingsPath, sdata, 0o644); err != nil {
		return "", err
	}
	return settingsPath, nil
}

// LoadPathGuardSpec reads a spec written by WritePathGuard. A missing or
// broken spec is an error: the guard then blocks, it does not open up.
func LoadPathGuardSpec(path string) (PathGuardSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PathGuardSpec{}, err
	}
	var s PathGuardSpec
	if err := json.Unmarshal(data, &s); err != nil {
		return PathGuardSpec{}, err
	}
	return s, nil
}

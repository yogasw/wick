package event

// display_detectors.go — the built-in detectors. Priorities leave gaps so
// a new detector can slot between two existing ones:
//
//	100 binary (data URL / bare base64 with a known magic)
//	 90 error result
//	 80 per-tool call cards (bash, read/write/edit/patch, grep/glob, mcp, web search)
//	 70 per-tool results (bash → terminal, read → code, grep/glob → search)
//	 50 json · 40 diff · 30 markdown — anything else falls through to text

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

func init() {
	RegisterDetector(KindImage, 100, detectBinary)
	RegisterDetector(KindError, 90, detectError)
	RegisterDetector(KindCommand, 80, detectBashCall)
	RegisterDetector(KindFile, 80, detectFileCall)
	RegisterDetector(KindSearch, 80, detectSearchCall)
	RegisterDetector(KindMCP, 80, detectMCPCall)
	RegisterDetector(KindTerminal, 70, detectToolResult)
	RegisterDetector(KindJSON, 50, detectJSON)
	RegisterDetector(KindDiff, 40, detectDiff)
	RegisterDetector(KindMarkdown, 30, detectMarkdown)
}

func detectBinary(in DetectInput) (Display, bool) {
	t := strings.TrimSpace(in.Text)
	if m := dataURLRe.FindStringSubmatch(t); m != nil {
		return decodeBinary(t[len(m[0]):], m[1])
	}
	if in.Call || !looksBase64(t) || !binaryMagic(t) {
		return Display{}, false
	}
	return decodeBinary(t, "")
}

var errorPrefixRe = regexp.MustCompile(`^(?i)(<tool_use_error>|error:|fatal:|exception:)`)

func detectError(in DetectInput) (Display, bool) {
	if in.Call {
		return Display{}, false
	}
	t := strings.TrimSpace(in.Text)
	// A failed shell command is still terminal output — the card shows
	// what it printed plus the exit code, not an error banner.
	if in.Tool == ToolBash && t != "" {
		return Display{}, false
	}
	if !in.IsError && !errorPrefixRe.MatchString(t) {
		return Display{}, false
	}
	t = strings.TrimSuffix(strings.TrimPrefix(t, "<tool_use_error>"), "</tool_use_error>")
	return Display{Kind: KindError, Body: strings.TrimSpace(t), Summary: firstLine(t, 120)}, true
}

// detectBashCall: claude {command,description,timeout}, codex exec_command
// {cmd,workdir} or a bare command string, local_shell {command:[argv]}.
func detectBashCall(in DetectInput) (Display, bool) {
	if !in.Call || in.Tool != ToolBash {
		return Display{}, false
	}
	d := Display{Kind: KindCommand, Lang: "shell"}
	m := jsonObject(in.Text)
	if m == nil {
		d.Command = strings.TrimSpace(in.Text)
	} else {
		d.Command = str(m, "command", "cmd", "chars")
		if argv, ok := m["command"].([]any); ok {
			d.Command = shellJoin(argv)
		}
		d.Cwd = str(m, "cwd", "workdir", "working_directory", "dir")
		d.Summary = str(m, "description", "justification")
		if v, ok := m["timeout"].(float64); ok {
			d.Timeout = int(v)
		} else if v, ok := m["timeout_ms"].(float64); ok {
			d.Timeout = int(v)
		}
	}
	d.Command = unwrapShellC(d.Command)
	if d.Summary == "" {
		d.Summary = firstLine(d.Command, 120)
	}
	d.Body = d.Command
	return d, true
}

// shellJoin renders an argv array, unwrapping `bash -lc "<script>"`.
func shellJoin(argv []any) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		if s, ok := a.(string); ok {
			parts = append(parts, s)
		}
	}
	if len(parts) == 3 && (parts[1] == "-lc" || parts[1] == "-c") && strings.HasSuffix(parts[0], "sh") {
		return parts[2]
	}
	return strings.Join(parts, " ")
}

var shellCRe = regexp.MustCompile(`^(?:/usr)?(?:/bin/)?(?:ba|z)?sh -l?c (?s:(['"])(.*)['"])$`)

// unwrapShellC turns codex's `/bin/bash -lc 'git status'` into `git status`.
func unwrapShellC(cmd string) string {
	m := shellCRe.FindStringSubmatch(strings.TrimSpace(cmd))
	if m == nil || strings.Contains(m[2], m[1]) {
		return cmd
	}
	return m[2]
}

func detectFileCall(in DetectInput) (Display, bool) {
	if !in.Call {
		return Display{}, false
	}
	switch in.Tool {
	case ToolRead, ToolWrite, ToolEdit, ToolPatch:
	default:
		return Display{}, false
	}
	m := jsonObject(in.Text)
	if in.Tool == ToolPatch {
		patch := in.Text
		if m != nil {
			patch = str(m, "input", "patch", "content")
		}
		d := Display{Kind: KindDiff, Lang: "diff", Body: patch, Path: patchPath(patch)}
		d.Summary = d.Path
		return d, true
	}
	if m == nil {
		return Display{}, false
	}
	p := str(m, "file_path", "path", "filePath", "filename", "notebook_path")
	d := Display{Kind: KindFile, Path: p, Lang: LangForPath(p), Summary: p}
	switch in.Tool {
	case ToolWrite:
		d.Body = str(m, "content", "contents", "text")
	case ToolEdit:
		d.Kind, d.Lang = KindDiff, "diff"
		d.Body = editDiff(p, m)
	case ToolRead:
		if off, ok := m["offset"].(float64); ok {
			d.Summary += fmt.Sprintf(" @%d", int(off))
		}
	}
	return d, true
}

// editDiff renders claude Edit/MultiEdit (old_string/new_string, edits[])
// and opencode edit (oldString/newString) as a minimal unified diff.
func editDiff(p string, m map[string]any) string {
	type pair struct{ old, new string }
	var pairs []pair
	if edits, ok := m["edits"].([]any); ok {
		for _, e := range edits {
			if em, ok := e.(map[string]any); ok {
				pairs = append(pairs, pair{str(em, "old_string", "oldString", "oldText"), str(em, "new_string", "newString", "newText")})
			}
		}
	} else {
		pairs = append(pairs, pair{str(m, "old_string", "oldString", "old_str", "oldText"), str(m, "new_string", "newString", "new_str", "newText")})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", p, p)
	for _, pr := range pairs {
		b.WriteString("@@\n")
		for _, l := range splitLines(pr.old) {
			b.WriteString("-" + l + "\n")
		}
		for _, l := range splitLines(pr.new) {
			b.WriteString("+" + l + "\n")
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

var patchFileRe = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: (.+)$|^\+\+\+ (?:b/)?(.+)$`)

func patchPath(patch string) string {
	m := patchFileRe.FindStringSubmatch(patch)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(m[2])
}

func detectSearchCall(in DetectInput) (Display, bool) {
	if !in.Call {
		return Display{}, false
	}
	switch in.Tool {
	case ToolGrep, ToolGlob, ToolWebSearch:
	default:
		return Display{}, false
	}
	d := Display{Kind: KindSearch}
	if m := jsonObject(in.Text); m != nil {
		d.Pattern = str(m, "pattern", "query", "regex", "q")
		d.Path = str(m, "path", "dir", "directory", "cwd")
		if g := str(m, "glob", "include"); g != "" && d.Pattern != g {
			d.Summary = g
		}
	} else {
		d.Pattern = strings.TrimSpace(in.Text)
	}
	d.Summary = strings.TrimSpace(strings.Join(nonEmpty(d.Pattern, d.Summary, d.Path), " · "))
	d.Body = d.Pattern
	return d, true
}

func nonEmpty(ss ...string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// detectMCPCall: claude mcp__<server>__<tool>, codex <server>.<tool>, and
// wick_execute {tool_id:"conn:<id>/<op>", params:{…}} — the card shows
// connector + op, the body the params as JSON.
func detectMCPCall(in DetectInput) (Display, bool) {
	if !in.Call || in.Tool != ToolMCP {
		return Display{}, false
	}
	d := Display{Kind: KindMCP, Lang: "json", Body: in.Text}
	name := in.ToolName
	switch {
	case strings.HasPrefix(name, "mcp__"):
		rest := strings.TrimPrefix(name, "mcp__")
		if i := strings.Index(rest, "__"); i >= 0 {
			d.Connector, d.Op = rest[:i], rest[i+2:]
		} else {
			d.Op = rest
		}
	case strings.Contains(name, "."):
		i := strings.LastIndex(name, ".")
		d.Connector, d.Op = name[:i], name[i+1:]
	default:
		d.Op = name
	}
	if strings.HasSuffix(d.Op, "wick_execute") {
		if m := jsonObject(in.Text); m != nil {
			if id := str(m, "tool_id"); id != "" {
				id = strings.TrimPrefix(id, "conn:")
				if i := strings.Index(id, "/"); i >= 0 {
					d.Connector, d.Op = id[:i], id[i+1:]
				} else {
					d.Op = id
				}
			}
			if p, ok := m["params"]; ok {
				if b, err := json.MarshalIndent(p, "", "  "); err == nil {
					d.Body = string(b)
				}
			}
		}
	}
	d.Summary = strings.Join(nonEmpty(d.Connector, d.Op), " · ")
	return d, true
}

// detectToolResult shapes results whose tool has a natural rendering.
// JSON/markdown output from a shell still reads best as terminal output:
// it is what the command printed.
func detectToolResult(in DetectInput) (Display, bool) {
	if in.Call {
		return Display{}, false
	}
	switch in.Tool {
	case ToolBash:
		return Display{Kind: KindTerminal, Body: in.Text}, true
	case ToolRead:
		d := Display{Kind: KindCode, Body: in.Text}
		if in.CallInfo != nil {
			d.Path, d.Lang = in.CallInfo.Path, in.CallInfo.Lang
		}
		if d.Lang == "markdown" {
			d.Kind = KindMarkdown
		}
		return d, true
	case ToolGrep, ToolGlob:
		if strings.TrimSpace(in.Text) == "" || isJSON(in.Text) {
			return Display{}, false
		}
		return Display{Kind: KindSearch, Body: in.Text}, true
	}
	return Display{}, false
}

func isJSON(s string) bool {
	t := strings.TrimSpace(s)
	return len(t) > 1 && (t[0] == '{' || t[0] == '[') && json.Valid([]byte(t))
}

func detectJSON(in DetectInput) (Display, bool) {
	if !isJSON(in.Text) {
		return Display{}, false
	}
	return Display{Kind: KindJSON, Lang: "json", Body: in.Text}, true
}

var (
	diffHunkRe = regexp.MustCompile(`(?m)^@@ .* @@`)
	diffHdrRe  = regexp.MustCompile(`(?m)^(diff --git |--- \S|\+\+\+ \S|\*\*\* Begin Patch)`)
)

func detectDiff(in DetectInput) (Display, bool) {
	if !diffHdrRe.MatchString(in.Text) || !(diffHunkRe.MatchString(in.Text) || strings.Contains(in.Text, "*** Begin Patch")) {
		return Display{}, false
	}
	return Display{Kind: KindDiff, Lang: "diff", Body: in.Text, Path: patchPath(in.Text)}, true
}

var (
	mdHeadingRe = regexp.MustCompile(`(?m)^#{1,6} \S`)
	mdFenceRe   = regexp.MustCompile("(?m)^```")
	mdTableRe   = regexp.MustCompile(`(?m)^\|.*\|\s*\n\|\s*:?-{3,}`)
	mdListRe    = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+\.) \S`)
	mdInlineRe  = regexp.MustCompile(`\*\*[^*\n]+\*\*|\[[^\]\n]+\]\([^)\s]+\)`)
)

// detectMarkdown needs a structural signal (heading, fence, table) or a
// list plus inline formatting — a bare "- item" list alone stays text.
func detectMarkdown(in DetectInput) (Display, bool) {
	t := in.Text
	if mdHeadingRe.MatchString(t) || mdFenceRe.MatchString(t) || mdTableRe.MatchString(t) ||
		(len(mdListRe.FindAllStringIndex(t, 2)) >= 2 && mdInlineRe.MatchString(t)) {
		return Display{Kind: KindMarkdown, Lang: "markdown", Body: t}, true
	}
	return Display{}, false
}

// ── call → result pairing ───────────────────────────────────────────────

// toolCalls remembers each call's Display by id, so its result can be
// classified with the call's context (family, path). Parsers embed one and
// run every event they return through decorate.
type toolCalls struct {
	calls map[string]callInfo
}

type callInfo struct {
	name string
	d    *Display
}

// maxTrackedCalls bounds the map for a provider that never reports
// results for some calls; the oldest entries simply stop pairing.
const maxTrackedCalls = 256

// decorate stamps Display onto ToolUse/ToolResult events that do not have
// one yet. Every other event passes through untouched.
func (t *toolCalls) decorate(ev *AgentEvent) {
	switch ev.Type {
	case ToolUse:
		if ev.Display == nil {
			d := ClassifyCall(ev.ToolName, ev.ToolInput)
			ev.Display = &d
		}
		if ev.ToolUseID == "" {
			return
		}
		if t.calls == nil {
			t.calls = map[string]callInfo{}
		}
		if len(t.calls) >= maxTrackedCalls {
			for k := range t.calls {
				delete(t.calls, k)
				break
			}
		}
		t.calls[ev.ToolUseID] = callInfo{name: ev.ToolName, d: ev.Display}
	case ToolResult:
		ci := t.calls[ev.ToolUseID]
		delete(t.calls, ev.ToolUseID)
		if ev.Display != nil {
			return
		}
		d := ClassifyResult(ci.name, ev.Text, ev.IsError, ci.d)
		if ev.ExitCode != nil {
			d.ExitCode = ev.ExitCode
		}
		ev.Display = &d
	}
}

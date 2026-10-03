package event

import (
	"strings"
	"testing"
)

// toolDisplays returns the Display kinds of every tool event, in order,
// "use:<kind>" / "result:<kind>".
func toolDisplays(t *testing.T, evs []AgentEvent) ([]string, []AgentEvent) {
	t.Helper()
	var kinds []string
	var tools []AgentEvent
	for _, ev := range evs {
		if ev.Type != ToolUse && ev.Type != ToolResult {
			continue
		}
		if ev.Display == nil {
			t.Fatalf("%s %s has no Display", ev.Type, ev.ToolUseID)
		}
		prefix := "use:"
		if ev.Type == ToolResult {
			prefix = "result:"
		}
		kinds = append(kinds, prefix+ev.Display.Kind)
		tools = append(tools, ev)
	}
	return kinds, tools
}

func wantKinds(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("kinds\n got  %v\n want %v", got, want)
	}
}

func TestClaudeToolDisplays(t *testing.T) {
	kinds, evs := toolDisplays(t, feed(t, NewClaudeParser(), "testdata/claude_tools.jsonl"))
	wantKinds(t, kinds,
		"use:command", "result:terminal",
		"use:json", "result:markdown",
		"use:file", "result:image",
		"use:file", "result:error")
	if evs[0].Display.Command != "ls" || evs[0].Display.Summary != "List files" {
		t.Errorf("bash card = %+v", evs[0].Display)
	}
	// The escaped content-block array is unwrapped; the raw text is not.
	if md := evs[3]; !strings.HasPrefix(md.Display.Body, "# P1 Laporan") || !strings.HasPrefix(md.Text, `[{"type"`) {
		t.Errorf("markdown body %.40q raw %.40q", md.Display.Body, md.Text)
	}
	if img := evs[5].Display; img.Name != "shot.png" || img.Mime != "image/png" || len(img.Blob) == 0 {
		t.Errorf("image = %+v", img)
	}
	if e := evs[7].Display; e.Body != "File does not exist." {
		t.Errorf("error body = %q", e.Body)
	}
}

func TestCodexToolDisplays(t *testing.T) {
	kinds, evs := toolDisplays(t, feed(t, NewCodexParser(), "testdata/codex_tools.jsonl"))
	wantKinds(t, kinds,
		"use:command", "result:terminal",
		"use:command", "result:terminal",
		"use:mcp", "result:text")
	if c := evs[0].Display; c.Command != "go test ./..." {
		t.Errorf("shell -lc not unwrapped: %q", c.Command)
	}
	if r := evs[1]; r.Display.ExitCode == nil || *r.Display.ExitCode != 1 || !r.IsError {
		t.Errorf("command_execution exit code lost: %+v", r.Display)
	}
	if c := evs[2].Display; c.Command != "cat README.md" {
		t.Errorf("argv command = %q", c.Command)
	}
	// Quirk: function_call_output arrives as {"output":…,"metadata":…}.
	r := evs[3]
	if !strings.HasPrefix(r.Display.Body, "# P1 Laporan") || r.Display.ExitCode == nil || *r.Display.ExitCode != 0 {
		t.Errorf("envelope not unwrapped: body %.40q exit %v", r.Display.Body, r.Display.ExitCode)
	}
	if !strings.HasPrefix(r.Text, `{"output"`) || r.Display.OriginalBytes != len(r.Text) {
		t.Errorf("raw text must stay the envelope (orig=%d len=%d)", r.Display.OriginalBytes, len(r.Text))
	}
	// Quirk: MCP image blocks used to be dropped.
	if m := evs[4].Display; m.Connector != "playwright" || m.Op != "browser_take_screenshot" {
		t.Errorf("mcp card = %+v", m)
	}
	if res := evs[5].Display; res.Body != "Took screenshot" || len(res.Parts) != 1 || res.Parts[0].Kind != KindImage {
		t.Errorf("mcp image part missing: %+v", res)
	}
}

func TestOMPToolDisplays(t *testing.T) {
	kinds, _ := toolDisplays(t, feed(t, NewOMPParser("omp"), "testdata/omp_turn.jsonl"))
	wantKinds(t, kinds, "use:command", "result:terminal")

	kinds, evs := toolDisplays(t, feed(t, NewOMPParser("omp"), "testdata/omp_tools.jsonl"))
	wantKinds(t, kinds, "use:file", "result:code", "use:diff", "result:error")
	// Quirk: omp's image block used to vanish from the result text.
	if r := evs[1].Display; len(r.Parts) != 1 || r.Parts[0].Mime != "image/png" || r.Parts[0].Name != "shot.png" && r.Parts[0].Name != "image.png" {
		t.Errorf("omp image part = %+v", r.Parts)
	}
	if d := evs[2].Display; !strings.Contains(d.Body, "-a\n+b") {
		t.Errorf("omp edit diff = %q", d.Body)
	}
}

func TestOpencodeToolDisplays(t *testing.T) {
	kinds, evs := toolDisplays(t, feed(t, NewOpencodeParser("oc"), "testdata/opencode_turn.jsonl"))
	wantKinds(t, kinds, "use:command", "result:terminal", "use:file", "result:error")
	if evs[2].Display.Path != "x" {
		t.Errorf("read path = %q", evs[2].Display.Path)
	}
}

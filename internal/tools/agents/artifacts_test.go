package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentstore "github.com/yogasw/wick/internal/agents/store"
)

func TestClassifyArtifactKind(t *testing.T) {
	cases := map[string]string{
		"a.png": "image", "b.JPG": "image", "c.svg": "image", "d.webp": "image",
		"e.pdf": "pdf", "f.html": "html", "g.htm": "html",
		// markdown + text/code get their own previewable kinds; only
		// unknown/binary types stay plain "file".
		"k.md": "markdown", "l.markdown": "markdown",
		"i.go": "text", "m.json": "text", "n.txt": "text", "o.log": "text",
		"h.zip": "file", "j": "file",
	}
	for name, want := range cases {
		if got := classifyArtifactKind(name); got != want {
			t.Errorf("classifyArtifactKind(%q)=%q want %q", name, got, want)
		}
	}
}

func TestIsTextArtifactExt(t *testing.T) {
	for _, n := range []string{"main.go", "x.ts", "y.py", "z.md", "a.json", "b.txt", "c.css"} {
		if !isTextArtifactExt(n) {
			t.Errorf("%q should be text", n)
		}
	}
	for _, n := range []string{"a.png", "b.pdf", "c.zip", "d.bin"} {
		if isTextArtifactExt(n) {
			t.Errorf("%q should NOT be text", n)
		}
	}
}

func TestResolveWithinCwd(t *testing.T) {
	cwd := "/sess/cwd"
	rel, ok := resolveWithinCwd(cwd, "/sess/cwd/sub/a.png")
	if !ok || rel != "sub/a.png" {
		t.Fatalf("abs inside: rel=%q ok=%v", rel, ok)
	}
	rel, ok = resolveWithinCwd(cwd, "sub/b.png")
	if !ok || rel != "sub/b.png" {
		t.Fatalf("relative: rel=%q ok=%v", rel, ok)
	}
	if _, ok := resolveWithinCwd(cwd, "/etc/passwd"); ok {
		t.Fatal("outside cwd must be rejected")
	}
	if _, ok := resolveWithinCwd(cwd, "../escape"); ok {
		t.Fatal("traversal must be rejected")
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveArtifacts(t *testing.T) {
	base := t.TempDir()
	layout := agentconfig.NewLayout(base)
	const sid, tid = "S1", "T1"
	cwd := filepath.Join(layout.SessionDir(sid), "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cwd, "chart.svg"), []byte("<svg/>"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "chart.png"), []byte("\x89PNG"), 0o644)
	_ = os.WriteFile(filepath.Join(cwd, "main.go"), []byte("package x"), 0o644)

	idx := agentstore.TurnTraceIndex{
		TurnID: tid,
		Events: []agentstore.TurnEventIndex{
			{Type: "tool_use", ToolName: "Write", ToolInput: `{"file_path":"` + filepath.Join(cwd, "chart.svg") + `","content":"<svg/>"}`},
			{Type: "tool_use", ToolName: "Read", ToolInput: `{"file_path":"` + filepath.Join(cwd, "chart.png") + `"}`},
			{Type: "tool_use", ToolName: "Read", ToolInput: `{"file_path":"` + filepath.Join(cwd, "main.go") + `"}`},
			{Type: "tool_use", ToolName: "Read", ToolInput: `{"file_path":"/etc/passwd"}`},
		},
	}
	writeJSON(t, layout.SessionThinking(sid, tid), idx)

	turn := agentstore.ConversationTurn{TurnID: tid, Role: "assistant", HasTrace: true}
	got := deriveArtifacts(layout, sid, "/base", cwd, turn)

	names := map[string]string{}
	for _, a := range got {
		names[a.Path] = a.Kind
	}
	if names["chart.svg"] != "image" {
		t.Errorf("svg (written) should be image artifact; got %v", names)
	}
	if names["chart.png"] != "image" {
		t.Errorf("png (read, binary) should be image artifact; got %v", names)
	}
	if _, ok := names["main.go"]; ok {
		t.Errorf("main.go (read-only text) must be excluded; got %v", names)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 artifacts, got %d: %v", len(got), got)
	}
	for _, a := range got {
		if a.URL == "" || a.DownloadURL == "" {
			t.Errorf("artifact %q missing URLs", a.Path)
		}
	}
}

func TestAttachArtifactsToTurns(t *testing.T) {
	base := t.TempDir()
	layout := agentconfig.NewLayout(base)
	const sid, tid = "S2", "T2"
	cwd := filepath.Join(layout.SessionDir(sid), "cwd")
	_ = os.MkdirAll(cwd, 0o755)
	_ = os.WriteFile(filepath.Join(cwd, "out.png"), []byte("\x89PNG"), 0o644)
	writeJSON(t, layout.SessionThinking(sid, tid), agentstore.TurnTraceIndex{
		TurnID: tid,
		Events: []agentstore.TurnEventIndex{
			{Type: "tool_use", ToolName: "Write", ToolInput: `{"file_path":"out.png"}`},
		},
	})
	turns := []agentstore.ConversationTurn{
		{TurnID: "u", Role: "user", Text: "hi"},
		{TurnID: tid, Role: "assistant", HasTrace: true, Text: "done"},
	}
	attachArtifactsToTurns(layout, sid, "/base", cwd, turns)
	if len(turns[0].Artifacts) != 0 || turns[0].HasArtifact {
		t.Errorf("user turn must have no artifacts and has_artifact=false")
	}
	if len(turns[1].Artifacts) != 1 || turns[1].Artifacts[0].Path != "out.png" {
		t.Fatalf("assistant turn artifacts = %v", turns[1].Artifacts)
	}
	if !turns[1].HasArtifact {
		t.Errorf("assistant turn with artifacts must have has_artifact=true")
	}
}

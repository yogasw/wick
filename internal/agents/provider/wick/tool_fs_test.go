package wick

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tool_fs_test.go covers the native filesystem tools (read_file,
// write_file, edit_file) added so "create/edit files inside the session
// worktree" works without depending on the shell tool's command
// whitelist — a whitelist entry can never permit `echo ... > file`
// because gate.Matcher blocks shell redirect metacharacters outright,
// regardless of how the whitelist is configured. These tools bypass the
// shell gate entirely (they're not the "shell" tool, so gateCheckerFn
// never inspects them) while staying confined to the workspace via
// fsResolvePath.

func callFsTool(t *testing.T, workspace string, td toolDef, args map[string]any) (string, bool) {
	t.Helper()
	return td.handler(context.Background(), args)
}

func TestWriteFile_CreatesFileWithContent(t *testing.T) {
	ws := t.TempDir()
	tc := toolContext{Workspace: ws}
	out, isErr := callFsTool(t, ws, writeFileTool(tc), map[string]any{"path": "hello.txt", "content": "hi there"})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(ws, "hello.txt"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != "hi there" {
		t.Errorf("want %q, got %q", "hi there", string(data))
	}
}

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	ws := t.TempDir()
	tc := toolContext{Workspace: ws}
	out, isErr := callFsTool(t, ws, writeFileTool(tc), map[string]any{"path": "sub/dir/f.txt", "content": "x"})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if _, err := os.Stat(filepath.Join(ws, "sub", "dir", "f.txt")); err != nil {
		t.Fatalf("nested file not created: %v", err)
	}
}

func TestWriteFile_OverwritesExisting(t *testing.T) {
	ws := t.TempDir()
	tc := toolContext{Workspace: ws}
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, isErr := callFsTool(t, ws, writeFileTool(tc), map[string]any{"path": "f.txt", "content": "new"})
	if isErr {
		t.Fatal("unexpected error overwriting file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Errorf("want 'new', got %q", string(data))
	}
}

func TestReadFile_ReturnsContent(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "f.txt"), []byte("read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	out, isErr := callFsTool(t, ws, readFileTool(tc), map[string]any{"path": "f.txt"})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if out != "read me" {
		t.Errorf("want 'read me', got %q", out)
	}
}

func TestReadFile_MissingFileErrors(t *testing.T) {
	ws := t.TempDir()
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, readFileTool(tc), map[string]any{"path": "nope.txt"})
	if !isErr {
		t.Error("reading a missing file should report isErr=true")
	}
}

func TestReadFile_EmptyFileReportsEmpty(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "empty.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	out, isErr := callFsTool(t, ws, readFileTool(tc), map[string]any{"path": "empty.txt"})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "empty") {
		t.Errorf("want an 'empty file' marker, got %q", out)
	}
}

func TestEditFile_ReplacesUniqueMatch(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	out, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{"path": "f.txt", "old_str": "world", "new_str": "wick"})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello wick" {
		t.Errorf("want 'hello wick', got %q", string(data))
	}
}

func TestEditFile_AmbiguousMatchRejected(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("foo foo foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{"path": "f.txt", "old_str": "foo", "new_str": "bar"})
	if !isErr {
		t.Error("edit with 3 matches should be rejected as ambiguous")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "foo foo foo" {
		t.Error("file should be unchanged after a rejected ambiguous edit")
	}
}

func TestEditFile_NoMatchRejected(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{"path": "f.txt", "old_str": "not there", "new_str": "x"})
	if !isErr {
		t.Error("edit with no match should be rejected")
	}
}

// TestFsPath_EscapeRejected proves the filesystem tools cannot read/write
// outside the session workspace — the native equivalent of the shell
// tool's scope restriction.
func TestFsPath_EscapeRejected(t *testing.T) {
	ws := t.TempDir()
	tc := toolContext{Workspace: ws}
	outside := filepath.Join(filepath.Dir(ws), "outside.txt")
	_, isErr := callFsTool(t, ws, writeFileTool(tc), map[string]any{"path": outside, "content": "escape"})
	if !isErr {
		t.Fatal("writing outside the workspace should be rejected")
	}
	if _, err := os.Stat(outside); err == nil {
		os.Remove(outside)
		t.Fatal("file was written outside the workspace — path scoping failed")
	}
}

func TestFsPath_RelativeTraversalRejected(t *testing.T) {
	ws := t.TempDir()
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, writeFileTool(tc), map[string]any{"path": "../escape.txt", "content": "x"})
	if !isErr {
		t.Fatal("../ path traversal should be rejected")
	}
}

func TestReadFile_LineRange(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	out, isErr := callFsTool(t, ws, readFileTool(tc), map[string]any{"path": "f.txt", "start_line": 2, "end_line": 4})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	want := "2\ttwo\n3\tthree\n4\tfour\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestReadFile_LineRangeOutOfBoundsErrors(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, readFileTool(tc), map[string]any{"path": "f.txt", "start_line": 10, "end_line": 20})
	if !isErr {
		t.Error("start_line beyond EOF should be rejected")
	}
}

func TestEditFile_LineRangeReplacesLines(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\nfive"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	out, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{
		"path": "f.txt", "start_line": 2, "end_line": 3, "new_str": "TWO\nTHREE",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\nTWO\nTHREE\nfour\nfive" {
		t.Errorf("got %q", string(data))
	}
}

func TestEditFile_LineRangeCanShrinkOrGrowLineCount(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{
		"path": "f.txt", "start_line": 2, "end_line": 2, "new_str": "TWO-A\nTWO-B\nTWO-C",
	})
	if isErr {
		t.Fatal("unexpected error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\nTWO-A\nTWO-B\nTWO-C\nthree" {
		t.Errorf("got %q", string(data))
	}
}

func TestEditFile_LineRangeOutOfBoundsRejected(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{
		"path": "f.txt", "start_line": 5, "end_line": 6, "new_str": "x",
	})
	if !isErr {
		t.Error("out-of-bounds start_line should be rejected")
	}
}

func TestEditFile_RejectsMixingOldStrAndLineRange(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{
		"path": "f.txt", "old_str": "one", "start_line": 1, "end_line": 1, "new_str": "x",
	})
	if !isErr {
		t.Error("mixing old_str and line-range should be rejected")
	}
}

func TestEditFile_RejectsPartialLineRange(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tc := toolContext{Workspace: ws}
	_, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{
		"path": "f.txt", "start_line": 1, "new_str": "x",
	})
	if !isErr {
		t.Error("start_line without end_line should be rejected")
	}
}

// TestReadFile_RoundTripsWriteThenEdit exercises the full sequence an
// agent would use: write, read back, edit, read back again.
func TestFsTools_RoundTrip(t *testing.T) {
	ws := t.TempDir()
	tc := toolContext{Workspace: ws}

	if _, isErr := callFsTool(t, ws, writeFileTool(tc), map[string]any{"path": "note.txt", "content": "line one"}); isErr {
		t.Fatal("write failed")
	}
	out, isErr := callFsTool(t, ws, readFileTool(tc), map[string]any{"path": "note.txt"})
	if isErr || out != "line one" {
		t.Fatalf("read after write mismatch: %q isErr=%v", out, isErr)
	}
	if _, isErr := callFsTool(t, ws, editFileTool(tc), map[string]any{"path": "note.txt", "old_str": "one", "new_str": "two"}); isErr {
		t.Fatal("edit failed")
	}
	out, isErr = callFsTool(t, ws, readFileTool(tc), map[string]any{"path": "note.txt"})
	if isErr || out != "line two" {
		t.Fatalf("read after edit mismatch: %q isErr=%v", out, isErr)
	}
}

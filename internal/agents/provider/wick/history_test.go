package wick

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/genai"
)

// writeConv writes conversation.jsonl lines into a temp session dir.
func writeConv(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestLoadHistory_TextTurns replays user + assistant text turns in order
// with the right roles, and skips display-only system turns.
func TestLoadHistory_TextTurns(t *testing.T) {
	dir := writeConv(t,
		`{"role":"user","text":"hello"}`,
		`{"role":"assistant","text":"hi there"}`,
		`{"role":"system","text":"provider switched","kind":"provider_switch"}`,
		`{"role":"user","text":"next"}`,
	)
	got := loadHistory(dir, 0)
	if len(got) != 3 {
		t.Fatalf("want 3 contents (system skipped), got %d: %+v", len(got), got)
	}
	if got[0].Role != genai.RoleUser || got[0].Parts[0].Text != "hello" {
		t.Errorf("content[0] wrong: %+v", got[0])
	}
	if got[1].Role != genai.RoleModel || got[1].Parts[0].Text != "hi there" {
		t.Errorf("content[1] wrong: %+v", got[1])
	}
	if got[2].Role != genai.RoleUser || got[2].Parts[0].Text != "next" {
		t.Errorf("content[2] wrong: %+v", got[2])
	}
}

// TestLoadHistory_CompactionIncluded includes a compaction summary as a
// model note (it carries context worth replaying).
func TestLoadHistory_CompactionIncluded(t *testing.T) {
	dir := writeConv(t,
		`{"role":"system","text":"...summary...","kind":"compaction"}`,
		`{"role":"user","text":"go on"}`,
	)
	got := loadHistory(dir, 0)
	if len(got) != 2 {
		t.Fatalf("want 2 contents, got %d", len(got))
	}
	if got[0].Role != genai.RoleModel {
		t.Errorf("compaction summary should be a model note, got role %q", got[0].Role)
	}
}

// TestLoadHistory_BudgetTrim drops oldest turns to fit the budget while
// keeping the most recent.
func TestLoadHistory_BudgetTrim(t *testing.T) {
	// Each text ~ 400 chars ≈ 100 tokens. Budget 150 tokens keeps ~1 turn.
	big := make([]byte, 400)
	for i := range big {
		big[i] = 'x'
	}
	dir := writeConv(t,
		`{"role":"user","text":"`+string(big)+`"}`,
		`{"role":"assistant","text":"`+string(big)+`"}`,
		`{"role":"user","text":"recent"}`,
	)
	got := loadHistory(dir, 150)
	if len(got) == 0 {
		t.Fatal("budget trim removed everything")
	}
	// The most recent turn must survive.
	last := got[len(got)-1]
	if last.Parts[0].Text != "recent" {
		t.Errorf("most recent turn not kept: %+v", last)
	}
	if len(got) >= 3 {
		t.Errorf("expected trim to drop older turns, kept %d", len(got))
	}
}

// TestLoadHistory_Missing returns nil for a missing file / empty dir.
func TestLoadHistory_Missing(t *testing.T) {
	if got := loadHistory(t.TempDir(), 0); got != nil {
		t.Errorf("want nil for missing conversation.jsonl, got %+v", got)
	}
	if got := loadHistory("", 0); got != nil {
		t.Errorf("want nil for empty sessionDir, got %+v", got)
	}
}

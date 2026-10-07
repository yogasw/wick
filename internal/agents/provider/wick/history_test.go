package wick

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/genai"

	"github.com/yogasw/wick/internal/agents/store"
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
	got := loadHistory(dir, 0, store.SenderName)
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
	got := loadHistory(dir, 0, store.SenderName)
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
	got := loadHistory(dir, 150, store.SenderName)
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
	if got := loadHistory(t.TempDir(), 0, store.SenderName); got != nil {
		t.Errorf("want nil for missing conversation.jsonl, got %+v", got)
	}
	if got := loadHistory("", 0, store.SenderName); got != nil {
		t.Errorf("want nil for empty sessionDir, got %+v", got)
	}
}

// A replayed user turn has to carry WHO sent it. The store keeps the sender
// as a sibling field of the text — never inside it — so a provider rebuilding
// a prompt from conversation.jsonl must re-apply the `[from: …]` line, the
// same way it re-appends attachment paths. Without this, resuming or
// compacting a shared thread brings every message back anonymous and the
// model answers the wrong person.
func TestLoadHistory_ReappliesSenderOnReplay(t *testing.T) {
	dir := writeConv(t,
		`{"role":"user","text":"cek error 401","sender":{"id":"U0104","name":"Yoga Setiawan","channel":"slack"}}`,
		`{"role":"assistant","text":"on it"}`,
		`{"role":"user","text":"aku juga","sender":{"id":"U0999","name":"Budi","channel":"slack"}}`,
	)
	got := loadHistory(dir, 0, store.SenderName)
	if len(got) != 3 {
		t.Fatalf("want 3 contents, got %d", len(got))
	}
	if want := "[from: Yoga Setiawan]\ncek error 401"; got[0].Parts[0].Text != want {
		t.Errorf("content[0] = %q, want %q", got[0].Parts[0].Text, want)
	}
	// Two different people in one thread must stay distinguishable.
	if want := "[from: Budi]\naku juga"; got[2].Parts[0].Text != want {
		t.Errorf("content[2] = %q, want %q", got[2].Parts[0].Text, want)
	}
	// An assistant turn never gets a sender line.
	if got[1].Parts[0].Text != "on it" {
		t.Errorf("assistant turn was rewritten: %q", got[1].Parts[0].Text)
	}
}

// The operator's visibility setting applies to a replay too — otherwise
// turning it down would still leak the identity on every resume.
func TestLoadHistory_SenderVisibilityApplies(t *testing.T) {
	line := `{"role":"user","text":"halo","sender":{"id":"U0104","name":"Yoga","handle":"yoga","channel":"slack"}}`

	if got := loadHistory(writeConv(t, line), 0, store.SenderOff); got[0].Parts[0].Text != "halo" {
		t.Errorf("SenderOff replay = %q, want the bare text", got[0].Parts[0].Text)
	}
	if got := loadHistory(writeConv(t, line), 0, store.SenderNameID); got[0].Parts[0].Text != "[from: Yoga (U0104)]\nhalo" {
		t.Errorf("SenderNameID replay = %q", got[0].Parts[0].Text)
	}
}

// Turns written before senders existed, and turns with nobody behind them,
// replay exactly as they did before this feature.
func TestLoadHistory_NoSenderIsUnchanged(t *testing.T) {
	got := loadHistory(writeConv(t, `{"role":"user","text":"nightly run"}`), 0, store.SenderName)
	if got[0].Parts[0].Text != "nightly run" {
		t.Errorf("got %q, want the text unchanged", got[0].Parts[0].Text)
	}
}

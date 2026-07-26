package wick

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// ── C1: tool result ingestion cap + spill ───────────────────────────

func TestCapToolResult_SmallPassesThrough(t *testing.T) {
	e := &engine{}
	small := "just a short result"
	if got := e.capToolResult("call_1", "shell", small); got != small {
		t.Errorf("small result must pass through unchanged, got %q", got)
	}
}

func TestCapToolResult_LargeSpillsToFileWithPointer(t *testing.T) {
	dir := t.TempDir()
	e := &engine{toolSpillDir: dir}
	big := strings.Repeat("A", toolResultMaxChars+50_000)

	got := e.capToolResult("call_42", "wick_execute", big)

	// Context copy must be far smaller than the original.
	if len(got) >= len(big) {
		t.Fatalf("result not capped: got %d, original %d", len(got), len(big))
	}
	// Must mention truncation + the spill path + the search-don't-read guidance.
	for _, want := range []string{"truncated", "saved to", "grep", "wick_execute"} {
		if !strings.Contains(got, want) {
			t.Errorf("capped result missing %q:\n%s", want, got[:min(len(got), 400)])
		}
	}
	// Spill file must exist with the FULL body.
	spill := filepath.Join(dir, "tool-out", "call_42.txt")
	b, err := os.ReadFile(spill)
	if err != nil {
		t.Fatalf("spill file not written: %v", err)
	}
	if len(b) != len(big) {
		t.Errorf("spill file truncated: got %d want %d", len(b), len(big))
	}
}

func TestCapToolResult_NoSpillDirStillTruncates(t *testing.T) {
	e := &engine{} // no toolSpillDir
	big := strings.Repeat("B", toolResultMaxChars+10_000)
	got := e.capToolResult("call_x", "shell", big)
	if len(got) >= len(big) {
		t.Fatal("result not capped without spill dir")
	}
	if !strings.Contains(got, "truncated") || !strings.Contains(got, "narrowly") {
		t.Errorf("no-spill path should truncate + advise re-run narrowly, got:\n%s", got[:min(len(got), 400)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── C2: compaction sidecar roundtrip + load cutoff ──────────────────

func TestCompactionSidecar_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	writeCompactionState(dir, compactionState{Summary: "did X and Y", CoveredThrough: 12})

	got := readCompactionState(dir)
	if got == nil {
		t.Fatal("sidecar not read back")
	}
	if got.Summary != "did X and Y" || got.CoveredThrough != 12 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestReadCompactionState_AbsentAndMalformed(t *testing.T) {
	dir := t.TempDir()
	if readCompactionState(dir) != nil {
		t.Error("absent sidecar should read nil")
	}
	// Malformed → nil, not a crash.
	_ = os.WriteFile(compactionSidecarPath(dir), []byte("{not json"), 0o644)
	if readCompactionState(dir) != nil {
		t.Error("malformed sidecar should read nil")
	}
}

func TestCountConversationTurns(t *testing.T) {
	dir := t.TempDir()
	if countConversationTurns(dir) != 0 {
		t.Error("missing conversation.jsonl should count 0")
	}
	lines := `{"role":"user","text":"a"}
{"role":"assistant","text":"b"}
{"role":"user","text":"c"}
`
	_ = os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(lines), 0o644)
	if n := countConversationTurns(dir); n != 3 {
		t.Errorf("want 3 turns, got %d", n)
	}
}

// TestLoadHistory_SidecarSkipsCoveredTurns is the crux of C2 (the user's
// worry): after compaction, the already-summarized turns must NOT be
// replayed — only the summary note + turns past the cutoff.
func TestLoadHistory_SidecarSkipsCoveredTurns(t *testing.T) {
	dir := t.TempDir()
	// 4 turns on disk.
	conv := `{"role":"user","text":"old-1"}
{"role":"assistant","text":"old-2"}
{"role":"user","text":"recent-3"}
{"role":"assistant","text":"recent-4"}
`
	_ = os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(conv), 0o644)
	// Summary covers the first 2 turns.
	writeCompactionState(dir, compactionState{Summary: "summary of old-1 and old-2", CoveredThrough: 2})

	got := loadHistory(dir, 0)

	// Expect: [summary note] + recent-3 + recent-4 = 3 contents.
	if len(got) != 3 {
		t.Fatalf("want 3 contents (summary + 2 recent), got %d", len(got))
	}
	if !isCompactionNote(got[0]) {
		t.Errorf("first content should be the pinned summary note, got %q", text0(got[0]))
	}
	if !strings.Contains(got[0].Parts[0].Text, "summary of old-1") {
		t.Errorf("summary body missing: %q", text0(got[0]))
	}
	// The covered (old) turns must be gone.
	joined := text0(got[1]) + "|" + text0(got[2])
	if strings.Contains(joined, "old-1") || strings.Contains(joined, "old-2") {
		t.Errorf("covered turns leaked into history: %q", joined)
	}
	if !strings.Contains(joined, "recent-3") || !strings.Contains(joined, "recent-4") {
		t.Errorf("recent turns missing: %q", joined)
	}
}

// TestLoadHistory_NoSidecarReplaysAll: without a sidecar, every turn
// replays (baseline behaviour preserved).
func TestLoadHistory_NoSidecarReplaysAll(t *testing.T) {
	dir := t.TempDir()
	conv := `{"role":"user","text":"one"}
{"role":"assistant","text":"two"}
`
	_ = os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(conv), 0o644)
	got := loadHistory(dir, 0)
	if len(got) != 2 {
		t.Fatalf("want 2 contents, got %d", len(got))
	}
}

// TestTrimToBudget_PinsSummaryNote: a leading summary note survives even
// when the budget forces trimming (it represents everything folded away),
// while the oldest real turns after it are dropped to fit.
func TestTrimToBudget_PinsSummaryNote(t *testing.T) {
	note := genai.NewContentFromText(compactionMarker+"\nsummary of everything old", genai.RoleModel)
	contents := []*genai.Content{
		note,
		genai.NewContentFromText("old "+bigText(4000), genai.RoleUser),
		genai.NewContentFromText("mid "+bigText(4000), genai.RoleModel),
		genai.NewContentFromText("newest "+bigText(400), genai.RoleUser),
	}
	// Budget small enough to force dropping the old/mid turns.
	got := trimToBudget(contents, 300)

	if !isCompactionNote(got[0]) {
		t.Fatalf("summary note must stay pinned at head, got %q", text0(got[0]))
	}
	// The newest turn must survive; the note must not have been dropped.
	last := text0(got[len(got)-1])
	if !strings.Contains(last, "newest") {
		t.Errorf("newest turn should survive, tail=%q", last)
	}
}

func text0(c *genai.Content) string {
	if c == nil || len(c.Parts) == 0 || c.Parts[0] == nil {
		return ""
	}
	return c.Parts[0].Text
}

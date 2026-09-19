package event

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The numbers below are a real codex-cli 0.149.1 run: one turn, five
// model requests, reading three files. The stream's turn.completed said
// 92,842 input tokens; the window in use at the end was 18,874. Every
// assertion here exists so the meter can never again be handed the
// first number — that is the codex twin of the claude 337% bug.
const (
	codexTurnSum   = 92842
	codexTrueLevel = 18874
	codexWindow    = 258400
)

func writeCodexRollout(t *testing.T, home, threadID string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "09", "19")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "rollout-2026-09-19T09-38-46-"+threadID+".jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
}

func tokenCountLine(total, last, window int) string {
	return `{"timestamp":"2026-09-19T02:38:46Z","type":"event_msg","payload":{"type":"token_count","info":` +
		`{"total_token_usage":{"input_tokens":` + strconv.Itoa(total) + `,"cached_input_tokens":0,"output_tokens":5},` +
		`"last_token_usage":{"input_tokens":` + strconv.Itoa(last) + `,"cached_input_tokens":0,"output_tokens":5},` +
		`"model_context_window":` + strconv.Itoa(window) + `}}}`
}

// TestCodexLevelComesFromRolloutNotTurnSum pins the whole point: the
// level is the last request's input, the flows are the turn's sum.
func TestCodexLevelComesFromRolloutNotTurnSum(t *testing.T) {
	home := t.TempDir()
	const thread = "01a0b787-d0b3-7611-9be0-962d3b6e3406"
	writeCodexRollout(t, home, thread,
		`{"timestamp":"2026-09-19T02:38:00Z","type":"response_item","payload":{"type":"message"}}`,
		tokenCountLine(18261, 18261, codexWindow),
		tokenCountLine(codexTurnSum, codexTrueLevel, codexWindow),
	)

	p := NewCodexParserIn(home)
	if _, err := p.Parse(`{"type":"thread.started","thread_id":"` + thread + `"}`); err != nil {
		t.Fatalf("thread.started: %v", err)
	}
	ev, err := p.Parse(`{"type":"turn.completed","usage":{"input_tokens":92842,"cached_input_tokens":84480,"cache_write_input_tokens":0,"output_tokens":578,"reasoning_output_tokens":177}}`)
	if err != nil {
		t.Fatalf("turn.completed: %v", err)
	}
	if ev.Type != Done {
		t.Fatalf("type = %v, want Done", ev.Type)
	}
	if ev.Usage == nil {
		t.Fatal("usage is nil")
	}
	if ev.Usage.ContextUsed != codexTrueLevel {
		t.Errorf("ContextUsed = %d, want %d (the LAST request, not the turn's %d)",
			ev.Usage.ContextUsed, codexTrueLevel, codexTurnSum)
	}
	if ev.Usage.Window != codexWindow {
		t.Errorf("Window = %d, want %d — codex reports it in the rollout only", ev.Usage.Window, codexWindow)
	}
	// Flows stay the turn's real spend: the cache WAS re-read on every
	// request, and the bill says so.
	if ev.Usage.CacheRead != 84480 {
		t.Errorf("CacheRead = %d, want 84480", ev.Usage.CacheRead)
	}
	if want := 92842 - 84480; ev.Usage.Input != want {
		t.Errorf("Input = %d, want %d (total minus cached)", ev.Usage.Input, want)
	}
	if ev.Usage.Output != 578 {
		t.Errorf("Output = %d, want 578", ev.Usage.Output)
	}
}

// TestCodexLevelUnknownWithoutRollout: no journal, no level. The meter
// keeps its previous reading, which is stale but true — reporting the
// turn sum would be neither.
func TestCodexLevelUnknownWithoutRollout(t *testing.T) {
	old := codexRolloutRetryDelay
	codexRolloutRetryDelay = time.Millisecond
	defer func() { codexRolloutRetryDelay = old }()

	p := NewCodexParserIn(t.TempDir())
	if _, err := p.Parse(`{"type":"thread.started","thread_id":"missing-thread"}`); err != nil {
		t.Fatalf("thread.started: %v", err)
	}
	ev, err := p.Parse(`{"type":"turn.completed","usage":{"input_tokens":92842,"cached_input_tokens":84480,"output_tokens":578}}`)
	if err != nil {
		t.Fatalf("turn.completed: %v", err)
	}
	if ev.Usage == nil {
		t.Fatal("usage is nil — the flows are still known even when the level is not")
	}
	if ev.Usage.ContextUsed != 0 {
		t.Errorf("ContextUsed = %d, want 0 (unknown), never the turn sum", ev.Usage.ContextUsed)
	}
	if ev.Usage.Output != 578 {
		t.Errorf("Output = %d, want 578", ev.Usage.Output)
	}
}

// TestCodexLevelMatchesTheTurnBySum: the rollout is shared by every turn
// of the thread, so the entry is chosen by the sum the stream reported —
// otherwise a turn would read the level of a later one.
func TestCodexLevelMatchesTheTurnBySum(t *testing.T) {
	home := t.TempDir()
	const thread = "thread-with-history"
	writeCodexRollout(t, home, thread,
		tokenCountLine(24477, 24477, codexWindow), // an earlier, resumed turn
		tokenCountLine(codexTurnSum, codexTrueLevel, codexWindow),
		tokenCountLine(29901, 29901, codexWindow), // a later turn, must not win
	)
	got, ok := codexRolloutLevel(home, thread, codexTurnSum)
	if !ok {
		t.Fatal("no reading found")
	}
	if got.Level != codexTrueLevel {
		t.Errorf("Level = %d, want %d — the entry whose total matches this turn", got.Level, codexTrueLevel)
	}
}

// TestCodexRolloutPathIgnoresOtherThreads guards the glob: two sessions
// in the same day dir must not read each other's numbers.
func TestCodexRolloutPathIgnoresOtherThreads(t *testing.T) {
	home := t.TempDir()
	writeCodexRollout(t, home, "thread-a", tokenCountLine(100, 100, codexWindow))
	writeCodexRollout(t, home, "thread-b", tokenCountLine(200, 200, codexWindow))

	got, ok := codexRolloutLevel(home, "thread-b", 200)
	if !ok {
		t.Fatal("thread-b not found")
	}
	if got.Level != 200 {
		t.Errorf("Level = %d, want 200 — read thread-a's file", got.Level)
	}
	if p := codexRolloutPath(home, "thread-c"); p != "" {
		t.Errorf("path for an unknown thread = %q, want empty", p)
	}
}

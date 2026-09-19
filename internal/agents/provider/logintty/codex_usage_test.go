package logintty

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// The line below is a verbatim (trimmed) token_count entry from a real
// codex-cli 0.149.1 rollout — the shape this reader exists for.
const codexTokenCountLine = `{"timestamp":"2026-09-19T02:41:56.302Z","type":"event_msg","payload":` +
	`{"type":"token_count","info":{"total_token_usage":{"input_tokens":29901},` +
	`"last_token_usage":{"input_tokens":29901},"model_context_window":258400},` +
	`"rate_limits":{"limit_id":"codex","primary":{"used_percent":1.0,"window_minutes":300,` +
	`"resets_at":1789793085},"secondary":{"used_percent":84.0,"window_minutes":10080,` +
	`"resets_at":1789837227}}}}`

func writeRollout(t *testing.T, home, name, body string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "09", "19")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadCodexUsage: the two windows codex reports map onto the same
// keys claude's do, so the UI labels them without special-casing.
func TestReadCodexUsage(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "rollout-2026-09-19T09-38-46-thread-a.jsonl",
		`{"timestamp":"2026-09-19T02:38:00Z","type":"response_item","payload":{"type":"message"}}`+"\n"+
			codexTokenCountLine+"\n")

	wins, err := readCodexUsage([]string{"CODEX_HOME=" + home})
	if err != nil {
		t.Fatalf("readCodexUsage: %v", err)
	}
	if len(wins) != 2 {
		t.Fatalf("windows = %d, want 2: %+v", len(wins), wins)
	}
	if wins[0].Key != "five_hour" || wins[0].Utilization != 1 {
		t.Errorf("primary = %+v, want the 5-hour window at 1%%", wins[0])
	}
	if wins[1].Key != "seven_day" || wins[1].Utilization != 84 {
		t.Errorf("secondary = %+v, want the weekly window at 84%%", wins[1])
	}
	if wins[1].ResetsAt.Unix() != 1789837227 {
		t.Errorf("resets_at = %v, want the unix time codex gave", wins[1].ResetsAt)
	}
	// The whole reason ObservedAt exists: this number is as old as the
	// turn that recorded it, and the UI must be able to say so.
	want, _ := time.Parse(time.RFC3339, "2026-09-19T02:41:56.302Z")
	if !wins[0].ObservedAt.Equal(want.UTC()) {
		t.Errorf("ObservedAt = %v, want %v (when codex saw it, not when we read it)", wins[0].ObservedAt, want)
	}
}

// TestReadCodexUsagePrefersTheNewestRun: limits are account-wide, so the
// most recent turn on the host is the current answer.
func TestReadCodexUsagePrefersTheNewestRun(t *testing.T) {
	home := t.TempDir()
	oldLine := `{"timestamp":"2026-09-18T01:00:00Z","type":"event_msg","payload":{"type":"token_count",` +
		`"info":{"last_token_usage":{"input_tokens":10}},"rate_limits":{"secondary":` +
		`{"used_percent":12.0,"window_minutes":10080,"resets_at":1789837227}}}}`
	oldPath := writeRollout(t, home, "rollout-2026-09-18T01-00-00-thread-old.jsonl", oldLine+"\n")
	writeRollout(t, home, "rollout-2026-09-19T09-38-46-thread-new.jsonl", codexTokenCountLine+"\n")
	// Make the ordering unambiguous regardless of write speed.
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatal(err)
	}

	wins, err := readCodexUsage([]string{"CODEX_HOME=" + home})
	if err != nil {
		t.Fatalf("readCodexUsage: %v", err)
	}
	for _, w := range wins {
		if w.Key == "seven_day" && w.Utilization != 84 {
			t.Errorf("weekly = %v%%, want 84 — read yesterday's file", w.Utilization)
		}
	}
}

// TestReadCodexUsageIgnoresRolloutsWithoutLimits: a session that died
// before its first response has no rate_limits, and must not shadow the
// file that does.
func TestReadCodexUsageIgnoresRolloutsWithoutLimits(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "rollout-2026-09-19T08-00-00-thread-has.jsonl", codexTokenCountLine+"\n")
	empty := writeRollout(t, home, "rollout-2026-09-19T09-00-00-thread-none.jsonl",
		`{"timestamp":"2026-09-19T09:00:00Z","type":"session_meta","payload":{"id":"x"}}`+"\n")
	now := time.Now()
	if err := os.Chtimes(empty, now, now); err != nil {
		t.Fatal(err)
	}

	wins, err := readCodexUsage([]string{"CODEX_HOME=" + home})
	if err != nil {
		t.Fatalf("readCodexUsage: %v", err)
	}
	if len(wins) != 2 {
		t.Fatalf("windows = %+v, want the two from the file that has them", wins)
	}
}

// TestCodexUsageGoesThroughReadUsage keeps the dispatch honest — the
// endpoint calls ReadUsage, not the codex reader directly.
func TestCodexUsageGoesThroughReadUsage(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "rollout-2026-09-19T09-38-46-thread-a.jsonl", codexTokenCountLine+"\n")
	wins, err := ReadUsage(provider.TypeCodex, []string{"CODEX_HOME=" + home})
	if err != nil || len(wins) != 2 {
		t.Fatalf("ReadUsage(codex) = %+v, %v", wins, err)
	}
}

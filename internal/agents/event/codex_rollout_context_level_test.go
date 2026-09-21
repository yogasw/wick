package event

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCodexContextLevelSkipsPostCompactionZero: codex appends a
// token_count with input_tokens 0 right after a compaction. Taking the
// newest entry blindly would report "before: 0" on the next manual
// /compact, so the newest REAL level is what must come back.
func TestCodexContextLevelSkipsPostCompactionZero(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "09", "20")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(input, total int) string {
		return `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` +
			itoa(total) + `},"last_token_usage":{"input_tokens":` + itoa(input) + `}}}}` + "\n"
	}
	body := line(31261, 31261) + line(81390, 154659) + line(0, 0)
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-09-20T09-53-35-thread-1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := CodexContextLevel(home, "thread-1")
	if !ok || got != 81390 {
		t.Fatalf("want the newest real level 81390, got %d (ok=%v)", got, ok)
	}
	if _, ok := CodexContextLevel(home, "missing-thread"); ok {
		t.Fatal("an unknown thread has no level to report")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

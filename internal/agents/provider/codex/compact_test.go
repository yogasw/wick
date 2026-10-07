package codex

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunCompactRPC(t *testing.T) {
	responses := strings.Join([]string{
		`{"id":1,"result":{}}`,
		`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"totalTokens":16035}}}}`,
		`{"id":2,"result":{"thread":{"id":"thread-1"}}}`,
		`{"id":3,"result":{}}`,
		`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"totalTokens":4944}}}}`,
		`{"method":"item/completed","params":{"item":{"type":"contextCompaction","id":"compact-1"}}}`,
		`{"method":"turn/completed","params":{"turn":{"status":"completed"}}}`,
	}, "\n") + "\n"

	var requests bytes.Buffer
	var translated bytes.Buffer
	if err := runCompactRPC(context.Background(), &requests, strings.NewReader(responses), &translated, "thread-1", 0); err != nil {
		t.Fatal(err)
	}
	if got := requests.String(); !strings.Contains(got, `"method":"thread/resume"`) || !strings.Contains(got, `"method":"thread/compact/start"`) {
		t.Fatalf("missing RPC requests: %s", got)
	}
	got := translated.String()
	if !strings.Contains(got, `"pre_tokens":16035`) || !strings.Contains(got, `"post_tokens":4944`) || !strings.Contains(got, `"type":"turn.completed"`) {
		t.Fatalf("bad translated stream: %s", got)
	}
}

// TestRunCompactRPCSurfacesServerError: an app-server error arrives as a
// JSON-RPC error envelope, not as a dead stream. Swallowing it would leave
// the turn hanging until the process was killed, so it must come back as
// the turn's error.
func TestRunCompactRPCSurfacesServerError(t *testing.T) {
	responses := `{"id":2,"error":{"message":"thread not found"}}` + "\n"
	var requests, translated bytes.Buffer
	err := runCompactRPC(context.Background(), &requests, strings.NewReader(responses), &translated, "thread-1", 0)
	if err == nil {
		t.Fatal("want an error when app-server rejects the request")
	}
	if !strings.Contains(err.Error(), "thread not found") {
		t.Fatalf("error must name the server's reason, got %v", err)
	}
	if translated.Len() != 0 {
		t.Fatalf("nothing should be translated for a failed compaction: %s", translated.String())
	}
}

// TestRunCompactRPCFailsWhenStreamEndsEarly: app-server dying mid-compaction
// must not read as success — the caller would report a compaction that never
// happened and the token meter would keep showing the old window.
func TestRunCompactRPCFailsWhenStreamEndsEarly(t *testing.T) {
	responses := strings.Join([]string{
		`{"id":1,"result":{}}`,
		`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"totalTokens":16035}}}}`,
		`{"id":2,"result":{"thread":{"id":"thread-1"}}}`,
	}, "\n") + "\n"
	var requests, translated bytes.Buffer
	err := runCompactRPC(context.Background(), &requests, strings.NewReader(responses), &translated, "thread-1", 0)
	if err == nil {
		t.Fatal("want an error when the stream ends before compaction completes")
	}
	if !strings.Contains(requests.String(), `"method":"thread/compact/start"`) {
		t.Fatalf("compaction should still have been requested: %s", requests.String())
	}
}

// TestRunCompactRPCReadsOversizedResumeLine: a resumed thread comes back as
// ONE JSON-RPC line carrying the whole history — routinely past bufio's
// 64 KiB default. That is exactly the session a person compacts, so the
// scanner's raised limit is part of the contract, not a detail.
func TestRunCompactRPCReadsOversizedResumeLine(t *testing.T) {
	huge := `{"id":2,"result":{"thread":{"id":"thread-1","padding":"` + strings.Repeat("x", 200*1024) + `"}}}`
	responses := strings.Join([]string{
		`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"totalTokens":16035}}}}`,
		huge,
		`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"totalTokens":4944}}}}`,
		`{"method":"item/completed","params":{"item":{"type":"contextCompaction","id":"compact-1"}}}`,
		`{"method":"turn/completed","params":{"turn":{"status":"completed"}}}`,
	}, "\n") + "\n"
	var requests, translated bytes.Buffer
	if err := runCompactRPC(context.Background(), &requests, strings.NewReader(responses), &translated, "thread-1", 0); err != nil {
		t.Fatal(err)
	}
	if got := translated.String(); !strings.Contains(got, `"pre_tokens":16035`) || !strings.Contains(got, `"post_tokens":4944`) {
		t.Fatalf("bad translated stream: %s", got)
	}
}

// TestRunCompactRPCWithoutTokenUsageOnResume pins the codex 0.149 shape:
// app-server answers thread/resume and then says nothing until a turn
// runs, so no thread/tokenUsage/updated precedes the compaction. Gating
// thread/compact/start on that number left the request unsent and the
// turn hanging until wick's spawn timeout killed it — the bug behind
// "/compact does nothing in the UI". The request must go out on resume
// alone, and the "before" number comes from the rollout fallback.
func TestRunCompactRPCWithoutTokenUsageOnResume(t *testing.T) {
	responses := strings.Join([]string{
		`{"id":1,"result":{}}`,
		`{"method":"thread/status/changed","params":{}}`,
		`{"id":2,"result":{"thread":{"id":"thread-1"}}}`,
		`{"id":3,"result":{}}`,
		`{"method":"turn/started","params":{}}`,
		`{"method":"item/started","params":{"item":{"type":"contextCompaction","id":"compact-1"}}}`,
		`{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{"last":{"totalTokens":4944}}}}`,
		`{"method":"item/completed","params":{"item":{"type":"contextCompaction","id":"compact-1"}}}`,
		`{"method":"turn/completed","params":{"turn":{"status":"completed"}}}`,
	}, "\n") + "\n"

	var requests, translated bytes.Buffer
	if err := runCompactRPC(context.Background(), &requests, strings.NewReader(responses), &translated, "thread-1", 16035); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requests.String(), `"method":"thread/compact/start"`) {
		t.Fatalf("compaction must be requested on resume alone: %s", requests.String())
	}
	got := translated.String()
	if !strings.Contains(got, `"pre_tokens":16035`) || !strings.Contains(got, `"post_tokens":4944`) {
		t.Fatalf("fallback must supply the before-count: %s", got)
	}
	if !strings.Contains(got, `"type":"thread.started"`) {
		t.Fatalf("resume must be reported so the spawn leaves the spawning state: %s", got)
	}
}

// TestCodexHomeFromEnv: the rollout that holds the before-count lives
// under the spawn's own CODEX_HOME, so a relocated home must be read off
// the environment rather than assumed to be ~/.codex.
func TestCodexHomeFromEnv(t *testing.T) {
	if got := codexHomeFromEnv([]string{"PATH=/bin", "CODEX_HOME=/tmp/a", "CODEX_HOME=/tmp/b"}); got != "/tmp/b" {
		t.Fatalf("last assignment wins, got %q", got)
	}
	if got := codexHomeFromEnv([]string{"PATH=/bin"}); got != "" {
		t.Fatalf("no CODEX_HOME means default, got %q", got)
	}
}

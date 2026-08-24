package slack

import (
	"os"
	"strings"
	"testing"
)

// TestOwnerStampPrecedesSend pins the ordering that decides which identity a
// Slack-started agent runs as.
//
// sendFn is what triggers a spawn, and a spawn mints its MCP credential from
// the session's UserID at that moment (ClaudeFactory.mcpTokenFor). So the
// owner has to be stamped BEFORE the first sendFn. When the stamp came after,
// the first spawn of a new thread raced it: a spawn that won fell back to the
// shared internal token — a synthetic ADMIN with no tag filter — and since the
// token is baked into the process argv, the agent kept that identity for the
// whole life of the thread. Correct-looking meta.json on disk could not undo
// it, and reuse of the running process meant retries could not either. That is
// what made the wrong identity look intermittent: stable within a thread,
// different between threads.
//
// This is asserted structurally rather than by driving handleMessage, which
// would need a live Slack API for GetUserInfo/resolveUserGroups. The ordering
// is the invariant worth protecting; a reader moving either statement gets a
// failure that explains why.
func TestOwnerStampPrecedesSend(t *testing.T) {
	src, err := os.ReadFile("slack.go")
	if err != nil {
		t.Fatalf("read slack.go: %v", err)
	}
	body := handlerBody(t, string(src))

	stamp := strings.Index(body, "s.resolveSessionOwner(ev.User)")
	if stamp < 0 {
		t.Fatal("handleMessage no longer resolves the session owner")
	}
	send := strings.Index(body, "s.sendFn(")
	if send < 0 {
		t.Fatal("handleMessage no longer dispatches via sendFn")
	}
	if stamp > send {
		t.Errorf("owner resolved at offset %d, AFTER the first sendFn at %d; "+
			"the first spawn of a new thread will race the stamp and can fall "+
			"back to the internal admin token for the life of the agent", stamp, send)
	}
}

// TestOwnerlessSlackMessageIsRefused pins that an unresolvable sender is
// refused rather than run.
//
// The fallback in ClaudeFactory.mcpTokenFor hands an ownerless session the
// shared internal token, which is a synthetic ADMIN with no tag filter. That
// makes a failure to resolve identity grant MORE access than the sender has —
// identity failing open. For a channel message there is always a human on the
// other end, so the right answer is to refuse and say so.
func TestOwnerlessSlackMessageIsRefused(t *testing.T) {
	src, err := os.ReadFile("slack.go")
	if err != nil {
		t.Fatalf("read slack.go: %v", err)
	}
	body := handlerBody(t, string(src))

	guard := strings.Index(body, `if callerUserID == "" {`)
	if guard < 0 {
		t.Fatal("handleMessage no longer guards against an unresolved caller; " +
			"an ownerless spawn silently becomes the synthetic admin")
	}
	send := strings.Index(body, "s.sendFn(")
	if send < 0 {
		t.Fatal("handleMessage no longer dispatches via sendFn")
	}
	if guard > send {
		t.Errorf("caller guard at offset %d comes AFTER sendFn at %d; the turn "+
			"would already have spawned as the synthetic admin", guard, send)
	}
	// The guard must actually stop the dispatch, not just log.
	tail := body[guard:send]
	if !strings.Contains(tail, "return") {
		t.Error("caller guard does not return before sendFn, so an unresolved " +
			"sender still reaches a spawn")
	}
}

// handlerBody returns the source of handleMessage, bounded by the next
// top-level func so a match cannot drift into a neighbouring function.
func handlerBody(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, "func (s *Channel) handleMessage(")
	if start < 0 {
		t.Fatal("handleMessage not found in slack.go")
	}
	rest := src[start+1:]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		return src[start:]
	}
	return src[start : start+1+end]
}

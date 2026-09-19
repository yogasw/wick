package pool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
)

// TestCompactOnCodexNeverReachesTheModel is the whole point of the
// guard. Measured on codex-cli 0.149.1: `codex exec` has no slash
// commands, so "/compact" arrives as a plain user message and the model
// answers "Context compacted." while the context level goes UP. A
// session that spawns nothing and says so is the honest outcome.
func TestCompactOnCodexNeverReachesTheModel(t *testing.T) {
	sp := &scriptedSpawner{}
	p, layout := newPool(t, 2, sp)
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "S-codex", Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S-codex", "default", "codex/default"); err != nil {
		t.Fatal(err)
	}

	if err := p.Send(context.Background(), "S-codex", "default", "ui", "user", "/compact"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if p.Active() != 0 {
		t.Fatalf("active agents = %d, want 0 — /compact must not spawn codex", p.Active())
	}

	body, err := os.ReadFile(layout.SessionConversation("S-codex"))
	if err != nil {
		t.Fatalf("read conversation: %v", err)
	}
	conv := string(body)
	if !strings.Contains(conv, "/compact") {
		t.Error("the command itself is missing from the transcript — it would look like the composer dropped it")
	}
	if !strings.Contains(conv, "codex has no /compact") {
		t.Errorf("no explanation recorded; transcript:\n%s", conv)
	}
}

// TestCompactOnClaudeStillGoesThrough: the guard is per provider, not a
// blanket block. Claude's print mode does handle /compact, so the
// message must reach it untouched.
func TestCompactOnClaudeStillGoesThrough(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S-claude")

	if err := p.Send(context.Background(), "S-claude", "default", "ui", "user", "/compact"); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, func() bool {
		sp.mu.Lock()
		defer sp.mu.Unlock()
		return sp.Last != nil
	}, 2*time.Second)

	sp.mu.Lock()
	last := sp.Last
	sp.mu.Unlock()
	if last == nil {
		t.Fatal("claude was never spawned — /compact was swallowed for a provider that supports it")
	}
}

// TestIsCompactCommand pins the narrow shape: only the bare command.
// "/compact please" is a sentence, and a person who wrote it meant the
// model to read it.
func TestIsCompactCommand(t *testing.T) {
	for _, in := range []string{"/compact", "  /compact  ", "/COMPACT"} {
		if !isCompactCommand(in) {
			t.Errorf("%q should be the command", in)
		}
	}
	for _, in := range []string{"/compact please", "tolong /compact", "compact", "/compacted", ""} {
		if isCompactCommand(in) {
			t.Errorf("%q should be an ordinary message", in)
		}
	}
}

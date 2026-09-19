package pool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// TestCompactOnCodexReachesTheProvider: codex was the one provider this
// guard used to stop. `codex exec` has no slash commands, so "/compact"
// arrived as a plain user message and the model answered "Context
// compacted." while the context level went UP. The codex spawner now
// intercepts the bare command and compacts the persisted thread over the
// app-server thread/compact/start RPC, so the message must reach the
// provider like any other — blocking it here would disable the only path
// that actually compacts.
func TestCompactOnCodexReachesTheProvider(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
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
	waitFor(t, func() bool {
		sp.mu.Lock()
		defer sp.mu.Unlock()
		return sp.Last != nil
	}, 2*time.Second)

	sp.mu.Lock()
	last := sp.Last
	sp.mu.Unlock()
	if last == nil {
		t.Fatal("codex was never spawned — /compact was swallowed by the unsupported guard")
	}

	body, err := os.ReadFile(layout.SessionConversation("S-codex"))
	if err != nil {
		t.Fatalf("read conversation: %v", err)
	}
	if conv := string(body); strings.Contains(conv, provider.CompactUnsupportedNote) {
		t.Errorf("wick answered on codex's behalf for a provider that can compact; transcript:\n%s", conv)
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

// TestCompactUnsupportedIsRecordedForAnOptedOutProvider keeps the other
// half of the guard alive. No provider type opts out today, so nothing
// reaches recordCompactUnsupported through Send — exercise it directly,
// otherwise the transcript it writes (the command, then wick's own
// answer) would rot unnoticed until the next provider needs it.
func TestCompactUnsupportedIsRecordedForAnOptedOutProvider(t *testing.T) {
	sp := &scriptedSpawner{}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S-optout")

	p.recordCompactUnsupported(context.Background(), "S-optout", "default", "ui", "/compact", nil)

	body, err := os.ReadFile(layout.SessionConversation("S-optout"))
	if err != nil {
		t.Fatalf("read conversation: %v", err)
	}
	conv := string(body)
	if !strings.Contains(conv, "/compact") {
		t.Error("the command itself is missing from the transcript — it would look like the composer dropped it")
	}
	if !strings.Contains(conv, provider.CompactUnsupportedNote) {
		t.Errorf("no explanation recorded; transcript:\n%s", conv)
	}
	if n := sp.callsSnapshot(); n != 0 {
		t.Errorf("spawned %d agents — answering in the transcript must not start one", n)
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

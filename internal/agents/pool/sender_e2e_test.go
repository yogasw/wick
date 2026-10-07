package pool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// scriptedOK is the minimal stream-json a spawn needs to complete a turn.
func scriptedOK() *scriptedSpawner {
	return &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hi"}`,
	}}}
}

// readTurns parses the session's conversation.jsonl.
func readTurns(t *testing.T, sessionDir string) []store.ConversationTurn {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(sessionDir, "conversation.jsonl"))
	if err != nil {
		t.Fatalf("read conversation.jsonl: %v", err)
	}
	var out []store.ConversationTurn
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var turn store.ConversationTurn
		if err := json.Unmarshal([]byte(line), &turn); err != nil {
			t.Fatalf("bad turn line %q: %v", line, err)
		}
		out = append(out, turn)
	}
	return out
}

// firstUserTurn returns the first role=="user" turn. Indexing blindly is
// wrong: the session file opens with a placeholder line, and an assistant
// turn follows, so position says nothing about which turn is whose.
func firstUserTurn(t *testing.T, sessionDir string) store.ConversationTurn {
	t.Helper()
	for _, turn := range readTurns(t, sessionDir) {
		if turn.Role == "user" {
			return turn
		}
	}
	t.Fatal("no user turn persisted")
	return store.ConversationTurn{}
}

// The split that makes this feature work: the model is told who is speaking,
// while the stored turn keeps the message the person actually typed with the
// sender in its own field. Getting either half wrong is a real bug — a
// prefix in storage would render raw in the dashboard, and a missing sender
// field would leave the UI nothing to build its chip from.
func TestSendSeparatesSenderFromStoredText(t *testing.T) {
	sp := scriptedOK()
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	sender := &store.Sender{ID: "U0104", Name: "Yoga Setiawan", Handle: "yoga", Channel: "slack"}
	p.cfg.SenderFrom = func(context.Context) *store.Sender { return sender }

	if err := p.Send(context.Background(), "S1", "default", "slack", "user", "cek error 401"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	// What the model received.
	sp.mu.Lock()
	last := sp.Last
	sp.mu.Unlock()
	stdin := last.recordedStdin()
	if !strings.Contains(stdin, `[from: Yoga Setiawan]`) {
		t.Errorf("model did not receive the sender line, stdin: %s", stdin)
	}

	// What went to disk.
	got := firstUserTurn(t, layout.SessionDir("S1"))
	if got.Text != "cek error 401" {
		t.Errorf("stored text = %q, want the original message with no sender line", got.Text)
	}
	if got.Sender == nil {
		t.Fatal("sender not persisted — the UI has nothing to render a chip from")
	}
	if got.Sender.ID != "U0104" || got.Sender.Name != "Yoga Setiawan" || got.Sender.Channel != "slack" {
		t.Errorf("sender = %+v", *got.Sender)
	}
}

// Turning visibility off is a privacy control over the MODEL's copy only.
// The stored sender must survive, or the setting would silently destroy
// history the dashboard still needs.
func TestSendVisibilityOffStillPersistsSender(t *testing.T) {
	sp := scriptedOK()
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.cfg.SenderFrom = func(context.Context) *store.Sender {
		return &store.Sender{ID: "U0104", Name: "Yoga Setiawan", Channel: "slack"}
	}
	p.cfg.SenderVisibilityLoader = func() string { return store.SenderOff }

	if err := p.Send(context.Background(), "S1", "default", "slack", "user", "halo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	sp.mu.Lock()
	last := sp.Last
	sp.mu.Unlock()
	if strings.Contains(last.recordedStdin(), "[from:") {
		t.Error("SenderOff still sent an identity line to the model")
	}

	got := firstUserTurn(t, layout.SessionDir("S1"))
	if got.Sender == nil {
		t.Fatal("sender must still be stored when the model is not told about it")
	}
	if got.Sender.Name != "Yoga Setiawan" {
		t.Errorf("stored sender = %+v", *got.Sender)
	}
}

// Non-user turns have no human behind them. A system context block or a
// sub-agent result must never be stamped with whoever happens to be on the
// context.
func TestSendSystemTurnCarriesNoSender(t *testing.T) {
	sp := scriptedOK()
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.cfg.SenderFrom = func(context.Context) *store.Sender {
		return &store.Sender{ID: "U0104", Name: "Yoga Setiawan", Channel: "slack"}
	}

	if err := p.Send(context.Background(), "S1", "default", "slack", "system", "[channel context]"); err != nil {
		t.Fatal(err)
	}
	// A system turn does not spawn on its own; it is buffered and persisted.
	var sys store.ConversationTurn
	waitFor(t, func() bool {
		for _, turn := range readTurns(t, layout.SessionDir("S1")) {
			if turn.Role == "system" {
				sys = turn
				return true
			}
		}
		return false
	}, 2*time.Second)

	if sys.Sender != nil {
		t.Errorf("system turn was attributed to %+v", *sys.Sender)
	}
	if strings.Contains(sys.Text, "[from:") {
		t.Errorf("system turn text was rewritten: %q", sys.Text)
	}
}

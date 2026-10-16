package pool

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/store"
)

const replyQuote = `> Replying to @ops: \"deploy finished\"`

func testReply() *store.ReplyTo {
	return &store.ReplyTo{TurnID: "turn-3", Role: "assistant", Author: "@ops", Excerpt: "deploy finished"}
}

// sawQuote reports whether the model's stdin holds the quote line (stdin is
// stream-json, so its quotes arrive escaped).
func sawQuote(stdin string) bool {
	return strings.Contains(stdin, replyQuote) || strings.Contains(stdin, `> Replying to @ops: "deploy finished"`)
}

// A web reply reaches the model as a quote line in front of the person's
// words, while the stored turn keeps only what they typed plus the quote
// as its own field, under the id the endpoint chose. Buffered/spawn path.
func TestSendReplyQuotesForModelNotStorage(t *testing.T) {
	sp := scriptedOK()
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	ctx := store.WithUserTurnID(store.WithReplyTo(context.Background(), "S1", testReply()), "S1", "1700000000000000001")
	if err := p.Send(ctx, "S1", "default", "ui", "user", "roll it back"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	sp.mu.Lock()
	last := sp.Last
	sp.mu.Unlock()
	if stdin := last.recordedStdin(); !sawQuote(stdin) {
		t.Errorf("model did not receive the quote line, stdin: %s", stdin)
	}
	got := firstUserTurn(t, layout.SessionDir("S1"))
	if got.Text != "roll it back" {
		t.Errorf("stored text = %q, want the person's words only", got.Text)
	}
	if got.ReplyTo == nil || *got.ReplyTo != *testReply() {
		t.Errorf("reply_to = %+v, want %+v", got.ReplyTo, testReply())
	}
	if got.TurnID != "1700000000000000001" {
		t.Errorf("turn id = %q, want the one on ctx", got.TurnID)
	}
}

// holdSpawner starts processes that write their lines and then keep stdout
// open, so the agent stays alive and the next send takes the live path.
type holdSpawner struct {
	mu    sync.Mutex
	lines []string
	procs []*scriptedProc
}

func (s *holdSpawner) Spawn(_ context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	pr, pw := io.Pipe()
	proc := &scriptedProc{stdoutR: pr, stdoutW: pw, stdinBuf: &bytes.Buffer{}, opt: opt, done: make(chan struct{}), pid: 71000}
	s.mu.Lock()
	s.procs = append(s.procs, proc)
	s.mu.Unlock()
	go func() {
		for _, l := range s.lines {
			if _, err := pw.Write([]byte(l + "\n")); err != nil {
				return
			}
		}
	}()
	return proc, nil
}

func (s *holdSpawner) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.procs) }

func (s *holdSpawner) stdin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.procs) == 0 {
		return ""
	}
	return s.procs[len(s.procs)-1].recordedStdin()
}

// The live path (a running agent) quotes and stores the reply the same way.
func TestSendReplyLivePath(t *testing.T) {
	held := &holdSpawner{lines: scriptedOK().Lines[0]}
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	factory := &ClaudeFactory{Layout: layout, Spawner: held}
	p := New(PoolConfig{MaxConcurrent: 2, IdleTimeout: time.Minute, Layout: layout, Factory: factory})
	factory.OnExit = p.HandleExit
	t.Cleanup(p.Stop)
	setupSession(t, layout, "S1")

	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, turn := range readTurns(t, layout.SessionDir("S1")) {
			if turn.Role == "assistant" {
				return true
			}
		}
		return false
	}, 3*time.Second)
	if p.Active() != 1 {
		t.Fatalf("agent not alive for the live path: active=%d", p.Active())
	}

	ctx := store.WithReplyTo(context.Background(), "S1", testReply())
	if err := p.Send(ctx, "S1", "default", "ui", "user", "roll it back"); err != nil {
		t.Fatal(err)
	}
	if held.calls() != 1 {
		t.Fatalf("second send respawned (calls=%d) — not the live path", held.calls())
	}
	waitFor(t, func() bool { return sawQuote(held.stdin()) }, 2*time.Second)
	var found bool
	for _, turn := range readTurns(t, layout.SessionDir("S1")) {
		if turn.Role == "user" && turn.Text == "roll it back" {
			found = true
			if turn.ReplyTo == nil || turn.ReplyTo.TurnID != "turn-3" || turn.TurnID == "" {
				t.Fatalf("live turn = %+v", turn)
			}
		}
	}
	if !found {
		t.Fatal("live reply turn not stored")
	}
}

// The B1 regression: a reply (and a fixed turn id) made for one session
// never lands on a send into another, even when the same ctx travels there
// — which is what routing an @mention to a sub-agent or teammate does.
func TestSendReplyBoundToItsSession(t *testing.T) {
	sp := scriptedOK()
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S2")

	ctx := store.WithUserTurnID(store.WithReplyTo(context.Background(), "S1", testReply()), "S1", "42")
	if err := p.Send(ctx, "S2", "default", "ui", "user", "@investigator check this"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)
	sp.mu.Lock()
	last := sp.Last
	sp.mu.Unlock()
	if stdin := last.recordedStdin(); strings.Contains(stdin, "Replying to") {
		t.Errorf("quote of S1 reached S2's model: %s", stdin)
	}
	got := firstUserTurn(t, layout.SessionDir("S2"))
	if got.ReplyTo != nil {
		t.Errorf("S2 turn carries S1's reply: %+v", got.ReplyTo)
	}
	if got.TurnID == "42" || got.TurnID == "" {
		t.Errorf("S2 turn id = %q, want its own", got.TurnID)
	}
}

// A system turn never carries a reply, even when ctx holds one.
func TestSendSystemTurnCarriesNoReply(t *testing.T) {
	sp := scriptedOK()
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")
	ctx := store.WithReplyTo(context.Background(), "S1", testReply())
	if err := p.Send(ctx, "S1", "default", "ui", "system", "context"); err != nil {
		t.Fatal(err)
	}
	for _, turn := range readTurns(t, layout.SessionDir("S1")) {
		if turn.ReplyTo != nil {
			t.Fatalf("system turn got reply_to %+v", turn.ReplyTo)
		}
	}
}

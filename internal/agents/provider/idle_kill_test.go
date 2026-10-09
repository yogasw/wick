package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
)

type idleKillRig struct {
	a        *Agent
	pw       interface{ Write([]byte) (int, error) }
	exited   chan ExitReason
	convPath string
}

// newIdleKillRig starts an agent on a process that stays alive and only
// says what the test writes, with a store so flushes land on disk. The
// fake's Wait returns nil — the kill looks like a clean exit unless the
// agent remembers it killed the process.
func newIdleKillRig(t *testing.T, idle time.Duration) *idleKillRig {
	t.Helper()
	pr, pw := makePipePair()
	layout := config.NewLayout(t.TempDir())
	st := store.New(store.Options{Layout: layout, SessionID: "s1", AgentName: "main"})
	exited := make(chan ExitReason, 4)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   idle,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       &keepAliveSpawner{stdoutR: pr, stdoutW: pw},
		State:         state.New(nil),
		Store:         st,
		OnExit:        func(r ExitReason, _ string) { exited <- r },
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return &idleKillRig{a: a, pw: pw, exited: exited, convPath: layout.SessionConversation("s1")}
}

func (r *idleKillRig) say(t *testing.T, line string) {
	t.Helper()
	if _, err := r.pw.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
}

func (r *idleKillRig) waitExit(t *testing.T) ExitReason {
	t.Helper()
	select {
	case reason := <-r.exited:
		return reason
	case <-time.After(3 * time.Second):
		t.Fatal("idle timer did not end the spawn")
		return ExitClean
	}
}

func (r *idleKillRig) turns(t *testing.T) []store.ConversationTurn {
	t.Helper()
	f, err := os.Open(r.convPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []store.ConversationTurn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var turn store.ConversationTurn
		if json.Unmarshal([]byte(line), &turn) != nil || turn.Role == "" {
			continue // file header
		}
		out = append(out, turn)
	}
	return out
}

// The kill closes stdout, so the reader can reach EOF and file the exit
// before the timer does; Wait says nil. Either way the exit is the idle
// kill — filed as clean, the pool took a killed turn for a turn boundary
// and the session stayed "running".
func TestAgentIdleKillMidTurnIsExitIdle(t *testing.T) {
	for i := 0; i < 50; i++ {
		r := newIdleKillRig(t, 30*time.Millisecond)
		r.say(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"half an answer"}]}}`)
		if got := r.waitExit(t); got != ExitIdle {
			t.Fatalf("run %d: exit reason %v, want ExitIdle", i, got)
		}
		_ = r.a.Stop()
	}
}

// A turn cut by the idle timer reaches the transcript with what it had
// said and who stopped it, instead of waiting in inflight.jsonl for the
// next boot.
func TestAgentIdleKillMidTurnFlushesInterruptedTurn(t *testing.T) {
	r := newIdleKillRig(t, 50*time.Millisecond)
	r.say(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"half an answer"}]}}`)
	r.waitExit(t)
	turns := r.turns(t)
	if len(turns) != 1 {
		t.Fatalf("turns: %+v", turns)
	}
	got := turns[0]
	if !got.Interrupted || got.InterruptedBy != "wick" || !strings.Contains(got.Text, "half an answer") {
		t.Fatalf("turn: %+v", got)
	}
	if !strings.Contains(got.InterruptedNote, "idle timer") {
		t.Fatalf("note: %q", got.InterruptedNote)
	}
}

// Reaping an agent between turns interrupts nothing, so it writes nothing.
func TestAgentIdleKillBetweenTurnsWritesNothing(t *testing.T) {
	r := newIdleKillRig(t, 50*time.Millisecond)
	r.say(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`)
	r.say(t, `{"type":"result","subtype":"success","is_error":false,"result":"done"}`)
	waitFor(t, func() bool { return len(r.turns(t)) == 1 }, 2*time.Second)
	if got := r.waitExit(t); got != ExitIdle {
		t.Fatalf("exit reason %v", got)
	}
	turns := r.turns(t)
	if len(turns) != 1 || turns[0].Interrupted {
		t.Fatalf("idle reap between turns wrote a turn: %+v", turns)
	}
}

// A message the process took and never wrote a line after is handed back
// on an idle kill, so the pool can re-buffer it instead of losing it.
func TestAgentIdleKillHandsBackUnansweredMessages(t *testing.T) {
	r := newIdleKillRig(t, 50*time.Millisecond)
	if err := r.a.Send("first"); err != nil {
		t.Fatal(err)
	}
	if err := r.a.Send("second"); err != nil {
		t.Fatal(err)
	}
	if got := r.waitExit(t); got != ExitIdle {
		t.Fatalf("exit reason %v", got)
	}
	got := r.a.TakeUnanswered()
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("unanswered: %q", got)
	}
	if again := r.a.TakeUnanswered(); len(again) != 0 {
		t.Fatalf("second take: %q", again)
	}
}

// Output after a message means the process is working on it: nothing is
// handed back when it is later reaped.
func TestAgentOutputClearsUnansweredMessages(t *testing.T) {
	r := newIdleKillRig(t, 50*time.Millisecond)
	if err := r.a.Send("hello"); err != nil {
		t.Fatal(err)
	}
	r.say(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`)
	r.say(t, `{"type":"result","subtype":"success","is_error":false,"result":"hi"}`)
	r.waitExit(t)
	if got := r.a.TakeUnanswered(); len(got) != 0 {
		t.Fatalf("unanswered after output: %q", got)
	}
}

// Package agents holds an end-to-end test that exercises the full
// phase 2 pipeline (pool → factory → agent → spawner → parser →
// state → store → session) using a fake spawner so no real claude
// binary is needed.
//
// This is the closest thing we get to an integration test in CI; the
// real claude smoke test happens manually.
package agents_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

// TestPipeline_HappyPath_HelloWorld walks one full message round
// trip: Send → spawn → text deltas → Done → conversation.jsonl
// has user + assistant turns → cli_session_id persisted.
func TestPipeline_HappyPath_HelloWorld(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"claude-abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hello world"}`,
	}}}
	p, layout := newE2EPool(t, 2, sp)
	setupSess(t, layout, "S1")

	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "say hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 10*time.Second)

	turns := readTurns(t, layout, "S1")
	if len(turns) != 2 {
		t.Fatalf("turns: got %d, want 2: %+v", len(turns), turns)
	}
	if turns[0].Role != "user" || turns[0].Text != "say hi" {
		t.Fatalf("user turn: %+v", turns[0])
	}
	if turns[1].Role != "assistant" || turns[1].Text != "hello world" {
		t.Fatalf("assistant turn: %+v", turns[1])
	}
	sess, _ := session.Load(layout, "S1")
	if sess.Agents[0].CLISessionID != "claude-abc" {
		t.Fatalf("cli_session_id: %q", sess.Agents[0].CLISessionID)
	}
}

// TestPipeline_ResumeAfterIdleKill: first spawn captures session_id,
// idle TTL kills it, second message reuses the captured ID via
// --resume.
func TestPipeline_ResumeAfterIdleKill(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{
		{
			`{"type":"system","subtype":"init","session_id":"resume-me"}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}`,
			`{"type":"result","subtype":"success","is_error":false,"result":"first"}`,
		},
		{
			`{"type":"system","subtype":"init","session_id":"resume-me"}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"second"}]}}`,
			`{"type":"result","subtype":"success","is_error":false,"result":"second"}`,
		},
	}}
	p, layout := newE2EPool(t, 2, sp)
	setupSess(t, layout, "S1")

	_ = p.Send(context.Background(), "S1", "default", "ui", "user", "first ask")
	waitFor(t, func() bool { return p.Active() == 0 }, 10*time.Second)
	_ = p.Send(context.Background(), "S1", "default", "ui", "user", "second ask")
	waitFor(t, func() bool { return p.Active() == 0 && sp.spawnCount() == 2 }, 10*time.Second)

	if got := sp.resumeIDs(); len(got) != 2 || got[1] != "resume-me" {
		t.Fatalf("resume IDs forwarded to spawn: %v", got)
	}
	turns := readTurns(t, layout, "S1")
	// 2 user + 2 assistant = 4 turns.
	if len(turns) != 4 {
		t.Fatalf("turns: %d (%+v)", len(turns), turns)
	}
}

// TestPipeline_ParserErrorSurfacedAsErrorEvent: a malformed line
// shouldn't crash the agent — it's emitted as an Error event and the
// pipeline keeps going.
func TestPipeline_ParserErrorSurfacedAsErrorEvent(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"x"}`,
		`not json at all`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"after error"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"after error"}`,
	}}}
	p, layout := newE2EPool(t, 2, sp)
	setupSess(t, layout, "S1")
	_ = p.Send(context.Background(), "S1", "default", "ui", "user", "go")
	waitFor(t, func() bool { return p.Active() == 0 }, 10*time.Second)
	turns := readTurns(t, layout, "S1")
	// Error event flushes whatever's buffered (nothing yet here) so
	// we may end up with one assistant turn from the post-error delta
	// + Done. Just confirm the pipeline didn't choke and we got a
	// final assistant turn.
	if len(turns) < 2 {
		t.Fatalf("turns: %d (%+v)", len(turns), turns)
	}
}

// helpers ---------------------------------------------------------

func newE2EPool(t *testing.T, max int, sp provider.Spawner) (*pool.Pool, config.Layout) {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	factory := &pool.ClaudeFactory{Layout: layout, Spawner: sp}
	p := pool.New(pool.PoolConfig{
		MaxConcurrent: max,
		IdleTimeout:   200 * time.Millisecond,
		Layout:        layout,
		Factory:       factory,
	})
	factory.OnExit = p.HandleExit
	t.Cleanup(p.Stop)
	return p, layout
}

func setupSess(t *testing.T, layout config.Layout, id string) {
	t.Helper()
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     id,
		Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, id, "default", "claude"); err != nil {
		t.Fatal(err)
	}
}

func readTurns(t *testing.T, layout config.Layout, id string) []store.ConversationTurn {
	t.Helper()
	var out []store.ConversationTurn
	err := storage.ReadJSONL(layout.SessionConversation(id), func(line []byte) bool {
		var turn store.ConversationTurn
		if err := json.Unmarshal(line, &turn); err != nil {
			t.Fatalf("decode: %v\nline: %s", err, line)
		}
		out = append(out, turn)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never satisfied")
}

// scriptedSpawner duplicates the per-package fake but lives at the
// integration-test scope so it can be shared across e2e cases.
type scriptedSpawner struct {
	mu        sync.Mutex
	Lines     [][]string
	calls     int
	resumeArg []string
}

func (s *scriptedSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	s.mu.Lock()
	idx := s.calls
	s.calls++
	s.resumeArg = append(s.resumeArg, opt.ResumeID)
	var lines []string
	if idx < len(s.Lines) {
		lines = s.Lines[idx]
	}
	s.mu.Unlock()
	pr, pw := io.Pipe()
	proc := &scriptedProc{
		stdoutR:  pr,
		stdoutW:  pw,
		stdinBuf: &bytes.Buffer{},
		done:     make(chan struct{}),
		pid:      80000 + idx,
	}
	go func() {
		for _, l := range lines {
			if _, err := pw.Write([]byte(l + "\n")); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	return proc, nil
}

func (s *scriptedSpawner) spawnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedSpawner) resumeIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.resumeArg))
	copy(out, s.resumeArg)
	return out
}

type scriptedProc struct {
	stdoutR  *io.PipeReader
	stdoutW  *io.PipeWriter
	stdinMu  sync.Mutex
	stdinBuf *bytes.Buffer
	done     chan struct{}
	once     sync.Once
	pid      int
}

func (p *scriptedProc) Stdout() io.Reader     { return p.stdoutR }
func (p *scriptedProc) Stdin() io.WriteCloser { return &scriptedStdin{p: p} }
func (p *scriptedProc) Wait() error           { <-p.done; return nil }
func (p *scriptedProc) Pid() int              { return p.pid }
func (p *scriptedProc) Binary() string        { return "" }
func (p *scriptedProc) Argv() []string        { return nil }
func (p *scriptedProc) Kill() error {
	p.once.Do(func() {
		_ = p.stdoutR.Close()
		_ = p.stdoutW.Close()
		close(p.done)
	})
	return nil
}

type scriptedStdin struct{ p *scriptedProc }

func (s *scriptedStdin) Write(b []byte) (int, error) {
	s.p.stdinMu.Lock()
	defer s.p.stdinMu.Unlock()
	return s.p.stdinBuf.Write(b)
}
func (s *scriptedStdin) Close() error { return nil }

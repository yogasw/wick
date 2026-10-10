package agents

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// heldProc is an agent process that starts a turn and never ends it.
type heldProc struct {
	r    *io.PipeReader
	w    *io.PipeWriter
	mu   sync.Mutex
	in   bytes.Buffer
	done chan struct{}
	once sync.Once
}

func (p *heldProc) Stdout() io.Reader     { return p.r }
func (p *heldProc) Stdin() io.WriteCloser { return heldStdin{p} }
func (p *heldProc) Wait() error           { <-p.done; return nil }
func (p *heldProc) Pid() int              { return 72000 }
func (p *heldProc) Binary() string        { return "" }
func (p *heldProc) Argv() []string        { return nil }
func (p *heldProc) Env() []string         { return nil }
func (p *heldProc) Kill() error {
	p.once.Do(func() {
		_ = p.r.Close()
		_ = p.w.Close()
		close(p.done)
	})
	return nil
}

type heldStdin struct{ p *heldProc }

func (s heldStdin) Write(b []byte) (int, error) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()
	return s.p.in.Write(b)
}
func (heldStdin) Close() error { return nil }

type heldSpawner struct{}

func (heldSpawner) Spawn(context.Context, provider.SpawnOptions) (provider.Process, error) {
	r, w := io.Pipe()
	p := &heldProc{r: r, w: w, done: make(chan struct{})}
	go func() { _, _ = w.Write([]byte(`{"type":"system","subtype":"init","session_id":"abc"}` + "\n")) }()
	return p, nil
}

// stopTaskWorld runs a team task's turn in session "s1" of a real pool
// wired like the server's (OnSend: NoteSessionMessage), with a fresh
// gate. It returns the pool.
func stopTaskWorld(t *testing.T) *pool.Pool {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "s1", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "s1", "default", "claude"); err != nil {
		t.Fatal(err)
	}
	factory := &pool.ClaudeFactory{Layout: layout, Spawner: heldSpawner{}}
	p := pool.New(pool.PoolConfig{Layout: layout, MaxConcurrent: 2, Factory: factory, OnSend: NoteSessionMessage})
	factory.OnExit = p.HandleExit
	prevPool, prevGate := globalPool, teamTurnGate
	globalPool, teamTurnGate = p, teamlink.NewTurnGate(poolSessionBusy)
	t.Cleanup(func() {
		p.Stop()
		globalPool, teamTurnGate = prevPool, prevGate
	})

	release, err := teamTurnGate.Acquire(context.Background(), "s1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	if err := p.Send(context.Background(), "s1", "default", sourceTeam, "user", "long scan"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the task's turn", func() bool { return p.Active() == 1 })
	return p
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A person's message sent into the session through the pool while a task's
// turn runs keeps the real StopTask from killing that turn.
func TestStopTaskSparesTurnAPersonsMessageReached(t *testing.T) {
	p := stopTaskWorld(t)
	if err := p.Send(teamlink.WithPersonMessage(context.Background()), "s1", "default", "ui", "user", "still there?"); err != nil {
		t.Fatal(err)
	}
	out, err := poolTurns{}.StopTask(context.Background(), teamlink.Peer{}, "s1", "t1", "@captain")
	if err != nil || out != teamlink.TaskStopShared {
		t.Fatalf("stop = %v, %v; want shared", out, err)
	}
	time.Sleep(50 * time.Millisecond)
	if p.Active() != 1 {
		t.Fatal("the turn with the person's message in it was killed")
	}
}

// A sub-agent's result (no person behind it) reaching the task's turn
// leaves the real StopTask free to kill it.
func TestStopTaskKillsTurnASubAgentResultReached(t *testing.T) {
	p := stopTaskWorld(t)
	if err := p.Send(context.Background(), "s1", "default", "subagent", "user", "sub-agent finished"); err != nil {
		t.Fatal(err)
	}
	out, err := poolTurns{}.StopTask(context.Background(), teamlink.Peer{}, "s1", "t1", "@captain")
	if err != nil || out != teamlink.TaskStopRunning {
		t.Fatalf("stop = %v, %v; want running", out, err)
	}
	waitUntil(t, "the turn killed", func() bool { return p.Active() == 0 })
}

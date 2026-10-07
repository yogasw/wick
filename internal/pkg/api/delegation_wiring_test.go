package api

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// heldTurn is an in-wick turn (Pid 0, like an omp RPC turn): it runs
// until killed and ignores its spawn ctx.
type heldTurn struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	done   chan struct{}
	once   sync.Once
	killed bool
	mu     sync.Mutex
}

func (p *heldTurn) Stdout() io.Reader     { return p.pr }
func (p *heldTurn) Stdin() io.WriteCloser { return nopWC{} }
func (p *heldTurn) Wait() error           { <-p.done; return nil }
func (p *heldTurn) Pid() int              { return 0 }
func (p *heldTurn) Binary() string        { return "" }
func (p *heldTurn) Argv() []string        { return nil }
func (p *heldTurn) Env() []string         { return nil }
func (p *heldTurn) Kill() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.killed = true
		p.mu.Unlock()
		_ = p.pw.Close()
		close(p.done)
	})
	return nil
}

type nopWC struct{}

func (nopWC) Write(b []byte) (int, error) { return len(b), nil }
func (nopWC) Close() error                { return nil }

type heldSpawner struct {
	mu    sync.Mutex
	turns []*heldTurn
	msgs  []string
}

func (s *heldSpawner) Spawn(_ context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	pr, pw := io.Pipe()
	p := &heldTurn{pr: pr, pw: pw, done: make(chan struct{})}
	s.mu.Lock()
	s.turns = append(s.turns, p)
	s.msgs = append(s.msgs, opt.InitialMessage)
	s.mu.Unlock()
	return p, nil
}

// A message to a finished omp sub-agent arrives through a connector call
// whose ctx is cancelled as soon as it answers "sent". The turn it wakes
// must survive that: before the fix it was killed (or, for omp, left
// wedged before its prompt) the moment the call returned.
func TestSendToChildTurnOutlivesCallerCtx(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	sp := &heldSpawner{}
	factory := &agentpool.ClaudeFactory{Layout: layout, Spawner: sp}
	pool := agentpool.New(agentpool.PoolConfig{MaxConcurrent: 2, IdleTimeout: time.Minute, Layout: layout, Factory: factory})
	factory.OnExit = pool.HandleExit
	t.Cleanup(pool.Stop)
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "C1", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "C1", "main", "omp"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := (poolSteerer{pool: pool}).SendToChild(ctx, "C1", "main", "keep going"); err != nil {
		t.Fatal(err)
	}
	cancel() // the connector call answered "sent"

	deadline := time.Now().Add(2 * time.Second)
	for {
		sp.mu.Lock()
		n := len(sp.turns)
		sp.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turns spawned = %d, want 1", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	sp.mu.Lock()
	turn, msg := sp.turns[0], sp.msgs[0]
	sp.mu.Unlock()
	if !strings.Contains(msg, "keep going") {
		t.Fatalf("turn prompt = %q", msg)
	}
	turn.mu.Lock()
	killed := turn.killed
	turn.mu.Unlock()
	if killed {
		t.Fatal("the woken turn died with the call that delivered its message")
	}
}

package pool

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/state"
)

// linesSpawner emits canned lines per spawn and exits clean.
type linesSpawner struct{ lines []string }

func (s linesSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	pr, pw := io.Pipe()
	go func() {
		for _, l := range s.lines {
			_, _ = io.WriteString(pw, l+"\n")
		}
		_ = pw.Close()
	}()
	return &linesProc{r: pr}, nil
}

type linesProc struct{ r *io.PipeReader }

func (p *linesProc) Stdout() io.Reader     { return p.r }
func (p *linesProc) Stdin() io.WriteCloser { return nopStdin{} }
func (p *linesProc) Wait() error           { return nil }
func (p *linesProc) Pid() int              { return 0 }
func (p *linesProc) Binary() string        { return "omp" }
func (p *linesProc) Argv() []string        { return nil }
func (p *linesProc) Env() []string         { return nil }
func (p *linesProc) Kill() error           { return p.r.Close() }

// omp RPC reports a missing --resume session as the turn's error and exits
// clean. The clean exit must still clear the persisted id and tell the
// user — once — so the next turn starts fresh instead of failing again.
func TestHealStaleResumeInBandOnCleanExit(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var notices []string
	p := New(PoolConfig{MaxConcurrent: 2, IdleTimeout: time.Second, Layout: layout,
		Factory: &ClaudeFactory{Layout: layout, Spawner: linesSpawner{}},
		OnSpawnError: func(ev SpawnErrorEvent) {
			mu.Lock()
			notices = append(notices, ev.Message)
			mu.Unlock()
		}})
	t.Cleanup(p.Stop)
	setupSession(t, layout, "s-heal")
	if err := session.SetCLISessionID(layout, "s-heal", "default", "sid-gone"); err != nil {
		t.Fatal(err)
	}

	a := provider.New(provider.Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   time.Second,
		ParserFactory: func() event.Parser { return event.NewOMPParser("omp") },
		Spawner: linesSpawner{lines: []string{
			`{"type":"session","id":"","cwd":"/w"}`,
			`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"omp rpc exited before ready: Error: Session \"sid-gone\" not found."}}`,
			`{"type":"agent_end","messages":[]}`,
		}},
		State:    state.New(nil),
		SendMode: provider.SendRespawnQueue,
		ResumeID: "sid-gone",
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Send("hi"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for (a.Running() || a.ResumeID() != "") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	p.mu.Lock()
	p.active[sessionKey("s-heal", "default")] = &runEntry{agent: a, sessID: "s-heal", agentNm: "default"}
	p.mu.Unlock()
	// Hand-placed entry: take it out again, or Stop waits for it forever.
	t.Cleanup(func() {
		_ = a.Stop()
		p.mu.Lock()
		delete(p.active, sessionKey("s-heal", "default"))
		p.mu.Unlock()
	})

	p.healStaleResume("s-heal", "default")
	p.healStaleResume("s-heal", "default") // a second exit must not repeat it

	loaded, err := session.Load(layout, "s-heal")
	if err != nil {
		t.Fatal(err)
	}
	if id := loaded.Agents[0].CLISessionID; id != "" {
		t.Fatalf("persisted resume id = %q, want cleared", id)
	}
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(notices)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 || !strings.Contains(notices[0], "starts a fresh one") {
		t.Fatalf("notices = %q, want the resume-dropped notice once", notices)
	}
}

package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/state"
)

// TestE2EQueueAndKillRealBinary drives a real opencode through a real
// Agent, in server mode and in run-per-turn mode: messages sent while a
// turn runs, a kill in the middle of a turn, and a model that does not
// exist. Opt-in (it calls a hosted model), and meant to run inside a
// memory-capped scope so the CLI it starts is capped too:
//
//	systemd-run --user --scope -p MemoryMax=900M env \
//	  WICK_E2E_PROVIDER_BIN=/path/opencode WICK_E2E_DIR=/scratch \
//	  go test -run TestE2EQueueAndKillRealBinary -v ./internal/agents/provider/opencode/
func TestE2EQueueAndKillRealBinary(t *testing.T) {
	bin, dir := os.Getenv("WICK_E2E_PROVIDER_BIN"), os.Getenv("WICK_E2E_DIR")
	if bin == "" || dir == "" {
		t.Skip("set WICK_E2E_PROVIDER_BIN and WICK_E2E_DIR")
	}
	for _, mode := range []struct {
		name       string
		runPerTurn bool
	}{{"server", false}, {"run", true}} {
		t.Run(mode.name, func(t *testing.T) {
			// One CLI process at a time: the server this subtest started
			// goes down before the next subtest.
			useFreshServers(t, startServe)
			ins := provider.Instance{Type: provider.TypeOpencode, Name: "e2e", RunPerTurn: mode.runPerTurn,
				OpencodeConfig: &provider.OpencodeConfig{DataDir: filepath.Join(dir, "data-e2e"), Model: "opencode/big-pickle", AllowHosted: true}}

			t.Run("rapid messages", func(t *testing.T) {
				watchdog(t, 4*time.Minute)
				r := newE2ERun(t, bin, dir, ins)
				_ = r.a.Send("Reply with exactly the word: alpha")
				r.waitSpawns(t, 1)
				time.Sleep(500 * time.Millisecond)
				_ = r.a.Send("Also add the word: bravo")
				_ = r.a.Send("Also add the word: charlie")
				r.settle(t)
				r.log(t)
				r.noCrash(t)
				// Every message must be answered, not just accepted: a turn
				// that ends with no text (or loses the injected ones) fails.
				if d := r.dones(); d < 1 {
					t.Fatalf("no turn finished (dones=%d)", d)
				}
				if txt := strings.ToLower(r.text()); !strings.Contains(txt, "alpha") || !strings.Contains(txt, "bravo") || !strings.Contains(txt, "charlie") {
					t.Fatalf("reply must carry alpha, bravo and charlie; got %q", r.text())
				}
				spawns := r.spawnMsgs()
				if mode.runPerTurn {
					if len(spawns) != 2 || !strings.Contains(spawns[1], "bravo") || !strings.Contains(spawns[1], "charlie") {
						t.Fatalf("run mode: want 2 spawns, the second carrying both queued messages; got %q", spawns)
					}
				} else if len(spawns) != 1 {
					t.Fatalf("server mode: want the messages injected into the one running turn, got %d spawns %q", len(spawns), spawns)
				}
			})

			t.Run("kill mid-turn", func(t *testing.T) {
				watchdog(t, 2*time.Minute)
				r := newE2ERun(t, bin, dir, ins)
				_ = r.a.Send("Use the bash tool to run `sleep 30`, then reply done.")
				r.waitSpawns(t, 1)
				time.Sleep(6 * time.Second)
				_ = r.a.Send("one more")
				_ = r.a.Send("and another")
				before := len(r.spawnMsgs())
				start := time.Now()
				_ = r.a.Stop()
				t.Logf("Stop took %s", time.Since(start))
				time.Sleep(8 * time.Second)
				r.log(t)
				r.noCrash(t)
				if n := len(r.spawnMsgs()); n != before || n != 1 {
					t.Fatalf("spawned after kill: %d → %d (want exactly the one turn)", before, n)
				}
				if r.a.QueuedCount() != 0 {
					t.Fatalf("queue survived the kill: %d", r.a.QueuedCount())
				}
			})

			t.Run("model error", func(t *testing.T) {
				watchdog(t, 4*time.Minute)
				bad := ins
				cfg := *ins.OpencodeConfig
				cfg.Model = "opencode/no-such-model-e2e"
				bad.OpencodeConfig = &cfg
				r := newE2ERun(t, bin, dir, bad)
				_ = r.a.Send("hi")
				r.settle(t)
				r.log(t)
				r.noCrash(t)
				if r.errors() == 0 {
					t.Fatal("a bad model must surface as the turn's error")
				}
				r.a.Stop()
			})
		})
	}
}

type e2eRun struct {
	a      *provider.Agent
	mu     sync.Mutex
	spawns []string
	events []event.AgentEvent
	exits  []provider.ExitReason
	last   time.Time
}

func newE2ERun(t *testing.T, bin, dir string, ins provider.Instance) *e2eRun {
	// The workspace must exist: opencode serve takes a prompt for a
	// missing directory and ends the run with nothing.
	if err := os.MkdirAll(filepath.Join(dir, "ws"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &e2eRun{last: time.Now()}
	r.a = provider.New(provider.Options{
		Workspace:     filepath.Join(dir, "ws"),
		SessionID:     "e2e-queue-" + t.Name(),
		Instance:      &ins,
		IdleTimeout:   2 * time.Minute,
		ParserFactory: func() event.Parser { return event.NewOpencodeParser("e2e") },
		Spawner:       Spawner{Binary: bin},
		State:         state.New(nil),
		SendMode:      provider.SendRespawnQueue,
		OnEvent: func(e event.AgentEvent) {
			r.mu.Lock()
			r.events, r.last = append(r.events, e), time.Now()
			r.mu.Unlock()
		},
		OnExit: func(reason provider.ExitReason, _ string) {
			r.mu.Lock()
			r.exits = append(r.exits, reason)
			r.mu.Unlock()
		},
		OnSpawn: func(_ string, _ []string, _ []string, _ int, msg string) {
			r.mu.Lock()
			r.spawns, r.last = append(r.spawns, msg), time.Now()
			r.mu.Unlock()
		},
	})
	if err := r.a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.a.Stop() })
	return r
}

func (r *e2eRun) spawnMsgs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.spawns...)
}

func (r *e2eRun) waitSpawns(t *testing.T, n int) {
	deadline := time.Now().Add(30 * time.Second)
	for len(r.spawnMsgs()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("never reached %d spawns", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// settle waits until nothing has happened for a while and nothing is
// queued or running.
func (r *e2eRun) settle(t *testing.T) {
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		quiet := time.Since(r.last) > 5*time.Second
		r.mu.Unlock()
		if quiet && r.a.QueuedCount() == 0 && !r.a.Running() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("agent never settled")
}

func (r *e2eRun) dones() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Type == event.Done {
			n++
		}
	}
	return n
}

func (r *e2eRun) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, e := range r.events {
		if e.Type == event.TextDelta {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

// watchdog turns a hung subtest into a fast failure: past d it panics
// with every goroutine's stack (t.Fatal cannot be called off the test
// goroutine). It covers the subtest's cleanups too (registered first,
// so it is stopped last).
func watchdog(t *testing.T, d time.Duration) {
	name := t.Name()
	tm := time.AfterFunc(d, func() {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		panic(fmt.Sprintf("E2E subtest %s hung for more than %s\n%s", name, d, buf[:n]))
	})
	t.Cleanup(func() { tm.Stop() })
}

func (r *e2eRun) errors() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Type == event.Error {
			n++
		}
	}
	return n
}

func (r *e2eRun) noCrash(t *testing.T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.exits {
		if x == provider.ExitError {
			t.Fatalf("an exit was reported as a crash (pool would 'recover'): %v", r.exits)
		}
	}
}

func (r *e2eRun) log(t *testing.T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var text strings.Builder
	dones := 0
	for _, e := range r.events {
		switch e.Type {
		case event.TextDelta:
			text.WriteString(e.Text)
		case event.Done:
			dones++
			text.WriteString(" ⏎ ")
		case event.Error:
			text.WriteString(" [error: " + e.ErrorMsg + "] ")
		}
	}
	exits := make([]string, len(r.exits))
	for i, x := range r.exits {
		exits[i] = provider.ExitReasonName(x)
	}
	t.Logf("spawns=%d %q | dones=%d | exits=%v | text=%q", len(r.spawns), r.spawns, dones, exits, text.String())
}

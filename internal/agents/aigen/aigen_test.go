package aigen

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
)

// fakeProv answers StructuredCall after an optional block, counting how
// many calls overlap so tests can assert the slot limit held.
type fakeProv struct {
	name    string
	typ     string
	structd bool
	block   chan struct{} // nil = answer at once
	running atomic.Int32
	peak    atomic.Int32
	calls   atomic.Int32
}

func (f *fakeProv) Name() string         { return f.name }
func (f *fakeProv) ProviderType() string { return f.typ }
func (f *fakeProv) Capabilities() wfprovider.Capabilities {
	return wfprovider.Capabilities{StructuredOutput: f.structd}
}
func (f *fakeProv) AgentCall(context.Context, wfprovider.AgentRequest) (wfprovider.AgentResult, error) {
	return wfprovider.AgentResult{}, nil
}
func (f *fakeProv) ListSkills(context.Context) ([]wfprovider.Skill, error) { return nil, nil }
func (f *fakeProv) StructuredCall(ctx context.Context, req wfprovider.StructuredRequest) (wfprovider.StructuredResult, error) {
	f.calls.Add(1)
	n := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return wfprovider.StructuredResult{}, ctx.Err()
		}
	}
	return wfprovider.StructuredResult{OK: true, Parsed: map[string]any{"echo": req.Prompt}}, nil
}

// slotGate is a counting Gate with a fixed capacity.
type slotGate struct {
	mu   sync.Mutex
	cap  int
	used int
}

func (g *slotGate) TryLease(_, _, _ string) (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.used >= g.cap {
		return nil, false
	}
	g.used++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.used--
			g.mu.Unlock()
		})
	}, true
}

func (g *slotGate) setCap(n int) {
	g.mu.Lock()
	g.cap = n
	g.mu.Unlock()
}

var echoKind = Kind{
	Name: "echo",
	Build: func(in Input) (wfprovider.StructuredRequest, error) {
		return wfprovider.StructuredRequest{Prompt: in.Text}, nil
	},
	Finish: func(res wfprovider.StructuredResult) (any, error) { return res.Parsed["echo"], nil },
}

func newSvc(t *testing.T, prov *fakeProv, gate Gate, mod func(*Config)) *Service {
	t.Helper()
	cfg := Config{
		Gate: gate,
		Resolve: func(context.Context, string, string) (Resolved, error) {
			return Resolved{Provider: prov, Type: prov.typ, Name: prov.name}, nil
		},
		RetryEvery: 5 * time.Millisecond,
		MaxPerUser: 10,
	}
	if mod != nil {
		mod(&cfg)
	}
	s := New(cfg)
	s.Register(echoKind)
	t.Cleanup(s.Close)
	return s
}

func waitStatus(t *testing.T, s *Service, user, id string, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		j, err := s.Get(user, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if j.Status == want {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s: status %s (err %q), want %s", id, j.Status, j.Error, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSubmitRunsToDone(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true}
	s := newSvc(t, prov, &slotGate{cap: 1}, nil)
	j, err := s.Submit(context.Background(), "u1", "echo", Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusQueued || j.Position != 1 || j.Provider != "claude" {
		t.Fatalf("fresh job = %+v", j)
	}
	done := waitStatus(t, s, "u1", j.ID, StatusDone)
	if done.Result != "hello" || done.StartedAt == nil || done.FinishedAt == nil {
		t.Fatalf("done job = %+v", done)
	}
}

func TestQueueRespectsSlotGate(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true, block: make(chan struct{})}
	gate := &slotGate{cap: 0} // pool full
	s := newSvc(t, prov, gate, func(c *Config) { c.Workers = 3 })

	var ids []string
	for i := 0; i < 3; i++ {
		j, err := s.Submit(context.Background(), "u1", "echo", Input{Text: "x"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	time.Sleep(50 * time.Millisecond)
	if prov.calls.Load() != 0 {
		t.Fatal("a job ran while the pool had no free slot")
	}
	for i, id := range ids {
		j, _ := s.Get("u1", id)
		if j.Status != StatusQueued || j.Position != i+1 {
			t.Fatalf("job %d = status %s position %d", i, j.Status, j.Position)
		}
	}

	gate.setCap(1) // one slot frees up
	waitStatus(t, s, "u1", ids[0], StatusWorking)
	time.Sleep(50 * time.Millisecond)
	if got := prov.running.Load(); got != 1 {
		t.Fatalf("running = %d with one slot", got)
	}
	if j, _ := s.Get("u1", ids[2]); j.Position != 2 {
		t.Fatalf("third job position = %d, want 2 once the first started", j.Position)
	}
	close(prov.block)
	for _, id := range ids {
		waitStatus(t, s, "u1", id, StatusDone)
	}
	if p := prov.peak.Load(); p != 1 {
		t.Fatalf("peak concurrency = %d, gate allowed 1", p)
	}
}

func TestOwnerCheck(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true, block: make(chan struct{})}
	defer close(prov.block)
	s := newSvc(t, prov, nil, nil)
	j, err := s.Submit(context.Background(), "alice", "echo", Input{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("bob", j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob read alice's job: %v", err)
	}
	if _, err := s.Cancel("bob", j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob canceled alice's job: %v", err)
	}
	if _, err := s.Submit(context.Background(), "", "echo", Input{Text: "x"}); err == nil {
		t.Fatal("anonymous submit accepted")
	}
}

func TestCancelQueuedAndWorking(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true, block: make(chan struct{})}
	defer close(prov.block)
	s := newSvc(t, prov, &slotGate{cap: 1}, nil)
	a, _ := s.Submit(context.Background(), "u1", "echo", Input{Text: "a"})
	b, _ := s.Submit(context.Background(), "u1", "echo", Input{Text: "b"})
	waitStatus(t, s, "u1", a.ID, StatusWorking)

	cb, err := s.Cancel("u1", b.ID)
	if err != nil || cb.Status != StatusCanceled {
		t.Fatalf("cancel queued = %+v, %v", cb, err)
	}
	ca, err := s.Cancel("u1", a.ID)
	if err != nil || ca.Status != StatusCanceled {
		t.Fatalf("cancel working = %+v, %v", ca, err)
	}
	time.Sleep(30 * time.Millisecond)
	if j, _ := s.Get("u1", a.ID); j.Status != StatusCanceled {
		t.Fatalf("canceled job flipped to %s", j.Status)
	}
	if prov.calls.Load() != 1 {
		t.Fatalf("calls = %d; the canceled queued job must never run", prov.calls.Load())
	}
}

func TestRunTimeout(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true, block: make(chan struct{})}
	defer close(prov.block)
	s := newSvc(t, prov, nil, func(c *Config) { c.RunTimeout = 30 * time.Millisecond })
	j, _ := s.Submit(context.Background(), "u1", "echo", Input{Text: "x"})
	got := waitStatus(t, s, "u1", j.ID, StatusFailed)
	if !strings.Contains(got.Error, "timed out") {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestQueueTimeout(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true}
	s := newSvc(t, prov, &slotGate{cap: 0}, func(c *Config) { c.QueueTimeout = 30 * time.Millisecond })
	j, _ := s.Submit(context.Background(), "u1", "echo", Input{Text: "x"})
	got := waitStatus(t, s, "u1", j.ID, StatusFailed)
	if !strings.Contains(got.Error, "no free agent slot") || prov.calls.Load() != 0 {
		t.Fatalf("job = %+v calls=%d", got, prov.calls.Load())
	}
}

func TestSubmitGuards(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true, block: make(chan struct{})}
	defer close(prov.block)
	s := newSvc(t, prov, &slotGate{cap: 0}, func(c *Config) { c.MaxPerUser = 1 })
	s.Register(Kind{Name: "small", MaxInput: 4, Build: echoKind.Build,
		Validate: func(in Input) error {
			if in.Text == "bad" {
				return errors.New("bad input")
			}
			return nil
		}})
	if _, err := s.Submit(context.Background(), "u1", "nope", Input{}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := s.Submit(context.Background(), "u1", "small", Input{Text: "too long"}); err == nil {
		t.Fatal("oversized input accepted")
	}
	if _, err := s.Submit(context.Background(), "u1", "small", Input{Text: "bad"}); err == nil || err.Error() != "bad input" {
		t.Fatalf("validate not applied: %v", err)
	}
	if _, err := s.Submit(context.Background(), "u1", "echo", Input{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), "u1", "echo", Input{Text: "y"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("per-user cap not enforced: %v", err)
	}
	if _, err := s.Submit(context.Background(), "u2", "echo", Input{Text: "y"}); err != nil {
		t.Fatalf("other user blocked by u1's cap: %v", err)
	}
}

func TestFinishedJobsExpire(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true}
	s := newSvc(t, prov, nil, func(c *Config) { c.ResultTTL = time.Minute })
	j, _ := s.Submit(context.Background(), "u1", "echo", Input{Text: "x"})
	waitStatus(t, s, "u1", j.ID, StatusDone)
	s.mu.Lock()
	s.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	s.mu.Unlock()
	if _, err := s.Get("u1", j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired job still readable: %v", err)
	}
}

func TestFinishErrorFailsJob(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true}
	s := newSvc(t, prov, nil, nil)
	s.Register(Kind{Name: "picky", Build: echoKind.Build,
		Finish: func(wfprovider.StructuredResult) (any, error) { return nil, errors.New("missing url") }})
	j, _ := s.Submit(context.Background(), "u1", "picky", Input{Text: "x"})
	if got := waitStatus(t, s, "u1", j.ID, StatusFailed); got.Error != "missing url" {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestListResolver(t *testing.T) {
	codex := &fakeProv{name: "codex", typ: "codex"}
	claude := &fakeProv{name: "claude", typ: "claude", structd: true}
	work := &fakeProv{name: "work", typ: "claude", structd: true}
	list := func() ([]wfprovider.Provider, error) { return []wfprovider.Provider{codex, claude, work}, nil }
	ctx := context.Background()

	cases := []struct {
		def, name, want string
		wantErr         bool
	}{
		{"", "", "claude", false},
		{"claude/work", "", "work", false},
		{`[{"id":"claude/work","name":"Work"}]`, "", "work", false},
		{"claude/gone", "", "claude", false}, // falls back to the type
		{"codex", "", "claude", false},       // default can't do structured output
		{"", "work", "work", false},
		{"", "codex", "", true}, // named but unqualified
		{"", "missing", "", true},
	}
	for _, c := range cases {
		r := ListResolver(list, func() string { return c.def })
		got, err := r(ctx, "u1", c.name)
		if c.wantErr {
			if err == nil {
				t.Errorf("def=%q name=%q: want error, got %s", c.def, c.name, got.Name)
			}
			continue
		}
		if err != nil || got.Name != c.want || got.Type != ProviderType(got.Provider) {
			t.Errorf("def=%q name=%q: got %+v err %v, want %s", c.def, c.name, got, err, c.want)
		}
	}
	none := ListResolver(func() ([]wfprovider.Provider, error) { return []wfprovider.Provider{codex}, nil }, nil)
	if _, err := none(ctx, "u1", ""); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("no structured provider: %v", err)
	}
}

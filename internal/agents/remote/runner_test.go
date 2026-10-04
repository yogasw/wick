package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

type line struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"`
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
	RemoteNote string `json:"remote_note"`
	Status     string `json:"status"`
	Message    struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// fake is a Source scripted per test: pushed events go out on push, pulled
// batches are returned one per Fetch.
type fake struct {
	mu      sync.Mutex
	listen  []ListenMode
	limits  Limits
	push    chan Event
	pulls   [][]Event
	fetches []time.Time
	ended   int
	noPush  bool
}

func (f *fake) Kind() string                                  { return "fake" }
func (f *fake) Label() string                                 { return "fake" }
func (f *fake) Listen() []ListenMode                          { return f.listen }
func (f *fake) Limits() Limits                                { return f.limits }
func (f *fake) Cancel(context.Context, Handle) error          { return nil }
func (f *fake) End(Handle)                                    { f.mu.Lock(); f.ended++; f.mu.Unlock() }
func (f *fake) Describe(context.Context) (Description, error) { return Description{}, nil }
func (f *fake) Test(context.Context) TestResult               { return TestResult{OK: true} }
func (f *fake) Send(context.Context, Turn) (Handle, error)    { return Handle{ID: "h"}, nil }

func (f *fake) Receive(context.Context, Handle) (<-chan Event, error) {
	if f.noPush {
		return nil, ErrPushUnavailable
	}
	return f.push, nil
}

func (f *fake) Fetch(_ context.Context, h Handle) ([]Event, Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches = append(f.fetches, time.Now())
	if len(f.pulls) == 0 {
		return nil, h, nil
	}
	b := f.pulls[0]
	f.pulls = f.pulls[1:]
	return b, h, nil
}

func fastPull(t *testing.T) {
	old := PullSteps
	PullSteps = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond}
	t.Cleanup(func() { PullSteps = old })
}

// run spawns src, sends one message and returns the text and closing line.
func run(t *testing.T, src Source) (string, line) {
	t.Helper()
	p, err := Spawner{Source: src}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: "hi", SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Kill()
	sc := bufio.NewScanner(p.Stdout())
	var sb strings.Builder
	for sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		switch l.Type {
		case "assistant":
			for _, c := range l.Message.Content {
				sb.WriteString(c.Text)
			}
		case "result":
			return sb.String(), l
		}
	}
	t.Fatal("stream ended without a result")
	return "", line{}
}

func TestPushDone(t *testing.T) {
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 5 * time.Second}, push: make(chan Event, 8)}
	f.push <- Event{Kind: EventStatus, Status: StatusThinking}
	f.push <- Event{Kind: EventTextDelta, Text: "Hel"}
	f.push <- Event{Kind: EventText, Text: "Hello"}
	f.push <- Event{Kind: EventDone}
	text, l := run(t, f)
	if text != "Hello" || l.IsError || l.Result != "Hello" {
		t.Fatalf("text=%q line=%+v", text, l)
	}
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		n := f.ended
		f.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("End was not called after the turn")
}

func TestPullBacksOffAndStopsAfterDone(t *testing.T) {
	fastPull(t)
	f := &fake{listen: []ListenMode{ListenPull}, limits: Limits{Max: 5 * time.Second},
		pulls: [][]Event{nil, nil, nil, {{Kind: EventTextDelta, Text: "ok"}}, nil, {{Kind: EventDone}}}}
	text, l := run(t, f)
	if text != "ok" || l.IsError {
		t.Fatalf("text=%q line=%+v", text, l)
	}
	f.mu.Lock()
	n, fs := len(f.fetches), f.fetches
	f.mu.Unlock()
	if n != 6 {
		t.Fatalf("fetches = %d, want 6", n)
	}
	// The gap grows while nothing comes back.
	if fs[3].Sub(fs[2]) <= fs[1].Sub(fs[0]) {
		t.Fatalf("no backoff: %v then %v", fs[1].Sub(fs[0]), fs[3].Sub(fs[2]))
	}
	time.Sleep(80 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.fetches) != n {
		t.Fatalf("polling went on after the turn: %d fetches", len(f.fetches))
	}
}

func TestPushUnavailableFallsBackToPull(t *testing.T) {
	fastPull(t)
	f := &fake{listen: []ListenMode{ListenPush, ListenPull}, limits: Limits{Max: 5 * time.Second}, noPush: true,
		pulls: [][]Event{{{Kind: EventDone, Text: "from poll"}}}}
	text, l := run(t, f)
	if text != "from poll" || l.Result != "from poll" {
		t.Fatalf("text=%q line=%+v", text, l)
	}
}

func TestIdleEndsWithoutMarker(t *testing.T) {
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 5 * time.Second, Idle: 60 * time.Millisecond}, push: make(chan Event, 8)}
	f.push <- Event{Kind: EventTextDelta, Text: "partial"}
	text, l := run(t, f)
	if text != "partial" || l.IsError || l.RemoteNote != NoteNoMarker {
		t.Fatalf("text=%q line=%+v", text, l)
	}
}

func TestMaxTimesOut(t *testing.T) {
	old := pushGrace
	pushGrace = 10 * time.Millisecond
	t.Cleanup(func() { pushGrace = old })
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 50 * time.Millisecond}, push: make(chan Event)}
	_, l := run(t, f)
	if !l.IsError || !strings.Contains(l.Result, "No reply from the remote agent after") {
		t.Fatalf("line=%+v", l)
	}
}

func TestRewriteKeptForResult(t *testing.T) {
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 5 * time.Second}, push: make(chan Event, 8)}
	f.push <- Event{Kind: EventText, Text: "draft"}
	f.push <- Event{Kind: EventText, Text: "final answer"}
	f.push <- Event{Kind: EventDone}
	text, l := run(t, f)
	if text != "draft" || l.Result != "final answer" {
		t.Fatalf("text=%q line=%+v", text, l)
	}
}

func TestRegistry(t *testing.T) {
	Register(Adapter{Kind: "zz-test", Label: "Test", Listen: []ListenMode{ListenPull}, Schema: SchemaVersion})
	if a, ok := Lookup("zz-test"); !ok || a.Label != "Test" {
		t.Fatalf("lookup: %+v %v", a, ok)
	}
	found := false
	for _, a := range Adapters() {
		found = found || a.Kind == "zz-test"
	}
	if !found {
		t.Fatal("Adapters misses zz-test")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Register took an adapter of another event schema")
		}
	}()
	Register(Adapter{Kind: "zz-old", Label: "Old", Listen: []ListenMode{ListenPull}, Schema: SchemaVersion + 1})
}

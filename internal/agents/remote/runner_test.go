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
	QueueID    string `json:"queue_id"`
	QueueState string `json:"queue_state"`
	Text       string `json:"text"`
	URL        string `json:"url"`
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
	// The gap grows while nothing comes back. A timer never fires early, so
	// the lower bound holds on a loaded host where comparing two gaps does not.
	if gap := fs[3].Sub(fs[2]); gap < PullSteps[3] {
		t.Fatalf("no backoff: third empty gap %v, want >= %v", gap, PullSteps[3])
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

func TestDraftShownOnlyAtEnd(t *testing.T) {
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 5 * time.Second}, push: make(chan Event, 8)}
	f.push <- Event{Kind: EventDraft, Text: "partial"}
	f.push <- Event{Kind: EventDraft, Text: "the whole answer"}
	f.push <- Event{Kind: EventDone}
	text, l := run(t, f)
	if text != "the whole answer" || l.Result != "the whole answer" {
		t.Fatalf("text=%q line=%+v", text, l)
	}
}

// fakeClock drives the idle window by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func TestIdleWaitsWhileBusyThenEnds(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	oldNow, oldTick := now, idleTick
	now, idleTick = clk.now, 5*time.Millisecond
	t.Cleanup(func() { now, idleTick = oldNow, oldTick })
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 10 * time.Second, Idle: 30 * time.Second}, push: make(chan Event, 8)}
	f.push <- Event{Kind: EventText, Text: "first"}
	f.push <- Event{Kind: EventStatus, Status: StatusWorking, Detail: "checking"}
	type res struct {
		text string
		l    line
	}
	got := make(chan res, 1)
	go func() { text, l := run(t, f); got <- res{text, l} }()
	time.Sleep(50 * time.Millisecond)
	clk.add(5 * time.Minute) // a long tool run: still working, no end
	select {
	case r := <-got:
		t.Fatalf("ended while busy: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	f.push <- Event{Kind: EventText, Text: "first\n\nsecond"}
	time.Sleep(50 * time.Millisecond)
	clk.add(29 * time.Second) // under the window: a new message reset it
	select {
	case r := <-got:
		t.Fatalf("ended inside the idle window: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	clk.add(2 * time.Second)
	select {
	case r := <-got:
		if r.text != "first\n\nsecond" || r.l.Result != "first\n\nsecond" || r.l.RemoteNote != NoteNoMarker {
			t.Fatalf("text=%q line=%+v", r.text, r.l)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle window never ended the turn")
	}
}

func TestPullStepsCapped(t *testing.T) {
	got := pullSteps(3 * time.Second)
	if got[0] != time.Second || got[len(got)-1] != 3*time.Second {
		t.Fatalf("steps = %v", got)
	}
	if len(pullSteps(0)) != len(PullSteps) {
		t.Fatal("no cap changed the steps")
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

func (f *fake) Reopen(Handle) {}

func TestGraceWindowPassesOnALateMessage(t *testing.T) {
	old := followUpQuiet
	followUpQuiet = 10 * time.Millisecond
	var asked []string
	var amu sync.Mutex
	OnFollowUp = func(sid, text, _ string) { amu.Lock(); asked = append(asked, text); amu.Unlock() }
	t.Cleanup(func() { followUpQuiet, OnFollowUp = old, nil })
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 5 * time.Second, Grace: 2 * time.Second}, push: make(chan Event, 8)}
	f.push <- Event{Kind: EventText, Text: "first"}
	f.push <- Event{Kind: EventDone, Text: "first"}
	p, err := Spawner{Source: f}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: "hi", SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Kill()
	sc := bufio.NewScanner(p.Stdout())
	var results []line
	var texts []string
	for len(results) < 2 && sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		switch l.Type {
		case "assistant":
			for _, c := range l.Message.Content {
				texts = append(texts, c.Text)
			}
		case "result":
			results = append(results, l)
			if len(results) == 1 {
				f.push <- Event{Kind: EventText, Text: "first\n\nlate addition"}
			}
		}
	}
	if len(results) != 2 || results[1].Result != "late addition" || results[1].RemoteNote != NoteFollowUp {
		t.Fatalf("results=%+v texts=%q", results, texts)
	}
	amu.Lock()
	defer amu.Unlock()
	if len(asked) != 1 || asked[0] != "late addition" {
		t.Fatalf("asker got %q", asked)
	}
}

// A remote that keeps showing life moves Max on: the turn is not cut at
// Max while text keeps coming or a working status stands.
func TestActivityExtendsMax(t *testing.T) {
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 100 * time.Millisecond, Ceiling: 3 * time.Second, Idle: time.Second}, push: make(chan Event, 16)}
	f.push <- Event{Kind: EventStatus, Status: StatusWorking, Detail: "reading"}
	go func() {
		for i := 1; i <= 4; i++ {
			time.Sleep(80 * time.Millisecond)
			f.push <- Event{Kind: EventText, Text: strings.Repeat("x", i)}
			f.push <- Event{Kind: EventStatus, Status: StatusWorking, Detail: "reading"}
		}
		time.Sleep(150 * time.Millisecond) // busy, silent, past Max
		f.push <- Event{Kind: EventDone, Text: "xxxx done"}
	}()
	_, l := run(t, f)
	if l.IsError || l.Result != "xxxx done" {
		t.Fatalf("cut while active: %+v", l)
	}
}

// A remote that stays silent still times out at Max.
func TestSilentStillTimesOutAtMax(t *testing.T) {
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 80 * time.Millisecond, Ceiling: 3 * time.Second}, push: make(chan Event, 1)}
	start := time.Now()
	_, l := run(t, f)
	if !l.IsError || l.Result != TimeoutMessage(80*time.Millisecond) || time.Since(start) > 3*time.Second {
		t.Fatalf("line=%+v after %s", l, time.Since(start))
	}
}

// A reply the remote finishes after its turn timed out still reaches the
// session, as a late reply of the same turn.
func TestFinalAfterTimeoutIsDelivered(t *testing.T) {
	old := followUpQuiet
	followUpQuiet = 10 * time.Millisecond
	t.Cleanup(func() { followUpQuiet = old })
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 60 * time.Millisecond, Grace: 2 * time.Second}, push: make(chan Event, 8)}
	p, err := Spawner{Source: f}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: "hi", SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Kill()
	sc := bufio.NewScanner(p.Stdout())
	var results []line
	for len(results) < 2 && sc.Scan() {
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Type == "result" {
			results = append(results, l)
			if len(results) == 1 {
				f.push <- Event{Kind: EventText, Text: "final answer"}
			}
		}
	}
	if len(results) != 2 || !results[0].IsError || results[1].IsError || results[1].Result != "final answer" || results[1].RemoteNote != NoteLate {
		t.Fatalf("results=%+v", results)
	}
}

// After the result, the grace window follows the remote silently: a
// heartbeat then would flip the agent back to "working" (thinking…) in the
// UI. Busy keeps the idle timer away instead, until the window closes.
func TestNoHeartbeatAfterResultButStillBusy(t *testing.T) {
	oldBeat, oldLate := heartbeatEvery.Get(), lateListen.Get()
	heartbeatEvery.Set(5 * time.Millisecond)
	lateListen.Set(50 * time.Millisecond)
	t.Cleanup(func() { heartbeatEvery.Set(oldBeat); lateListen.Set(oldLate) })
	f := &fake{listen: []ListenMode{ListenPush}, limits: Limits{Max: 5 * time.Second, Grace: 300 * time.Millisecond}, push: make(chan Event, 8)}
	p, err := Spawner{Source: f}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: "hi", SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Kill()
	lines := make(chan line, 64)
	go func() {
		sc := bufio.NewScanner(p.Stdout())
		for sc.Scan() {
			var l line
			if json.Unmarshal(sc.Bytes(), &l) == nil {
				lines <- l
			}
		}
		close(lines)
	}()
	// Heartbeats flow while the remote has not answered.
	sawBeat := false
	for !sawBeat {
		l := <-lines
		sawBeat = l.Type == "system" && l.Subtype == "heartbeat"
	}
	f.push <- Event{Kind: EventText, Text: "done"}
	f.push <- Event{Kind: EventDone, Text: "done"}
	for l := range lines {
		if l.Type == "result" {
			break
		}
	}
	busy := p.(provider.BusyReporter)
	if !busy.Busy() {
		t.Fatal("not busy during the grace window")
	}
	timeout := time.After(150 * time.Millisecond)
	for quiet := false; !quiet; {
		select {
		case l := <-lines:
			if l.Type == "system" && l.Subtype == "heartbeat" {
				t.Fatal("heartbeat after the result")
			}
		case <-timeout:
			quiet = true
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for busy.Busy() {
		if time.Now().After(deadline) {
			t.Fatal("still busy after the grace window")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// turnFake opens a push channel per Send and records what was sent.
type turnFake struct {
	mu    sync.Mutex
	sends chan string
	push  []chan Event
}

func newTurnFake() *turnFake { return &turnFake{sends: make(chan string, 4)} }

func (f *turnFake) Kind() string                                  { return "fake" }
func (f *turnFake) Label() string                                 { return "fake" }
func (f *turnFake) Listen() []ListenMode                          { return []ListenMode{ListenPush} }
func (f *turnFake) Limits() Limits                                { return Limits{Max: 5 * time.Second} }
func (f *turnFake) Cancel(context.Context, Handle) error          { return nil }
func (f *turnFake) End(Handle)                                    {}
func (f *turnFake) Describe(context.Context) (Description, error) { return Description{}, nil }
func (f *turnFake) Test(context.Context) TestResult               { return TestResult{OK: true} }

func (f *turnFake) Send(_ context.Context, t Turn) (Handle, error) {
	f.mu.Lock()
	f.push = append(f.push, make(chan Event, 8))
	f.mu.Unlock()
	f.sends <- t.Text
	return Handle{ID: t.Text}, nil
}

func (f *turnFake) Receive(_ context.Context, h Handle) (<-chan Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.push[len(f.push)-1], nil
}

func (f *turnFake) reply(i int, text string) {
	f.mu.Lock()
	ch := f.push[i]
	f.mu.Unlock()
	ch <- Event{Kind: EventText, Text: text}
	ch <- Event{Kind: EventDone}
}

// injFake is a turnFake whose remote takes a message mid-turn.
type injFake struct {
	*turnFake
	err     error
	injects chan string
}

func (f *injFake) Inject(_ context.Context, h Handle, t Turn) error {
	f.injects <- h.ID + "|" + t.Text
	return f.err
}

func recv(t *testing.T, ch <-chan string, what string) string {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(3 * time.Second):
		t.Fatalf("no %s", what)
		return ""
	}
}

// midTurn starts src with "hi", sends "more" while that turn runs, and
// returns the process and its stream lines.
func midTurn(t *testing.T, src Source, sends chan string) (provider.Process, <-chan line) {
	t.Helper()
	p, err := Spawner{Source: src}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: "hi", SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	lines := make(chan line, 32)
	go func() {
		sc := bufio.NewScanner(p.Stdout())
		for sc.Scan() {
			var l line
			if json.Unmarshal(sc.Bytes(), &l) == nil {
				lines <- l
			}
		}
		close(lines)
	}()
	if got := recv(t, sends, "first send"); got != "hi" {
		t.Fatalf("first send = %q", got)
	}
	if _, err := p.Stdin().Write([]byte(`{"type":"user","message":{"content":"more"}}`)); err != nil {
		t.Fatal(err)
	}
	return p, lines
}

// result waits for the next result line; forwarded reports whether a
// forwarded status came before it.
func result(t *testing.T, lines <-chan line) (l line, forwarded bool) {
	t.Helper()
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("stream ended without a result")
			}
			if l.Subtype == "remote_status" && l.Status == StatusForwarded {
				forwarded = true
			}
			if l.Type == "result" {
				return l, forwarded
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no result")
		}
	}
}

func TestMidTurnMessageInjected(t *testing.T) {
	f := &injFake{turnFake: newTurnFake(), injects: make(chan string, 4)}
	_, lines := midTurn(t, f, f.sends)
	if got := recv(t, f.injects, "inject"); got != "hi|more" {
		t.Fatalf("inject = %q", got)
	}
	f.reply(0, "both answered")
	l, forwarded := result(t, lines)
	if l.IsError || l.Result != "both answered" || !forwarded {
		t.Fatalf("result=%+v forwarded=%v", l, forwarded)
	}
	select {
	case s := <-f.sends:
		t.Fatalf("injected message was also sent as a turn: %q", s)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMidTurnInjectErrorQueuesNextTurn(t *testing.T) {
	f := &injFake{turnFake: newTurnFake(), injects: make(chan string, 4), err: ErrInjectUnsupported}
	_, lines := midTurn(t, f, f.sends)
	recv(t, f.injects, "inject")
	f.reply(0, "first")
	if l, forwarded := result(t, lines); l.Result != "first" || forwarded {
		t.Fatalf("result=%+v forwarded=%v", l, forwarded)
	}
	if got := recv(t, f.sends, "second send"); got != "more" {
		t.Fatalf("second send = %q", got)
	}
	f.reply(1, "second")
	if l, _ := result(t, lines); l.Result != "second" {
		t.Fatalf("second result=%+v", l)
	}
}

func TestMidTurnWithoutInjectorWaits(t *testing.T) {
	f := newTurnFake()
	_, lines := midTurn(t, f, f.sends)
	select {
	case s := <-f.sends:
		t.Fatalf("sent %q while the first turn ran", s)
	case <-time.After(100 * time.Millisecond):
	}
	f.reply(0, "first")
	if l, forwarded := result(t, lines); l.Result != "first" || forwarded {
		t.Fatalf("result=%+v forwarded=%v", l, forwarded)
	}
	if got := recv(t, f.sends, "second send"); got != "more" {
		t.Fatalf("second send = %q", got)
	}
}

// queueLine waits for the remote_queue line of text in state.
func queueLine(t *testing.T, lines <-chan line, text, state string) line {
	t.Helper()
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream ended before %s %q", state, text)
			}
			if l.Subtype == "remote_queue" && l.Text == text && l.QueueState == state {
				return l
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no %s line for %q", state, text)
		}
	}
}

func TestQueuedMessageCancelledIsNeverSent(t *testing.T) {
	f := newTurnFake()
	p, lines := midTurn(t, f, f.sends)
	more := queueLine(t, lines, "more", QueueQueued)
	if more.QueueID == "" {
		t.Fatal("queued line has no id")
	}
	if _, err := p.Stdin().Write([]byte(`{"type":"user","message":{"content":"third"}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	queueLine(t, lines, "third", QueueQueued)
	qc, ok := p.(interface{ CancelQueued(string) bool })
	if !ok {
		t.Fatal("remote process does not cancel queued messages")
	}
	if !qc.CancelQueued(more.QueueID) {
		t.Fatal("CancelQueued = false for a queued message")
	}
	if qc.CancelQueued(more.QueueID) {
		t.Fatal("CancelQueued twice = true")
	}
	queueLine(t, lines, "more", QueueCancelled)
	f.reply(0, "first")
	if got := recv(t, f.sends, "next send"); got != "third" {
		t.Fatalf("next send = %q, want the message left in the queue", got)
	}
	queueLine(t, lines, "third", QueueSent)
}

// cancelFake is a turnFake that records the turns stopped on the remote.
type cancelFake struct {
	*turnFake
	cancels chan string
}

func (f *cancelFake) Cancel(_ context.Context, h Handle) error {
	f.cancels <- h.ID
	return nil
}

// spawnRead spawns a remote process and reads its stdout into lines.
func spawnRead(t *testing.T, src Source) (provider.Process, <-chan line) {
	t.Helper()
	p, err := Spawner{Source: src}.Spawn(context.Background(), provider.SpawnOptions{InitialMessage: "hi", SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	lines := make(chan line, 32)
	go func() {
		sc := bufio.NewScanner(p.Stdout())
		for sc.Scan() {
			var l line
			if json.Unmarshal(sc.Bytes(), &l) == nil {
				lines <- l
			}
		}
		close(lines)
	}()
	return p, lines
}

// waitLine waits for the first line ok accepts.
func waitLine(t *testing.T, lines <-chan line, what string, ok func(line) bool) line {
	t.Helper()
	for {
		select {
		case l, open := <-lines:
			if !open {
				t.Fatalf("stream ended before %s", what)
			}
			if ok(l) {
				return l
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no %s", what)
		}
	}
}

func TestKillCancelsRunningTurnOnRemote(t *testing.T) {
	f := &cancelFake{turnFake: newTurnFake(), cancels: make(chan string, 2)}
	p, lines := spawnRead(t, f)
	recv(t, f.sends, "send")
	// A status line out means the turn is listening, its handle known.
	f.mu.Lock()
	ch := f.push[0]
	f.mu.Unlock()
	ch <- Event{Kind: EventStatus, Status: StatusWorking, Detail: "busy"}
	waitLine(t, lines, "status", func(l line) bool { return l.Subtype == "remote_status" })
	_ = p.Kill()
	if got := recv(t, f.cancels, "cancel"); got != "hi" {
		t.Fatalf("cancelled handle = %q", got)
	}
}

func TestTurnLinkShownOnce(t *testing.T) {
	f := newTurnFake()
	_, lines := spawnRead(t, f)
	recv(t, f.sends, "send")
	f.mu.Lock()
	ch := f.push[0]
	f.mu.Unlock()
	ch <- Event{Kind: EventStatus, Status: StatusWorking, Detail: "s1", URL: "https://example.com/s1"}
	ch <- Event{Kind: EventStatus, Status: StatusThinking, URL: "https://example.com/s1"}
	ch <- Event{Kind: EventText, Text: "ok"}
	ch <- Event{Kind: EventDone}
	links := 0
	waitLine(t, lines, "result", func(l line) bool {
		if l.Subtype == "remote_link" {
			links++
			if l.URL != "https://example.com/s1" {
				t.Errorf("link = %q", l.URL)
			}
		}
		return l.Type == "result"
	})
	if links != 1 {
		t.Fatalf("remote_link lines = %d, want 1", links)
	}
}

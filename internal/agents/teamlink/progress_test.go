package teamlink

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// locatingTurns is fakeTurns that names the teammate's main session, as
// the pool does, so a turn's progress has a session to come from.
type locatingTurns struct{ *fakeTurns }

func (locatingTurns) MainSession(_ context.Context, agent Peer) string { return "sess-" + agent.ID }

// taskEvents records OnTaskChange: the session told and the view.
type taskEvents struct {
	mu  sync.Mutex
	got []taskEvent
}

type taskEvent struct {
	session string
	view    TaskView
}

func (e *taskEvents) hook(session string, v TaskView) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, taskEvent{session, v})
}

// progress lists the progress events told, in order.
func (e *taskEvents) progress() []taskEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []taskEvent
	for _, g := range e.got {
		if g.view.Progress != "" {
			out = append(out, g)
		}
	}
	return out
}

func (e *taskEvents) lines() []string {
	var out []string
	for _, g := range e.progress() {
		out = append(out, string(g.view.TaskID)+": "+g.view.Progress)
	}
	return out
}

// progressRig is a Hub whose progress runs by hand: a clock the test
// moves, held flushes it fires, sends run at once (or queued, held).
type progressRig struct {
	h       *Hub
	ev      *taskEvents
	clock   time.Time
	flushes []func()
	queued  []func()
	hold    bool
}

func newProgressRig(t *testing.T, tasks ...a2a.TaskID) *progressRig {
	t.Helper()
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	r := &progressRig{h: h, ev: &taskEvents{}, clock: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	h.now = func() time.Time { return r.clock }
	h.OnTaskChange = r.ev.hook
	h.progressAfter = func(_ time.Duration, f func()) { r.flushes = append(r.flushes, f) }
	h.progressGo = func(f func()) {
		if r.hold {
			r.queued = append(r.queued, f)
			return
		}
		f()
	}
	h.mu.Lock()
	for _, id := range tasks {
		h.tasks[id] = &taskRef{callerSession: "sess-cap", callerAgentID: "a-cap", to: Peer{ID: "a-anton", Handle: "anton"}, started: r.clock, touched: r.clock}
	}
	h.mu.Unlock()
	return r
}

// fire runs the held flushes scheduled so far.
func (r *progressRig) fire() {
	fs := r.flushes
	r.flushes = nil
	for _, f := range fs {
		f()
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A working task's progress goes out at once, then at most once per
// window: lines inside it are held, only the newest is sent at its end,
// and a line equal to the one shown is never sent again.
func TestProgressThrottle(t *testing.T) {
	r := newProgressRig(t, "t1")
	h := r.h
	h.startProgress("sess-anton", "t1")

	h.TurnText("sess-anton", "Looking at the logs.\n\n")
	h.TurnText("sess-anton", "Checking **Loki** for the 21:00 window…")
	h.TurnTool("sess-anton", "query_range")
	r.clock = r.clock.Add(500 * time.Millisecond)
	h.TurnTool("sess-anton", "Bash")
	h.TurnTool("sess-anton", "Read")
	if got := r.ev.lines(); !eq(got, []string{"t1: Checking Loki for the 21:00 window…"}) {
		t.Fatalf("first window = %q", got)
	}
	if len(r.flushes) != 1 {
		t.Fatalf("flushes scheduled = %d, want one per window", len(r.flushes))
	}
	r.clock = r.clock.Add(progressEvery)
	r.fire()
	if got := r.ev.lines(); len(got) != 2 || got[1] != "t1: Running Read…" {
		t.Fatalf("after the window = %q (Bash dropped, Read sent once)", got)
	}
	r.clock = r.clock.Add(progressEvery)
	h.TurnTool("sess-anton", "Read")
	if got := r.ev.lines(); len(got) != 2 || len(r.flushes) != 0 {
		t.Fatalf("same line sent again: %q", got)
	}
	// A session answering no task is ignored.
	h.TurnText("sess-vera", "hello")
	h.TurnTool("sess-vera", "Bash")
	if got := r.ev.lines(); len(got) != 2 {
		t.Fatalf("a session with no task told: %q", got)
	}
	for _, e := range r.ev.progress() {
		if e.session != "sess-cap" {
			t.Fatalf("progress told to %q, want only the sending chat", e.session)
		}
	}
}

// The line is plain and one line, capped at progressMax runes; code
// blocks never become the line, a link reads as its label; a turn that
// wrote nothing else reads the tool's name.
func TestProgressLine(t *testing.T) {
	long := strings.Repeat("ö", 300)
	got := progressLine("intro\n## "+long+"\n", "Bash")
	if utf8.RuneCountInString(got) != progressMax || !strings.HasSuffix(got, "…") || strings.Contains(got, "\n") || strings.HasPrefix(got, "#") {
		t.Fatalf("capped = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	cases := []struct{ text, tool, want string }{
		{"```\n", "query_range", "Running query_range…"},
		{"Running the query now:\n```sql\nSELECT token FROM users\n", "Bash", "Running the query now:"},
		{"Here:\n```\nsecret=1\n```\n", "Bash", "Here:"},
		{"~~~\nraw\n~~~", "Read", "Running Read…"},
		{"See [the dashboard](https://grafana.example/d/x?from=1&to=2) for it", "", "See the dashboard for it"},
		{"- `tail`   the  <b>log</b>", "", "tail the <b>log</b>"},
		{"-5 errors since 20:58", "", "-5 errors since 20:58"},
		{"2. Checking deploys", "", "Checking deploys"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := progressLine(c.text, c.tool); got != c.want {
			t.Errorf("progressLine(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

// A fence opened in the part of a long turn's text cut off stays open:
// what follows is still code, not progress.
func TestProgressTextCutKeepsFence(t *testing.T) {
	r := newProgressRig(t, "t1")
	r.h.startProgress("sess-anton", "t1")
	r.h.TurnText("sess-anton", "```\n"+strings.Repeat("x = 1\n", progressTextMax/6+10))
	r.h.TurnText("sess-anton", "password=hunter2")
	r.h.TurnTool("sess-anton", "Bash")
	if got := r.ev.lines(); !eq(got, []string{"t1: Running Bash…"}) {
		t.Fatalf("progress = %q", got)
	}
}

// Progress rides only on the sending chat's events while the task works,
// is gone from the view once it ends, and is never written to its record.
func TestProgressClearedWhenTaskEnds(t *testing.T) {
	var h *Hub
	h, turns, _ := newTestHub(func(p Peer, _ string) string {
		// The sender registers the task after the teammate took it.
		for until := time.Now().Add(5 * time.Second); len(h.SentFrom("sess-cap")) == 0 && time.Now().Before(until); {
			time.Sleep(5 * time.Millisecond)
		}
		h.TurnText("sess-"+p.ID, "Pulling message_logs for channel 0812…")
		h.TurnTool("sess-"+p.ID, "query")
		return "no template errors"
	})
	h.Turns = locatingTurns{turns}
	h.progressGo = func(f func()) { f() }
	dir := t.TempDir()
	if err := h.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ev := &taskEvents{}
	h.OnTaskChange = ev.hook
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "check WA templates"})
	if err != nil || res.State != "completed" {
		t.Fatalf("send = %+v, %v", res, err)
	}
	got := ev.progress()
	if len(got) != 1 || got[0].view.Progress != "Pulling message_logs for channel 0812…" || got[0].session != "sess-cap" {
		t.Fatalf("progress = %+v", got)
	}
	ev.mu.Lock()
	last := ev.got[len(ev.got)-1].view
	ev.mu.Unlock()
	if last.State != "completed" || last.Progress != "" {
		t.Fatalf("final view = %+v", last)
	}
	for _, v := range h.SentFrom("sess-cap") {
		if v.Progress != "" {
			t.Fatalf("listed with progress after the end: %+v", v)
		}
	}
	if len(h.SentFrom("sess-anton")) != 0 {
		t.Fatal("the teammate's session lists the task")
	}
	h.progMu.Lock()
	left := len(h.progress) + len(h.sessions)
	h.progMu.Unlock()
	if left != 0 {
		t.Fatalf("progress kept after the turn: %d", left)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "message_logs") || strings.Contains(string(b), `"progress"`) {
			t.Fatalf("progress written to %s", filepath.Base(f))
		}
	}
}

// A task that left working sends no progress: not a held line flushing
// late, nor a send already on its way.
func TestProgressNotSentAfterLeavingWorking(t *testing.T) {
	r := newProgressRig(t, "t1")
	h := r.h
	h.startProgress("sess-anton", "t1")
	h.TurnTool("sess-anton", "Bash")
	h.TurnTool("sess-anton", "Read") // held for the window
	h.mu.Lock()
	h.tasks["t1"].finished = true
	h.mu.Unlock()
	h.stopProgress("t1")
	r.clock = r.clock.Add(progressEvery)
	r.fire()
	if got := r.ev.lines(); !eq(got, []string{"t1: Running Bash…"}) {
		t.Fatalf("progress = %q", got)
	}
	h.mu.Lock()
	v := h.viewLocked("t1", h.tasks["t1"], r.clock)
	h.mu.Unlock()
	if v.Progress != "" {
		t.Fatalf("ended view = %+v", v)
	}

	// A send queued off the event reader, then the task stops: nothing.
	r2 := newProgressRig(t, "t2")
	r2.hold = true
	r2.h.startProgress("sess-anton", "t2")
	r2.h.TurnTool("sess-anton", "Bash")
	r2.h.stopProgress("t2")
	for _, f := range r2.queued {
		f()
	}
	if got := r2.ev.lines(); len(got) != 0 {
		t.Fatalf("ghost progress after stop: %q", got)
	}
}

// The send runs off the goroutine that reported the tool call (the
// provider's event reader): TurnTool returns before anyone is told.
func TestProgressSendOffCallerGoroutine(t *testing.T) {
	r := newProgressRig(t, "t1")
	r.hold = true
	r.h.startProgress("sess-anton", "t1")
	r.h.TurnTool("sess-anton", "Bash")
	if len(r.ev.lines()) != 0 || len(r.queued) != 1 {
		t.Fatalf("sent on the caller's goroutine: %q, queued %d", r.ev.lines(), len(r.queued))
	}
	r.queued[0]()
	if got := r.ev.lines(); !eq(got, []string{"t1: Running Bash…"}) {
		t.Fatalf("progress = %q", got)
	}
}

// gateRig wires a real TurnGate as the Hub's TurnOwner, with busy under
// the test's control.
func gateRig(t *testing.T, tasks ...a2a.TaskID) (*progressRig, *TurnGate, *atomic.Bool) {
	t.Helper()
	r := newProgressRig(t, tasks...)
	busy := &atomic.Bool{}
	g := NewTurnGate(func(string) bool { return busy.Load() })
	g.poll = time.Millisecond
	r.h.TurnOwner = g.Owner
	return r, g, busy
}

// (a) A person chatting in the teammate's main chat while the task waits
// at the gate: that turn's text and tools never show on the task.
func TestProgressIgnoresTurnTaskWaitsBehind(t *testing.T) {
	r, g, busy := gateRig(t, "t1")
	busy.Store(true)
	r.h.startProgress("sess-anton", "t1")
	acquired := make(chan func(), 1)
	go func() {
		release, err := g.Acquire(context.Background(), "sess-anton", "t1")
		if err != nil {
			t.Error(err)
			return
		}
		acquired <- release
	}()
	r.h.TurnText("sess-anton", "Sure, here is your salary sheet summary")
	r.h.TurnTool("sess-anton", "Read")
	if got := r.ev.lines(); len(got) != 0 {
		t.Fatalf("someone else's turn shown on the task: %q", got)
	}
	busy.Store(false)
	release := <-acquired
	defer release()
	r.h.TurnText("sess-anton", "Checking Loki")
	r.h.TurnTool("sess-anton", "query_range")
	if got := r.ev.lines(); !eq(got, []string{"t1: Checking Loki"}) {
		t.Fatalf("task's own turn = %q", got)
	}
}

// (b) Two tasks of one session take the gate in another order than they
// were tracked: each turn shows on the task that owns it.
func TestProgressFollowsGateOrder(t *testing.T) {
	r, g, _ := gateRig(t, "tA", "tB")
	r.h.startProgress("sess-anton", "tA")
	r.h.startProgress("sess-anton", "tB")
	releaseB, err := g.Acquire(context.Background(), "sess-anton", "tB")
	if err != nil {
		t.Fatal(err)
	}
	r.h.TurnText("sess-anton", "B is working")
	r.h.TurnTool("sess-anton", "Bash")
	releaseB()
	r.h.stopProgress("tB")
	releaseA, err := g.Acquire(context.Background(), "sess-anton", "tA")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	r.h.TurnText("sess-anton", "A is working")
	r.h.TurnTool("sess-anton", "Bash")
	if got := r.ev.lines(); !eq(got, []string{"tB: B is working", "tA: A is working"}) {
		t.Fatalf("progress = %q", got)
	}
}

// (c) The turn after the task's own, starting between the gate's release
// and the task's stop, never shows on it; nor does a turn that a
// person's message reached while the task held it.
func TestProgressIgnoresTurnAfterRelease(t *testing.T) {
	r, g, _ := gateRig(t, "t1")
	r.h.startProgress("sess-anton", "t1")
	release, err := g.Acquire(context.Background(), "sess-anton", "t1")
	if err != nil {
		t.Fatal(err)
	}
	r.h.TurnTool("sess-anton", "Bash")
	release()
	// Still tracked (stopProgress runs once the reply is read).
	r.clock = r.clock.Add(progressEvery)
	r.h.TurnText("sess-anton", "Answering the owner's next message")
	r.h.TurnTool("sess-anton", "Read")
	r.fire()
	if got := r.ev.lines(); !eq(got, []string{"t1: Running Bash…"}) {
		t.Fatalf("progress = %q", got)
	}

	// Shared: a person's message reached the task's turn.
	r2, g2, _ := gateRig(t, "t2")
	r2.h.startProgress("sess-vera", "t2")
	rel2, err := g2.Acquire(context.Background(), "sess-vera", "t2")
	if err != nil {
		t.Fatal(err)
	}
	defer rel2()
	done, err := g2.NoteMessage(WithPersonMessage(context.Background()), "sess-vera", "user")
	if err != nil {
		t.Fatal(err)
	}
	done(true)
	r2.h.TurnTool("sess-vera", "Read")
	if got := r2.ev.lines(); len(got) != 0 {
		t.Fatalf("shared turn shown on the task: %q", got)
	}
}

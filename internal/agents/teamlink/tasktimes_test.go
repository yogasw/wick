package teamlink

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// testClock is a Hub clock the test moves by hand.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// viewOf is task id as SentFrom lists it for sess-cap.
func viewOf(t *testing.T, h *Hub, id string) TaskView {
	t.Helper()
	for _, v := range h.SentFrom("sess-cap") {
		if v.TaskID == id {
			return v
		}
	}
	t.Fatalf("task %s not listed", id)
	return TaskView{}
}

// A question stamps asked_at and leaves ended_at unset; the reply to the
// answer stamps ended_at. Both survive a restart.
func TestTaskAskedAndEndedAt(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	h1, _, _ := newTestHub(func(_ Peer, text string) string {
		if strings.Contains(text, "deploy") {
			return InputRequiredToken + " which environment?"
		}
		return "deployed to prod"
	})
	h1.now = clock.get
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	askedAt := clock.add(2 * time.Minute)
	q, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	v := viewOf(t, h1, q.TaskID)
	if !v.AskedAt.Equal(askedAt) || !v.EndedAt.IsZero() {
		t.Fatalf("asking: asked_at %v ended_at %v, want %v and unset", v.AskedAt, v.EndedAt, askedAt)
	}

	endedAt := clock.add(3 * time.Minute)
	a, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "prod"})
	if err != nil || a.State != "completed" {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	v = viewOf(t, h1, q.TaskID)
	if !v.AskedAt.Equal(askedAt) || !v.EndedAt.Equal(endedAt) {
		t.Fatalf("replied: asked_at %v ended_at %v, want %v and %v", v.AskedAt, v.EndedAt, askedAt, endedAt)
	}

	h2, _, _ := newTestHub(func(Peer, string) string { return "" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	v = viewOf(t, h2, q.TaskID)
	if !v.AskedAt.Equal(askedAt) || !v.EndedAt.Equal(endedAt) {
		t.Fatalf("after restart: asked_at %v ended_at %v, want %v and %v", v.AskedAt, v.EndedAt, askedAt, endedAt)
	}
}

// A cancel ends the task; a refusal ends it rejected — the state the UI
// shows as declined — with its own ended_at.
func TestTaskEndedAtOnCancelAndRejected(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	h, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which one?" })
	h.now = clock.get
	ctx := context.Background()

	c, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "pick"})
	if err != nil || c.State != "input_required" {
		t.Fatalf("send = %+v, %v", c, err)
	}
	canceledAt := clock.add(time.Minute)
	if _, err := h.CancelTask(ctx, "a-cap", c.TaskID); err != nil {
		t.Fatal(err)
	}
	if v := viewOf(t, h, c.TaskID); v.State != "canceled" || !v.EndedAt.Equal(canceledAt) {
		t.Fatalf("canceled = %+v, want ended_at %v", v, canceledAt)
	}

	r, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "pick"})
	if err != nil || r.State != "input_required" {
		t.Fatalf("send = %+v, %v", r, err)
	}
	rejectedAt := clock.add(time.Minute)
	h.finished(ctx, a2a.TaskID(r.TaskID), a2a.TaskStateRejected, "not my job")
	if v := viewOf(t, h, r.TaskID); v.State != "rejected" || !v.EndedAt.Equal(rejectedAt) || v.Summary != "not my job" {
		t.Fatalf("rejected = %+v, want ended_at %v", v, rejectedAt)
	}
}

// The new times are snake_case on the wire and absent while unset, so an
// old record (and a task that never asked) reads them zero.
func TestTaskTimesJSON(t *testing.T) {
	at := time.Date(2026, 10, 10, 10, 12, 0, 0, time.UTC)
	b, _ := json.Marshal(TaskView{AskedAt: at, EndedAt: at.Add(time.Minute)})
	if s := string(b); !strings.Contains(s, `"asked_at":"2026-10-10T10:12:00Z"`) || !strings.Contains(s, `"ended_at":"2026-10-10T10:13:00Z"`) {
		t.Fatalf("view json = %s", s)
	}
	b, _ = json.Marshal(TaskView{})
	if s := string(b); strings.Contains(s, "asked_at") || strings.Contains(s, "ended_at") {
		t.Fatalf("unset times written: %s", s)
	}

	ref := &taskRef{asked: at, ended: at.Add(time.Minute)}
	b, _ = json.Marshal(ref.record("t1"))
	var rec taskRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if back := refFromRecord(rec); !back.asked.Equal(at) || !back.ended.Equal(at.Add(time.Minute)) {
		t.Fatalf("round trip = %v / %v", back.asked, back.ended)
	}
	var old taskRecord
	if err := json.Unmarshal([]byte(`{"task_id":"t0","state":"TASK_STATE_COMPLETED","finished":true}`), &old); err != nil {
		t.Fatal(err)
	}
	if back := refFromRecord(old); !back.asked.IsZero() || !back.ended.IsZero() {
		t.Fatalf("old record = %v / %v", back.asked, back.ended)
	}
}

// A settle delivered again (handBack once the caller left) keeps the time
// the task first asked or ended.
func TestTaskTimesKeptOnSettleAgain(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	h, _, _ := newTestHub(func(_ Peer, text string) string {
		if strings.Contains(text, "deploy") {
			return InputRequiredToken + " which environment?"
		}
		return "no 401s"
	})
	h.now = clock.get
	ctx := context.Background()
	endedAt := clock.add(time.Minute)
	done, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "any 401s?"})
	if err != nil || done.State != "completed" {
		t.Fatalf("send = %+v, %v", done, err)
	}
	askedAt := clock.add(time.Minute)
	q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	clock.add(5 * time.Minute)
	h.finished(ctx, a2a.TaskID(done.TaskID), a2a.TaskStateCompleted, "no 401s")
	h.finished(ctx, a2a.TaskID(q.TaskID), a2a.TaskStateInputRequired, InputRequiredToken+" which environment?")
	if v := viewOf(t, h, done.TaskID); !v.EndedAt.Equal(endedAt) {
		t.Fatalf("ended_at moved: %v, want %v", v.EndedAt, endedAt)
	}
	if v := viewOf(t, h, q.TaskID); !v.AskedAt.Equal(askedAt) || !v.EndedAt.IsZero() {
		t.Fatalf("asked_at moved: %v (ended_at %v), want %v", v.AskedAt, v.EndedAt, askedAt)
	}
}

// A second question moves asked_at; the task only ends with the reply.
func TestTaskAskedAtMovesOnReask(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	n := 0
	h, _, _ := newTestHub(func(Peer, string) string {
		mu.Lock()
		defer mu.Unlock()
		if n++; n < 3 {
			return InputRequiredToken + " which one?"
		}
		return "done"
	})
	h.now = clock.get
	ctx := context.Background()
	first := clock.add(time.Minute)
	q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	if v := viewOf(t, h, q.TaskID); !v.AskedAt.Equal(first) {
		t.Fatalf("asked_at = %v, want %v", v.AskedAt, first)
	}
	second := clock.add(2 * time.Minute)
	if a, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "prod"}); err != nil || a.State != "input_required" {
		t.Fatalf("re-ask = %+v, %v", a, err)
	}
	if v := viewOf(t, h, q.TaskID); !v.AskedAt.Equal(second) || !v.EndedAt.IsZero() {
		t.Fatalf("re-ask: asked_at %v ended_at %v, want %v and unset", v.AskedAt, v.EndedAt, second)
	}
	endedAt := clock.add(2 * time.Minute)
	if a, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "eu"}); err != nil || a.State != "completed" {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	if v := viewOf(t, h, q.TaskID); !v.AskedAt.Equal(second) || !v.EndedAt.Equal(endedAt) {
		t.Fatalf("replied: asked_at %v ended_at %v, want %v and %v", v.AskedAt, v.EndedAt, second, endedAt)
	}
}

// A question lost with a restart and answered anyway is superseded: it
// ends then.
func TestTaskEndedAtOnSupersede(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which environment?" })
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	q, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	asked := viewOf(t, h1, q.TaskID).AskedAt

	clock := &testClock{now: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	h2, _, _ := newTestHub(func(Peer, string) string { return "deployed to prod" })
	h2.now = clock.get
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.AnswerFromUser(ctx, "sess-cap", q.TaskID, "prod"); err != nil {
		t.Fatal(err)
	}
	v := viewOf(t, h2, q.TaskID)
	if v.State != "canceled" || !v.EndedAt.Equal(clock.get()) || !v.AskedAt.Equal(asked) {
		t.Fatalf("superseded = %+v, want ended_at %v, asked_at %v", v, clock.get(), asked)
	}
}

// A late reply turns the person's failed task (a remote's timeout) into a
// replied one, ending it again then; a later follow-up to the reply
// leaves ended_at alone.
func TestTaskEndedAtOnLateReply(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	h, _, _ := newTestHub(func(Peer, string) string { return "no 401s" })
	h.now = clock.get
	ctx := context.Background()
	clock.add(time.Minute)
	done, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "any 401s?"})
	if err != nil || done.State != "completed" {
		t.Fatalf("send = %+v, %v", done, err)
	}
	h.mu.Lock()
	ref := h.tasks[a2a.TaskID(done.TaskID)]
	ref.state, ref.origin = a2a.TaskStateFailed, OriginUser
	h.mu.Unlock()
	lateAt := clock.add(4 * time.Minute)
	if !h.FollowUp(ctx, "sess-a-anton", "no 401s, late") {
		t.Fatal("late reply not handled")
	}
	if v := viewOf(t, h, done.TaskID); v.State != "completed" || !v.EndedAt.Equal(lateAt) {
		t.Fatalf("late reply = %+v, want ended_at %v", v, lateAt)
	}
	clock.add(4 * time.Minute)
	if !h.FollowUp(ctx, "sess-a-anton", "also no 500s") {
		t.Fatal("follow-up not handled")
	}
	if v := viewOf(t, h, done.TaskID); !v.EndedAt.Equal(lateAt) {
		t.Fatalf("follow-up moved ended_at: %v, want %v", v.EndedAt, lateAt)
	}
}

// A task working when wick restarted is settled failed: it ends then.
// The working record is written as the gone process left it, so no
// teammate turn outlives the test.
func TestTaskEndedAtOnOrphanSettle(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	id := "t-orphan"
	writeRecord(dir, a2a.TaskID(id), taskRecord{
		TaskID: id, ContextID: "c-orphan", AgentID: "a-anton", ToHandle: "anton", ToName: "Anton",
		CallerAgentID: "a-cap", CallerOwner: "u1", CallerSession: "sess-cap", Title: "long scan",
		State: string(a2a.TaskStateWorking), WaiterGone: true, Started: started, Touched: started,
	})
	clock := &testClock{now: started.Add(5 * time.Minute)}
	h2, _, _ := newTestHub(func(Peer, string) string { return "" })
	h2.now = clock.get
	h2.orphanPoll = 5 * time.Millisecond
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task to settle", func() bool {
		got, err := h2.GetTask(context.Background(), "a-cap", id)
		return err == nil && got.State == "failed"
	})
	if v := viewOf(t, h2, id); !v.Interrupted || !v.EndedAt.Equal(clock.get()) || !v.AskedAt.IsZero() {
		t.Fatalf("orphan = %+v, want ended_at %v", v, clock.get())
	}
}

// disabledDir lists every peer as fakeDir does but reads id disabled, the
// way a teammate switched off after the sender picked it looks to the
// executor.
type disabledDir struct {
	*fakeDir
	id string
}

func (d disabledDir) Get(ctx context.Context, id string) (Peer, error) {
	p, err := d.fakeDir.Get(ctx, id)
	if id == d.id {
		p.Disabled = true
	}
	return p, err
}

// The executor refusing the turn (executor.fail) ends the task rejected.
func TestTaskEndedAtOnExecutorRefusal(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)}
	h, turns, _ := newTestHub(func(Peer, string) string { return "never" })
	h.Dir = disabledDir{fakeDir: h.Dir.(*fakeDir), id: "a-anton"}
	h.now = clock.get
	endedAt := clock.add(time.Minute)
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "scan"})
	if err != nil || res.State != "rejected" {
		t.Fatalf("send = %+v, %v", res, err)
	}
	if v := viewOf(t, h, res.TaskID); v.State != "rejected" || !v.EndedAt.Equal(endedAt) {
		t.Fatalf("refused = %+v, want ended_at %v", v, endedAt)
	}
	turns.mu.Lock()
	ran := len(turns.seen)
	turns.mu.Unlock()
	if ran != 0 {
		t.Fatalf("a refused task ran %d turns", ran)
	}
}

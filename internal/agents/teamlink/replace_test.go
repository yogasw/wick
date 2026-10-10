package teamlink

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// endedTask sends brief from sess-cap to anton and ends it as end.
func endedTask(t *testing.T, h *Hub, brief string, end a2a.TaskState) string {
	t.Helper()
	r, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: brief})
	if err != nil {
		t.Fatal(err)
	}
	h.finished(context.Background(), a2a.TaskID(r.TaskID), end, "no access to the repo")
	return r.TaskID
}

// askHub's teammates ask a question on every brief, so a test ends the
// task itself (endedTask).
func askHub() (*Hub, *fakeTurns, *fakeNotify) {
	return newTestHub(func(Peer, string) string { return InputRequiredToken + " which repo?" })
}

// B6: Retry takes only a failed task; it sends the whole brief again as a
// new task to the same teammate, links both ways, tells the agent, and a
// second Retry is refused.
func TestRetryFromUser(t *testing.T) {
	h, turns, note := askHub()
	ctx := context.Background()
	done := endedTask(t, h, "check logs", a2a.TaskStateCompleted)
	if _, err := h.RetryFromUser(ctx, "sess-cap", done); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("retry of a completed task: %v", err)
	}
	declined := endedTask(t, h, "check logs", a2a.TaskStateRejected)
	if _, err := h.RetryFromUser(ctx, "sess-cap", declined); !errors.Is(err, ErrTaskNotRetryable) {
		t.Fatalf("retry of a declined task: %v", err)
	}
	brief := "scan the webhook\n" + strings.Repeat("x", 2*threadTextMax)
	failed := endedTask(t, h, brief, a2a.TaskStateFailed)

	var changed []TaskView
	var mu sync.Mutex
	h.OnTaskChange = func(s string, v TaskView) {
		mu.Lock()
		defer mu.Unlock()
		if s == "sess-cap" {
			changed = append(changed, v)
		}
	}
	out, err := h.RetryFromUser(ctx, "sess-cap", failed)
	if err != nil || out.TaskID == "" || out.TaskID == failed || out.To != "@anton" {
		t.Fatalf("retry = %+v, %v", out, err)
	}
	waitFor(t, "the retried turn", func() bool {
		turns.mu.Lock()
		defer turns.mu.Unlock()
		return turns.seen[len(turns.seen)-1] == "anton <- Message from Yoga Bot (@captain):\n"+brief
	})
	old, next := viewOf(t, h, failed), viewOf(t, h, out.TaskID)
	if old.State != "failed" || old.ReplacedBy != out.TaskID || old.ReplacedHow != ReplaceRetry || old.ReplacedToHandle != "anton" || old.ReplacedToName != "Anton" {
		t.Fatalf("old = %+v", old)
	}
	if next.RetryOf != failed || next.ToHandle != "anton" || next.Title != "scan the webhook" {
		t.Fatalf("new = %+v", next)
	}
	// The sending chat hears of both: the new task and the old one's link.
	mu.Lock()
	sawNew, sawLink := false, false
	for _, v := range changed {
		sawNew = sawNew || (v.TaskID == out.TaskID && v.RetryOf == failed)
		sawLink = sawLink || (v.TaskID == failed && v.ReplacedBy == out.TaskID)
	}
	mu.Unlock()
	if !sawNew || !sawLink {
		t.Fatalf("changes = %+v", changed)
	}
	delivered, _ := note.snapshot()
	if !strings.Contains(strings.Join(delivered, "\n"), "sess-cap: The user retried the task to Anton (@anton) [task "+failed+"]") {
		t.Fatalf("delivered = %v", delivered)
	}
	if _, err := h.RetryFromUser(ctx, "sess-cap", failed); !errors.Is(err, ErrAlreadyReplaced) {
		t.Fatalf("second retry: %v", err)
	}
	if _, err := h.ReassignFromUser(ctx, "sess-cap", failed, "vera"); !errors.Is(err, ErrAlreadyReplaced) {
		t.Fatalf("reassign after retry: %v", err)
	}
}

// B6: Reassign takes a declined (or failed) task to another teammate the
// conversation reaches — never the same one, a stranger's or a disabled
// agent.
func TestReassignFromUser(t *testing.T) {
	h, turns, _ := askHub()
	ctx := context.Background()
	done := endedTask(t, h, "check logs", a2a.TaskStateCompleted)
	if _, err := h.ReassignFromUser(ctx, "sess-cap", done, "vera"); !errors.Is(err, ErrTaskNotReassignable) {
		t.Fatalf("reassign of a completed task: %v", err)
	}
	declined := endedTask(t, h, "check raya logs", a2a.TaskStateRejected)
	if _, err := h.ReassignFromUser(ctx, "sess-cap", declined, " "); err == nil {
		t.Fatal("reassign without a teammate went through")
	}
	if _, err := h.ReassignFromUser(ctx, "sess-cap", declined, "@Anton"); !errors.Is(err, ErrReassignSame) {
		t.Fatalf("reassign to the same teammate: %v", err)
	}
	for _, to := range []string{"mallory", "sleepy", "captain", "nobody"} {
		if _, err := h.ReassignFromUser(ctx, "sess-cap", declined, to); !errors.Is(err, ErrUnknownTeammate) {
			t.Fatalf("reassign to %s: %v", to, err)
		}
	}
	out, err := h.ReassignFromUser(ctx, "sess-cap", declined, "@vera")
	if err != nil || out.To != "@vera" {
		t.Fatalf("reassign = %+v, %v", out, err)
	}
	waitFor(t, "vera's turn", func() bool {
		turns.mu.Lock()
		defer turns.mu.Unlock()
		return turns.seen[len(turns.seen)-1] == "vera <- Message from Yoga Bot (@captain):\ncheck raya logs"
	})
	old := viewOf(t, h, declined)
	if old.State != "rejected" || old.ReplacedBy != out.TaskID || old.ReplacedHow != ReplaceReassign || old.ReplacedToName != "Vera" {
		t.Fatalf("old = %+v", old)
	}
	if v := viewOf(t, h, out.TaskID); v.RetryOf != declined || v.ToHandle != "vera" {
		t.Fatalf("new = %+v", v)
	}
	// A failed task can be reassigned too.
	failed := endedTask(t, h, "check logs", a2a.TaskStateFailed)
	if _, err := h.ReassignFromUser(ctx, "sess-cap", failed, "vera"); err != nil {
		t.Fatalf("reassign of a failed task: %v", err)
	}
}

// A task of another conversation, or one never sent, reads as unknown.
func TestReplaceFromUserOwnership(t *testing.T) {
	h, _, _ := askHub()
	ctx := context.Background()
	failed := endedTask(t, h, "check logs", a2a.TaskStateFailed)
	for _, c := range []struct{ session, task string }{{"sess-other", failed}, {"", failed}, {"sess-cap", "t-nope"}} {
		if _, err := h.RetryFromUser(ctx, c.session, c.task); !errors.Is(err, ErrUnknownTask) {
			t.Fatalf("retry %+v: %v", c, err)
		}
		if _, err := h.ReassignFromUser(ctx, c.session, c.task, "vera"); !errors.Is(err, ErrUnknownTask) {
			t.Fatalf("reassign %+v: %v", c, err)
		}
	}
}

// Two clicks at once send one task: the other is refused.
func TestRetryFromUserDoubleSubmit(t *testing.T) {
	h, _, _ := askHub()
	failed := endedTask(t, h, "check logs", a2a.TaskStateFailed)
	before := len(h.SentFrom("sess-cap"))
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, refused := 0, 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = h.RetryFromUser(context.Background(), "sess-cap", failed)
			} else {
				_, err = h.ReassignFromUser(context.Background(), "sess-cap", failed, "vera")
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrAlreadyReplaced):
				refused++
			default:
				t.Errorf("click %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 || refused != 5 {
		t.Fatalf("ok=%d refused=%d", ok, refused)
	}
	if n := len(h.SentFrom("sess-cap")); n != before+1 {
		t.Fatalf("tasks = %d, want %d", n, before+1)
	}
}

// The link survives a restart: both records carry it, and the old task
// still refuses a second Retry.
func TestReplaceLinkPersists(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := askHub()
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	failed := endedTask(t, h1, "check logs", a2a.TaskStateFailed)
	out, err := h1.RetryFromUser(context.Background(), "sess-cap", failed)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new task to settle", func() bool { return viewOf(t, h1, out.TaskID).State != "working" })

	h2, _, _ := askHub()
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	old, next := viewOf(t, h2, failed), viewOf(t, h2, out.TaskID)
	if old.ReplacedBy != out.TaskID || old.ReplacedHow != ReplaceRetry || old.ReplacedToName != "Anton" || next.RetryOf != failed {
		t.Fatalf("after restart: old %+v new %+v", old, next)
	}
	if _, err := h2.RetryFromUser(context.Background(), "sess-cap", failed); !errors.Is(err, ErrAlreadyReplaced) {
		t.Fatalf("retry after restart: %v", err)
	}
	h2.mu.Lock()
	brief := h2.tasks[a2a.TaskID(failed)].brief
	h2.mu.Unlock()
	if brief != "check logs" {
		t.Fatalf("brief after restart = %q", brief)
	}
}

// lastSeen is the newest turn text fakeTurns ran.
func lastSeen(turns *fakeTurns) string {
	turns.mu.Lock()
	defer turns.mu.Unlock()
	return turns.seen[len(turns.seen)-1]
}

// editTask changes task id in memory, under h.mu.
func editTask(h *Hub, id string, edit func(*taskRef)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	edit(h.tasks[a2a.TaskID(id)])
}

// n1: Retry goes to the agent the task went to, by id: a handle that is
// gone, or now names another agent, is refused and nothing is sent.
func TestRetryRefusesAChangedTeammate(t *testing.T) {
	h, _, _ := askHub()
	ctx := context.Background()
	failed := endedTask(t, h, "check logs", a2a.TaskStateFailed)
	before := len(h.SentFrom("sess-cap"))
	d := h.Dir.(*fakeDir)
	setPeer(h, "a-anton", func(p *Peer) { p.Disabled = true })
	if _, err := h.RetryFromUser(ctx, "sess-cap", failed); !errors.Is(err, ErrTeammateGone) {
		t.Fatalf("retry to a disabled teammate: %v", err)
	}
	d.peers = append(d.peers, Peer{ID: "a-anton2", OwnerID: "u1", Handle: "anton", Name: "Anton II"})
	if _, err := h.RetryFromUser(ctx, "sess-cap", failed); !errors.Is(err, ErrTeammateChanged) {
		t.Fatalf("retry to a handle that changed hands: %v", err)
	}
	if n := len(h.SentFrom("sess-cap")); n != before {
		t.Fatalf("tasks = %d, want %d (nothing sent)", n, before)
	}
	// Both refusals gave the claim back.
	d.peers = d.peers[:len(d.peers)-1]
	setPeer(h, "a-anton", func(p *Peer) { p.Disabled = false })
	out, err := h.RetryFromUser(ctx, "sess-cap", failed)
	if err != nil {
		t.Fatalf("retry once the teammate is back: %v", err)
	}
	if v := viewOf(t, h, failed); v.ReplacedBy != out.TaskID || v.ReplacedToName != "Anton" {
		t.Fatalf("old = %+v", v)
	}
}

// n2: the claim is on disk before anything is sent. A restart that cut a
// Retry short before it sent lets the next Retry through; one that cut it
// after it sent finds the new task (retry_of) and refuses a second one.
func TestPendingReplaceAfterRestart(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := askHub()
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	notSent := endedTask(t, h1, "check logs", a2a.TaskStateFailed)
	sent := endedTask(t, h1, "pull logs", a2a.TaskStateFailed)
	for _, id := range []string{notSent, sent} {
		editTask(h1, id, func(r *taskRef) { r.replacePending = ReplaceRetry })
		h1.persist(a2a.TaskID(id))
		if rec, ok := readRecord(filepath.Join(dir, id+".json")); !ok || rec.ReplacePending != ReplaceRetry {
			t.Fatalf("pending claim not on disk: %+v", rec)
		}
	}
	// The Retry of `sent` went out just before the restart.
	next, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "pull logs", retryOf: a2a.TaskID(sent)})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new task to settle", func() bool { return viewOf(t, h1, next.TaskID).State != "working" })

	h2, _, _ := askHub()
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.RetryFromUser(ctx, "sess-cap", sent); !errors.Is(err, ErrAlreadyReplaced) {
		t.Fatalf("retry after a restart that already sent: %v", err)
	}
	if v := viewOf(t, h2, sent); v.ReplacedBy != next.TaskID || v.ReplacedHow != ReplaceRetry || v.ReplacedToHandle != "anton" {
		t.Fatalf("link not recovered: %+v", v)
	}
	out, err := h2.RetryFromUser(ctx, "sess-cap", notSent)
	if err != nil {
		t.Fatalf("retry after a restart that never sent: %v", err)
	}
	rec, _ := readRecord(filepath.Join(dir, notSent+".json"))
	if rec.ReplacePending != "" || rec.ReplacedBy != out.TaskID {
		t.Fatalf("record after retry = %+v", rec)
	}
}

// The person's own @mention task is sent again as theirs: origin user,
// their mention scope (a teammate whose policy is off still takes it),
// and nothing is told to the agent.
func TestReplaceUserTaskKeepsOrigin(t *testing.T) {
	h, turns, note := askHub()
	ctx := context.Background()
	r, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "check raya", Human: true, SessionUser: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	h.finished(ctx, a2a.TaskID(r.TaskID), a2a.TaskStateFailed, "stopped")
	out, err := h.RetryFromUser(ctx, "sess-cap", r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if v := viewOf(t, h, out.TaskID); v.Origin != OriginUser || v.RetryOf != r.TaskID {
		t.Fatalf("retried = %+v", v)
	}
	waitFor(t, "the retried turn", func() bool { return strings.HasSuffix(lastSeen(turns), "\ncheck raya") })

	setPeer(h, "a-vera", func(p *Peer) { p.MentionFrom = MentionOff })
	r2, _ := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "diff deploys", Human: true, SessionUser: "u1"})
	h.finished(ctx, a2a.TaskID(r2.TaskID), a2a.TaskStateRejected, "no access")
	targets, err := h.ReassignTargets(ctx, "sess-cap", r2.TaskID)
	if err != nil || len(targets) != 1 || targets[0].Handle != "vera" || targets[0].Refused {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	moved, err := h.ReassignFromUser(ctx, "sess-cap", r2.TaskID, "vera")
	if err != nil {
		t.Fatalf("reassign of the person's task: %v", err)
	}
	if v := viewOf(t, h, moved.TaskID); v.Origin != OriginUser || v.ToHandle != "vera" {
		t.Fatalf("reassigned = %+v", v)
	}
	delivered, _ := note.snapshot()
	for _, d := range delivered {
		if strings.HasPrefix(d, "sess-cap: The user ") {
			t.Fatalf("agent told about the person's task: %q", d)
		}
	}
}

// A record from before the brief was kept sends the thread's first entry
// when that entry is the brief, else the title (n6).
func TestReplaceBriefFallback(t *testing.T) {
	h, turns, _ := askHub()
	ctx := context.Background()
	long := strings.Repeat("é", threadTextMax+10)
	a := endedTask(t, h, long, a2a.TaskStateFailed)
	editTask(h, a, func(r *taskRef) { r.brief = "" })
	if _, err := h.RetryFromUser(ctx, "sess-cap", a); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the fallback brief", func() bool {
		return lastSeen(turns) == "anton <- Message from Yoga Bot (@captain):\n"+capText(long, threadTextMax)
	})

	b := endedTask(t, h, "pull logs\nfrom the worker", a2a.TaskStateFailed)
	editTask(h, b, func(r *taskRef) {
		r.brief = ""
		r.log = []ThreadEntry{{Role: ThreadUser, Text: "a later message", At: r.started.Add(time.Minute)}}
	})
	if _, err := h.RetryFromUser(ctx, "sess-cap", b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the title as brief", func() bool {
		return lastSeen(turns) == "anton <- Message from Yoga Bot (@captain):\npull logs"
	})
}

// A Send that fails gives the claim back: the next Retry goes through.
func TestReplaceSendFailureReleasesClaim(t *testing.T) {
	h, _, _ := askHub()
	ctx := context.Background()
	failed := endedTask(t, h, "check logs", a2a.TaskStateFailed)
	editTask(h, failed, func(r *taskRef) { r.brief, r.title, r.log = "", "", nil })
	if _, err := h.RetryFromUser(ctx, "sess-cap", failed); err == nil || errors.Is(err, ErrAlreadyReplaced) {
		t.Fatalf("retry with nothing to send: %v", err)
	}
	editTask(h, failed, func(r *taskRef) { r.brief = "check logs" })
	if _, err := h.RetryFromUser(ctx, "sess-cap", failed); err != nil {
		t.Fatalf("retry after a failed send: %v", err)
	}
}

// An agent shared with the sender's owner is a Reassign target too.
func TestReassignToSharedAgent(t *testing.T) {
	h, _, _ := newShareHub()
	ctx := context.Background()
	r, err := h.Send(ctx, SendInput{CallerSession: "s-bob", CallerAgentID: "b-cap", To: "anton", Text: "check logs", Wait: -1})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task to settle", func() bool { got, _ := h.GetTask(ctx, "b-cap", r.TaskID); return got != nil && got.State != "working" })
	// chatTurns always replies: the teammate declining is set by hand.
	editTask(h, r.TaskID, func(r *taskRef) { r.state = a2a.TaskStateRejected })
	targets, err := h.ReassignTargets(ctx, "s-bob", r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var lena *ReassignTarget
	for i := range targets {
		if targets[i].Handle == "anton" {
			t.Fatalf("own teammate offered: %+v", targets)
		}
		if targets[i].Handle == "lena" {
			lena = &targets[i]
		}
	}
	if lena == nil || !lena.Shared || lena.Refused {
		t.Fatalf("targets = %+v", targets)
	}
	out, err := h.ReassignFromUser(ctx, "s-bob", r.TaskID, "lena")
	if err != nil || out.To != "@lena" {
		t.Fatalf("reassign to a shared agent = %+v, %v", out, err)
	}
}

// The agent's task never goes to a teammate whose policy refuses that
// agent: the menu marks it, the route refuses it, the claim comes back.
func TestReassignRefusedByMentionPolicy(t *testing.T) {
	h, _, _ := askHub()
	ctx := context.Background()
	declined := endedTask(t, h, "check logs", a2a.TaskStateRejected)
	setPeer(h, "a-vera", func(p *Peer) { p.MentionFrom = MentionOff })
	targets, err := h.ReassignTargets(ctx, "sess-cap", declined)
	if err != nil || len(targets) != 1 || targets[0].Handle != "vera" || !targets[0].Refused {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	if _, err := h.ReassignFromUser(ctx, "sess-cap", declined, "vera"); !errors.Is(err, ErrTeammateRefuses) {
		t.Fatalf("reassign to a refusing teammate: %v", err)
	}
	setPeer(h, "a-vera", func(p *Peer) { p.MentionFrom = "" })
	if _, err := h.ReassignFromUser(ctx, "sess-cap", declined, "vera"); err != nil {
		t.Fatalf("reassign once vera takes it: %v", err)
	}
}

// n5: a stored brief is cut on a rune boundary.
func TestCutUTF8(t *testing.T) {
	if got := cutUTF8("aé", 2); got != "a…" {
		t.Fatalf("cut = %q", got)
	}
	if got := cutUTF8(strings.Repeat("é", 10), 7); !utf8.ValidString(got) || got != strings.Repeat("é", 3)+"…" {
		t.Fatalf("cut = %q", got)
	}
	if got := cutUTF8("abc", 3); got != "abc" {
		t.Fatalf("uncut = %q", got)
	}
}

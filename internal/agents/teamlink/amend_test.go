package teamlink

import (
	"context"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// heldTurns holds a turn at its gate until start, then reads the task's
// text the way the pool's RunIn does (TurnText) and answers from reply.
type heldTurns struct {
	start chan struct{}
	got   chan string
	reply chan string
}

func newHeldTurns() *heldTurns {
	return &heldTurns{start: make(chan struct{}), got: make(chan string, 4), reply: make(chan string, 4)}
}

func (g *heldTurns) MainSession(_ context.Context, agent Peer) string { return "sess-" + agent.ID }

func (g *heldTurns) Run(ctx context.Context, agent Peer, text string) (string, string, error) {
	<-g.start
	g.got <- TurnText(ctx, text)
	return "sess-" + agent.ID, <-g.reply, nil
}

func amendHub(t *testing.T) (*Hub, *heldTurns, *fanNotify, *Result) {
	t.Helper()
	h, _, note := newFanHub(t, false)
	g := newHeldTurns()
	h.Turns = g
	a := fanSend(t, h, "anton", nil)
	waitFor(t, "the task at anton's gate", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		_, ok := h.inflight["a-anton"][a2a.TaskID(a.TaskID)]
		return ok
	})
	return h, g, note, a
}

func amend(t *testing.T, h *Hub, id, text string, replace bool) *Result {
	t.Helper()
	res, err := h.Amend(context.Background(), AmendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: id, Text: text, Replace: replace})
	if err != nil {
		t.Fatalf("amend %s: %v", id, err)
	}
	return res
}

// A revision before the turn starts is read with the brief: one task,
// one reply.
func TestAmendBeforeTurn(t *testing.T) {
	h, g, note, a := amendHub(t)
	r1 := amend(t, h, a.TaskID, "only the last 24h", false)
	r2 := amend(t, h, a.TaskID, "check vera's logs instead", true)
	if r1.TaskID != a.TaskID || r2.TaskID != a.TaskID || !strings.Contains(r2.Note, "Revision 2") {
		t.Fatalf("results = %+v / %+v", r1, r2)
	}
	close(g.start)
	got := <-g.got
	if !strings.Contains(got, "check anton") || !strings.Contains(got, "Revision 1") || !strings.Contains(got, "only the last 24h") ||
		!strings.Contains(got, "Revision 2") || !strings.Contains(got, "replacing everything above") {
		t.Fatalf("turn read %q", got)
	}
	g.reply <- "vera's logs are clean"
	settled(t, h, a.TaskID, "completed")
	if n := len(h.SentFrom("sess-cap")); n != 1 {
		t.Fatalf("%d tasks, want 1", n)
	}
	if d := quiet(t, note, 1); !strings.Contains(d[0], "vera's logs are clean") {
		t.Fatalf("delivered = %q", d)
	}
}

// A revision of a running task goes into its turn's session.
func TestAmendRunning(t *testing.T) {
	h, g, note, a := amendHub(t)
	close(g.start)
	<-g.got
	res := amend(t, h, a.TaskID, "also count the 500s", false)
	if res.TaskID != a.TaskID || !strings.Contains(res.Note, "running turn") {
		t.Fatalf("result = %+v", res)
	}
	d := quiet(t, note, 1)
	if !strings.HasPrefix(d[0], "sess-a-anton: Revision 1 of task "+a.TaskID+" from Captain (@captain)") || !strings.Contains(d[0], "also count the 500s") {
		t.Fatalf("delivered = %q", d)
	}
	g.reply <- "12 401s, 3 500s"
	settled(t, h, a.TaskID, "completed")
	if n := len(h.SentFrom("sess-cap")); n != 1 {
		t.Fatalf("%d tasks, want 1", n)
	}
}

// A revision of a task that ended is a new task, linked both ways.
func TestAmendEnded(t *testing.T) {
	h, g, _, a := amendHub(t)
	close(g.start)
	<-g.got
	g.reply <- "done"
	settled(t, h, a.TaskID, "completed")
	g.reply <- "redone"
	res := amend(t, h, a.TaskID, "redo it for yesterday", false)
	if res.TaskID == a.TaskID || !strings.Contains(res.Note, "already ended") {
		t.Fatalf("result = %+v", res)
	}
	h.mu.Lock()
	old, nw := h.tasks[a2a.TaskID(a.TaskID)], h.tasks[a2a.TaskID(res.TaskID)]
	linked := old.amendedBy == a2a.TaskID(res.TaskID) && nw.amendOf == a2a.TaskID(a.TaskID)
	h.mu.Unlock()
	if !linked {
		t.Fatal("old and new task not linked")
	}
	if got := <-g.got; !strings.Contains(got, "Revision of task "+a.TaskID) {
		t.Fatalf("turn read %q", got)
	}
}

// Another agent's task, a canceled one, an empty revision: refused.
func TestAmendRefused(t *testing.T) {
	h, g, _, a := amendHub(t)
	ctx := context.Background()
	if _, err := h.Amend(ctx, AmendInput{CallerSession: "sess-v", CallerAgentID: "a-vera", TaskID: a.TaskID, Text: "x"}); err == nil {
		t.Fatal("another agent revised the task")
	}
	if _, err := h.Amend(ctx, AmendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: a.TaskID, Text: "  "}); err != ErrAmendEmpty {
		t.Fatalf("empty revision = %v", err)
	}
	if _, err := h.CancelTask(ctx, "a-cap", a.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Amend(ctx, AmendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: a.TaskID, Text: "x"}); err == nil {
		t.Fatal("a canceled task was revised")
	}
	close(g.start)
}

// An agent's cancel of a running turn asks it to stop instead of killing
// it; the task still ends canceled at once.
func TestSoftCancel(t *testing.T) {
	h, g, note, a := amendHub(t)
	close(g.start)
	<-g.got
	h.TurnOwner = func(session string) a2a.TaskID {
		if session == "sess-a-anton" {
			return a2a.TaskID(a.TaskID)
		}
		return ""
	}
	res, err := h.CancelTask(context.Background(), "a-cap", a.TaskID)
	if err != nil || res.State != "canceled" || !strings.Contains(res.Reason+res.ReplyText, "told to stop") {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
	d := quiet(t, note, 1)
	if !strings.HasPrefix(d[0], "sess-a-anton: Task "+a.TaskID+" was canceled by @captain") {
		t.Fatalf("delivered = %q", d)
	}
	g.reply <- "[silent]"
}

package teamlink

import (
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// LiveTasks lists every session's open tasks in one pass — working, or a
// question back to the sender — and leaves out the settled ones and those
// not started yet, as the Sub-agents panel counts them.
func TestLiveTasksOpenOnly(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	now := h.now()
	anton := Peer{ID: "a-anton", Handle: "anton"}
	vera := Peer{ID: "a-vera", Handle: "vera"}
	ref := func(session string, to Peer, chat string, finished bool, state a2a.TaskState) *taskRef {
		return &taskRef{callerSession: session, callerAgentID: "a-cap", to: to, chat: chat, finished: finished, state: state, started: now, touched: now}
	}
	h.mu.Lock()
	h.tasks["t-work"] = ref("sess-cap", anton, "sess-anton-1", false, a2a.TaskStateWorking)
	h.tasks["t-ask"] = ref("sess-cap", vera, "sess-vera-1", true, a2a.TaskStateInputRequired)
	h.tasks["t-ask"].reply = "which deploy?\nmore detail"
	h.tasks["t-done"] = ref("sess-cap", vera, "sess-vera-2", true, a2a.TaskStateCompleted)
	h.tasks["t-cancel"] = ref("sess-other", anton, "", true, a2a.TaskStateCanceled)
	h.tasks["t-failed"] = ref("sess-other", anton, "", true, a2a.TaskStateFailed)
	unstarted := ref("sess-other", vera, "", false, a2a.TaskStateWorking)
	unstarted.started = time.Time{}
	h.tasks["t-unstarted"] = unstarted
	h.mu.Unlock()

	got := h.LiveTasks()
	want := []LiveTask{
		{FromSession: "sess-cap", FromAgentID: "a-cap", ToID: "a-anton", ToHandle: "anton", Chat: "sess-anton-1", Working: true},
		{FromSession: "sess-cap", FromAgentID: "a-cap", ToID: "a-vera", ToHandle: "vera", Chat: "sess-vera-1", Question: "which deploy?"},
	}
	if len(got) != len(want) {
		t.Fatalf("LiveTasks = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("LiveTasks[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if empty, _, _ := newTestHub(nil); len(empty.LiveTasks()) != 0 {
		t.Fatalf("empty hub = %+v", empty.LiveTasks())
	}
	if got := h.LiveTasks(); len(got) != len(want) {
		t.Fatalf("second read = %+v", got)
	}
}

// LastSettled keeps each teammate's newest answered or declined task per
// sending session; open, canceled, interrupted and expired ones do not
// count.
func TestLastSettled(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	now := h.now()
	anton := Peer{ID: "a-anton", Handle: "anton"}
	vera := Peer{ID: "a-vera", Handle: "vera"}
	ref := func(session string, to Peer, finished bool, state a2a.TaskState, reply string, at time.Time) *taskRef {
		return &taskRef{callerSession: session, callerAgentID: "a-cap", to: to, chat: "c-" + to.Handle, finished: finished, state: state, reply: reply, started: at, touched: at}
	}
	h.mu.Lock()
	h.tasks["t-old"] = ref("sess-cap", anton, true, a2a.TaskStateCompleted, "old reply", now.Add(-2*time.Minute))
	h.tasks["t-new"] = ref("sess-cap", anton, true, a2a.TaskStateRejected, "no access to the repo\nmore", now.Add(-time.Minute))
	h.tasks["t-open"] = ref("sess-cap", anton, false, a2a.TaskStateWorking, "", now)
	h.tasks["t-other"] = ref("sess-b", anton, true, a2a.TaskStateCompleted, "for b", now.Add(-3*time.Minute))
	h.tasks["t-vera"] = ref("sess-cap", vera, true, a2a.TaskStateCompleted, "3 rooms hit the same 401", now.Add(-time.Minute))
	h.tasks["t-vera-cancel"] = ref("sess-cap", vera, true, a2a.TaskStateCanceled, "canceled", now)
	cut := ref("sess-cap", vera, true, a2a.TaskStateFailed, "interrupted by a restart", now)
	cut.interrupted = true
	h.tasks["t-vera-cut"] = cut
	h.tasks["t-vera-expired"] = ref("sess-old", vera, true, a2a.TaskStateCompleted, "long ago", now.Add(-TaskTTL-time.Minute))
	h.mu.Unlock()

	got := map[string]SettledTask{}
	for _, s := range h.LastSettled() {
		got[s.ToID+"/"+s.FromSession] = s
	}
	if a := got["a-anton/sess-cap"]; !a.Declined || a.Summary != "no access to the repo" || a.FromAgentID != "a-cap" || a.Chat != "c-anton" {
		t.Fatalf("anton = %+v", a)
	}
	if b := got["a-anton/sess-b"]; b.Declined || b.Summary != "for b" {
		t.Fatalf("anton for another session = %+v", b)
	}
	if v := got["a-vera/sess-cap"]; v.Declined || v.Summary != "3 rooms hit the same 401" {
		t.Fatalf("vera = %+v (interrupted or canceled counted)", v)
	}
	if len(got) != 3 {
		t.Fatalf("LastSettled = %+v", got)
	}
}

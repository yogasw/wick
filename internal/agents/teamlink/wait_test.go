package teamlink

import (
	"strings"
	"sync"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// A working task whose teammate is being restarted, or waits in the
// pool queue, stays working and carries the wait; a settled one never
// does. RefreshWaiting re-sends a task only when its wait moved.
func TestTaskWaitRestartingAndQueued(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	now := h.now()
	anton := Peer{ID: "a-anton", Handle: "anton"}
	vera := Peer{ID: "a-vera", Handle: "vera"}
	var mu sync.Mutex
	waits := map[string]TargetWait{
		"sess-anton-1": {RestartAttempt: 1, RestartMax: 2},
		"sess-vera-1":  {QueuePos: 3},
		"sess-vera-2":  {RestartAttempt: 2, RestartMax: 2},
	}
	h.WaitOf = func(_ Peer, chat string) TargetWait {
		mu.Lock()
		defer mu.Unlock()
		return waits[chat]
	}
	var sent []TaskView
	h.OnTaskChange = func(_ string, v TaskView) {
		mu.Lock()
		sent = append(sent, v)
		mu.Unlock()
	}
	ref := func(to Peer, chat string, finished bool, state a2a.TaskState) *taskRef {
		return &taskRef{callerSession: "sess-cap", callerAgentID: "a-cap", to: to, chat: chat, finished: finished, state: state, started: now, touched: now}
	}
	h.mu.Lock()
	h.tasks["t-restart"] = ref(anton, "sess-anton-1", false, a2a.TaskStateWorking)
	h.tasks["t-queued"] = ref(vera, "sess-vera-1", false, a2a.TaskStateWorking)
	h.tasks["t-done"] = ref(vera, "sess-vera-2", true, a2a.TaskStateCompleted)
	h.mu.Unlock()

	byID := map[string]TaskView{}
	for _, v := range h.SentFrom("sess-cap") {
		byID[v.TaskID] = v
	}
	if v := byID["t-restart"]; v.State != "working" || v.RestartAttempt != 1 || v.RestartMax != 2 ||
		v.WaitNote != "target restarting after an idle timeout, retry 1/2" {
		t.Fatalf("restarting task = %+v", v)
	}
	if v := byID["t-queued"]; v.State != "working" || v.QueuePos != 3 || v.WaitNote != "target queued for a free slot (#3)" {
		t.Fatalf("queued task = %+v", v)
	}
	if v := byID["t-done"]; v.RestartAttempt != 0 || v.WaitNote != "" {
		t.Fatalf("settled task carries a wait: %+v", v)
	}

	// Nothing moved: nothing re-sent.
	h.RefreshWaiting()
	if len(sent) != 0 {
		t.Fatalf("unchanged waits re-sent: %+v", sent)
	}
	// The restart came back: only that task goes out, with no wait.
	mu.Lock()
	delete(waits, "sess-anton-1")
	mu.Unlock()
	h.RefreshWaiting()
	if len(sent) != 1 || sent[0].TaskID != "t-restart" || sent[0].RestartAttempt != 0 || sent[0].WaitNote != "" {
		t.Fatalf("after the restart came back: %+v", sent)
	}
}

// The agent waiting on a working task is told it still works while its
// teammate restarts; without a wait the note is left alone.
func TestNoteWaitOnResult(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	h.WaitOf = func(_ Peer, chat string) TargetWait {
		if chat == "sess-anton-1" {
			return TargetWait{RestartAttempt: 1, RestartMax: 2}
		}
		return TargetWait{}
	}
	h.mu.Lock()
	h.tasks["t1"] = &taskRef{callerSession: "sess-cap", to: Peer{ID: "a-anton"}, chat: "sess-anton-1", started: h.now()}
	h.tasks["t2"] = &taskRef{callerSession: "sess-cap", to: Peer{ID: "a-vera"}, chat: "sess-vera-1", started: h.now()}
	h.mu.Unlock()
	out := &Result{TaskID: "t1", State: "working", Note: "Reply comes later."}
	h.noteWait(out)
	if !strings.HasPrefix(out.Note, "Still working: target restarting after an idle timeout, retry 1/2.") ||
		!strings.HasSuffix(out.Note, "Reply comes later.") || out.State != "working" {
		t.Fatalf("note = %q state = %q", out.Note, out.State)
	}
	quiet := &Result{TaskID: "t2", State: "working", Note: "Reply comes later."}
	h.noteWait(quiet)
	if quiet.Note != "Reply comes later." {
		t.Fatalf("note without a wait = %q", quiet.Note)
	}
	failed := &Result{TaskID: "t1", State: "failed"}
	h.noteWait(failed)
	if failed.Note != "" {
		t.Fatalf("failed task got a wait note: %q", failed.Note)
	}
}

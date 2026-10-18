package teamlink

import (
	"context"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// A second message to a teammate that has not started on the first one
// joins it (auto-merge); separate=true keeps it a task of its own. The
// line shows on the task (gate_pos) and in the teammate's chat (QueueAt).
func TestQueueAndAutoMerge(t *testing.T) {
	h, g, note, a := amendHub(t)
	gate := NewTurnGate(nil)
	h.GateLine = gate.Waiting
	// Something else holds anton's chat: the task waits in line.
	release, err := gate.Acquire(context.Background(), "sess-a-anton", "other")
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	go func() {
		rel, err := gate.Acquire(context.Background(), "sess-a-anton", a2a.TaskID(a.TaskID))
		if err == nil {
			<-waiting
			rel()
		}
	}()
	waitFor(t, "the task in line", func() bool { return len(gate.Waiting("sess-a-anton")) == 1 })

	v := h.SentFrom("sess-cap")
	if len(v) != 1 || v[0].GatePos != 1 || !strings.Contains(v[0].WaitNote, "#1 in line") {
		t.Fatalf("view = %+v", v)
	}
	q := h.QueueAt(context.Background(), "sess-a-anton")
	if len(q) != 1 || q[0].TaskID != a.TaskID || q[0].FromHandle != "captain" || q[0].Pos != 1 {
		t.Fatalf("queue = %+v", q)
	}

	res := fanSend(t, h, "anton", func(in *SendInput) { in.Text = "and the 500s" })
	if res.TaskID != a.TaskID || res.State != "merged" || !strings.Contains(res.Note, "amend_task="+a.TaskID) {
		t.Fatalf("second message = %+v", res)
	}
	if q := h.QueueAt(context.Background(), "sess-a-anton"); len(q) != 1 || q[0].Revisions != 1 {
		t.Fatalf("queue after merge = %+v", q)
	}
	g.answer = func(text string) string {
		if strings.Contains(text, "unrelated") {
			return "unrelated: done"
		}
		return "401s: 12, 500s: 3"
	}
	sep := fanSend(t, h, "anton", func(in *SendInput) { in.Text = "unrelated"; in.Separate = true })
	if sep.TaskID == a.TaskID || sep.State == "merged" {
		t.Fatalf("separate message merged: %+v", sep)
	}

	release()
	close(waiting)
	close(g.start)
	reads := <-g.got + "\n---\n" + <-g.got
	if !strings.Contains(reads, "and the 500s") {
		t.Fatalf("turns read %q", reads)
	}
	settled(t, h, a.TaskID, "completed")
	settled(t, h, sep.TaskID, "completed")
	if d := strings.Join(quiet(t, note, 2), "\n"); !strings.Contains(d, "500s: 3") || !strings.Contains(d, "unrelated: done") {
		t.Fatalf("delivered = %q", d)
	}
}

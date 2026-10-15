package ticket

import (
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// joinDeps is ticket mode on with the built-in statuses, recording the
// pointer write-back.
func joinDeps(l config.Layout, pointers map[string]string) AutoJoinDeps {
	return AutoJoinDeps{
		Layout: l,
		LoadConfig: func(string) (project.TicketConfig, error) {
			return project.TicketConfig{Enabled: true}, nil
		},
		WriteBackPointer: func(sid, tid string) { pointers[sid] = tid },
	}
}

func ticketWithThreads(t *testing.T, l config.Layout, title, threads string) Ticket {
	t.Helper()
	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: title})
	if err != nil {
		t.Fatal(err)
	}
	if threads != "" {
		tk.Fields = map[string]string{ThreadsField: threads}
		if err := Save(l, tk); err != nil {
			t.Fatal(err)
		}
	}
	return tk
}

func TestAutoJoinByOwnThread(t *testing.T) {
	cap := withCapture(t)
	l := newLayout(t)
	tk := ticketWithThreads(t, l, "inquiry", "C030CBY48KF/1792044355.318329, C0BD4UHBQRJ/1792044360.000100")
	pointers := map[string]string{}

	res := EvaluateAutoJoin(joinDeps(l, pointers), "p1", "s1", AutoJoinInput{
		ChatID: "C030CBY48KF", ThreadID: "1792044355.318329",
	})
	if !res.Joined || res.Ticket.ID != tk.ID || res.Rule != JoinByThread {
		t.Fatalf("result = %+v, want joined %s by thread", res, tk.ID)
	}
	if got, ok := FindBySession(l, "p1", "s1"); !ok || got.ID != tk.ID {
		t.Fatalf("session not on ticket: %+v %v", got, ok)
	}
	if pointers["s1"] != tk.ID {
		t.Errorf("pointer = %q, want %s", pointers["s1"], tk.ID)
	}
	attached := false
	for _, n := range cap.names() {
		if n == EventSessionAttached {
			attached = true
		}
	}
	if !attached {
		t.Errorf("events = %v, want %s", cap.names(), EventSessionAttached)
	}
}

func TestAutoJoinNeedsWholeToken(t *testing.T) {
	l := newLayout(t)
	ticketWithThreads(t, l, "inquiry", "C030CBY48KF/1792044355.3183290")

	res := EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "s1", AutoJoinInput{
		ChatID: "C030CBY48KF", ThreadID: "1792044355.318329",
	})
	if res.Joined {
		t.Fatalf("a token prefix must not match: %+v", res)
	}
}

func TestAutoJoinIgnoresFinishedTicket(t *testing.T) {
	l := newLayout(t)
	tk := ticketWithThreads(t, l, "old", "C1/1.2")
	tk, _ = Load(l, "p1", tk.ID)
	tk.Status = StatusDone
	if err := Save(l, tk); err != nil {
		t.Fatal(err)
	}

	res := EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "s1", AutoJoinInput{ChatID: "C1", ThreadID: "1.2"})
	if res.Joined || len(res.Ambiguous) > 0 {
		t.Fatalf("a done ticket must not be a candidate: %+v", res)
	}
}

func TestAutoJoinTwoCandidatesAttachesNothing(t *testing.T) {
	l := newLayout(t)
	a := ticketWithThreads(t, l, "a", "C1/1.2")
	b := ticketWithThreads(t, l, "b", "C9/9.9\nC1/1.2")

	res := EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "s1", AutoJoinInput{ChatID: "C1", ThreadID: "1.2"})
	if res.Joined {
		t.Fatalf("ambiguous match must not attach: %+v", res)
	}
	want := []string{a.ID, b.ID}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if !reflect.DeepEqual(res.Ambiguous, want) {
		t.Errorf("ambiguous = %v, want %v", res.Ambiguous, want)
	}
	if _, ok := FindBySession(l, "p1", "s1"); ok {
		t.Error("session was attached")
	}
}

func TestAutoJoinSubAgentFollowsParent(t *testing.T) {
	l := newLayout(t)
	tk := ticketWithThreads(t, l, "parent's", "")
	if err := AttachSession(l, "p1", tk.ID, "parent"); err != nil {
		t.Fatal(err)
	}

	res := EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "child", AutoJoinInput{ParentSessionID: "parent"})
	if !res.Joined || res.Ticket.ID != tk.ID || res.Rule != JoinByParent {
		t.Fatalf("result = %+v, want joined %s by parent", res, tk.ID)
	}
}

func TestAutoJoinByPermalinkAddsThread(t *testing.T) {
	l := newLayout(t)
	tk := ticketWithThreads(t, l, "inquiry", "C030CBY48KF/1792044355.318329")
	msg := "lanjut dari <https://acme.slack.com/archives/C030CBY48KF/p1792044399000200?thread_ts=1792044355.318329&cid=C030CBY48KF|thread ini>"

	res := EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "s2", AutoJoinInput{
		ChatID: "C0BD4UHBQRJ", ThreadID: "1792050000.000001", FirstMessage: msg,
	})
	if !res.Joined || res.Ticket.ID != tk.ID || res.Rule != JoinByPermalink {
		t.Fatalf("result = %+v, want joined %s by permalink", res, tk.ID)
	}
	got, _ := Load(l, "p1", tk.ID)
	want := "C030CBY48KF/1792044355.318329,C0BD4UHBQRJ/1792050000.000001"
	if got.Fields[ThreadsField] != want {
		t.Errorf("threads = %q, want %q", got.Fields[ThreadsField], want)
	}

	// The thread is now listed, so the next session born in it joins by
	// J1 — and the field is not appended to twice.
	res = EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "s3", AutoJoinInput{
		ChatID: "C0BD4UHBQRJ", ThreadID: "1792050000.000001", FirstMessage: msg,
	})
	if !res.Joined || res.Rule != JoinByThread {
		t.Fatalf("second session = %+v, want joined by thread", res)
	}
	if got, _ := Load(l, "p1", tk.ID); got.Fields[ThreadsField] != want {
		t.Errorf("threads after second join = %q, want %q", got.Fields[ThreadsField], want)
	}
}

func TestAutoJoinPermalinkToRootMessage(t *testing.T) {
	if got := permalinkTokens("https://x.slack.com/archives/C1/p1792044355318329"); !reflect.DeepEqual(got, []string{"C1/1792044355.318329"}) {
		t.Errorf("tokens = %v", got)
	}
}

func TestAutoJoinSkips(t *testing.T) {
	l := newLayout(t)
	tk := ticketWithThreads(t, l, "inquiry", "C1/1.2")
	other := ticketWithThreads(t, l, "other", "")
	if err := AttachSession(l, "p1", other.ID, "s-ticketed"); err != nil {
		t.Fatal(err)
	}
	in := AutoJoinInput{ChatID: "C1", ThreadID: "1.2"}

	if res := EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "s-ticketed", in); res.Joined {
		t.Errorf("a session already on a ticket must be left alone: %+v", res)
	}
	if got, _ := FindBySession(l, "p1", "s-ticketed"); got.ID != other.ID {
		t.Errorf("session moved to %s", got.ID)
	}

	room := in
	room.Room = true
	if res := EvaluateAutoJoin(joinDeps(l, map[string]string{}), "p1", "s-room", room); res.Joined {
		t.Errorf("a room session must not auto-join: %+v", res)
	}

	off := joinDeps(l, map[string]string{})
	off.LoadConfig = func(string) (project.TicketConfig, error) { return project.TicketConfig{}, nil }
	if res := EvaluateAutoJoin(off, "p1", "s-off", in); res.Joined {
		t.Errorf("ticket mode off must not auto-join: %+v", res)
	}
	if len(tk.Sessions) != 0 {
		t.Fatal("fixture ticket unexpectedly had sessions")
	}
}

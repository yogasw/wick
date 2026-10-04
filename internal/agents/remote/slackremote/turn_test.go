package slackremote

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/remote"
)

func markerTracker() *tracker {
	return newTracker("C1", "1.0", "1.0", "tok1", false, map[string]bool{"UWICK": true}, func(Message) bool { return true })
}

// feed observes each message and returns every event, the reply the turn
// ended with ("" when it did not end) and the status labels seen.
func feed(tr *tracker, msgs ...Message) (evs []remote.Event, done string, labels []string) {
	for _, m := range msgs {
		if m.Channel == "" {
			m.Channel = "C1"
		}
		if m.ThreadTS == "" {
			m.ThreadTS = "1.0"
		}
		if m.User == "" {
			m.User = "UBOT"
		}
		for _, ev := range tr.observe(m) {
			evs = append(evs, ev)
			switch ev.Kind {
			case remote.EventDone:
				done = ev.Text
			case remote.EventStatus:
				if ev.Detail != "" {
					labels = append(labels, ev.Detail)
				}
			}
		}
	}
	return evs, done, labels
}

func TestProgressThenFinal(t *testing.T) {
	_, done, labels := feed(markerTracker(),
		Message{TS: "1.1", Text: "_checking the repo list…_"},
		Message{TS: "1.2", Text: "Here are the results.\n\nEND RESPONSE tok1"},
	)
	if done != "Here are the results." {
		t.Fatalf("done = %q", done)
	}
	if len(labels) != 1 || labels[0] != "checking the repo list…" {
		t.Fatalf("labels = %q", labels)
	}
}

func TestProgressEditedIntoFinal(t *testing.T) {
	_, done, _ := feed(markerTracker(),
		Message{TS: "1.1", Text: "_checking the repo list…_"},
		Message{TS: "1.1", Edited: true, Text: "Three results:\n1. a\n2. b\n3. c\nEND RESPONSE tok1"},
	)
	if done != "Three results:\n1. a\n2. b\n3. c" {
		t.Fatalf("done = %q", done)
	}
}

func TestProgressDeletedThenFinal(t *testing.T) {
	tr := markerTracker()
	_, done, _ := feed(tr,
		Message{TS: "1.1", Text: "Looking into it"},
		Message{TS: "1.1", Deleted: true},
		Message{TS: "1.2", Text: "The answer. END RESPONSE tok1"},
	)
	if done != "The answer." {
		t.Fatalf("done = %q", done)
	}
}

func TestEditReplacesNotAppends(t *testing.T) {
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{}, func(Message) bool { return true })
	feed(tr, Message{TS: "1.1", Text: "first take"}, Message{TS: "1.1", Edited: true, Text: "second take"})
	if got, _ := tr.compose(); got != "second take" {
		t.Fatalf("compose = %q", got)
	}
}

func TestPruneDropsDeletedReply(t *testing.T) {
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{}, func(Message) bool { return true })
	feed(tr, Message{TS: "1.1", Text: "gone soon"}, Message{TS: "1.2", Text: "stays"})
	evs := tr.prune(map[string]bool{"1.2": true})
	if len(evs) == 0 || evs[len(evs)-1].Kind != remote.EventText || evs[len(evs)-1].Text != "stays" {
		t.Fatalf("prune events = %+v", evs)
	}
}

// The final message repeats the opening an earlier message already posted,
// starts with a mention of wick's own identity, comes as Slack mrkdwn and
// carries the marker at the end of a run-together line.
func TestInterimRepeatedByFinal(t *testing.T) {
	_, done, _ := feed(markerTracker(),
		Message{TS: "1.1", Text: "Okay, here is the rundown."},
		Message{TS: "1.2", Text: "<@UWICK> Okay, here is the rundown.  *1 &amp; 2 — Skills*  *a. Logs* — read only.  END RESPONSE tok1"},
	)
	want := "Okay, here is the rundown.  *1 & 2 — Skills*  *a. Logs* — read only."
	if done != want {
		t.Fatalf("done = %q, want %q", done, want)
	}
}

// shown lists the reply each EventText passed on.
func shown(evs []remote.Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Kind == remote.EventText {
			out = append(out, ev.Text)
		}
	}
	return out
}

func TestEachMessagePassedOnInOrder(t *testing.T) {
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{}, func(Message) bool { return true })
	evs, done, _ := feed(tr, Message{TS: "1.1", Text: "part one"}, Message{TS: "1.3", Text: "part three"}, Message{TS: "1.2", Text: "part two"})
	got := shown(evs)
	if done != "" || len(got) != 3 || got[2] != "part one\n\npart two\n\npart three" {
		t.Fatalf("done=%q shown=%q", done, got)
	}
	// An edit changes that message in place: no new bubble, no duplicate.
	evs, _, _ = feed(tr, Message{TS: "1.2", Edited: true, Text: "part 2"})
	if got := shown(evs); len(got) != 1 || got[0] != "part one\n\npart 2\n\npart three" {
		t.Fatalf("after edit = %q", got)
	}
}

func busyOf(evs []remote.Event) (last string) {
	for _, ev := range evs {
		if ev.Kind == remote.EventStatus {
			last = ev.Status
		}
	}
	return last
}

func TestBusySignals(t *testing.T) {
	tr := markerTracker()
	// A progress note after an answer: working again until the next answer.
	evs, _, _ := feed(tr, Message{TS: "1.1", Text: "First part."}, Message{TS: "1.2", Text: "_running the query…_"})
	if busyOf(evs) != remote.StatusWorking {
		t.Fatalf("progress after answer = %+v", evs)
	}
	evs, _, _ = feed(tr, Message{TS: "1.3", Text: "Second part."})
	if busyOf(evs) != remote.StatusIdle {
		t.Fatalf("answer after progress = %+v", evs)
	}
	// ⏳ on our message: working while it stays, idle once it is gone.
	evs, _, _ = feed(tr, Message{TS: "1.0", Reactions: []string{"hourglass_flowing_sand"}})
	if busyOf(evs) != remote.StatusWorking {
		t.Fatalf("hourglass = %+v", evs)
	}
	evs, _, _ = feed(tr, Message{TS: "1.0"})
	if busyOf(evs) != remote.StatusIdle {
		t.Fatalf("hourglass gone = %+v", evs)
	}
}

func TestCheckReactionEndsTurn(t *testing.T) {
	for _, r := range []string{"white_check_mark", "heavy_check_mark", "x"} {
		_, done, _ := feed(markerTracker(), Message{TS: "1.1", Text: "All set."}, Message{TS: "1.0", Reactions: []string{"eyes", r}})
		if done != "All set." {
			t.Errorf("%s: done = %q", r, done)
		}
	}
}

func TestStatusClearedEndsTurn(t *testing.T) {
	tr := markerTracker()
	evs, done, _ := feed(tr, Message{TS: "1.1", Text: "Answer so far.", Status: "working"})
	if done != "" || busyOf(evs) != remote.StatusWorking {
		t.Fatalf("status set: done=%q evs=%+v", done, evs)
	}
	if _, done, _ = feed(tr, Message{TS: "1.1", Edited: true, Text: "Answer so far."}); done != "Answer so far." {
		t.Fatalf("status cleared: done = %q", done)
	}
}

func TestProgressEditedIntoAnswerShows(t *testing.T) {
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{}, func(Message) bool { return true })
	evs, _, _ := feed(tr, Message{TS: "1.1", Text: "_looking…_"})
	if len(shown(evs)) != 0 {
		t.Fatalf("progress shown: %+v", evs)
	}
	evs, _, _ = feed(tr, Message{TS: "1.1", Edited: true, Text: "Found it: three rows."})
	if got := shown(evs); len(got) != 1 || got[0] != "Found it: three rows." || busyOf(evs) == remote.StatusWorking {
		t.Fatalf("edited into answer: %+v", evs)
	}
}

func TestCleanMrkdwn(t *testing.T) {
	tr := markerTracker()
	for in, want := range map[string]string{
		"<@UWICK> hi":                         "hi",
		"<@UOTHER> hi":                        "<@UOTHER> hi",
		"see <https://example.com/x|the doc>": "see the doc (https://example.com/x)",
		"see <https://example.com/x>":         "see https://example.com/x",
		"a &lt;b&gt; &amp;amp; c":             "a <b> &amp; c",
	} {
		if got := tr.clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReopenTakesLateMessages(t *testing.T) {
	tr := markerTracker()
	if _, done, _ := feed(tr, Message{TS: "1.1", Text: "Answer. END RESPONSE tok1"}); done != "Answer." {
		t.Fatalf("done = %q", done)
	}
	if evs, _, _ := feed(tr, Message{TS: "1.2", Text: "One more thing."}); len(evs) != 0 {
		t.Fatalf("ended turn still reported: %+v", evs)
	}
	src := &Source{cur: tr}
	src.Reopen(remote.Handle{})
	evs, _, _ := feed(tr, Message{TS: "1.3", Text: "And a fix."})
	if got := shown(evs); len(got) != 1 || got[0] != "Answer.\n\nAnd a fix." {
		t.Fatalf("after reopen = %+v", evs)
	}
}

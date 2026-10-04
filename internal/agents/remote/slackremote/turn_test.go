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
			case remote.EventText:
				done = "SHOWN BEFORE THE END: " + ev.Text
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
	if len(evs) == 0 || evs[len(evs)-1].Kind != remote.EventDraft || evs[len(evs)-1].Text != "stays" {
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

func TestNothingShownBeforeEnd(t *testing.T) {
	evs, done, _ := feed(markerTracker(), Message{TS: "1.1", Text: "part one"})
	if done != "" {
		t.Fatalf("ended or shown early: %q", done)
	}
	var working bool
	for _, ev := range evs {
		working = working || ev.Kind == remote.EventStatus && ev.Status == remote.StatusWorking
	}
	if !working {
		t.Fatalf("no working status: %+v", evs)
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

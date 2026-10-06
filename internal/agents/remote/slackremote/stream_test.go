package slackremote

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/remote"
)

// lastBusy is the last working/idle status in evs ("" = none).
func lastBusy(evs []remote.Event) string {
	s := ""
	for _, ev := range evs {
		if ev.Kind == remote.EventStatus && (ev.Status == remote.StatusWorking || ev.Status == remote.StatusIdle) {
			s = ev.Status
		}
	}
	return s
}

func lastText(evs []remote.Event) string {
	s := ""
	for _, ev := range evs {
		if ev.Kind == remote.EventText {
			s = ev.Text
		}
	}
	return s
}

func TestSplitProgress(t *testing.T) {
	cases := []struct {
		in, body, label string
		ok              bool
	}{
		{"Found the config.\n_lagi pakai code read…_", "Found the config.", "lagi pakai code read…", true},
		{"Part one.\n\nPart two...", "Part one.", "Part two...", true},
		{"_checking…_", "", "checking…", true},
		{"Done.\n_Bash: ls_", "Done.", "Bash: ls", true},
		// An italic closing line that does not trail off is the answer's.
		{"Here it is.\n_hope this helps_", "", "", false},
		{"Plain answer.", "", "", false},
	}
	for _, c := range cases {
		body, label, ok := splitProgress(c.in)
		if body != c.body || label != c.label || ok != c.ok {
			t.Errorf("splitProgress(%q) = %q %q %v", c.in, body, label, ok)
		}
	}
}

// A reply being written with its progress note as the last line: the
// text above streams, the note is the label, and the remote is busy.
func TestMixedReplyWithProgressIsBusy(t *testing.T) {
	tr := markerTracker()
	evs, done, labels := feed(tr, Message{TS: "1.1", Text: "Temuan 1: config ok.\n_lagi pakai code read…_"})
	if done != "" || lastBusy(evs) != remote.StatusWorking {
		t.Fatalf("not busy: %+v", evs)
	}
	if lastText(evs) != "Temuan 1: config ok." || len(labels) == 0 || labels[len(labels)-1] != "lagi pakai code read…" {
		t.Fatalf("text/labels = %q %q", lastText(evs), labels)
	}
	// Edited with more findings, still working: the new text streams and
	// the remote is said to be working again after it.
	evs, _, _ = feed(tr, Message{TS: "1.1", Edited: true, Text: "Temuan 1: config ok.\nTemuan 2: token expired.\n_lagi cek log…_"})
	if lastText(evs) != "Temuan 1: config ok.\nTemuan 2: token expired." || lastBusy(evs) != remote.StatusWorking {
		t.Fatalf("edit = %+v", evs)
	}
	// The final edit drops the note: idle, so the idle window may run.
	evs, _, _ = feed(tr, Message{TS: "1.1", Edited: true, Text: "Temuan 1: config ok.\nTemuan 2: token expired.\nKesimpulan: refresh token."})
	if lastBusy(evs) != remote.StatusIdle || lastText(evs) == "" {
		t.Fatalf("final edit = %+v", evs)
	}
}

// ⏳ on our message keeps the turn busy across text; an event of our
// message (no reactions on it) does not clear it; a reaction event does.
func TestHourglassBusyAcrossText(t *testing.T) {
	tr := markerTracker()
	evs := tr.react("1.0", "UBOT", "hourglass_flowing_sand", true)
	if lastBusy(evs) != remote.StatusWorking {
		t.Fatalf("hourglass added = %+v", evs)
	}
	evs, _, _ = feed(tr, Message{TS: "1.1", Text: "Partial finding."})
	if lastText(evs) != "Partial finding." || lastBusy(evs) != remote.StatusWorking {
		t.Fatalf("text under hourglass must stay busy: %+v", evs)
	}
	// message_changed of our own message carries no reactions.
	if evs, _, _ = feed(tr, Message{TS: "1.0", Edited: true, Text: "our question"}); lastBusy(evs) == remote.StatusIdle {
		t.Fatalf("edit of our message cleared the hourglass: %+v", evs)
	}
	if !tr.busy() {
		t.Fatal("hourglass lost")
	}
	// wick's own reaction is not the remote's.
	if evs := tr.react("1.0", "UWICK", "hourglass_flowing_sand", false); len(evs) != 0 || !tr.busy() {
		t.Fatalf("wick reaction changed busy: %+v", evs)
	}
	if evs := tr.react("1.0", "UBOT", "hourglass_flowing_sand", false); lastBusy(evs) != remote.StatusIdle {
		t.Fatalf("hourglass removed = %+v", evs)
	}
}

// Halodev: ⏳ while working, one message edited many times with a
// progress line at its end, a final edit, then ⏳ taken off — no ✅/❌.
// One reply streams, nothing ends the turn early, and only after ⏳ is
// gone does it go idle.
func TestHalodevScenarioWithoutCheckmark(t *testing.T) {
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{"UWICK": true}, func(Message) bool { return true })
	var all []remote.Event
	all = append(all, tr.react("1.0", "UHALO", "hourglass_flowing_sand", true)...)
	edits := []string{
		"_lagi baca thread…_",
		"Temuan: A.\n_lagi pakai code read…_",
		"Temuan: A.\nTemuan: B.\n_lagi pakai code read…_",
		"Temuan: A.\nTemuan: B.\nJawaban akhir.",
	}
	for i, txt := range edits {
		evs, done, _ := feed(tr, Message{TS: "1.1", User: "UHALO", BotID: "BHALO", Edited: i > 0, Text: txt})
		if done != "" {
			t.Fatalf("ended at edit %d", i)
		}
		if !tr.busy() {
			t.Fatalf("not busy at edit %d while ⏳ is on", i)
		}
		all = append(all, evs...)
	}
	if lastText(all) != "Temuan: A.\nTemuan: B.\nJawaban akhir." {
		t.Fatalf("reply = %q", lastText(all))
	}
	evs := tr.react("1.0", "UHALO", "hourglass_flowing_sand", false)
	if lastBusy(evs) != remote.StatusIdle || tr.busy() {
		t.Fatalf("⏳ removed = %+v", evs)
	}
	for _, ev := range append(all, evs...) {
		if ev.Kind == remote.EventDone {
			t.Fatalf("turn ended without marker/idle: %+v", ev)
		}
	}
}

// A remote's message in its turn's thread is its own — during the turn
// and after it ended — so the Slack channel does not dispatch it to an
// agent as a new message; wick's own and other people's are not.
func TestRouterOwnsLateReply(t *testing.T) {
	r := NewRouter()
	tr := newTracker("C1", "1.0", "1.0", "", false, map[string]bool{"UWICK": true}, func(m Message) bool { return m.User == "UHALO" })
	tr.targeted = true
	r.add(tr)
	reply := Message{Channel: "C1", TS: "1.5", ThreadTS: "1.0", User: "UHALO", BotID: "BHALO", Text: "<@UWICK> final"}
	if !r.Owns(reply) {
		t.Fatal("reply during the turn not owned")
	}
	r.remove(tr)
	if !r.Owns(reply) {
		t.Fatal("late reply after the turn not owned")
	}
	for _, m := range []Message{
		{Channel: "C1", TS: "1.6", ThreadTS: "1.0", User: "UWICK"},
		{Channel: "C1", TS: "1.7", ThreadTS: "1.0", User: "UPERSON"},
		{Channel: "C1", TS: "1.8", ThreadTS: "9.0", User: "UHALO"},
	} {
		if r.Owns(m) {
			t.Fatalf("owned %+v", m)
		}
	}
}

func TestRouterDispatchesReactions(t *testing.T) {
	r := NewRouter()
	tr := markerTracker()
	r.add(tr)
	r.DispatchReaction("C1", "1.0", "UBOT", "hourglass_flowing_sand", true)
	if !tr.busy() {
		t.Fatal("reaction_added not routed")
	}
	r.DispatchReaction("C1", "1.0", "UBOT", "hourglass_flowing_sand", false)
	if tr.busy() {
		t.Fatal("reaction_removed not routed")
	}
	if len(tr.events) == 0 {
		t.Fatal("no events queued")
	}
}

// Recheck reads the thread again: still working while ⏳ is on our
// message, the finished reply once it is gone — and posts nothing.
func TestRecheckReadsThreadAgain(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", MentionID: "UHALO"})
	dir := t.TempDir()
	if _, err := src.Recheck(context.Background(), dir); !errors.Is(err, ErrNoTurn) {
		t.Fatalf("no turn: %v", err)
	}
	saveState(dir, State{Channel: "C1", ThreadTS: "1700000000.000001", SentTS: "1700000000.000001"})
	f.mu.Lock()
	f.replies = []map[string]any{
		{"ts": "1700000000.000001", "user": "UWICK", "text": "question", "reactions": []map[string]any{{"name": "hourglass_flowing_sand"}}},
		{"ts": "1700000000.000002", "thread_ts": "1700000000.000001", "user": "UHALO", "bot_id": "BHALO", "text": "Temuan A.\n_lagi pakai code read…_"},
	}
	f.mu.Unlock()
	r, err := src.Recheck(context.Background(), dir)
	if err != nil || !r.Busy || r.Done || r.Text != "Temuan A." || r.Label != "lagi pakai code read…" {
		t.Fatalf("working: %+v %v", r, err)
	}
	f.mu.Lock()
	f.replies = []map[string]any{
		{"ts": "1700000000.000001", "user": "UWICK", "text": "question"},
		{"ts": "1700000000.000002", "thread_ts": "1700000000.000001", "user": "UHALO", "bot_id": "BHALO", "text": "Temuan A.\nJawaban akhir.\nEND RESPONSE abc123"},
	}
	posts := len(f.posts)
	f.mu.Unlock()
	r, err = src.Recheck(context.Background(), dir)
	if err != nil || r.Busy || !r.Done || r.Text != "Temuan A.\nJawaban akhir." {
		t.Fatalf("finished: %+v %v", r, err)
	}
	if posts != 0 {
		t.Fatal("recheck posted to the remote")
	}
}

// A turn posted top-level while the state named an older target's thread:
// Check again reads the turn's own thread instead of failing with
// thread_not_found, and keeps it for the next turn.
func TestRecheckFallsBackToTheTurnsOwnThread(t *testing.T) {
	f, src, _ := setup(t, Config{ConnectorID: "c", Target: TargetChannel, Channel: "C1", MentionID: "UHALO"})
	dir := t.TempDir()
	saveState(dir, State{Channel: "C1", ThreadTS: "1600000000.000001", SentTS: "1700000000.000001"})
	f.mu.Lock()
	f.missing = "1600000000.000001"
	f.replies = []map[string]any{
		{"ts": "1700000000.000001", "user": "UWICK", "text": "question"},
		{"ts": "1700000000.000002", "thread_ts": "1700000000.000001", "user": "UHALO", "bot_id": "BHALO", "text": "Jawaban.\nEND RESPONSE abc123"},
	}
	f.mu.Unlock()
	r, err := src.Recheck(context.Background(), dir)
	if err != nil || !r.Done || r.Text != "Jawaban." {
		t.Fatalf("recheck: %+v %v", r, err)
	}
	if st := LoadState(dir); st.ThreadTS != "1700000000.000001" || st.Target != "channel:C1" {
		t.Fatalf("state not repaired: %+v", st)
	}
}

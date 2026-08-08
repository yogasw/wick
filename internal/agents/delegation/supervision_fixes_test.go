package delegation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

/* ── #1: the result must be the sub-agent's answer ───────────────────── */

// The most damaging bug in the supervision report: a delegation's result
// held work belonging to a different question.
//
// A peer messaged the sub-agent mid-run; DeliverInbox injected that as a
// user turn; the reply to THAT was in the accumulator when the turn
// closed, so "Balasan terkirim ke @evidence-checker" was recorded as the
// answer to a question about currency codes. The real findings sat
// untouched in the envelope — and the async wake-up shows only `result`,
// so a leader that never ran collect acted on the wrong text with nothing
// to warn it.
func TestReportedResultWinsOverAPeerReply(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")

	if err := r.SaveResultJSON(context.Background(), row.ID, &ResultEnvelope{
		Summary:    "BRL, ARS, NZD",
		Structured: true,
	}); err != nil {
		t.Fatalf("save envelope: %v", err)
	}
	// A peer messaged it mid-run — the delivery is what makes the closing
	// text a reply to someone else rather than this delegation's answer.
	deliverPeerMessage(t, r, row, "evidence-checker")

	got := s.authoritativeResult(context.Background(), row,
		"Balasan terkirim ke @evidence-checker.")

	if got != "BRL, ARS, NZD" {
		t.Fatalf("result = %q, want the reported answer — a peer reply must not be recorded as the answer", got)
	}
}

// deliverPeerMessage records a message from another agent as having been
// delivered into this delegation, which is what marks its closing text as
// a reply to somebody else.
func deliverPeerMessage(t *testing.T, r *Repo, row *entity.AgentDelegation, from string) {
	t.Helper()
	now := time.Now().UTC()
	m := &entity.AgentMessage{
		ID:          "m-" + row.ID,
		RootID:      row.RootID,
		FromHandle:  from,
		ToHandle:    row.Handle,
		Body:        "what did you find?",
		Kind:        entity.MessageAsk,
		Status:      entity.MessageDelivered,
		CreatedAt:   now,
		DeliveredAt: &now,
	}
	if err := r.DB().Create(m).Error; err != nil {
		t.Fatalf("seed peer message: %v", err)
	}
}

// The mirror of the case above, and the reason the override is narrow: a
// sub-agent that reports structured findings and then closes with "done —
// see my report" has said two true things. The prose is its own summary
// of its own work, so the envelope is ADDITIVE and the text must survive.
//
// Overriding on "the two differ" alone would delete a perfectly good
// human-readable answer on every well-behaved run.
func TestReportedResultDoesNotOverrideTheAgentsOwnProse(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")

	if err := r.SaveResultJSON(context.Background(), row.ID, &ResultEnvelope{
		Summary:    "401 spike traced to the retry path",
		Structured: true,
	}); err != nil {
		t.Fatalf("save envelope: %v", err)
	}
	// No peer message: nothing interrupted this run.

	got := s.authoritativeResult(context.Background(), row, "done — see my report")
	if got != "done — see my report" {
		t.Fatalf("result = %q, want the sub-agent's own closing prose kept", got)
	}
}

// Most sub-agents never call report_result. Their closing text IS the
// answer and must be left exactly as it was.
func TestStreamedTextStandsWhenNothingWasReported(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")

	got := s.authoritativeResult(context.Background(), row, "the whole answer")
	if got != "the whole answer" {
		t.Fatalf("result = %q, want the streamed text unchanged", got)
	}
}

// An envelope reconstructed from prose (structured=false) is not a claim
// the sub-agent made — it is wick's own guess, so it must not override
// the text it was guessed from.
func TestUnstructuredEnvelopeDoesNotOverrideTheText(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")

	if err := r.SaveResultJSON(context.Background(), row.ID, &ResultEnvelope{
		Summary:    "a reconstruction",
		Structured: false,
	}); err != nil {
		t.Fatalf("save envelope: %v", err)
	}

	if got := s.authoritativeResult(context.Background(), row, "the streamed answer"); got != "the streamed answer" {
		t.Fatalf("result = %q, want the streamed text — an unasserted envelope must not win", got)
	}
}

/* ── #2 / #6 / #14: stop must actually stop ──────────────────────────── */

// Stop answered "killed", the row went interrupted — and the sub-agent
// worked on to step 10. Only its progress calls started failing, because
// the row was terminal. Work carried on with nobody watching.
//
// The cause: Interrupt handed the kill to whichever Run was waiting, and
// returned as soon as it found one. That waiter only kills when its
// select reaches the ctx.Done branch, which a streaming child keeps busy.
func TestInterruptKillsEvenWhenAWaiterExists(t *testing.T) {
	s, r, runner := newService(t)
	row := seedRunning(t, r, "a1")

	// A Run is parked on this delegation, exactly as during a live async run.
	_, cancel := context.WithCancel(context.Background())
	s.trackInflight(row.ID, cancel)

	if _, err := s.Interrupt(context.Background(), row.ID, "user-1", false); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	kills := runner.killed()
	if len(kills) == 0 {
		t.Fatal("no kill was issued — the sub-agent keeps working while its row reads interrupted")
	}
	if want := "sub-a1::researcher"; kills[0] != want {
		t.Fatalf("killed %q, want %q", kills[0], want)
	}
}

// "Partial work is kept and returned" is what the tool promises. It
// returned "" instead: PartialText holds the in-flight turn's buffer, and
// a respawn-per-turn provider (codex) is between turns for most of its
// life. Every completed step was lost even though the sub-agent had been
// reporting them all along.
func TestInterruptFallsBackToTheLastProgressReport(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = "" // between turns, as codex usually is
	row := seedRunning(t, r, "a1")

	if err := r.SaveProgress(context.Background(), row.ID, "step 4/10 done: Egypt\nNext: step 5"); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	if _, err := s.Interrupt(context.Background(), row.ID, "user-1", false); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	got, err := r.Get(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(got.Result, "step 4/10 done") {
		t.Fatalf("result = %q, want the last progress report rather than nothing", got.Result)
	}
	// And it must not read as a finished answer.
	if !strings.Contains(got.Result, "not a finished answer") {
		t.Fatalf("result = %q, want it labelled as partial", got.Result)
	}
}

// A live turn's buffer is the better source, so it still wins.
func TestInterruptPrefersLiveTextOverTheLastReport(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = "mid-sentence work"
	row := seedRunning(t, r, "a1")
	if err := r.SaveProgress(context.Background(), row.ID, "older note"); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	if _, err := s.Interrupt(context.Background(), row.ID, "user-1", false); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	got, _ := r.Get(context.Background(), row.ID)
	if got.Result != "mid-sentence work" {
		t.Fatalf("result = %q, want the in-flight text", got.Result)
	}
}

// Observed: turns_used went 1 → 0 after an interrupt. Interrupt wrote
// back a snapshot taken before the kill, while the child had kept working
// and UpdateTurns had moved the counter on. Writing the stale value walks
// it backwards, under-reporting spend and corrupting a continuation's
// budget arithmetic.
func TestInterruptNeverWalksTheTurnCounterBackwards(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")

	// The child completes turns between Interrupt's read and its write.
	if err := r.UpdateTurns(context.Background(), row.ID, 5); err != nil {
		t.Fatalf("update turns: %v", err)
	}

	if _, err := s.Interrupt(context.Background(), row.ID, "user-1", false); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	got, _ := r.Get(context.Background(), row.ID)
	if got.TurnsUsed < 5 {
		t.Fatalf("turns_used = %d after interrupt, want at least the 5 already spent", got.TurnsUsed)
	}
}

/* ── #9 / #11 / #12: the pending reply must describe itself ──────────── */

// A note promising "the progress below" when `progress` is empty teaches
// a leader to stop reading notes. It is empty whenever the sub-agent is
// between turns, which is most of the time for codex.
func TestPendingNoteDoesNotPromiseFieldsItDoesNotHave(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = ""
	seedRunning(t, r, "a1")

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got.Progress != "" || got.LastReport != "" {
		t.Fatalf("fixture is wrong: progress=%q last_report=%q", got.Progress, got.LastReport)
	}
	if strings.Contains(got.Note, "progress below") {
		t.Fatalf("note points at a field that is not there: %q", got.Note)
	}
	if !strings.Contains(got.Note, "supervised=true") {
		t.Fatalf("note does not say why there is nothing to read: %q", got.Note)
	}
}

// When there IS something to read, the note has to name the field that
// holds it — and recommend a control that works. `message` waits for the
// recipient's turn boundary and times out often enough that naming it as
// THE way to intervene sends a supervisor down a failing path.
func TestPendingNoteNamesTheFieldAndAWorkingControl(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")
	if err := r.SaveProgress(context.Background(), row.ID, "step 2/10 done"); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !strings.Contains(got.Note, "last_report") {
		t.Fatalf("note does not name the field holding the report: %q", got.Note)
	}
	if !strings.Contains(got.Note, "stop it") {
		t.Fatalf("note does not offer a control that always applies: %q", got.Note)
	}
}

// Without a timestamp a supervisor cannot tell a report filed ten seconds
// ago from one filed ten minutes ago — a healthy run from a stalled one.
// The text reads identically in both cases.
func TestProgressCarriesWhenItWasFiled(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")

	before := time.Now().UTC().Add(-time.Second)
	if err := r.SaveProgress(context.Background(), row.ID, "step 2/10"); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got.LastReportAt == "" {
		t.Fatal("last_report has no timestamp — a stalled run reads exactly like a healthy one")
	}
	at, perr := time.Parse(time.RFC3339, got.LastReportAt)
	if perr != nil {
		t.Fatalf("last_report_at %q is not RFC3339: %v", got.LastReportAt, perr)
	}
	if at.Before(before) {
		t.Fatalf("last_report_at = %v, want a time from this run", at)
	}
}

/* ── the async result must survive a mid-run peek ────────────────────── */

// Re-asserted here against the real Run path rather than a seeded row:
// the fixes above touch the same write, and losing a finished answer to a
// progress check is the most expensive failure this feature could cause.
func TestSupervisionCheckDoesNotEatTheFinalAnswer(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "the real answer"},
		{Type: event.Done},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{partial: "working"})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := s.Collect(context.Background(), res.DelegationID, "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got.Result != "the real answer" {
		t.Fatalf("result = %q", got.Result)
	}
	_ = r
}

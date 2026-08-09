package delegation

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

/* Round-4 supervision findings, as regression tests.

   B1: any assistant text at a turn boundary was read as the final answer.
   "Gunung 1 selesai. Lanjut ke-2." — the text literally says the agent is
   continuing — closed a 12-step task at step 1 with status done and a
   plausible-looking envelope. A supervised sub-agent signals completion
   with report_result; narration must nudge it onward, not close the run.

   B2: that premature close then made stop a no-op: the row was terminal,
   so Interrupt answered already_done without touching the still-running
   process.

   B4: turns_used was wrong even on success (6+ real turns recorded as 1)
   because the SSE bridge dropped Raw, where the provider's num_turns
   lives. */

// liveStream is a scriptedStream the test can keep feeding mid-run.
type liveStream struct {
	ch chan StreamEvent
}

func newLiveStream() *liveStream {
	return &liveStream{ch: make(chan StreamEvent, 16)}
}
func (l *liveStream) SubscribeSession(string) (<-chan StreamEvent, func()) {
	return l.ch, func() {}
}
func (l *liveStream) push(ev StreamEvent) { l.ch <- ev }

// nudgeDeliverer captures nudges sent back into the child session and
// lets the test react to them, standing in for the sub-agent receiving
// the message.
type nudgeDeliverer struct {
	mu        sync.Mutex
	sent      []string
	onDeliver func(text string)
}

func (d *nudgeDeliverer) DeliverToChannel(context.Context, string, string) error { return nil }

func (d *nudgeDeliverer) DeliverToSession(_ context.Context, _, _, text string) error {
	d.mu.Lock()
	d.sent = append(d.sent, text)
	cb := d.onDeliver
	d.mu.Unlock()
	if cb != nil {
		cb(text)
	}
	return nil
}
func (d *nudgeDeliverer) delivered() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sent...)
}

func TestSupervisedNarrationAtTurnBoundaryDoesNotCloseTheRun(t *testing.T) {
	stream := newLiveStream()
	runner := &fakeRunner{}
	s, r, _ := runService(t, stream, runner)
	deliver := &nudgeDeliverer{}
	s.Deliver = deliver

	// Captured at spawn so the nudge callback can address the row.
	var mu sync.Mutex
	rowID := ""

	// The narration turn: progress was filed, then the model chatted at
	// the turn boundary instead of calling report_result.
	runner.onStart = func(spec ChildSpec) {
		row, err := r.FindByChildSession(context.Background(), spec.SessionID)
		if err != nil || row == nil {
			t.Errorf("row not found for child session: %v", err)
			return
		}
		mu.Lock()
		rowID = row.ID
		mu.Unlock()
		_ = r.SaveProgress(context.Background(), row.ID, "Gunung 1/12: Everest")
		stream.push(StreamEvent{Type: event.TextDelta, Text: "Gunung 1 selesai: Everest - Nepal/China. Lanjut ke-2."})
		stream.push(StreamEvent{Type: event.Done})
	}

	// When the nudge lands the child "finishes properly": files
	// report_result, restates the answer, ends its turn.
	var once sync.Once
	deliver.onDeliver = func(string) {
		once.Do(func() {
			mu.Lock()
			id := rowID
			mu.Unlock()
			_ = r.SaveResultJSON(context.Background(), id, &ResultEnvelope{
				Summary: "12 gunung selesai", Confidence: ConfidenceHigh, Structured: true,
			})
			stream.push(StreamEvent{Type: event.TextDelta, Text: "Semua 12 gunung selesai. Jawaban akhir terlampir."})
			stream.push(StreamEvent{Type: event.Done})
		})
	}

	req := baseReq()
	req.Supervised = true
	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := deliver.delivered(); len(got) == 0 || !strings.Contains(got[0], "report_result") {
		t.Fatalf("narration at a turn boundary must nudge the sub-agent toward report_result, got %v", got)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q, want done after the proper finish", res.Status)
	}
	if !strings.Contains(res.Result, "12 gunung selesai") {
		t.Fatalf("the real answer never became the result: %q", res.Result)
	}
}

func TestUnsupervisedStillClosesOnTurnBoundaryText(t *testing.T) {
	// The one-question-one-answer shape must keep working: no supervision,
	// no progress reports — the closing text IS the answer.
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "the changelog is at CHANGELOG.md"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationDone || !strings.Contains(res.Result, "CHANGELOG.md") {
		t.Fatalf("unsupervised close-on-text regressed: status=%q result=%q", res.Status, res.Result)
	}
}

func TestProviderNumTurnsBeatsOurDoneCount(t *testing.T) {
	// One wick-visible Done, but the provider's result reports 49 internal
	// turns. Billing must trust the larger number.
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "answer"},
		{Type: event.Done, Raw: `{"type":"result","subtype":"success","num_turns":49,"usage":{"input_tokens":10,"output_tokens":5}}`},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.TurnsUsed != 49 {
		t.Fatalf("turns_used = %d, want 49 (the provider's own num_turns)", res.TurnsUsed)
	}
}

func TestInterruptOnTerminalRowStillKillsASurvivor(t *testing.T) {
	s, r, runner := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 4)

	out, err := s.Interrupt(context.Background(), "d1", "user-1", false)
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if out != OutcomeAlreadyDone {
		t.Fatalf("outcome = %q, want %q", out, OutcomeAlreadyDone)
	}
	// The row being closed must not shield a still-running process: the
	// premature-close bug left exactly that combination, and stop became
	// the one button that did nothing.
	if len(runner.killed()) == 0 {
		t.Fatal("a stop on an already-terminal row must still kill any surviving agent")
	}
}

func TestLabelUnfinished(t *testing.T) {
	if got := labelUnfinished(""); got != "" {
		t.Fatalf("empty must stay empty, got %q", got)
	}
	labelled := labelUnfinished("Negara 1 selesai. Lanjut.")
	if !strings.Contains(labelled, "NOT a completed answer") {
		t.Fatalf("failure-path result must be labelled: %q", labelled)
	}
	if twice := labelUnfinished(labelled); strings.Count(twice, "(unfinished") != 1 {
		t.Fatalf("label must not stack: %q", twice)
	}
}

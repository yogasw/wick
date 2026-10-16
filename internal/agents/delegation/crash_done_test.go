package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// A stream that closes mid-turn — no Done for the turn — is a dead
// process, not an answer: the run fails and the text is labelled.
func TestRunStreamClosedBeforeDoneIsUnfinished(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "Langkah 1 selesai, lanjut ke 2"},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationFailed {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationFailed)
	}
	if !strings.HasPrefix(res.Result, "(unfinished") {
		t.Fatalf("result not labelled unfinished: %q", res.Result)
	}
	if res.Note == "" || res.Envelope == nil {
		t.Fatalf("want a note and a fallback envelope, got note=%q envelope=%v", res.Note, res.Envelope)
	}
}

// A stream that closes right after a Done closed between turns: that stays
// the normal done path.
func TestRunStreamClosedAfterDoneStaysDone(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationDone)
	}
}

// The Done wick publishes for a crash with no restart fails the run as
// unfinished instead of closing it on the narration so far.
func TestRunExitDoneWithoutRestartIsUnfinished(t *testing.T) {
	stream := &scriptedStream{hold: true, events: []StreamEvent{
		{Type: event.TextDelta, Text: "setengah jalan"},
		{Type: event.Done, Raw: ExitDoneRaw(false)},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationFailed {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationFailed)
	}
	if !strings.HasPrefix(res.Result, "(unfinished") || !strings.Contains(res.Result, "setengah jalan") {
		t.Fatalf("result = %q, want labelled narration", res.Result)
	}
	row, err := r.Get(context.Background(), res.DelegationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Status != entity.DelegationFailed {
		t.Fatalf("row status = %q, want %q", row.Status, entity.DelegationFailed)
	}
}

// When the pool restarted the crashed agent, the run keeps waiting on the
// same session stream and returns the restarted process's answer.
func TestRunExitDoneWithRestartWaitsForRealDone(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "setengah jalan"},
		{Type: event.Done, Raw: ExitDoneRaw(true)},
		{Type: event.TextDelta, Text: "jawaban akhir"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationDone)
	}
	if res.Result != "jawaban akhir" {
		t.Fatalf("result = %q, want only the restarted turn's answer", res.Result)
	}
}

// The turn cap still bounds a run that keeps crashing and restarting.
func TestRunExitDoneWithRestartStillHitsTurnCap(t *testing.T) {
	stream := &scriptedStream{hold: true, events: []StreamEvent{
		{Type: event.Done, Raw: ExitDoneRaw(true)},
		{Type: event.Done, Raw: ExitDoneRaw(true)},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})
	req := baseReq()
	req.MaxTurns = 2

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationStoppedMaxTurns {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationStoppedMaxTurns)
	}
}

func TestParseExitDone(t *testing.T) {
	cases := []struct {
		raw                string
		exited, restarting bool
	}{
		{ExitDoneRaw(true), true, true},
		{ExitDoneRaw(false), true, false},
		{"", false, false},
		{`{"type":"result","num_turns":3}`, false, false},
		{`not json wick_process_exit`, false, false},
	}
	for _, c := range cases {
		e, r := ParseExitDone(c.raw)
		if e != c.exited || r != c.restarting {
			t.Errorf("ParseExitDone(%q) = %v,%v want %v,%v", c.raw, e, r, c.exited, c.restarting)
		}
	}
}

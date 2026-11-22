package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func TestAttachWatchTodoRoundTrip(t *testing.T) {
	steps := []Step{{Kind: StepKindCheck, Rules: []Rule{{Path: "s", Op: OpEquals, Value: "OK"}}}}
	enc, err := EncodeWatch(steps, OnMatchStop)
	if err != nil {
		t.Fatal(err)
	}
	if WatchTodoOf(enc) != nil {
		t.Fatal("plain watch has no todo")
	}
	enc, err = AttachWatchTodo(enc, WatchTodo{Item: "track-1", Title: "job a"})
	if err != nil {
		t.Fatal(err)
	}
	got := WatchTodoOf(enc)
	if got == nil || got.Item != "track-1" || got.Title != "job a" {
		t.Fatalf("todo = %+v", got)
	}
	back, om, err := ParseWatch(enc)
	if err != nil || len(back) != 1 || om != OnMatchStop {
		t.Fatalf("parse back: %v %d %s", err, len(back), om)
	}
}

func TestWatchTodoPatch(t *testing.T) {
	m := entity.ScheduledMessage{ID: "sm_1"}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name     string
		rec      RunRecord
		terminal string
		want     string
	}{
		{"pending", RunRecord{Result: entity.WatchResultPending, Run: 2, Extract: map[string]any{"detail": "IN_PROGRESS"}}, "", "in_progress"},
		{"error retry", RunRecord{Result: entity.WatchResultError, Error: "timeout"}, "", "in_progress"},
		{"success", RunRecord{Result: entity.WatchResultMatched, Extract: map[string]any{"state": "SUCCESSFUL"}}, entity.ScheduledStatusDone, "completed"},
		{"fail rules", RunRecord{Result: entity.WatchResultMatched, Outcome: OutcomeFail, Reason: "state FAILED"}, entity.ScheduledStatusDone, "failed"},
		{"exhausted", RunRecord{Result: entity.WatchResultPending, Reason: "time limit"}, entity.ScheduledStatusDone, "failed"},
		{"failed", RunRecord{Result: entity.WatchResultError, Reason: "denied"}, entity.ScheduledStatusFailed, "failed"},
	}
	for _, c := range cases {
		st, body := watchTodoPatch(m, c.rec, c.terminal, now)
		if st != c.want {
			t.Errorf("%s: status = %s, want %s", c.name, st, c.want)
		}
		if !strings.Contains(body, "watch sm_1") || !strings.Contains(body, "03:04:05") {
			t.Errorf("%s: body = %q", c.name, body)
		}
	}
	_, body := watchTodoPatch(m, cases[0].rec, "", now)
	if !strings.Contains(body, "IN_PROGRESS") || !strings.Contains(body, "check #2") {
		t.Errorf("pending body = %q", body)
	}
}

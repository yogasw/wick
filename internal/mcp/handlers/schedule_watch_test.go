package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/entity"
)

func TestScheduleTypeArgs(t *testing.T) {
	every := schedule.Spec{Recurring: true, IntervalMs: 10000}
	steps := []any{
		map[string]any{"kind": "connector", "tool_id": "conn:bb/get_pipeline", "params": map[string]any{"repo_slug": "x"}},
		map[string]any{"kind": "check", "rules": []any{map[string]any{"path": "state.name", "op": "in", "value": []any{"COMPLETED"}}}},
	}
	typ, got, err := scheduleTypeArgs(map[string]any{"type": "watch", "steps": steps}, every)
	if err != nil || typ != entity.ScheduledTypeWatch || len(got) != 2 {
		t.Fatalf("valid watch: %v %s %d", err, typ, len(got))
	}
	cases := []struct {
		args map[string]any
		spec schedule.Spec
		want string
	}{
		{map[string]any{"type": "watch"}, every, "at least one step"},
		{map[string]any{"type": "watch", "steps": steps}, schedule.Spec{Recurring: true, IntervalMs: 5000}, "at least 10s"},
		{map[string]any{"type": "watch", "steps": []any{map[string]any{"kind": "go", "script": "x"}}}, every, "not supported"},
		{map[string]any{"steps": steps}, every, "only for type=watch"},
		{map[string]any{"type": "cron"}, every, "type must be"},
	}
	for _, tc := range cases {
		if _, _, err := scheduleTypeArgs(tc.args, tc.spec); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want %q, got %v", tc.args, tc.want, err)
		}
	}
	// A run_at watch (no interval) is one run; the handler defaults a watch
	// with no timing at all to every 10s before it gets here.
	if typ, _, err := scheduleTypeArgs(map[string]any{"type": "watch", "steps": steps}, schedule.Spec{}); err != nil || typ != entity.ScheduledTypeWatch {
		t.Fatalf("run_at watch: %s %v", typ, err)
	}
	if typ, _, err := scheduleTypeArgs(map[string]any{}, schedule.Spec{}); err != nil || typ != entity.ScheduledTypeMessage {
		t.Fatalf("default type: %s %v", typ, err)
	}
}

func TestScheduleWatchOwner(t *testing.T) {
	watch := entity.ScheduledMessage{Type: entity.ScheduledTypeWatch, OwnerUserID: "u1"}
	if !ScheduleWatchOwner(watch, &entity.User{ID: "u1"}) {
		t.Fatal("owner refused")
	}
	if ScheduleWatchOwner(watch, &entity.User{ID: "u2"}) || ScheduleWatchOwner(watch, nil) {
		t.Fatal("non-owner allowed on a watch")
	}
	if !ScheduleWatchOwner(entity.ScheduledMessage{OwnerUserID: "u1"}, &entity.User{ID: "u2"}) {
		t.Fatal("message schedules keep the old rules")
	}
}

func TestScheduleWatchCreateNote(t *testing.T) {
	ends := time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC)
	m := entity.ScheduledMessage{ID: "sm_x", IntervalMs: 10000, EndsAt: &ends,
		Steps: `[{"kind":"connector","name":"pipeline","tool_id":"conn:bb/get_pipeline"},{"kind":"check","rules":[{"path":"state.name","op":"equals","value":"COMPLETED"}]}]`}
	note := scheduleWatchCreateNote(m)
	for _, want := range []string{"sm_x", "1. ", "2. ", "every 10s", "2026-10-08T16:00:00Z", "action=test id=sm_x", "action=runs id=sm_x", "action=delete id=sm_x"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
}

func TestScheduleOwnerOrAdmin_AllTypes(t *testing.T) {
	msg := entity.ScheduledMessage{OwnerUserID: "u1"}
	if ScheduleOwnerOrAdmin(msg, &entity.User{ID: "u2"}) || ScheduleOwnerOrAdmin(msg, nil) {
		t.Fatal("a non-owner passed the owner check on a message schedule")
	}
	if !ScheduleOwnerOrAdmin(msg, &entity.User{ID: "u1"}) || !ScheduleOwnerOrAdmin(msg, &entity.User{ID: "a", Role: entity.RoleAdmin}) {
		t.Fatal("owner/admin refused")
	}
	if ScheduleOwnerOrAdmin(entity.ScheduledMessage{}, &entity.User{ID: ""}) {
		t.Fatal("ownerless row matched an empty user id")
	}
}

func TestScheduleMayOverrideSteps(t *testing.T) {
	m := entity.ScheduledMessage{Type: entity.ScheduledTypeWatch, OwnerUserID: "admin1", RunAsUserID: "u1"}
	if !ScheduleMayOverrideSteps(m, &entity.User{ID: "u1"}) {
		t.Fatal("run-as user refused")
	}
	if ScheduleMayOverrideSteps(m, &entity.User{ID: "u2"}) || ScheduleMayOverrideSteps(m, nil) {
		t.Fatal("another user may pick steps that run as u1")
	}
	if !ScheduleMayOverrideSteps(m, &entity.User{ID: "x", Role: entity.RoleAdmin}) {
		t.Fatal("admin refused")
	}
}

func TestScheduleRowOwner_WatchBelongsToCaller(t *testing.T) {
	caller := &entity.User{ID: "a"}
	if got := scheduleRowOwner(entity.ScheduledTypeWatch, "projectOwnerB", caller); got != "a" {
		t.Fatalf("watch owner = %q, want the caller", got)
	}
	if got := scheduleRowOwner(entity.ScheduledTypeMessage, "projectOwnerB", caller); got != "projectOwnerB" {
		t.Fatalf("message owner = %q, want the scope owner", got)
	}
	if got := scheduleRowOwner(entity.ScheduledTypeWatch, "b", nil); got != "b" {
		t.Fatalf("no caller: %q", got)
	}
}

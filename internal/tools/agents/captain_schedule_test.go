package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
)

// The Captain creates, edits, pauses and resumes another agent's schedules
// while that agent's routines switch is on, each change announced in the
// agent's chat; off, every action is refused.
func TestCaptainScheduleHonoursRoutines(t *testing.T) {
	w := withCaptainWorld(t)
	withScheduleStore(t)
	ctx := context.Background()
	ops := CaptainOps{}
	in := func(action string, mod func(*teamagents.ScheduleInput)) teamagents.ScheduleInput {
		x := teamagents.ScheduleInput{Agent: "@worker", Action: action}
		if mod != nil {
			mod(&x)
		}
		return x
	}

	out, err := ops.Schedule(ctx, "s-captain", in("create", func(x *teamagents.ScheduleInput) {
		x.Cron, x.Message = "0 9 * * 1-5", "Morning triage"
	}))
	if err != nil {
		t.Fatal(err)
	}
	vm := out.(map[string]any)["schedule"].(agentScheduleVM)
	if vm.Destination != "main" || !strings.Contains(convText(t, "s-worker"), `"kind":"schedule_changed"`) {
		t.Fatalf("create = %+v", vm)
	}
	if _, err := ops.Schedule(ctx, "s-captain", in("update", func(x *teamagents.ScheduleInput) {
		x.ScheduleID, x.Cron = vm.ID, "bad cron"
	})); err == nil {
		t.Error("an invalid cron must be refused")
	}
	if _, err := ops.Schedule(ctx, "s-captain", in("update", func(x *teamagents.ScheduleInput) {
		x.ScheduleID, x.Every, x.Message = vm.ID, "2h", "Triage twice"
	})); err != nil {
		t.Fatal(err)
	}
	if m, _ := globalSchedule.Get(ctx, vm.ID); m.Message != "Triage twice" || m.Cron != "" || m.IntervalMs == 0 {
		t.Errorf("update = %+v", m)
	}
	if _, err := ops.Schedule(ctx, "s-captain", in("pause", func(x *teamagents.ScheduleInput) { x.ScheduleID = vm.ID })); err != nil {
		t.Fatal(err)
	}
	if m, _ := globalSchedule.Get(ctx, vm.ID); !m.Paused {
		t.Error("pause did not pause")
	}
	list, err := ops.Schedule(ctx, "s-captain", in("list", nil))
	if err != nil || len(list.(map[string]any)["items"].([]agentScheduleVM)) != 1 {
		t.Fatalf("list = %v %v", list, err)
	}
	if _, err := ops.Schedule(ctx, "s-captain", in("pause", func(x *teamagents.ScheduleInput) { x.ScheduleID = "nope" })); err == nil {
		t.Error("an unknown schedule must be refused")
	}

	p, _ := globalTeam.Get(ctx, w.worker.ID)
	p.CaptainCan = team.EncodeCaptainCan(team.CaptainCan{Persona: true, Routines: false})
	if err := globalTeam.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Schedule(ctx, "s-captain", in("resume", func(x *teamagents.ScheduleInput) { x.ScheduleID = vm.ID })); err == nil ||
		!strings.Contains(err.Error(), "Settings › Captain") {
		t.Errorf("routines off must refuse, got %v", err)
	}
}

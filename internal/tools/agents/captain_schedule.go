package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
)

// Schedule implements agents.schedule: the Captain works another agent's
// Scheduled drawer when that agent's captain_can.routines allows it. Like
// update_persona it applies at once, and every change is announced in the
// target agent's main chat so the owner sees who moved what.
func (o CaptainOps) Schedule(ctx context.Context, sessionID string, in teamagents.ScheduleInput) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	p, err := o.target(ctx, mgr, in.Agent)
	if err != nil {
		return nil, err
	}
	if !team.DecodeCaptainCan(p.CaptainCan).Routines {
		return nil, fmt.Errorf("@%s does not let the Captain manage its schedules (its Settings › Captain); ask the owner", p.Handle)
	}
	if globalSchedule == nil {
		return nil, errors.New("scheduling is not available")
	}
	mainID := ""
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		mainID = s.ID
	}
	now := time.Now()
	pid, sids := agentScheduleScope(p)
	if in.Action == "list" {
		rows, err := globalSchedule.ListTargeting(ctx, pid, sids, now.Add(-scheduledHistory))
		if err != nil {
			return nil, err
		}
		items := make([]agentScheduleVM, 0, len(rows))
		for _, m := range rows {
			items = append(items, agentScheduleToVM(m, mainID, p.ID))
		}
		return map[string]any{"items": items, "server_timezone": schedule.ServerZoneLabel(now)}, nil
	}

	var m *entity.ScheduledMessage
	if in.Action != "create" {
		if m, err = globalSchedule.Get(ctx, in.ScheduleID); err != nil ||
			!((pid != "" && m.ProjectID == pid) || (m.SessionID != "" && slices.Contains(sids, m.SessionID))) {
			return nil, fmt.Errorf("no schedule %q on @%s; use action=list", in.ScheduleID, p.Handle)
		}
	}
	message := strings.TrimSpace(in.Message)
	if len([]rune(message)) > scheduleMaxMessageRunes {
		return nil, errors.New("message is too long")
	}
	var verb string
	switch in.Action {
	case "create":
		if mainID == "" {
			return nil, fmt.Errorf("@%s has no main chat yet; the owner opens it once first", p.Handle)
		}
		if message == "" {
			return nil, errors.New("message is required")
		}
		spec, err := schedule.ParseWhen(in.RunAt, in.Every, in.Cron, now)
		if err != nil {
			return nil, err
		}
		row := &entity.ScheduledMessage{
			SessionID: mainID, SessionMode: entity.ScheduledSessionExisting, OwnerUserID: p.OwnerUserID,
			CreatedBy: entity.ScheduledByAI, SourceSessionID: sessionID, Message: message, RunAt: spec.FirstRunAt,
			Paused: p.Disabled, HeldByAgent: p.Disabled,
		}
		if spec.Recurring {
			row.Kind, row.Status = entity.ScheduledKindRecurring, entity.ScheduledStatusActive
			row.IntervalMs, row.Cron = spec.IntervalMs, spec.Cron
		}
		if m, err = globalSchedule.Create(ctx, row); err != nil {
			return nil, err
		}
		verb = "created"
	case "update":
		var patch schedule.SchedulePatch
		if in.RunAt != "" || in.Every != "" || in.Cron != "" {
			spec, err := schedule.ParseWhen(in.RunAt, in.Every, in.Cron, now)
			if err != nil {
				return nil, err
			}
			if spec.Recurring != m.IsRecurring() {
				return nil, errors.New("cannot turn a one-shot into a repeating schedule or back; create a new one")
			}
			patch.RunAt = spec.FirstRunAt
			if spec.Recurring {
				iv, cr := spec.IntervalMs, spec.Cron
				patch.IntervalMs, patch.Cron = &iv, &cr
			}
		}
		if message != "" {
			patch.Message = &message
		}
		err = globalSchedule.Reschedule(ctx, m.ID, patch)
		verb = "updated"
	case "pause":
		err = globalSchedule.SetPaused(ctx, m.ID, true, time.Time{})
		verb = "paused"
	case "resume":
		var next time.Time
		if next, err = schedule.NextFrom(*m, now); err == nil {
			err = globalSchedule.SetPaused(ctx, m.ID, false, next)
		}
		verb = "resumed"
	default:
		return nil, errors.New("action must be list, create, update, pause or resume")
	}
	if err != nil {
		if errors.Is(err, schedule.ErrNotFound) {
			return nil, errors.New("that schedule has finished (or is not a repeating one) and cannot be changed")
		}
		return nil, err
	}
	fresh, err := globalSchedule.Get(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	title := scheduledTitle(fresh.Message)
	by := "@" + mgr.Handle
	if mainID != "" {
		emitSystemEvent(mainID, store.KindScheduleChanged, fmt.Sprintf("Schedule “%s” %s · by %s", title, verb, by),
			map[string]string{"schedule_id": fresh.ID, "title": title, "action": verb, "changed_by": by})
	}
	return map[string]any{"status": verb, "schedule": agentScheduleToVM(*fresh, mainID, p.ID)}, nil
}

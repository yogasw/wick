package agents

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// The "Scheduled" drawer of an agent and its Settings › Session tab. A
// schedule belongs to an agent by where it fires (see
// schedule.ListTargeting), so these routes reuse the ordinary schedule
// store and runner — there is no second scheduler.

// scheduledHistory is how far back finished schedules stay in the drawer.
const scheduledHistory = 7 * 24 * time.Hour

// agentScheduleScope is what "aimed at this agent" means for p: its own
// sessions, plus its project when the project is the agent's (Team tag) —
// a project several things share would drag unrelated jobs in.
func agentScheduleScope(p entity.AgentPersona) (projectID string, sessionIDs []string) {
	tgPrefix := agentTelegramSessionPrefix(p.ID)
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.AgentID == p.ID || strings.HasPrefix(s.ID, tgPrefix) {
			sessionIDs = append(sessionIDs, s.ID)
		}
	}
	if p.ProjectID != "" {
		if pr, ok := globalMgr.Registry().Project(p.ProjectID); ok && slices.Contains(pr.Meta.Tags, project.AgentTag) {
			projectID = p.ProjectID
		}
	}
	return projectID, sessionIDs
}

// agentScheduleVM is one drawer row: the ordinary schedule view plus where
// it lands, in the drawer's words.
type agentScheduleVM struct {
	scheduleVM
	Title       string `json:"title"`
	Destination string `json:"destination"`
	// TelegramSession is the chat a "telegram" destination posts into.
	TelegramSession string `json:"telegram_session,omitempty"`
	// SlackChannel is the channel a "slack" destination's thread is in.
	SlackChannel string `json:"slack_channel,omitempty"`
	HeldByAgent  bool   `json:"held_by_agent,omitempty"`
}

func agentScheduleToVM(m entity.ScheduledMessage, mainID, agentID string) agentScheduleVM {
	dest, tg, sl := "chat", "", ""
	switch {
	case m.Mode() != entity.ScheduledSessionExisting:
		dest = "new_chat"
	case m.SessionID == mainID:
		dest = scheduleDestMain
	case strings.HasPrefix(m.SessionID, agentTelegramSessionPrefix(agentID)):
		dest, tg = scheduleDestTelegram, m.SessionID
	default:
		if sl = scheduleSlackChannelOf(m.SessionID, agentID); sl != "" {
			dest = scheduleDestSlack
		}
	}
	return agentScheduleVM{scheduleVM: scheduleToVM(m), Title: scheduledTitle(m.Message), Destination: dest,
		TelegramSession: tg, SlackChannel: sl, HeldByAgent: m.HeldByAgent}
}

// scheduledTitle is the first line of the message, clipped — schedules
// carry no title of their own.
func scheduledTitle(msg string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	if r := []rune(line); len(r) > 60 {
		return string(r[:57]) + "…"
	}
	return line
}

// loadScheduledAgent is loadOwnTeamAgent plus the schedule store check.
func loadScheduledAgent(c *tool.Ctx) (entity.AgentPersona, bool) {
	if !teamReady(c) {
		return entity.AgentPersona{}, false
	}
	if globalSchedule == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "scheduling is not available"})
		return entity.AgentPersona{}, false
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok || !requireAgentProjectAccess(c, p) {
		return p, false
	}
	return p, true
}

// apiTeamAgentScheduledList handles GET /api/team/agents/{id}/scheduled.
func apiTeamAgentScheduledList(c *tool.Ctx) {
	p, ok := loadScheduledAgent(c)
	if !ok {
		return
	}
	pid, sids := agentScheduleScope(p)
	rows, err := globalSchedule.ListTargeting(c.Context(), pid, sids, time.Now().Add(-scheduledHistory))
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	mainID := ""
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		mainID = s.ID
	}
	items := make([]agentScheduleVM, 0, len(rows))
	for _, m := range rows {
		items = append(items, agentScheduleToVM(m, mainID, p.ID))
	}
	st, _ := agentSlackOf(p)
	slackOnline := st.Connected && st.Online && !st.Disabled
	// The Telegram option only exists while the bot is connected.
	tgChats := []telegramChatVM{}
	tgReady := agentTelegramReady(p)
	if tgReady {
		if ch := agentTelegramChats(p); ch != nil {
			tgChats = ch
		}
	}
	// The Slack option exists while the agent's bot (Custom) or the shared
	// app it rides (Instant) can post.
	slackChannels := []slackChannelVM{}
	slackMode := ""
	t, slackReady := agentSlackScheduleTarget(p)
	if slackReady {
		slackMode = t.mode
		if ch := agentSlackChannels(p, t); ch != nil {
			slackChannels = ch
		}
	}
	c.JSON(http.StatusOK, map[string]any{
		"telegram_connected": tgReady,
		"telegram_chats":     tgChats,
		"items":              items,
		"feature_on":         team.DecodeFeatures(p.Features).Schedule,
		"agent_disabled":     p.Disabled,
		"server_timezone":    schedule.ServerZoneLabel(time.Now()),
		"main_session_id":    mainID,
		"slack_online":       slackOnline,
		"slack_ready":        slackReady,
		"slack_mode":         slackMode,
		"slack_channels":     slackChannels,
	})
}

// apiTeamAgentScheduledCreate handles POST /api/team/agents/{id}/scheduled:
// a schedule into the agent's main chat (created on first use), one of its
// Telegram bot's chats, or a new thread its Slack bot opens in a channel.
func apiTeamAgentScheduledCreate(c *tool.Ctx) {
	p, ok := loadScheduledAgent(c)
	if !ok {
		return
	}
	var body struct {
		RunAt       string `json:"run_at"`
		Every       string `json:"every"`
		Cron        string `json:"cron"`
		Message     string `json:"message"`
		Destination string `json:"destination"`
		// TelegramSession picks the chat of a "telegram" destination.
		TelegramSession string `json:"telegram_session"`
		// SlackChannel is the channel of a "slack" destination.
		SlackChannel string `json:"slack_channel"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	target, toMain, err := scheduleDestination(p, body.Destination, body.TelegramSession, body.SlackChannel)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	if len([]rune(message)) > scheduleMaxMessageRunes {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "message is too long"})
		return
	}
	spec, err := schedule.ParseWhen(body.RunAt, body.Every, body.Cron, time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	mainID := ""
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		mainID = s.ID
	} else if toMain {
		if mainID, err = createTeamAgentSession(c, p, true); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if toMain {
		target = mainID
	}
	// The thread is opened last, once nothing else can refuse the request.
	if body.Destination == scheduleDestSlack {
		if target, err = openSlackScheduleThread(c, p, body.SlackChannel, message); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	row := &entity.ScheduledMessage{
		SessionID:       target,
		SessionMode:     entity.ScheduledSessionExisting,
		OwnerUserID:     p.OwnerUserID,
		CreatedBy:       entity.ScheduledByUser,
		SourceSessionID: target,
		Message:         message,
		RunAt:           spec.FirstRunAt,
		// A disabled agent's schedules are held; a new one joins them.
		Paused:      p.Disabled,
		HeldByAgent: p.Disabled,
	}
	if spec.Recurring {
		row.Kind = entity.ScheduledKindRecurring
		row.Status = entity.ScheduledStatusActive
		row.IntervalMs = spec.IntervalMs
		row.Cron = spec.Cron
	}
	m, err := globalSchedule.Create(c.Context(), row)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, agentScheduleToVM(*m, mainID, p.ID))
}

// agentScheduleOf loads the {sid} schedule and checks it is aimed at p.
func agentScheduleOf(c *tool.Ctx, p entity.AgentPersona) (*entity.ScheduledMessage, bool) {
	m, err := globalSchedule.Get(c.Context(), c.PathValue("sid"))
	if err == nil {
		pid, sids := agentScheduleScope(p)
		if (pid != "" && m.ProjectID == pid) || (m.SessionID != "" && slices.Contains(sids, m.SessionID)) {
			return m, true
		}
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": "schedule not found"})
	return nil, false
}

// apiTeamAgentScheduledMutate backs PATCH (edit) and the pause / resume /
// run_now actions on one of the agent's schedules.
func apiTeamAgentScheduledMutate(action string) func(*tool.Ctx) {
	return func(c *tool.Ctx) {
		p, ok := loadScheduledAgent(c)
		if !ok {
			return
		}
		m, ok := agentScheduleOf(c, p)
		if !ok {
			return
		}
		var err error
		switch action {
		case "pause":
			err = globalSchedule.SetPaused(c.Context(), m.ID, true, time.Time{})
			if errors.Is(err, schedule.ErrNotFound) && !m.IsRecurring() {
				err = errors.New("only repeating schedules can be paused")
			}
		case "resume":
			var next time.Time
			if next, err = schedule.NextFrom(*m, time.Now()); err == nil {
				err = globalSchedule.SetPaused(c.Context(), m.ID, false, next)
			}
		case "edit":
			err = editAgentSchedule(c, p, m)
		case "run_now":
			if err = globalSchedule.RunNow(c.Context(), m.ID); err == nil {
				schedule.WakeRunner()
			}
		}
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, schedule.ErrNotFound) {
				status, err = http.StatusConflict, errors.New("the schedule has finished and can no longer be changed")
			}
			c.JSON(status, map[string]string{"error": err.Error()})
			return
		}
		fresh, err := globalSchedule.Get(c.Context(), m.ID)
		if err != nil {
			c.JSON(http.StatusNotFound, map[string]string{"error": "schedule not found"})
			return
		}
		mainID := ""
		if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
			mainID = s.ID
		}
		c.JSON(http.StatusOK, agentScheduleToVM(*fresh, mainID, p.ID))
	}
}

// apiTeamAgentScheduledDelete handles DELETE /api/team/agents/{id}/scheduled/{sid}.
func apiTeamAgentScheduledDelete(c *tool.Ctx) {
	p, ok := loadScheduledAgent(c)
	if !ok {
		return
	}
	m, ok := agentScheduleOf(c, p)
	if !ok {
		return
	}
	if err := globalSchedule.Delete(c.Context(), m.ID); err != nil && !errors.Is(err, schedule.ErrNotFound) {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// syncAgentSchedules holds or releases the agent's schedules when it is
// disabled or enabled. Best-effort: a failure is logged, never fails the
// settings save that triggered it.
func syncAgentSchedules(ctx context.Context, before, after entity.AgentPersona) {
	if globalSchedule == nil || globalMgr == nil || before.Disabled == after.Disabled {
		return
	}
	pid, sids := agentScheduleScope(after)
	var err error
	if after.Disabled {
		_, err = globalSchedule.HoldTargeting(ctx, pid, sids)
	} else {
		_, err = globalSchedule.ReleaseTargeting(ctx, pid, sids, time.Now())
	}
	if err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("agent", after.ID).Msg("team: sync agent schedules")
	}
}

// deleteAgentSchedules removes every schedule aimed at a deleted agent.
func deleteAgentSchedules(ctx context.Context, p entity.AgentPersona) {
	if globalSchedule == nil || globalMgr == nil {
		return
	}
	pid, sids := agentScheduleScope(p)
	if _, err := globalSchedule.DeleteTargeting(ctx, pid, sids); err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("agent", p.ID).Msg("team: delete agent schedules")
	}
}

// onScheduledFired records the "⏰ Scheduled … ran" event in an agent
// chat a schedule just fired into. Ordinary sessions get nothing: the
// event is part of the Team conversation, not of every nudge.
func onScheduledFired(ctx context.Context, m entity.ScheduledMessage, sessionID string) {
	if globalTeam == nil || globalTeam.AgentFor(ctx, sessionID) == nil {
		return
	}
	title := scheduledTitle(m.Message)
	emitSystemEvent(sessionID, store.KindScheduledFired, "⏰ Scheduled “"+title+"” ran",
		map[string]string{"schedule_id": m.ID, "title": title})
}

/* ── Settings › Session ───────────────────────────────────────────────── */

// agentSlackOf is the agent's Slack status, zero when there is no DB
// to read it from or the read fails — both tabs only show it.
func agentSlackOf(p entity.AgentPersona) (AgentSlackStatus, bool) {
	if globalDB == nil {
		return AgentSlackStatus{}, false
	}
	st, err := agentSlackStatusOf(p)
	return st, err == nil
}

// agentSessionVM is the Session tab: the policy plus what the tab shows
// beside it.
type agentSessionVM struct {
	team.SessionPolicy
	MainSessionID string `json:"main_session_id"`
	// SlackDMMainChat mirrors the Connections setting, read-only here.
	SlackDMMainChat bool `json:"slack_dm_main_chat"`
	SlackConnected  bool `json:"slack_connected"`
}

func agentSessionOf(p entity.AgentPersona) agentSessionVM {
	vm := agentSessionVM{SessionPolicy: team.DecodeSessionPolicy(p.SessionPolicy)}
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		vm.MainSessionID = s.ID
	}
	if st, ok := agentSlackOf(p); ok {
		vm.SlackConnected, vm.SlackDMMainChat = st.Connected, st.DMMainChat
	}
	return vm
}

// apiTeamAgentSessionGet handles GET /api/team/agents/{id}/session.
func apiTeamAgentSessionGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, agentSessionOf(p))
}

// apiTeamAgentSessionSave handles PATCH /api/team/agents/{id}/session:
// absent fields keep their value.
func apiTeamAgentSessionSave(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var body struct {
		Compact          *string `json:"compact"`
		IdleHours        *int    `json:"idle_hours"`
		SummariseThreads *bool   `json:"summarise_threads"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	pol := team.DecodeSessionPolicy(p.SessionPolicy)
	if body.Compact != nil {
		if *body.Compact != team.CompactAuto && *body.Compact != team.CompactIdle {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "compact must be auto or idle"})
			return
		}
		pol.Compact = *body.Compact
	}
	if body.IdleHours != nil {
		if *body.IdleHours < team.MinIdleHours || *body.IdleHours > team.MaxIdleHours {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "idle hours must be between 1 and 168"})
			return
		}
		pol.IdleHours = *body.IdleHours
	}
	if body.SummariseThreads != nil {
		pol.SummariseThreads = *body.SummariseThreads
	}
	p.SessionPolicy = team.EncodeSessionPolicy(pol)
	if err := globalTeam.Update(c.Context(), &p); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, agentSessionOf(p))
}

// apiTeamAgentCompact handles POST /api/team/agents/{id}/compact: queues
// /compact into the caller's main chat with the agent, through the same
// send path a typed /compact takes.
func apiTeamAgentCompact(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	s, ok := mainSessionOf(p.OwnerUserID, p.ID)
	if !ok {
		c.JSON(http.StatusConflict, map[string]string{"error": "the agent has no main chat yet"})
		return
	}
	if err := compactSession(c.Context(), s); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "queued", "session_id": s.ID})
}

func compactSession(ctx context.Context, s session.Session) error {
	if globalPool == nil {
		return errors.New("agents pool is not running")
	}
	agent := s.Meta.ActiveAgent
	if agent == "" {
		agent = "main"
	}
	return globalPool.Send(ctx, s.ID, agent, "ui", "user", "/compact")
}

// idleCompactInterval is how often idle main chats are checked. Idle
// thresholds are whole hours, so a few minutes of lag is invisible.
const idleCompactInterval = 5 * time.Minute

var startTeamBackground sync.Once

// startTeamJobs wires the Team side of scheduling (the scheduled_fired
// event) and starts the idle compactor. Called once the Team service is set.
func startTeamJobs() {
	startTeamBackground.Do(func() {
		schedule.SetFiredHook(onScheduledFired)
		go migrateRemoteUsage(context.Background())
		c := &team.IdleCompactor{
			Agents: func(ctx context.Context) ([]entity.AgentPersona, error) {
				if globalTeam == nil || globalPool == nil || globalMgr == nil {
					return nil, nil
				}
				return globalTeam.ListIdleCompact(ctx)
			},
			Main: func(p entity.AgentPersona) (team.MainChat, bool) {
				s, ok := mainSessionOf(p.OwnerUserID, p.ID)
				if !ok {
					return team.MainChat{}, false
				}
				lc := teamLiveNow().lifecycles[s.ID]
				busy := lc != "" && lc != "idle"
				return team.MainChat{SessionID: s.ID, LastActive: s.Meta.LastActive, Busy: busy}, true
			},
			Compact: func(ctx context.Context, sid string) error {
				s, ok := globalMgr.Registry().Session(sid)
				if !ok {
					return errors.New("session gone")
				}
				return compactSession(ctx, s)
			},
		}
		go c.Run(context.Background(), idleCompactInterval)
	})
}

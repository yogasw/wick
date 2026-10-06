package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/channels/telegram"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Where a drawer schedule lands besides the main chat, and what it did
// when it ran. A Telegram destination is no new column: it is the agent
// bot's chat session, and the bot posts a reply into its chat even when
// nobody typed there (telegram.Channel.turnLocked). The run history is
// read back from the "scheduled_fired" events those fires left.

// Drawer destinations (agentScheduleVM.Destination).
const (
	scheduleDestMain     = "main"
	scheduleDestTelegram = "telegram"
	scheduleDestSlack    = "slack"
)

// telegramChatVM is one Telegram chat a schedule can post into.
type telegramChatVM struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
}

// agentTelegramChats are the chats people opened with the agent's bot —
// a bot can only write to a chat that wrote to it first.
func agentTelegramChats(p entity.AgentPersona) []telegramChatVM {
	prefix := agentTelegramSessionPrefix(p.ID)
	var out []telegramChatVM
	for _, s := range globalMgr.Registry().Sessions() {
		if _, ok := telegram.ChatIDOf(prefix, s.ID); !ok {
			continue
		}
		title := strings.TrimSpace(s.Meta.Label)
		if title == "" {
			title = "Telegram chat " + strings.TrimPrefix(s.ID, prefix+"tg-")
		}
		out = append(out, telegramChatVM{SessionID: s.ID, Title: title})
	}
	slices.SortFunc(out, func(a, b telegramChatVM) int { return strings.Compare(a.Title, b.Title) })
	return out
}

// agentTelegramReady reports whether p's bot is connected and can post.
func agentTelegramReady(p entity.AgentPersona) bool {
	if globalDB == nil {
		return false
	}
	st, err := agentTelegramStatusOf(p)
	return err == nil && st.Connected && !st.Disabled
}

// errScheduleDest is a destination the caller cannot use (400).
type errScheduleDest struct{ msg string }

func (e errScheduleDest) Error() string { return e.msg }

// scheduleDestination resolves a drawer destination to the session the
// schedule fires into. main=true on an empty or "main" destination, where
// the caller supplies (or creates) the main chat. A "slack" destination is
// only checked here; its session is the thread openSlackScheduleThread
// opens once the rest of the request is valid.
func scheduleDestination(p entity.AgentPersona, dest, tgSession, slackChannel string) (sessionID string, main bool, err error) {
	switch dest {
	case "", scheduleDestMain:
		return "", true, nil
	case scheduleDestTelegram:
		if !agentTelegramReady(p) {
			return "", false, errScheduleDest{"the agent has no active Telegram connection"}
		}
		chats := agentTelegramChats(p)
		if len(chats) == 0 {
			return "", false, errScheduleDest{"nobody has messaged the agent's Telegram bot yet; a bot can only post to a chat that wrote to it"}
		}
		if tgSession == "" && len(chats) == 1 {
			return chats[0].SessionID, false, nil
		}
		for _, ch := range chats {
			if ch.SessionID == tgSession {
				return tgSession, false, nil
			}
		}
		return "", false, errScheduleDest{"pick one of the agent's Telegram chats"}
	case scheduleDestSlack:
		if _, ok := agentSlackScheduleTarget(p); !ok {
			return "", false, errScheduleDest{"the agent has no active Slack connection"}
		}
		if _, ok := normalizeSlackChannel(slackChannel); !ok {
			return "", false, errScheduleDest{"pick a Slack channel, or paste its id (C0123ABCD) or link"}
		}
		return "", false, nil
	default:
		return "", false, errScheduleDest{"destination must be main, telegram or slack"}
	}
}

// scheduleRunsLimit is how many runs the drawer's history shows.
const scheduleRunsLimit = 10

// scheduleRunVM is one fire of a schedule as the drawer's history lists it.
type scheduleRunVM struct {
	At        time.Time `json:"at"`
	SessionID string    `json:"session_id"`
	TurnID    string    `json:"turn_id"`
	// Status is ok (the agent answered), failed (the turn errored) or
	// running (no answer yet).
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// scheduleRunsIn scans one session's turns for fires of scheduleID: each
// scheduled_fired event opens a run, the first assistant answer after it
// settles it ok, an error turn settles it failed. Oldest first.
func scheduleRunsIn(turns []store.ConversationTurn, scheduleID, sessionID string) []scheduleRunVM {
	var out []scheduleRunVM
	open := -1
	for _, t := range turns {
		if t.Role == "system" && t.Kind == store.KindScheduledFired {
			open = -1
			if t.Extras["schedule_id"] == scheduleID {
				out = append(out, scheduleRunVM{At: t.Timestamp, SessionID: sessionID, TurnID: t.TurnID, Status: "running"})
				open = len(out) - 1
			}
			continue
		}
		if open < 0 {
			continue
		}
		switch {
		case t.IsError:
			out[open].Status, out[open].Error = "failed", firstLineOf(t.Text, 160)
			open = -1
		case t.Role == "assistant":
			out[open].Status = "ok"
			open = -1
		}
	}
	return out
}

// apiTeamAgentScheduledRuns handles GET /api/team/agents/{id}/scheduled/{sid}/runs:
// the last runs of one schedule, newest first. A delivery that never
// reached the chat leaves no event; its reason is last_error.
func apiTeamAgentScheduledRuns(c *tool.Ctx) {
	p, ok := loadScheduledAgent(c)
	if !ok {
		return
	}
	m, ok := agentScheduleOf(c, p)
	if !ok {
		return
	}
	var runs []scheduleRunVM
	seen := map[string]bool{}
	for _, sid := range []string{m.SessionID, m.LastSessionID} {
		if sid == "" || seen[sid] {
			continue
		}
		seen[sid] = true
		turns, err := loadConversation(globalLayout, sid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		runs = append(runs, scheduleRunsIn(turns, m.ID, sid)...)
	}
	slices.SortFunc(runs, func(a, b scheduleRunVM) int { return b.At.Compare(a.At) })
	if len(runs) > scheduleRunsLimit {
		runs = runs[:scheduleRunsLimit]
	}
	if runs == nil {
		runs = []scheduleRunVM{}
	}
	c.JSON(http.StatusOK, map[string]any{"items": runs, "last_error": m.LastError})
}

// editAgentSchedule applies the drawer's edit form: time, message (via the
// ordinary schedule patch, which validates cron) and destination.
func editAgentSchedule(c *tool.Ctx, p entity.AgentPersona, m *entity.ScheduledMessage) error {
	raw, err := io.ReadAll(io.LimitReader(c.R.Body, 1<<20))
	if err != nil {
		return errors.New("invalid JSON")
	}
	var body struct {
		Destination     *string `json:"destination"`
		TelegramSession string  `json:"telegram_session"`
		SlackChannel    string  `json:"slack_channel"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return errors.New("invalid JSON")
		}
	}
	c.R.Body = io.NopCloser(bytes.NewReader(raw))
	patch, err := scheduleParsePatchUI(*m, c, m.SessionID, time.Now())
	if err != nil {
		return err
	}
	if body.Destination != nil {
		target, toMain, err := scheduleDestination(p, *body.Destination, body.TelegramSession, body.SlackChannel)
		if err != nil {
			return err
		}
		if *body.Destination == scheduleDestSlack {
			// The same channel keeps its thread; another one opens a new
			// thread there.
			want, _ := normalizeSlackChannel(body.SlackChannel)
			target = m.SessionID
			if scheduleSlackChannelOf(m.SessionID, p.ID) != want {
				msg := m.Message
				if patch.Message != nil {
					msg = *patch.Message
				}
				if target, err = openSlackScheduleThread(c, p, want, msg); err != nil {
					return err
				}
			}
		}
		if toMain {
			if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
				target = s.ID
			} else if target, err = createTeamAgentSession(c, p, true); err != nil {
				return err
			}
		}
		if target != m.SessionID {
			patch.SessionID = &target
		}
	}
	return globalSchedule.Reschedule(c.Context(), m.ID, patch)
}

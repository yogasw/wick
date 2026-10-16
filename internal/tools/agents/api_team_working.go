package agents

import (
	"sort"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/a2aserver"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// TeamWorkingSession is one of an agent's chats other than its main chat
// as the roster row tells it: one still running (WorkingSessions), or the
// newest that finished (LastSession).
type TeamWorkingSession struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Trigger is who the chat works for: "you" for a chat the person
	// opened in the app, the agent whose Team task runs in it ("Captain"),
	// or "" for anything else (an outside caller, a channel, a schedule).
	Trigger string `json:"trigger"`
	// Source names where a chat with no Trigger came from ("Slack", "A2A",
	// "Schedule", "API", "Telegram"); "" when unknown or Trigger is set.
	Source string `json:"source,omitempty"`
	// Since is when its current turn was first seen running (WorkingSessions)
	// or when it last moved (LastSession).
	Since *time.Time `json:"since,omitempty"`
}

// lastSessionTTL is how long a finished non-main chat keeps the row's
// "Last: …" line, as long as a settled Team task keeps its reply.
const lastSessionTTL = teamlink.TaskTTL

// turnSince remembers when each running session's turn was first seen, so
// the roster's oldest-first order stays put while turns go on: a turn
// that ends drops out, the next one starts afresh. first is the turn's
// first in-flight event as last seen: the pool keeps those per turn, so a
// different, later one means a new turn even when no roster read caught
// the session idle in between.
var turnSince = struct {
	sync.Mutex
	at map[string]turnMark
}{at: map[string]turnMark{}}

type turnMark struct{ at, first time.Time }

// noteTurns updates turnSince from one pool snapshot's lifecycles and
// returns a copy for the response. A turn's first in-flight event, when
// older than the first sighting, is the better start.
func noteTurns(lifecycles map[string]string, firstEvent map[string]time.Time, now time.Time) map[string]time.Time {
	turnSince.Lock()
	defer turnSince.Unlock()
	out := make(map[string]time.Time, len(lifecycles))
	for id, lc := range lifecycles {
		if team.TurnStatus("", lc) != "running" {
			continue
		}
		m, ok := turnSince.at[id]
		ev := firstEvent[id]
		if !ok || (!ev.IsZero() && !m.first.IsZero() && ev.After(m.first)) {
			// Never seen running, or a later turn than the one noted.
			m = turnMark{at: now}
		}
		if !ev.IsZero() {
			if ev.Before(m.at) {
				m.at = ev
			}
			m.first = ev
		}
		turnSince.at[id] = m
		out[id] = m.at
	}
	for id := range turnSince.at {
		if _, ok := out[id]; !ok {
			delete(turnSince.at, id)
		}
	}
	return out
}

// workingSessionsOf reads, from sessions (userID's chats with one agent,
// agentSessions), the chats other than its main chat that run a turn now,
// oldest turn first (so a click on the row keeps opening the same one
// until it ends), and the newest of the rest that finished within
// lastSessionTTL. A queued chat is neither: it has not started, and
// counting it would move the click target once it does.
func workingSessionsOf(sessions []session.Session, mainID string, live teamLive, now time.Time) ([]TeamWorkingSession, *TeamWorkingSession) {
	type running struct {
		item  TeamWorkingSession
		since time.Time
	}
	var work []running
	var last *TeamWorkingSession
	var lastAt time.Time
	for _, s := range sessions {
		if s.ID == mainID || s.Meta.AgentMain {
			continue
		}
		trigger, source := sessionTrigger(s, live)
		item := TeamWorkingSession{ID: s.ID, Label: s.Meta.Label, Trigger: trigger, Source: source}
		switch team.TurnStatus(string(s.Meta.Status), live.lifecycles[s.ID]) {
		case "running":
			since, ok := live.since[s.ID]
			if !ok || since.IsZero() {
				// A turn noteTurns has not seen yet starts now.
				since = now
			}
			item.Since = timePtr(since)
			work = append(work, running{item, since})
			continue
		case "queued":
			continue
		}
		at := s.Meta.LastActive
		if at.IsZero() || now.Sub(at) > lastSessionTTL || (last != nil && !at.After(lastAt)) {
			continue
		}
		item.Since = timePtr(at)
		last, lastAt = &item, at
	}
	sort.SliceStable(work, func(i, j int) bool {
		if !work[i].since.Equal(work[j].since) {
			return work[i].since.Before(work[j].since)
		}
		return work[i].item.ID < work[j].item.ID
	})
	var working []TeamWorkingSession
	for _, w := range work {
		working = append(working, w.item)
	}
	return working, last
}

// sessionTrigger says who a chat works for: the agent whose Team task runs
// in it, "you" for a chat the person opened in the app, and otherwise no
// one but where it came from ("Slack", "A2A", "Schedule", …) — an outside
// caller's request is not the person's.
func sessionTrigger(s session.Session, live teamLive) (trigger, source string) {
	if sender := live.taskChats[s.ID]; sender != "" {
		return sender, ""
	}
	switch s.Meta.Origin {
	case session.OriginUI:
		return "you", ""
	case session.OriginSlack:
		return "", "Slack"
	case session.OriginTelegram:
		return "", "Telegram"
	case session.OriginSchedule:
		return "", "Schedule"
	case session.OriginREST:
		return "", "API"
	case session.Origin(a2aserver.Source):
		return "", "A2A"
	}
	return "", ""
}

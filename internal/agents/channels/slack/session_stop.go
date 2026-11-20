// Package slack — session_stop.go: Slack's native Stop button.
//
// An agent app subscribed to agent_session_stopped gets a Stop button on
// the thread's loading bubble while its status is "processing" (which
// assistant.threads.setStatus with a non-empty status maps to). A click
// arrives as that event; Slack does not move the status itself, so the
// app stops the turn and clears the status.
//
// Dependencies: slack-go slackevents (the event is not in its mapping, so
// it is registered here), the stop function wired by SetStopFn.
package slack

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/slack-go/slack/slackevents"
)

// agentSessionStoppedType is the Events API type of a Stop click.
const agentSessionStoppedType = "agent_session_stopped"

// AgentSessionStoppedEvent is the inner event of a Stop click.
// StreamingMessageTS lists the app's streams Slack stopped (empty when
// none was active).
type AgentSessionStoppedEvent struct {
	Type               string   `json:"type"`
	Channel            string   `json:"channel"`
	ThreadTS           string   `json:"thread_ts"`
	User               string   `json:"user"`
	EventTS            string   `json:"event_ts"`
	StreamingMessageTS []string `json:"streaming_message_ts"`
}

func init() {
	// Without the mapping slackevents.ParseEvent rejects the payload and
	// socket mode drops it before it reaches handleEventsAPI.
	slackevents.EventsAPIInnerEventMapping[agentSessionStoppedType] = AgentSessionStoppedEvent{}
}

// StopFn stops the running turn of a session; by names who asked.
type StopFn func(sessionID, by string) error

// SetStopFn wires what a Stop click calls. Nil = the click only clears
// the status.
func (s *Channel) SetStopFn(fn StopFn) {
	s.mu.Lock()
	s.stopFn = fn
	s.mu.Unlock()
}

// stopSessionFor finds the session of the turn running in channel/thread.
// The turns map is keyed by session, and a DM main chat's session is not
// sessionKey(thread), so the live turn is the authority; sessionKey is the
// fallback when this process holds no turn for the thread.
func (s *Channel) stopSessionFor(channelID, threadTS string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fallback := ""
	for sid, t := range s.turns {
		if t == nil || t.channelID != channelID || t.threadTS != threadTS {
			continue
		}
		if t.running {
			return sid, true
		}
		fallback = sid
	}
	if fallback != "" {
		return fallback, false
	}
	return s.sessionKey(threadTS), false
}

// handleAgentSessionStopped answers a Stop click: a person allowed to use
// the bot here stops the turn; the status is cleared either way so the
// bubble never stays stuck on "processing".
func (s *Channel) handleAgentSessionStopped(ctx context.Context, ev *AgentSessionStoppedEvent) {
	if ev == nil || ev.Channel == "" || ev.ThreadTS == "" {
		return
	}
	defer s.setAssistantStatus(ev.Channel, ev.ThreadTS, "")
	if ok, reason, _ := s.allowedSender(s.snapshot(), ev.User, ev.Channel); !ok {
		log.Info().Str("channel", "slack").Str("user", ev.User).Str("reason", reason).
			Msg("stop button: sender not allowed; turn left running")
		return
	}
	sid, running := s.stopSessionFor(ev.Channel, ev.ThreadTS)
	s.mu.Lock()
	fn := s.stopFn
	s.mu.Unlock()
	log.Info().Str("channel", "slack").Str("bot", s.logBot()).Str("session", sid).
		Str("user", ev.User).Bool("running", running).Msg("stop button clicked")
	if fn == nil || !running {
		return
	}
	by := "slack:" + ev.User
	if name := s.heardName(ev.User); name != "" {
		by = name
	}
	if err := fn(sid, by); err != nil {
		log.Warn().Str("channel", "slack").Str("session", sid).Err(err).Msg("stop button: stop failed")
		s.postReply(ev.Channel, ev.ThreadTS, fmt.Sprintf("Couldn't stop: %v", err))
		return
	}
	s.postReply(ev.Channel, ev.ThreadTS, fmt.Sprintf("⏹️ Stopped by <@%s>.", ev.User))
}

// Package slack — agent_dm.go: a DM to a Team agent's bot can continue the
// sender's main chat with that agent instead of opening a session per DM
// thread. Threads in channels and mentions always keep their own session.

package slack

import (
	"context"

	"github.com/slack-go/slack/slackevents"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// DMMainFn returns the session a DM from wickUserID continues, "" for the
// usual per-thread session.
type DMMainFn func(wickUserID string) string

// SetDMMainFn turns DM-continues-main-chat on (nil = off).
func (s *Channel) SetDMMainFn(fn DMMainFn) {
	s.cfgMu.Lock()
	s.dmMainFn = fn
	s.cfgMu.Unlock()
}

// dmMainSession is the main chat ev continues, "" when ev is not a DM, the
// option is off, or the sender maps to no wick user.
func (s *Channel) dmMainSession(ev *slackevents.MessageEvent) string {
	if ev.ChannelType != "im" {
		return ""
	}
	s.cfgMu.Lock()
	fn := s.dmMainFn
	s.cfgMu.Unlock()
	if fn == nil {
		return ""
	}
	uid := s.wickUserIDOf(ev.User)
	if uid == "" {
		return ""
	}
	id := fn(uid)
	if id != "" {
		s.dmMain.Store(id, struct{}{})
	}
	return id
}

// wickUserIDOf maps a Slack sender to a wick user without registering
// anyone; checkSenderIdentity has already gated the message.
func (s *Channel) wickUserIDOf(slackUserID string) string {
	s.cfgMu.Lock()
	users, api, instanceKey := s.users, s.api, s.sessionPrefix
	s.cfgMu.Unlock()
	if users == nil || api == nil {
		return ""
	}
	u, err := api.GetUserInfo(slackUserID)
	if err != nil {
		return ""
	}
	id, err := agentchannels.ResolveWickUser(context.Background(), users, senderIdentityFrom(slackUserFrom(u), slackUserID), false, "slack", instanceKey)
	if err != nil {
		return ""
	}
	return id
}

// releaseDMTurn forgets a main chat's turn once it is answered, so the next
// turn typed in the web app is not delivered to the DM.
func (s *Channel) releaseDMTurn(sessionID string) {
	s.mu.Lock()
	t := s.turns[sessionID]
	delete(s.turns, sessionID)
	s.mu.Unlock()
	if t != nil {
		s.stopStatusAnimation(t)
	}
}

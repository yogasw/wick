package slack

import (
	"fmt"
	"strings"

	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// OpenThread posts text as a new top-level message in channelID and returns
// the session key of the thread it opens plus the binding that session must
// carry. A Team agent's schedule aimed at a Slack channel fires into that
// session: ensureTurn restores the turn from the binding, so every run is
// answered as a reply in the thread although nobody typed there.
//
// as is the Instant agent the message is posted as (zero = the bot itself);
// without chat:write.customize it falls back to the bot, as postThread does.
// The caller creates the session and stores the binding — neither exists
// until the session is on disk.
func (s *Channel) OpenThread(channelID, text string, as Persona) (string, agentchannels.ThreadBinding, error) {
	api := s.API()
	if api == nil {
		return "", agentchannels.ThreadBinding{}, fmt.Errorf("the Slack bot is not running")
	}
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(text) == "" {
		return "", agentchannels.ThreadBinding{}, fmt.Errorf("channel and text are required")
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionText(text, false)}
	var extra []slackgo.MsgOption
	if as.AgentID != "" && !s.customizeDenied.Load() {
		extra = append(extra, slackgo.MsgOptionUsername(as.Username))
		if as.IconURL != "" {
			extra = append(extra, slackgo.MsgOptionIconURL(as.IconURL))
		}
	}
	_, ts, err := api.PostMessage(channelID, append(opts, extra...)...)
	if err != nil && len(extra) > 0 && isCustomizeDenied(err) {
		s.customizeDenied.Store(true)
		_, ts, err = api.PostMessage(channelID, opts...)
	}
	if err != nil {
		return "", agentchannels.ThreadBinding{}, fmt.Errorf("post to %s: %w", channelID, err)
	}
	key := s.sessionKey(ts)
	if as.AgentID != "" {
		s.personas.Store(key, as)
	}
	return key, agentchannels.ThreadBinding{
		Channel: "slack", ChatID: channelID, ThreadID: ts, Instance: s.sessionPrefixSnapshot(),
	}, nil
}

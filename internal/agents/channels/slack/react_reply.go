package slack

import (
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
)

// A reply that is only "[react :pray:]" (or "[react:pray]") becomes a
// reaction on the message that woke the turn instead of a post: a thank-you
// gets an emoji, not a sentence.
var reactReplyRe = regexp.MustCompile(`^\[react[:\s]\s*:?([a-z0-9_+'\-]+(?:::skin-tone-[2-6])?):?\s*\]$`)

// reactOnlyReply reports the emoji name when text is just a react marker.
func reactOnlyReply(text string) (string, bool) {
	m := reactReplyRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// reactPending reports a streaming reply that may still turn out to be a
// react marker: it is held back from the live message until the turn ends.
func reactPending(body string) bool {
	t := strings.ToLower(strings.TrimSpace(body))
	return strings.HasPrefix(t, "[react") || (t != "" && strings.HasPrefix("[react", t))
}

// reactInsteadOfReply puts name on the turn's waking message (the thread's
// top when that is unknown) and drops a live message a stream may have
// posted already.
func (s *Channel) reactInsteadOfReply(sessionKey, channelID, threadTS, liveTS, name string) {
	s.mu.Lock()
	msgTS := ""
	if cur := s.turns[sessionKey]; cur != nil {
		msgTS = cur.msgTS
		cur.liveTS, cur.lastSent = "", ""
	}
	s.mu.Unlock()
	if msgTS == "" {
		msgTS = threadTS
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || channelID == "" || msgTS == "" {
		return
	}
	if liveTS != "" {
		_, _, _ = api.DeleteMessage(channelID, liveTS)
	}
	err := api.AddReaction(name, slackgo.ItemRef{Channel: channelID, Timestamp: msgTS})
	if err != nil && !strings.Contains(err.Error(), "already_reacted") {
		log.Warn().Str("channel", "slack").Str("slack_channel", channelID).Str("ts", msgTS).
			Str("emoji", name).Err(err).Msg("react reply: could not add the reaction")
		return
	}
	log.Info().Str("channel", "slack").Str("slack_channel", channelID).Str("ts", msgTS).
		Str("emoji", name).Msg("react reply: answered with a reaction")
}

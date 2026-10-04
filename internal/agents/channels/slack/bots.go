package slack

import (
	"time"

	"github.com/rs/zerolog/log"
	"github.com/slack-go/slack/slackevents"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// Bot-to-bot loop guard. A whitelisted bot may trigger a turn like a person,
// which means two wick agents that whitelist each other could answer each
// other forever. Two rules keep that bounded:
//
//   - a bot only reaches an agent through an explicit @mention in a channel
//     (or a DM) — the 🤖 auto-reply switch never applies to bot messages, so a
//     plain reply from another agent does not start the next turn, and
//   - at most botTurnLimit bot-triggered turns run per thread inside
//     botTurnWindow. Anything past that is dropped until the window slides.
//
// The agent's own bot is refused before either rule (allowedBotCfg).
const (
	botTurnLimit  = 5
	botTurnWindow = 10 * time.Minute
)

// isFromBot reports whether a message event was posted by an app or bot.
func isFromBot(ev *slackevents.MessageEvent) bool {
	return ev != nil && (ev.BotID != "" || ev.SubType == "bot_message")
}

// allowedBotCfg is allowedCfg for bot senders. A bot passes the identity
// check only through BotsMode / AllowedBots — the users and groups lists are
// for people. The channels whitelist is AND'd on top exactly like it is for
// people. reason is "self" for this instance's own bot, "bots" when bots are
// ignored or the bot is not whitelisted, "channels" for the channel gate.
func (s *Channel) allowedBotCfg(cfg agentconfig.SlackChannelConfig, ev *slackevents.MessageEvent, channelID string) (bool, string) {
	if s.postedByThisInstance(ev) {
		return false, "self"
	}
	switch cfg.BotsMode {
	case "all":
	case "whitelist":
		if !pickerHas(cfg.AllowedBots, ev.User) && !pickerHas(cfg.AllowedBots, ev.BotID) {
			return false, "bots"
		}
	default: // "none" / unset: bots are ignored, the behaviour before BotsMode existed
		return false, "bots"
	}
	if channelID != "" && cfg.ChannelsMode == "whitelist" && !pickerHas(cfg.AllowedChannels, channelID) {
		return false, "channels"
	}
	return true, ""
}

// allowBotTurn records one bot-triggered turn for sessionID and reports
// whether it fits the per-thread budget (botTurnLimit per botTurnWindow).
func (s *Channel) allowBotTurn(sessionID string, now time.Time) bool {
	s.botTurnsMu.Lock()
	defer s.botTurnsMu.Unlock()
	if s.botTurns == nil {
		s.botTurns = map[string][]time.Time{}
	}
	kept := s.botTurns[sessionID][:0]
	for _, at := range s.botTurns[sessionID] {
		if now.Sub(at) < botTurnWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) >= botTurnLimit {
		s.botTurns[sessionID] = kept
		log.Warn().Str("channel", "slack").Str("session", sessionID).
			Int("limit", botTurnLimit).Dur("window", botTurnWindow).
			Msg("bot turn budget spent for this thread, ignoring bot message")
		return false
	}
	s.botTurns[sessionID] = append(kept, now)
	return true
}

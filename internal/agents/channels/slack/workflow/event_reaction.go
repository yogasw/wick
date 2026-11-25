package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// SlackReactionMatch is the trigger filter form of slack.reaction and
// slack.reaction_removed. Reaction is the emoji name without colons
// ("white_check_mark"); ItemUser is whose message was reacted to (e.g.
// only reactions on a bot's answer).
type SlackReactionMatch struct {
	Mode            string `wick:"dropdown=all|whitelist;default=all;desc=Filter mode: all=fire on every reaction, whitelist=only matching ones"`
	Reaction        string `wick:"visible_when=mode:whitelist;key=reaction;desc=Emoji name without colons, e.g. white_check_mark (empty = any emoji)"`
	AllowedChannels string `wick:"picker=slack.channels;visible_when=mode:whitelist;key=channel_id;desc=Only in these channels"`
	AllowedUsers    string `wick:"picker=slack.users;visible_when=mode:whitelist;key=user;desc=Only when one of these people reacts"`
	ItemUsers       string `wick:"picker=slack.users;visible_when=mode:whitelist;key=item_user;desc=Only on messages written by these users or bots"`
	TextContains    string `wick:"visible_when=mode:whitelist;key=text_contains;desc=Only when the reacted message contains this text (case-insensitive)"`
}

// ReactionEvent is the payload of a reaction added to (or removed from)
// a message the bot can see. No extra Slack call is made per reaction:
// the thread of a reply is read with get_thread_replies on ts (Slack
// accepts a reply's ts there and returns the whole thread).
type ReactionEvent struct {
	User      string `json:"user"`       // who reacted (U…)
	Reaction  string `json:"reaction"`   // emoji name, no colons
	ChannelID string `json:"channel_id"` // C… or D…
	TS        string `json:"ts"`         // ts of the reacted message
	ItemUser  string `json:"item_user"`  // who wrote the reacted message
	Added     bool   `json:"added"`      // true for slack.reaction, false for slack.reaction_removed
	IsDM      bool   `json:"is_dm"`
}

func registerEventReaction(reg *integration.Registry) {
	reg.RegisterEvent(reactionDescriptor("reaction", "Slack: Reaction added",
		"Fires when someone adds an emoji reaction to a message the bot can see (the bot's own reactions are skipped). Pushed by Slack, so no polling and no rate limit."))
	reg.RegisterEvent(reactionDescriptor("reaction_removed", "Slack: Reaction removed",
		"Fires when someone removes an emoji reaction from a message the bot can see."))
}

func reactionDescriptor(event, name, desc string) integration.EventDescriptor {
	return integration.EventDescriptor{
		Channel:     Channel,
		Event:       event,
		Name:        name,
		Description: desc,
		PayloadType: ReactionEvent{},
		MatchSchema: entity.StructToConfigs(SlackReactionMatch{}),
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.reaction":    "Emoji name without colons (white_check_mark). Skin tones come as name::skin-tone-2.",
				"payload.user":        "Slack user ID of the person who reacted.",
				"payload.channel_id":  "Channel of the reacted message.",
				"payload.ts":          "ts of the reacted message. For its thread, call get_thread_replies with thread_ts = this ts: a reply's ts returns the whole thread.",
				"payload.item_user":   "Who wrote the reacted message (a user or a bot user id). Filter on it to react only to a bot's answers.",
				"payload.added":       "true on slack.reaction, false on slack.reaction_removed.",
				"payload.is_dm":       "True when channel_id starts with D.",
				"payload.text":        "Text of the reacted message (read once from Slack, cached).",
				"payload.thread":      "Thread root ts of the reacted message (== payload.ts for a top-level post). Pass to reply_thread to answer in that thread.",
				"payload.is_reply":    "True when the reacted message is a reply inside a thread.",
				"payload.item_bot_id": "Bot id (B…) when a bot wrote the reacted message.",
				"payload.bot_user_id": "Slack user ID of the wick bot that RECEIVED this event.",
			},
			Quirks: []string{
				"Every bot in the channel receives the same reaction; deliveries are collapsed per (channel, ts, emoji, reactor), so a workflow fires once.",
				"Reactions by the receiving bot itself are skipped, so a workflow that adds a reaction cannot loop.",
				"The Slack app needs the reaction_added / reaction_removed events and reactions:read (the shipped manifest has them).",
				"Filter activation needs BOTH MatchEnabled:true AND a non-empty Match map.",
				"text / thread / is_reply are read from Slack only while an enabled workflow listens to this event, once per message (cached 10 minutes); if Slack refuses the read they are empty.",
			},
			PairWith: []string{
				"channel:slack.reply_thread",
				"channel:slack.send_to_session",
				"channel:slack.add_reaction",
			},
			CommonPitfalls: []string{
				"Don't poll reactions with reactions.get: subscribe here instead.",
				"payload.ts is the reacted message, not always the thread root: read the thread with get_thread_replies on it.",
			},
			InputSample:  `{"mode":"whitelist","reaction":"white_check_mark","channel_id":[{"id":"C12345","name":"#support"}]}`,
			OutputSample: `{"type":"channel","channel":"slack","event":"` + event + `","at":"2026-05-19T10:32:17Z","payload":{"user":"U02ABCDEF","reaction":"white_check_mark","channel_id":"C12345","ts":"1700001234.005600","item_user":"U0BOT1234","added":true,"is_dm":false}}`,
			Examples: []wickdocs.Example{{
				Name: "verified_answer",
				Body: `- type: channel
  channel: slack
  event: reaction
  entry_node: verify
  match_enabled: true
  match:
    mode: whitelist
    reaction: white_check_mark
    item_user:
      - { id: U0BOT1234, name: "@helper" }`,
			}},
		},
	}
}

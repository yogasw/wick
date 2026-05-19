package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// AddReactionInput is the schema for slack.add_reaction. Emoji is the
// shortname without colons (e.g. "thumbsup", "white_check_mark").
type AddReactionInput struct {
	Channel string `json:"channel" wick:"required;desc=Channel ID containing the message"`
	TS      string `json:"ts"      wick:"required;desc=Message timestamp (ts from send_message output)"`
	Emoji   string `json:"emoji"   wick:"required;desc=Emoji shortname without colons (e.g. thumbsup)"`
}

func registerActionAddReaction(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "add_reaction",
		Name:        "Slack: Add reaction",
		Description: "Add an emoji reaction to a message. Idempotent — Slack returns already_reacted on duplicates which we treat as success.",
		InputType:   AddReactionInput{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"ok":      "True on success or when the reaction already existed.",
				"already": "True when Slack returned already_reacted — wick treats it as success.",
			},
			TemplateableFields: []string{"channel", "ts", "emoji"},
			Quirks: []string{
				"emoji is the shortname WITHOUT colons — \"thumbsup\", not \":thumbsup:\".",
				"Idempotent: re-adding the same reaction returns already=true rather than erroring.",
			},
			PairWith: []string{"channel:slack.remove_reaction", "channel:slack.send_message"},
			InputSample:  `{"channel":"C12345","ts":"1700001234.005600","emoji":"white_check_mark"}`,
			OutputSample: `{"ok":true}`,
			Examples: []wickdocs.Example{
				{
					Name: "ack_received",
					YAML: `- id: ack
  type: channel
  channel: slack
  action: add_reaction
  arg_modes:
    channel: expression
    ts: expression
  args:
    channel: '{{.Node.trigger.payload.channel_id}}'
    ts: '{{.Node.trigger.payload.ts}}'
    emoji: eyes`,
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			api := ch.API()
			if api == nil {
				return nil, fmt.Errorf("slack channel not configured")
			}
			channelID, err := argString(args, "channel")
			if err != nil {
				return nil, err
			}
			ts, err := argString(args, "ts")
			if err != nil {
				return nil, err
			}
			emoji, err := argString(args, "emoji")
			if err != nil {
				return nil, err
			}
			if err := api.AddReactionContext(ctx, emoji, slackgo.ItemRef{Channel: channelID, Timestamp: ts}); err != nil {
				if err.Error() == "already_reacted" {
					return map[string]any{"ok": true, "already": true}, nil
				}
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		},
	})
}

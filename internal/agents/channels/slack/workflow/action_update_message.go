package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// UpdateMessageInput is the schema for slack.update_message. The ts
// must reference a message posted by the same bot — Slack rejects
// edits to other users' messages.
type UpdateMessageInput struct {
	Channel string `json:"channel"          wick:"required;desc=Channel ID of the original message"`
	TS      string `json:"ts"               wick:"required;desc=Timestamp of the message to edit (ts from send_message)"`
	Text    string `json:"text"             wick:"desc=New fallback text"`
	Blocks  string `json:"blocks,omitempty" wick:"textarea;desc=New Block Kit JSON array (overrides text)"`
}

// UpdateMessageOutput echoes the channel + ts the edit landed on.
type UpdateMessageOutput struct {
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

func registerActionUpdateMessage(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "update_message",
		Name:        "Slack: Update message",
		Description: "Edit a message the bot previously posted. ts is the original message timestamp returned by send_message.",
		InputType:   UpdateMessageInput{},
		OutputType:  UpdateMessageOutput{},
		Destructive: true,
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"channel": "Channel ID the edit landed on (echo of input).",
				"ts":      "Edited message ts (matches input ts).",
			},
			TemplateableFields: []string{"channel", "ts", "text", "blocks"},
			Quirks: []string{
				"Can only edit messages the bot itself posted. Slack rejects edits to other users' messages.",
				"Doesn't work on ephemerals — use respond_url with replace_original instead.",
				"blocks REPLACES the entire block set; pass the full new state, not a diff.",
			},
			PairWith: []string{"channel:slack.send_message", "channel:slack.delete_message"},
			InputSample:  `{"channel":"C12345","ts":"1700001234.005600","text":"Ticket resolved. Thanks!"}`,
			OutputSample: `{"channel":"C12345","ts":"1700001234.005600"}`,
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
			text := argStringOpt(args, "text")
			blocksRaw := argStringOpt(args, "blocks")
			if text == "" && blocksRaw == "" {
				return nil, fmt.Errorf("either text or blocks is required")
			}
			opts := []slackgo.MsgOption{}
			if text != "" {
				opts = append(opts, slackgo.MsgOptionText(text, false))
			}
			if blocksRaw != "" {
				var blocks []slackgo.Block
				if err := decodeBlocks(blocksRaw, &blocks); err != nil {
					return nil, fmt.Errorf("blocks: %w", err)
				}
				opts = append(opts, slackgo.MsgOptionBlocks(blocks...))
			}
			editedChan, editedTS, _, err := api.UpdateMessageContext(ctx, channelID, ts, opts...)
			if err != nil {
				return nil, err
			}
			return UpdateMessageOutput{Channel: editedChan, TS: editedTS}, nil
		},
	})
}

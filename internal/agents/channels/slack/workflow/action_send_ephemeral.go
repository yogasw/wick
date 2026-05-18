package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// SendEphemeralInput is the schema for slack.send_ephemeral. The
// message is visible only to the target user inside the channel.
type SendEphemeralInput struct {
	Channel string `json:"channel"          wick:"required;desc=Channel ID"`
	User    string `json:"user"             wick:"required;desc=Target user ID (message visible only to them)"`
	Text    string `json:"text"             wick:"desc=Message text (fallback / accessibility)"`
	Blocks  string `json:"blocks,omitempty" wick:"textarea;desc=Block Kit JSON array (overrides text)"`
}

// SendEphemeralOutput is the response — Slack returns only the
// message ts for ephemerals.
type SendEphemeralOutput struct {
	TS string `json:"ts"`
}

func registerActionSendEphemeral(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "send_ephemeral",
		Name:        "Slack: Send ephemeral",
		Description: "Post a message visible only to one user inside a channel. Useful for replying to slash commands or block actions without spamming the channel.",
		InputType:   SendEphemeralInput{},
		OutputType:  SendEphemeralOutput{},
		Destructive: true,
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			api := ch.API()
			if api == nil {
				return nil, fmt.Errorf("slack channel not configured")
			}
			channelID, err := argString(args, "channel")
			if err != nil {
				return nil, err
			}
			userID, err := argString(args, "user")
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
			ts, err := api.PostEphemeralContext(ctx, channelID, userID, opts...)
			if err != nil {
				return nil, err
			}
			return SendEphemeralOutput{TS: ts}, nil
		},
	})
}

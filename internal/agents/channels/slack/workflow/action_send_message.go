package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/appname"
)

// SendMessageInput is the schema for the slack.send_message action.
// Blocks travels as a JSON string so the operator can paste output
// from Slack's Block Kit Builder directly into the YAML / args field.
type SendMessageInput struct {
	Channel  string `json:"channel"`             // required — C…, D…, or @user
	Text     string `json:"text"`                // fallback / accessibility text
	Blocks   string `json:"blocks,omitempty"`    // JSON array of Block Kit blocks
	ThreadTS string `json:"thread_ts,omitempty"` // post inside a thread
	Signed   bool   `json:"signed,omitempty"`    // append "Sent by wick · <appname>" footer
}

// SendMessageOutput is the typed response a downstream node can
// reference via `{{.Node.<id>.ts}}`.
type SendMessageOutput struct {
	TS      string `json:"ts"`
	Channel string `json:"channel"`
}

func registerActionSendMessage(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "send_message",
		Name:        "Slack: Send message",
		Description: "Post a message to a channel, DM, or thread. blocks is a JSON string (Block Kit). Returns the posted ts.",
		InputType:   SendMessageInput{},
		OutputType:  SendMessageOutput{},
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
			text := argStringOpt(args, "text")
			blocksRaw := argStringOpt(args, "blocks")
			threadTS := argStringOpt(args, "thread_ts")
			signed, _ := args["signed"].(bool)
			if text == "" && blocksRaw == "" {
				return nil, fmt.Errorf("either text or blocks is required")
			}

			if signed && text != "" {
				text += "\n\n_Sent by *wick* · " + appname.Resolve() + "_"
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
			if threadTS != "" {
				opts = append(opts, slackgo.MsgOptionTS(threadTS))
			}

			postedChan, postedTS, err := api.PostMessageContext(ctx, channelID, opts...)
			if err != nil {
				return nil, err
			}
			return SendMessageOutput{TS: postedTS, Channel: postedChan}, nil
		},
	})
}

// decodeBlocks accepts a JSON string that is either a bare blocks
// array `[{...}]` or an object with a `blocks` key. Slack's Block Kit
// Builder copies the object form; operators typing inline tend to use
// the bare array — accept both.
func decodeBlocks(raw string, out *[]slackgo.Block) error {
	if err := json.Unmarshal([]byte(raw), out); err == nil {
		return nil
	}
	var wrapper struct {
		Blocks []slackgo.Block `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return err
	}
	*out = wrapper.Blocks
	return nil
}

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// SendMessageInput is the schema for the slack.send_message action.
// Blocks travels as a JSON string so the operator can paste output
// from Slack's Block Kit Builder directly into the YAML / args field.
type SendMessageInput struct {
	Channel  string `json:"channel"    wick:"required;desc=Channel ID, DM, or @user"`
	Text     string `json:"text"       wick:"desc=Message text (fallback / accessibility)"`
	Blocks   string `json:"blocks"     wick:"textarea;desc=Block Kit JSON array (overrides text)"`
	ThreadTS string `json:"thread_ts"  wick:"key=thread_ts;desc=Post inside this thread (message ts)"`
	Signed   bool   `json:"signed"     wick:"desc=Append 'Sent by wick' footer"`
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
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"ts":      "Posted message timestamp / Slack ID. Pass to update_message, delete_message, add_reaction, or thread_ts on follow-ups.",
				"channel": "Resolved channel ID (C…/D…/G…). Use this for any subsequent ops referring to the same message.",
			},
			TemplateableFields: []string{"channel", "text", "blocks", "thread_ts"},
			Quirks: []string{
				"channel accepts channel ID (C…), DM ID (D…), user ID (U…) for an auto-opened DM, or #channel-name. #name requires the bot to already be a member.",
				"When blocks is set, Slack still requires non-empty text for notifications / fallbacks — always set both.",
				"thread_ts must be the PARENT message ts (root of the thread), not a reply ts.",
				"signed:true appends a small \"Sent by wick · <app>\" footer to text. Doesn't apply to blocks-only messages.",
				"blocks accepts either a bare array `[{...}]` or Block Kit Builder's `{\"blocks\":[...]}` wrapper — wick normalises both.",
			},
			PairWith: []string{
				"channel:slack.update_message",
				"channel:slack.add_reaction",
				"channel:slack.send_ephemeral",
				"connector:slack.send_message",
			},
			CommonPitfalls: []string{
				"Don't put { } around JSON template — {{jsonEscape}} text values inside blocks to avoid unescaped quotes breaking the JSON.",
				"Don't reuse a stored ts across days for thread_ts on high-volume channels — Slack purges old thread state.",
			},
			InputSample:  `{"channel":"#alerts","text":"New ticket from U12345: payment refund","thread_ts":"1700001000.000100"}`,
			OutputSample: `{"ts":"1700001234.005600","channel":"C12345"}`,
			Examples: []wickdocs.Example{
				{
					Name: "reply_in_thread",
					YAML: `- id: reply
  type: channel
  channel: slack
  action: send_message
  arg_modes:
    channel: expression
    text: expression
    thread_ts: expression
  args:
    channel: '{{.Node.trigger.payload.channel_id}}'
    thread_ts: '{{.Node.trigger.payload.thread}}'
    text: "Got it — looking into this now."`,
				},
				{
					Name: "block_kit_post",
					YAML: `- id: rich_post
  type: channel
  channel: slack
  action: send_message
  arg_modes:
    blocks: expression
  args:
    channel: "#release"
    text: "Release v1.2.0"
    blocks: '{{toJSON .Node.build_view.blocks}}'`,
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
//
// slack.Block is an interface, so json.Unmarshal into *[]Block fails.
// Use slack.Blocks (which has a custom UnmarshalJSON via block_conv.go)
// wrapping the raw JSON in {"blocks":[...]} when it's a bare array.
func decodeBlocks(raw string, out *[]slackgo.Block) error {
	s := strings.TrimSpace(raw)
	// Normalize: if the input is an object with a "blocks" key (Block Kit
	// Builder export format), extract the array value first.
	if strings.HasPrefix(s, "{") {
		var wrapper struct {
			Blocks json.RawMessage `json:"blocks"`
		}
		if err := json.Unmarshal([]byte(s), &wrapper); err != nil {
			return err
		}
		s = string(wrapper.Blocks)
	}
	// slack.Blocks.UnmarshalJSON expects a bare JSON array [...].
	var b slackgo.Blocks
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		return err
	}
	*out = b.BlockSet
	return nil
}

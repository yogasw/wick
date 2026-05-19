package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// RespondURLInput posts to a Slack response_url returned with a block
// action, slash command, or view submission. It's the only way to
// edit / replace the original ephemeral or in-channel message a slash
// command produced — chat.update doesn't work on ephemerals.
type RespondURLInput struct {
	ResponseURL     string `json:"response_url"               wick:"required;key=response_url;desc=response_url from Slack interaction payload"`
	Text            string `json:"text,omitempty"             wick:"desc=Fallback text"`
	Blocks          string `json:"blocks,omitempty"           wick:"textarea;desc=Block Kit JSON array (overrides text)"`
	ResponseType    string `json:"response_type,omitempty"    wick:"key=response_type;desc=ephemeral or in_channel"`
	ReplaceOriginal bool   `json:"replace_original,omitempty" wick:"key=replace_original;desc=Replace the original message"`
	DeleteOriginal  bool   `json:"delete_original,omitempty"  wick:"key=delete_original;desc=Delete the original message"`
}

type respondURLBody struct {
	Text            string          `json:"text,omitempty"`
	Blocks          json.RawMessage `json:"blocks,omitempty"`
	ResponseType    string          `json:"response_type,omitempty"`
	ReplaceOriginal bool            `json:"replace_original,omitempty"`
	DeleteOriginal  bool            `json:"delete_original,omitempty"`
}

// respondURLClient is overridable for tests; production uses
// http.DefaultClient with a short timeout matching Slack's 3-second
// budget for follow-up posts.
var respondURLClient = &http.Client{Timeout: 5 * time.Second}

func registerActionRespondURL(reg *integration.Registry, _ *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "respond_url",
		Name:        "Slack: Respond via response_url",
		Description: "POST a reply to the response_url Slack issues with each interaction. Required for editing slash-command ephemerals and for delayed responses (up to 30 mins / 5 calls).",
		InputType:   RespondURLInput{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"ok":     "True when Slack accepted the POST.",
				"status": "HTTP status code from Slack's response_url endpoint.",
			},
			TemplateableFields: []string{"response_url", "text", "blocks", "response_type"},
			Quirks: []string{
				"response_url is valid for 30 minutes and at most 5 posts per interaction. After that wick returns the upstream error.",
				"response_type \"ephemeral\" replies privately to the invoking user; \"in_channel\" posts visible to the channel. Default is whatever Slack used for the original message.",
				"replace_original / delete_original work only when the original message was posted by Slack as a result of the interaction (slash command response, action ack). Cannot edit messages posted via chat.postMessage.",
				"Either text, blocks, or delete_original is required. Wick rejects empty payloads before sending.",
			},
			PairWith: []string{
				"channel:slack.block_action",
				"channel:slack.command",
				"channel:slack.send_ephemeral",
			},
			CommonPitfalls: []string{
				"Don't try to chat.update an ephemeral message — respond_url is the only way to edit/replace it.",
				"Don't store response_url across runs — it expires fast.",
			},
			InputSample:  `{"response_url":"https://hooks.slack.com/actions/T123/...","text":"Working on it…","response_type":"ephemeral","replace_original":true}`,
			OutputSample: `{"ok":true,"status":200}`,
		},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			respURL, err := argString(args, "response_url")
			if err != nil {
				return nil, err
			}
			body := respondURLBody{
				Text:            argStringOpt(args, "text"),
				ResponseType:    argStringOpt(args, "response_type"),
				ReplaceOriginal: argBool(args, "replace_original", false),
				DeleteOriginal:  argBool(args, "delete_original", false),
			}
			if raw := argStringOpt(args, "blocks"); raw != "" {
				// blocks is a JSON string in args — relay verbatim (Slack
				// expects a JSON array, the operator pastes one).
				body.Blocks = json.RawMessage(raw)
			}
			if body.Text == "" && len(body.Blocks) == 0 && !body.DeleteOriginal {
				return nil, fmt.Errorf("either text, blocks, or delete_original is required")
			}
			payload, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, respURL, bytes.NewReader(payload))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
			resp, err := respondURLClient.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			if resp.StatusCode >= 400 {
				return nil, fmt.Errorf("response_url returned %d: %s", resp.StatusCode, string(respBody))
			}
			return map[string]any{"ok": true, "status": resp.StatusCode}, nil
		},
	})
}

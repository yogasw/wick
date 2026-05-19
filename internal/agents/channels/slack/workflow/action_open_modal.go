package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// OpenModalInput opens a Slack modal in response to a block_action,
// shortcut, slash command, or any other event carrying a trigger_id.
// Slack's trigger_id expires 3 seconds after the originating event,
// so this action MUST fire on a short path (no LLM call upstream).
//
// View is a JSON string — paste straight from the Block Kit Builder
// modal export. Required at the top level: `type: "modal"`, `title`,
// `blocks`.
type OpenModalInput struct {
	TriggerID string `json:"trigger_id" wick:"required;key=trigger_id;desc=Trigger ID from block_action payload (expires in 3s)"`
	View      string `json:"view"       wick:"required;textarea;desc=Block Kit modal JSON (type:modal, title, blocks)"`
}

// OpenModalOutput exposes the opened view's id + hash so a subsequent
// update_modal node can target the right view.
type OpenModalOutput struct {
	ViewID   string `json:"view_id"`
	ViewHash string `json:"view_hash"`
}

func registerActionOpenModal(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "open_modal",
		Name:        "Slack: Open modal",
		Description: "Open a modal in response to a block_action / shortcut / command. trigger_id expires in 3 seconds — keep this node close to the triggering event, no LLM calls between them.",
		InputType:   OpenModalInput{},
		OutputType:  OpenModalOutput{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"view_id":   "Slack-assigned view ID. Pass to a subsequent update_modal or push_modal node to mutate this exact view.",
				"view_hash": "Optimistic-concurrency hash. Slack rejects the update if the hash is stale — always pull the latest from this output, never cache across runs.",
			},
			TemplateableFields: []string{"trigger_id", "view"},
			Quirks: []string{
				"Slack's trigger_id expires 3 seconds after the originating event (block_action / shortcut / command). Any node between the trigger and this action that takes >2s (most notably an agent / classify node) will burn the trigger and this call will fail with expired_trigger_id.",
				"view is a JSON string, not a YAML map. Paste straight from Block Kit Builder's modal export — the action wraps it in a slackgo.ModalViewRequest.",
				"Skeleton-then-update pattern: open a placeholder modal first (so the trigger_id is spent in time), then run the slow work, then update_modal with the real content. See the skeleton_then_update example.",
				"trigger_id MUST come from the originating Slack event payload (e.g. {{.Node.<trigger>.payload.trigger_id}}). Generating one manually is impossible — Slack signs them.",
				"Block Kit imposes a 100-block hard limit per view, and each input block requires a unique block_id. Validate before sending if the modal is template-driven.",
			},
			PairWith: []string{
				"channel:slack.update_modal",
				"channel:slack.push_modal",
				"channel:slack.block_action",
				"channel:slack.shortcut",
			},
			CommonPitfalls: []string{
				"Don't call an agent node between the trigger and this action — the trigger_id will expire. Pattern: trigger → open_modal (skeleton) → agent → update_modal.",
				"Don't reuse view_id across runs. Each open_modal call creates a fresh view; the id is only valid for the modal it just opened.",
				"Don't construct view as a YAML map — the input is typed as a JSON string. Use the toJSON template helper if you're building the structure dynamically.",
			},
			InputSample:  `{"trigger_id":"123.4567.8abc","view":"{\"type\":\"modal\",\"callback_id\":\"report_modal\",\"title\":{\"type\":\"plain_text\",\"text\":\"Working…\"},\"blocks\":[{\"type\":\"section\",\"text\":{\"type\":\"mrkdwn\",\"text\":\"_Loading report…_\"}}]}"}`,
			OutputSample: `{"view_id":"V0123ABCDE","view_hash":"1700001234.abcdef00"}`,
			Examples: []wickdocs.Example{
				{
					Name: "skeleton_then_update",
					YAML: `- id: open_modal_skeleton
  type: channel
  channel: slack
  action: open_modal
  arg_modes:
    trigger_id: expression
    view: fixed
  args:
    trigger_id: '{{.Node.action.payload.trigger_id}}'
    view: |
      {
        "type": "modal",
        "callback_id": "report_modal",
        "title": {"type":"plain_text","text":"Working…"},
        "blocks": [{"type":"section","text":{"type":"mrkdwn","text":"_Loading report…_"}}]
      }`,
				},
				{
					Name: "dynamic_view_from_agent",
					YAML: `- id: open_modal
  type: channel
  channel: slack
  action: open_modal
  arg_modes:
    trigger_id: expression
    view: expression
  args:
    trigger_id: '{{.Node.action.payload.trigger_id}}'
    view: '{{toJSON .Node.build_view.modal}}'`,
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			api := ch.API()
			if api == nil {
				return nil, fmt.Errorf("slack channel not configured")
			}
			triggerID, err := argString(args, "trigger_id")
			if err != nil {
				return nil, err
			}
			var view slackgo.ModalViewRequest
			if err := argJSON(args, "view", &view); err != nil {
				return nil, err
			}
			resp, err := api.OpenViewContext(ctx, triggerID, view)
			if err != nil {
				return nil, err
			}
			return OpenModalOutput{ViewID: resp.View.ID, ViewHash: resp.View.Hash}, nil
		},
	})
}

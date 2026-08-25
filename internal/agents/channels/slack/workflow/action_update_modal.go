package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// UpdateModalInput replaces the contents of an existing modal. Pass
// view_id (returned by open_modal) and a fresh view JSON. Hash is
// optional — Slack uses it for optimistic-concurrency, prevents racing
// updates.
type UpdateModalInput struct {
	ViewID string `json:"view_id"        wick:"required;key=view_id;desc=View ID from open_modal output"`
	Hash   string `json:"hash,omitempty" wick:"desc=Concurrency token (view_hash from open_modal, optional)"`
	View   string `json:"view"           wick:"required;textarea;desc=New Block Kit modal JSON (type: modal)"`
}

type UpdateModalOutput struct {
	ViewID   string `json:"view_id"`
	ViewHash string `json:"view_hash"`
}

func registerActionUpdateModal(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "update_modal",
		Name:        "Slack: Update modal",
		Description: "Replace the contents of an open modal. Pair with open_modal — pass the view_id from its output.",
		InputType:   UpdateModalInput{},
		OutputType:  UpdateModalOutput{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"view_id":   "Updated view ID (matches input view_id).",
				"view_hash": "Fresh hash after the update. Pass back on subsequent updates for safe optimistic concurrency.",
			},
			TemplateableFields: []string{"view_id", "hash", "view"},
			Quirks: []string{
				"No trigger_id needed — operates on view_id from a previous open_modal/push_modal.",
				"hash is optional but enables optimistic concurrency. Without it, racing updates silently overwrite each other.",
				"Top-level view JSON MUST have type: \"modal\".",
			},
			PairWith: []string{"channel:slack.open_modal", "channel:slack.push_modal"},
			CommonPitfalls: []string{
				"Don't try to update a view_id from view_submission — by the time the submit fires the view is closing. Use response_action: update on the submission ack instead (requires the view_submission to be sync-responded; wick's async model makes this tricky).",
			},
			InputSample:  `{"view_id":"V0123ABCDE","hash":"1700001234.abc","view":"{\"type\":\"modal\",\"callback_id\":\"report_modal\",\"title\":{\"type\":\"plain_text\",\"text\":\"Done\"},\"blocks\":[{\"type\":\"section\",\"text\":{\"type\":\"mrkdwn\",\"text\":\"Report ready.\"}}]}"}`,
			OutputSample: `{"view_id":"V0123ABCDE","view_hash":"1700001245.xyz"}`,
		},
		Execute: func(ctx context.Context, args map[string]any) (any, error) {
			api := ch.API()
			if api == nil {
				return nil, fmt.Errorf("slack channel not configured")
			}
			viewID, err := argString(args, "view_id")
			if err != nil {
				return nil, err
			}
			hash := argStringOpt(args, "hash")
			var view slackgo.ModalViewRequest
			if err := argJSON(args, "view", &view); err != nil {
				return nil, err
			}
			resp, err := api.UpdateViewContext(ctx, view, "", hash, viewID)
			if err != nil {
				return nil, err
			}
			return UpdateModalOutput{ViewID: resp.View.ID, ViewHash: resp.View.Hash}, nil
		},
	})
}

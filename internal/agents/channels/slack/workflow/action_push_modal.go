package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// PushModalInput layers a new modal on top of the current one — the
// user can dismiss it to return to the parent. Same 3-second
// trigger_id rule as open_modal.
type PushModalInput struct {
	TriggerID string `json:"trigger_id" wick:"required;key=trigger_id;desc=Trigger ID from block_action payload (expires in 3s)"`
	View      string `json:"view"       wick:"required;textarea;desc=Block Kit modal JSON (type: modal)"`
}

type PushModalOutput struct {
	ViewID   string `json:"view_id"`
	ViewHash string `json:"view_hash"`
}

func registerActionPushModal(reg *integration.Registry, ch *slack.Channel) {
	reg.RegisterAction(integration.ActionDescriptor{
		Channel:     Channel,
		Action:      "push_modal",
		Name:        "Slack: Push modal",
		Description: "Stack a new modal on top of the current one. trigger_id expires in 3s — keep this node on a short path from the originating event.",
		InputType:   PushModalInput{},
		OutputType:  PushModalOutput{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"view_id":   "Pushed modal view ID. Use for update_modal targeting the pushed layer.",
				"view_hash": "Concurrency hash.",
			},
			TemplateableFields: []string{"trigger_id", "view"},
			Quirks: []string{
				"Same 3-second trigger_id rule as open_modal — keep this on a short path.",
				"User can dismiss the pushed modal to return to the parent; on submit, view_submission fires only for the topmost view.",
			},
			PairWith: []string{"channel:slack.open_modal", "channel:slack.update_modal"},
			CommonPitfalls: []string{
				"Don't push more than ~3 levels — Slack lets you, but users get lost.",
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
			resp, err := api.PushViewContext(ctx, triggerID, view)
			if err != nil {
				return nil, err
			}
			return PushModalOutput{ViewID: resp.View.ID, ViewHash: resp.View.Hash}, nil
		},
	})
}

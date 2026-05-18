package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
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

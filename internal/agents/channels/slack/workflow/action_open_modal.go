package workflow

import (
	"context"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
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

package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
)

// SlackViewSubmissionMatch is the trigger filter form for slack.view_submission events.
// Router matches payload.callback_id against the whitelist.
//
// Use callback_id whitelist to route different modal forms to different workflows.
type SlackViewSubmissionMatch struct {
	Mode             string `wick:"dropdown=all|whitelist;default=all;desc=Filter mode: all=fire every submission; whitelist=only listed callback IDs"`
	AllowedCallbacks string `wick:"desc=Comma-separated callback_id values (exact match). E.g. create_ticket_modal,update_ticket_modal;key=callback_id;visible_when=mode:whitelist"`
}

// ViewSubmissionEvent fires when a user submits a modal. CallbackID is
// the modal identifier (set when opening). Values is the flattened
// state.values shape Slack returns — Block ID → Action ID → typed
// value — so templates can do `{{.Trigger.values.subject.input.value}}`.
type ViewSubmissionEvent struct {
	User       string         `json:"user"`
	CallbackID string         `json:"callback_id"`
	ViewID     string         `json:"view_id"`
	ViewHash   string         `json:"view_hash"`
	PrivateMD  string         `json:"private_metadata"`
	TriggerID  string         `json:"trigger_id"`
	Values     map[string]any `json:"values"` // state.values: block_id → action_id → value
	Raw        map[string]any `json:"raw"`    // full view payload
}

func registerEventViewSubmission(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "view_submission",
		Name:        "Slack: Modal submitted",
		Description: "Fires when a user clicks Submit on a modal. Match by callback_id to route different modal forms.",
		PayloadType: ViewSubmissionEvent{},
		MatchSchema: entity.StructToConfigs(SlackViewSubmissionMatch{}),
	})
}

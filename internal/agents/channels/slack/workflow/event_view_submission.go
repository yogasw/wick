package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
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
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.callback_id":      "Identifier set when opening the modal. Primary routing key.",
				"payload.values":           "Flattened state.values: block_id → action_id → typed value object. Use {{.Node.<trigger>.payload.values.<block>.<action>.value}}.",
				"payload.private_metadata": "Free-form string set when opening the modal. Useful to carry IDs without polluting the visible UI.",
				"payload.view_id":          "Resolved view ID — pass to update_modal for in-place success / error rendering.",
				"payload.trigger_id":       "Short-lived (3s) — typically already spent by the original open_modal flow. Treat as expired.",
				"payload.user":             "Submitting user ID.",
			},
			Quirks: []string{
				"trigger_id on view_submission is usually already-expired remnant from the open_modal. Don't re-use it for another open_modal.",
				"To return validation errors without closing the modal, respond synchronously within 3 seconds. Wick's workflow execution model is async — for hard validation use Block Kit input validation instead.",
				"values is keyed by block_id, then action_id. Access via {{index .Node.<trigger>.payload.values \"my_block\" \"my_action\" \"value\"}} for safe key lookup.",
			},
			PairWith: []string{
				"channel:slack.open_modal",
				"channel:slack.update_modal",
				"channel:slack.send_message",
			},
			CommonPitfalls: []string{
				"Don't try to call open_modal from view_submission — the trigger_id is dead. Use update_modal with the view_id instead.",
				"Don't expect .payload.values.<block> to be a string — it's a nested map. Drill down to .value (or .selected_option.value for selects).",
			},
			InputSample:  `{"mode":"whitelist","callback_id":"create_ticket_modal"}`,
			OutputSample: `{"type":"channel","channel":"slack","event":"view_submission","payload":{"user":"U02ABCDEF","callback_id":"create_ticket_modal","view_id":"V0123ABCDE","view_hash":"1700001234.abc","private_metadata":"src_msg=1700001000.001","trigger_id":"123.4567.8abc","values":{"subject_block":{"subject_input":{"type":"plain_text_input","value":"Payment refund issue"}},"prio_block":{"prio_select":{"type":"static_select","selected_option":{"value":"high"}}}}}}`,
			Examples: []wickdocs.Example{
				{
					Name: "route_by_callback",
					YAML: `- type: channel
  channel: slack
  event: view_submission
  entry_node: create_ticket
  match_enabled: true
  match:
    mode: whitelist
    callback_id: create_ticket_modal`,
				},
			},
		},
	})
}

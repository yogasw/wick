package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// ViewClosedEvent fires when a user dismisses a modal without
// submitting (X button or Esc). Use it to clean up state or log
// abandonment.
type ViewClosedEvent struct {
	User       string `json:"user"`
	CallbackID string `json:"callback_id"`
	ViewID     string `json:"view_id"`
	PrivateMD  string `json:"private_metadata"`
}

func registerEventViewClosed(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "view_closed",
		Name:        "Slack: Modal closed",
		Description: "Fires when a user dismisses a modal without submitting (close button or Esc).",
		PayloadType: ViewClosedEvent{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.callback_id":      "Modal identifier set when opening. Use to match against open_modal's callback_id.",
				"payload.view_id":          "Closed view ID — already destroyed, can't update_modal it.",
				"payload.private_metadata": "Free-form string passed when opening. Useful for cleanup correlation.",
				"payload.user":             "User who dismissed the modal.",
			},
			Quirks: []string{
				"Only fires when notify_on_close was true on the original open_modal view payload. Default is false — Slack does not deliver view_closed unless you opt in.",
				"view_id is already gone — don't try to update_modal it. Use this purely for cleanup / abandonment logging.",
			},
			PairWith: []string{
				"channel:slack.open_modal",
			},
			OutputSample: `{"type":"channel","channel":"slack","event":"view_closed","payload":{"user":"U02ABCDEF","callback_id":"create_ticket_modal","view_id":"V0123ABCDE","private_metadata":"src_msg=1700001000.001"}}`,
			Examples: []wickdocs.Example{
				{
					Name: "log_abandonment",
					YAML: `- type: channel
  channel: slack
  event: view_closed
  entry_node: log_dropped_form
  match_enabled: true
  match:
    callback_id: create_ticket_modal`,
				},
			},
		},
	})
}

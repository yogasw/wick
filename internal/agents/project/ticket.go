package project

// TicketField is one custom field in a project's ticket schema. Values
// live per session in session.Meta.Ticket.Fields keyed by Key.
type TicketField struct {
	Key      string   `json:"key"`   // snake_case identifier, unique within the project
	Label    string   `json:"label"` // shown in the card and the edit form
	Type     string   `json:"type"`  // "text" | "select"
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// TicketConfig turns a project's sessions into ticket cards. The zero
// value means the feature is off — every meta.json written before this
// field existed decodes to off, so no migration is needed.
//
// Durations are plain seconds (not time.Duration) so the JSON contract
// with the settings UI stays a bare number. 0 = that automation is off.
type TicketConfig struct {
	Enabled bool          `json:"enabled,omitempty"`
	Fields  []TicketField `json:"fields,omitempty"`
	// FollowupAfterSec: a non-done ticket untouched for this long is
	// stale — the sweeper wakes the session's agent with FollowupPrompt
	// (a system info turn, not a user message) and the agent decides
	// what to do. Repeats every window while the ticket stays stale.
	FollowupAfterSec int64  `json:"followup_after_sec,omitempty"`
	FollowupPrompt   string `json:"followup_prompt,omitempty"`
	// AutoResolveAfterSec: a non-done ticket untouched for this long is
	// closed — status set to done by the sweeper, no agent spawn.
	AutoResolveAfterSec int64 `json:"auto_resolve_after_sec,omitempty"`
}

// DefaultTicketFields returns the schema seeded when a project enables
// ticket mode with no fields configured yet.
func DefaultTicketFields() []TicketField {
	return []TicketField{
		{Key: "type", Label: "Type", Type: "select", Options: []string{"bug", "incident", "task", "question"}},
		{Key: "priority", Label: "Priority", Type: "select", Options: []string{"low", "normal", "high", "urgent"}},
	}
}

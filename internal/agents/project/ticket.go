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

	// AutoCreate decides when a new session gets a ticket of its own with
	// nobody asking for one. Empty = off, which is what every project
	// starts as. Rules are tried in order and the FIRST match wins, so a
	// narrow exception belongs above the broad rule it carves out of.
	AutoCreate []AutoCreateRule `json:"auto_create,omitempty"`
}

// Channel kinds an AutoCreateRule can be narrowed to. A DM is somebody's
// private conversation, so "track everything from Slack except DMs" has to
// be expressible as one rule rather than a list of channels.
const (
	ChannelKindAny     = ""
	ChannelKindDM      = "dm"
	ChannelKindChannel = "channel"
	ChannelKindThread  = "thread"
)

// AutoCreateRule is one condition under which a session is given a ticket.
type AutoCreateRule struct {
	// Origin scopes the rule to where the session came from: "ui",
	// "slack", "telegram", "rest", … or "*" for any.
	Origin string `json:"origin"`
	// ChannelKind narrows a channel-origin rule to dm / channel / thread.
	// Empty means any. Ignored for non-channel origins.
	ChannelKind string `json:"channel_kind,omitempty"`
	// Match is an optional text condition on the session's first message:
	//
	//	""              — origin alone decides
	//	contains:<text> — case-insensitive substring
	//	regex:<expr>    — Go regular expression
	//
	// A regex that does not compile is refused when the config is saved,
	// rather than silently never matching.
	Match string `json:"match,omitempty"`
	// Enabled lets a rule be parked without losing how it was written.
	Enabled bool `json:"enabled"`
	// Title optionally templates the new ticket's title. "{message}" is
	// replaced with the first message (truncated), "{origin}" with the
	// origin. Empty falls back to the message itself.
	Title string `json:"title,omitempty"`
}

// DefaultTicketFields returns the schema seeded when a project enables
// ticket mode with no fields configured yet.
func DefaultTicketFields() []TicketField {
	return []TicketField{
		{Key: "type", Label: "Type", Type: "select", Options: []string{"bug", "incident", "task", "question"}},
		{Key: "priority", Label: "Priority", Type: "select", Options: []string{"low", "normal", "high", "urgent"}},
	}
}

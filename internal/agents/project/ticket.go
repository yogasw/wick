package project

import (
	"slices"
	"strings"
)

// TicketField is one custom field in a project's ticket schema. Values
// live per session in session.Meta.Ticket.Fields keyed by Key.
type TicketField struct {
	Key      string   `json:"key"`   // snake_case identifier, unique within the project
	Label    string   `json:"label"` // shown in the card and the edit form
	Type     string   `json:"type"`  // "text" | "select"
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// TicketStatus is one column on a project's board.
//
// Statuses are per project because what a team calls its stages is theirs
// to name — "triage → coding → review → shipped" is as valid as the
// defaults. Two things are structural rather than cosmetic:
//
//   - Key is what a ticket stores and what the MCP surface accepts. It is
//     stable; Label is what the board draws and can be reworded freely.
//   - Exactly one status must be Terminal. The stale-followup and
//     auto-resolve timers need to know which stage means "finished", or a
//     sweeper would nag tickets that are already done and auto-resolve
//     would have nowhere to move them.
type TicketStatus struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	// Terminal marks the stage that means the work is over. Exactly one
	// status carries it.
	Terminal bool `json:"terminal,omitempty"`
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
	// Statuses are this project's board columns, in order. Empty means the
	// built-in set (DefaultTicketStatuses) — which is what every project
	// configured before statuses were editable decodes to.
	Statuses []TicketStatus `json:"statuses,omitempty"`
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

	// Integrations wires the board to the outside world — outbound webhooks
	// and the token-authed REST surface. Zero value = both off.
	Integrations TicketIntegrations `json:"integrations,omitempty"`
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

// Built-in status keys. A project that never edits its statuses uses
// exactly these, so existing tickets keep working unchanged.
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusWaiting    = "waiting"
	StatusDone       = "done"
)

// DefaultTicketStatuses is the set a project starts with.
func DefaultTicketStatuses() []TicketStatus {
	return []TicketStatus{
		{Key: StatusOpen, Label: "Open"},
		{Key: StatusInProgress, Label: "In Progress"},
		{Key: StatusWaiting, Label: "Waiting"},
		{Key: StatusDone, Label: "Done", Terminal: true},
	}
}

// StatusList returns the project's statuses, falling back to the built-in
// set. Read through this rather than the field: an empty list means "not
// customised", not "no columns", and a board with no columns would be a
// dead end.
func (c TicketConfig) StatusList() []TicketStatus {
	if len(c.Statuses) == 0 {
		return DefaultTicketStatuses()
	}
	return c.Statuses
}

// StatusKeys returns just the keys, in board order.
func (c TicketConfig) StatusKeys() []string {
	list := c.StatusList()
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Key)
	}
	return out
}

// HasStatus reports whether key is one of this project's statuses.
func (c TicketConfig) HasStatus(key string) bool {
	for _, s := range c.StatusList() {
		if s.Key == key {
			return true
		}
	}
	return false
}

// FirstStatus is where a new ticket lands when none was given.
func (c TicketConfig) FirstStatus() string {
	list := c.StatusList()
	if len(list) == 0 {
		return StatusOpen
	}
	return list[0].Key
}

// TerminalStatus is the stage that means the work is finished — what
// auto-resolve moves a ticket to, and what the followup timer treats as
// "leave it alone". Falls back to the last status when nothing is marked,
// which is the least surprising reading of a board's final column.
func (c TicketConfig) TerminalStatus() string {
	list := c.StatusList()
	for _, s := range list {
		if s.Terminal {
			return s.Key
		}
	}
	if len(list) == 0 {
		return StatusDone
	}
	return list[len(list)-1].Key
}

// StatusLabel returns a status's display label, falling back to its key so
// a board never draws a blank column header.
func (c TicketConfig) StatusLabel(key string) string {
	for _, s := range c.StatusList() {
		if s.Key == key {
			if s.Label != "" {
				return s.Label
			}
			return s.Key
		}
	}
	return key
}

// DefaultTicketFields returns the schema seeded when a project enables
// ticket mode with no fields configured yet.
func DefaultTicketFields() []TicketField {
	return []TicketField{
		{Key: "type", Label: "Type", Type: "select", Options: []string{"bug", "incident", "task", "question"}},
		{Key: "priority", Label: "Priority", Type: "select", Options: []string{"low", "normal", "high", "urgent"}},
	}
}

// TicketIntegrations wires a project's board to the outside world: outbound
// webhooks that fire when a ticket changes, and a token-authed REST surface
// so another system can create and move tickets.
//
// The zero value means both are off, so every meta.json written before this
// field existed decodes to "no integrations" and nothing starts talking to
// the network because of an upgrade.
type TicketIntegrations struct {
	// APIEnabled opens the ticket REST endpoints to Personal Access Token
	// auth for this project. Off by default: the board is reachable from
	// the browser either way, and a project should have to opt in before a
	// bearer token can move its work.
	APIEnabled bool `json:"api_enabled,omitempty"`
	// Webhooks are the endpoints notified when this project's tickets
	// change. Order is display order only.
	Webhooks []TicketWebhook `json:"webhooks,omitempty"`
}

// TicketWebhook is one outbound endpoint.
type TicketWebhook struct {
	// ID is stable across edits so a delivery log can be kept per webhook
	// even as its URL or name is reworded.
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
	// Secret keys the HMAC-SHA256 signature sent as X-Wick-Signature. Empty
	// means unsigned — allowed (a receiver on a private network may not
	// need it) but the UI nudges against it.
	//
	// Never rendered back to a client: the API redacts it on read, and a
	// blank value on save means "keep the stored one" rather than "clear
	// it", so editing a webhook's URL cannot silently unsign it.
	Secret string `json:"secret,omitempty"`
	// Events filters which events reach this endpoint. Empty means all of
	// them, which is the least surprising default for a fresh webhook.
	Events []string `json:"events,omitempty"`
	// Headers are extra request headers, for receivers that need a static
	// API key or a routing hint alongside the signature.
	Headers map[string]string `json:"headers,omitempty"`
	// Enabled parks an endpoint without losing how it was configured.
	Enabled bool `json:"enabled"`
}

// WantsEvent reports whether this webhook should receive event name.
func (w TicketWebhook) WantsEvent(name string) bool {
	if !w.Enabled {
		return false
	}
	if len(w.Events) == 0 {
		return true // no filter = everything
	}
	return slices.Contains(w.Events, name)
}

// ActiveWebhooks returns the enabled webhooks that want event name.
func (c TicketConfig) ActiveWebhooks(event string) []TicketWebhook {
	var out []TicketWebhook
	for _, w := range c.Integrations.Webhooks {
		if strings.TrimSpace(w.URL) == "" {
			continue // half-written row in the editor
		}
		if w.WantsEvent(event) {
			out = append(out, w)
		}
	}
	return out
}

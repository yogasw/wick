package entity

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// UserMetadata is the free-form preferences bag stored as JSON on the
// user row. Add fields here when a new per-user preference is needed —
// all consumers should default to the zero value when a field is unset
// so existing rows (NULL metadata) keep working without a backfill.
type UserMetadata struct {
	// HomeView picks the tool grid density: "compact" (icon+name) or
	// "detailed" (wider cards with description). Empty means compact.
	HomeView string `json:"home_view,omitempty"`

	// Theme picks the UI color palette. Values are Theme.ID from
	// internal/pkg/ui/theme.go ("light", "dark", "dracula", …).
	// Empty means "no preference" — guests follow the device
	// `prefers-color-scheme`, logged-in users can pick in the navbar.
	Theme string `json:"theme,omitempty"`

	// LightTheme / DarkTheme remember the last light- and dark-mode
	// theme the user picked from the dropdown, so the navbar toggle
	// can switch straight back to that variant instead of the generic
	// "light"/"dark" defaults. Values are Theme.ID.
	LightTheme string `json:"light_theme,omitempty"`
	DarkTheme  string `json:"dark_theme,omitempty"`

	// PinnedAgentProjectID is the agents Project this user pinned as
	// their personal default. One per user. When set, opening the agents
	// tool lands scoped to this project. Empty = unpinned. See
	// internal/planning/archive/project/design.md.
	PinnedAgentProjectID string `json:"pinned_agent_project_id,omitempty"`

	// PushPermission stores the last browser notification permission
	// state reported by the browser prompt ("granted" or "denied").
	PushPermission   string     `json:"push_permission,omitempty"`
	PushPermissionAt *time.Time `json:"push_permission_at,omitempty"`

	// TicketFilters stores this user's saved ticket-board filter per
	// project, keyed by project ID. The backend treats the value as
	// opaque preference data — validation lives in the board UI.
	TicketFilters map[string]TicketFilter `json:"ticket_filters,omitempty"`

	// Rail is the conversation rail's layout: which side tabs sit in the
	// visible strip, in what order, and how many before the rest fold into
	// "More". The rail has outgrown a fixed strip, so the arrangement is
	// the user's and travels with them.
	Rail RailPrefs `json:"rail,omitempty"`

	// AutoDeleteEmptyTickets answers "delete this ticket now that its last
	// chat moved away?" without asking again. Set by the "Don't ask again"
	// box on that prompt, and resettable in the profile.
	//
	// Only ever governs the EMPTY case, which destroys nothing but the
	// ticket record itself. Deleting a ticket that still holds chats always
	// asks, however this is set: that one takes conversations with it.
	AutoDeleteEmptyTickets string `json:"auto_delete_empty_tickets,omitempty"`
}

// Answers for AutoDeleteEmptyTickets. Empty means "not decided — ask".
const (
	AutoDeleteEmptyAsk    = ""
	AutoDeleteEmptyAlways = "always"
	AutoDeleteEmptyNever  = "never"
)

// RailPrefs is one user's conversation-rail layout.
type RailPrefs struct {
	// Order lists tab ids in the user's chosen sequence. Ids absent here
	// keep their built-in position behind the ones listed, so a tab added
	// by a later release appears rather than vanishing.
	Order []string `json:"order,omitempty"`
	// Visible is how many tabs the strip shows before "More". 0 means the
	// client's default.
	Visible int `json:"visible,omitempty"`
}

// TicketFilter is one saved ticket-board filter: which statuses to show,
// whose tickets ("" = everyone, "me", or a user ID), and the last view
// mode ("list" | "card") the user picked on that project page.
type TicketFilter struct {
	Statuses []string `json:"statuses,omitempty"`
	Assignee string   `json:"assignee,omitempty"`
	ViewMode string   `json:"view_mode,omitempty"`
	// HideUntracked collapses the board's untracked rail. Load-bearing, not
	// cosmetic: with it set the client stops asking for that list, so a
	// project with hundreds of loose chats costs nothing extra to poll.
	HideUntracked bool `json:"hide_untracked,omitempty"`
}

const (
	HomeViewCompact  = "compact"
	HomeViewDetailed = "detailed"
)

// HomeViewOrDefault returns a valid HomeView value, falling back to
// compact when unset or unrecognized.
func (m UserMetadata) HomeViewOrDefault() string {
	if m.HomeView == HomeViewDetailed {
		return HomeViewDetailed
	}
	return HomeViewCompact
}

func (m UserMetadata) Value() (driver.Value, error) {
	return json.Marshal(m)
}

func (m *UserMetadata) Scan(value any) error {
	if value == nil {
		*m = UserMetadata{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("user_metadata: unsupported scan type")
	}
	if len(b) == 0 {
		*m = UserMetadata{}
		return nil
	}
	return json.Unmarshal(b, m)
}

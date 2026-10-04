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

	// UIScale is the interface size: the root font-size in percent, 80 to
	// 100 in steps of 5. 0 means the default (UIScaleDefault). Read it
	// through UIScaleOrDefault.
	UIScale int `json:"ui_scale,omitempty"`

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
	// strip, which are folded behind "More", and in what order. The rail has
	// outgrown a fixed strip, so the arrangement is the user's and travels
	// with them.
	Rail RailPrefs `json:"rail,omitempty"`

	// Sidebar holds the width the user dragged each sidebar to, like the
	// theme: it travels with the account, not the browser. Team and Agents
	// keep their own, since they are different spaces (a wide chat list vs
	// a dense navigation column).
	Sidebar SidebarWidths `json:"sidebar,omitempty"`

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

// SidebarWidths is the dragged width, in CSS px, of each sidebar. 0 means
// "never resized": the page uses its own default.
type SidebarWidths struct {
	Team   int `json:"team,omitempty"`
	Agents int `json:"agents,omitempty"`
}

// Sidebar width bounds. Anything outside is clamped, so a stray value can
// never hide the sidebar or swallow the page.
const (
	SidebarMinWidth = 200
	SidebarMaxWidth = 480
)

// ClampSidebarWidth keeps w inside the bounds; 0 stays 0 (reset to default).
func ClampSidebarWidth(w int) int {
	switch {
	case w <= 0:
		return 0
	case w < SidebarMinWidth:
		return SidebarMinWidth
	case w > SidebarMaxWidth:
		return SidebarMaxWidth
	}
	return w
}

// RailPrefs is one user's conversation-rail layout.
type RailPrefs struct {
	// Order lists tab ids in the user's chosen sequence. Ids absent here
	// keep their built-in position behind the ones listed, so a tab added
	// by a later release appears rather than vanishing.
	Order []string `json:"order,omitempty"`

	// Hidden names the tabs folded behind "More". Everything not listed is
	// in the strip, so a tab added by a later release shows up instead of
	// hiding.
	//
	// nil and empty are DIFFERENT and must stay so on the wire — hence no
	// omitempty. nil is "never arranged", which the client resolves to its
	// own default (fold everything past the first few, so a fresh rail
	// arrives short). An empty LIST is someone having deliberately unfolded
	// every tab; dropping it would re-fold them all on the next load, as
	// though the choice had never been made.
	//
	// This replaced a `visible: N` count, which could not express the
	// choice anyone was actually making: hiding one panel says nothing
	// about how many you want, and a count folded away whichever tabs
	// happened to sit past position N — so reordering silently changed
	// what was hidden. A leftover count is migrated by the client, which
	// reads it as "the first N stay, name the rest as hidden".
	Hidden []string `json:"hidden"`
}

// TicketFilter is one saved ticket-board filter: which statuses to show,
// whose tickets ("" = everyone, "me", or a user ID), and the last view
// mode ("list" | "card") the user picked on that project page.
type TicketFilter struct {
	Statuses []string `json:"statuses,omitempty"`
	Assignee string   `json:"assignee,omitempty"`
	ViewMode string   `json:"view_mode,omitempty"`
	// ShowUntracked adds the board's untracked chats to what is fetched.
	// Load-bearing, not cosmetic: without it the client never asks for that
	// list, so a project with hundreds of loose chats costs nothing extra to
	// poll. Off by default — a board is about tickets, and the pool of loose
	// chats is something one opts into looking at. The zero value being
	// "don't fetch" is why this is spelled Show rather than Hide.
	ShowUntracked bool `json:"show_untracked,omitempty"`
}

const (
	HomeViewCompact  = "compact"
	HomeViewDetailed = "detailed"
)

// Interface size bounds, in percent of the browser's root font-size.
const (
	UIScaleMin     = 80
	UIScaleMax     = 100
	UIScaleStep    = 5
	UIScaleDefault = 90
)

// ValidUIScale returns v when it is a size the slider offers (80..100,
// a multiple of 5), else UIScaleDefault.
func ValidUIScale(v int) int {
	if v < UIScaleMin || v > UIScaleMax || v%UIScaleStep != 0 {
		return UIScaleDefault
	}
	return v
}

// UIScaleOrDefault returns the stored interface size, or the default.
func (m UserMetadata) UIScaleOrDefault() int { return ValidUIScale(m.UIScale) }

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

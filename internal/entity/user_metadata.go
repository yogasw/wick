package entity

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
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

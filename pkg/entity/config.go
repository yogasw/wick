package entity

import (
	"encoding/json"
	"strings"
	"time"
)

// Config is one row in the runtime-editable KV config table (legacy
// name: `app_variables`). Wick reconciles two sources into this table
// at boot:
//   - app-level defaults declared in internal/configs/spec.go
//     (Owner == "", for session_secret / app_url / app_name)
//   - per-module Config structs passed to app.RegisterTool. Framework
//     reflects the typed struct + `wick:"..."` tags into rows via
//     StructToConfigs, with Owner == Meta.Key stamped at boot.
//
// The composite primary key (Owner, Key) lets the same Key repeat
// across scopes without collision (every tool can have its own
// "api_key", for example).
//
// Type drives which widget the admin UI renders:
//
//	text (default), textarea, dropdown, number, checkbox,
//	email, url, color, date, datetime,
//	kvlist (editable table; Options = pipe-separated column names)
//
// Options is pipe-separated (`a|b|c`). For "dropdown" it holds option
// values; for "kvlist" it holds column names (e.g. "id|name"). Value
// for kvlist rows is a JSON array of string-keyed objects.
type Config struct {
	Owner         string `gorm:"primaryKey;type:varchar(64);default:''"`
	Key           string `gorm:"primaryKey;type:varchar(64)"`
	Value         string `gorm:"type:text"`
	Type          string `gorm:"type:varchar(16);default:'text'"`
	Options       string `gorm:"type:text"`
	IsSecret      bool   `gorm:"default:false"`
	CanRegenerate bool   `gorm:"default:false"`
	Locked        bool   `gorm:"default:false"`
	Required      bool   `gorm:"default:false"`
	Description   string `gorm:"type:text"`
	UpdatedAt     time.Time
	// Hidden is set from the `wick:"hidden"` tag at seed time. Not persisted
	// (gorm:"-") — tells the manager Settings page to skip this row. Hidden
	// configs are still seeded to DB so runtime reads work normally.
	//
	// JSON-exposed (omitempty) so the SPA inspector / workflow editor
	// receives the same presentation hint the templ admin page already
	// uses. Pure UI flag — no secret material here.
	Hidden bool `gorm:"-" json:"hidden,omitempty"`
	// Internal is set from the `wick:"internal"` tag. Not persisted. The row
	// is a value wick writes for itself (e.g. a token expiry) — every config
	// renderer skips it, including dedicated pages that do show Hidden rows,
	// so it never gets a widget or a "required" badge. Code still loads and
	// saves it like any other row.
	Internal bool `gorm:"-" json:"internal,omitempty"`
	// SessionLocked is set from the `wick:"session_locked"` tag (or a
	// custom connector field's "Lock in session"). The agent cannot set it
	// on a session-workspace clone (set_config / add values refuse it); a
	// human can, in the session Config tab, which seals the value. Unsealed,
	// the clone runs with the module default. Not persisted. Keeps the agent
	// from overriding a value for itself — e.g. the field an op's
	// VisibleWhen reads, so it cannot switch its own write ops on.
	SessionLocked bool `gorm:"-" json:"session_locked,omitempty"`
	// ColOptions holds per-column select options for kvlist rows. Not persisted.
	// Key = column name; Value = pipe-separated "label::value" pairs (or just
	// "value" when label == value). The kvlist component renders a <select>
	// instead of <input> for columns that have options set.
	ColOptions map[string]string `gorm:"-" json:"col_options,omitempty"`
	// VisibleWhen is a "<otherField>:<value>" predicate sourced from the
	// `wick:"visible_when=..."` tag. When set, the admin UI shows this row
	// only while the named field's current value equals <value>. Not
	// persisted — pure presentation hint.
	VisibleWhen string `gorm:"-" json:"visible_when,omitempty"`
	// Group is an optional section heading for the admin Settings page,
	// sourced from the `wick:"group=..."` tag. Simple (non-kvlist,
	// non-picker) rows that share a Group render together under one card
	// titled with the Group name, in first-seen order; rows with no Group
	// fall into the default "Configuration" card. Pure presentation hint —
	// not persisted, JSON-exposed for the SPA inspector.
	Group string `gorm:"-" json:"group,omitempty"`
	// Mode locks the workflow editor's Fixed ⇄ Expression toggle for this
	// field, sourced from the `wick:"mode=fixed"` / `wick:"mode=expression"`
	// tag. Empty = operator chooses freely (toggle enabled, defaults to
	// fixed). "fixed" or "expression" = toggle greyed out, forced to that
	// mode. Pure presentation hint for the SPA workflow inspector — the
	// admin Settings page (templ) does not render a mode toggle. Not
	// persisted.
	Mode string `gorm:"-" json:"mode,omitempty"`
	// EnvOverride names the environment variable currently overriding
	// this row's value. Set by Service.ListOwned when the operator has
	// exported an env var declared in configs.envOverrides — the UI
	// renders a badge + disables the input, and Set() rejects writes
	// while the override is live. Empty when no override is active.
	EnvOverride string `gorm:"-" json:"env_override,omitempty"`
}

func (Config) TableName() string { return "configs" }

// VisibleWhenMatches evaluates a "<field>:<a|b|c>" visible_when predicate
// against the current values map (keyed by config Key). An empty predicate
// (no visible_when tag) is always visible. The value half is pipe-separated
// and matched as an OR. Mirrors the manager templ applyVisibility and the
// FE isVisible helper so a required-but-hidden field is not counted missing.
//
// Single level only — it does not walk a dependency chain. That is enough
// for the required-completeness check: a field required under condition X is
// only enforced while X holds; the UI handles the cascading hide.
func VisibleWhenMatches(predicate string, values map[string]string) bool {
	if predicate == "" {
		return true
	}
	i := strings.IndexByte(predicate, ':')
	if i < 0 {
		return true
	}
	key := strings.TrimSpace(predicate[:i])
	current := values[key]
	for _, allowed := range strings.Split(predicate[i+1:], "|") {
		if strings.TrimSpace(allowed) == current {
			return true
		}
	}
	return false
}

// DefaultGroupsCollapsed reports the admin setting "config groups start
// collapsed" (config_groups_collapsed). nil means off. Set once at boot.
var DefaultGroupsCollapsed func() bool

// EffectiveGroup is Group with that setting applied: a named group with no
// third segment ("Title" or "Title|Desc") becomes "Title|Desc|collapsed".
// A group that says so explicitly keeps its own choice — "Title|Desc|open"
// stays open, "…|collapsed" stays collapsed — and rows with no group (the
// default "Configuration" card) are never collapsed by the setting.
func (c Config) EffectiveGroup() string {
	g := strings.TrimSpace(c.Group)
	if g == "" || DefaultGroupsCollapsed == nil || !DefaultGroupsCollapsed() {
		return c.Group
	}
	parts := strings.SplitN(g, "|", 3)
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
		return c.Group
	}
	for len(parts) < 2 {
		parts = append(parts, "")
	}
	return parts[0] + "|" + parts[1] + "|collapsed"
}

// MarshalJSON sends the effective group, so the SPA config form follows the
// collapsed-by-default setting without knowing about it.
func (c Config) MarshalJSON() ([]byte, error) {
	type plain Config
	p := plain(c)
	p.Group = c.EffectiveGroup()
	return json.Marshal(p)
}

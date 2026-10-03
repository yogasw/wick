package team

import (
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/tool"
)

// Access tiers sort a connector into one of the Access tab's three lists
// and decide what an agent gets when its checklist says nothing about it.
//
// The tier is read off the connector's code-declared DefaultTags — the
// same tags.Platform / tags.System a built-in connector already carries —
// so there is no second list to keep in sync. A new built-in connector
// tagged Platform or System lands in that list with the tier default the
// moment it registers; a connector with neither tag (integrations, custom
// connectors, plugins, the browser) lands in Connectors, unticked, and is
// reached only through a grant or the include-new toggle.
const (
	// TierConnector is every integration: nothing unless granted.
	TierConnector = ""
	// TierPlatform is wick's own working tools (notes, tickets, source,
	// notifications, schedule, …): write for every agent by default.
	TierPlatform = "platform"
	// TierSystem builds and manages wick (custom connectors, wick
	// manager): write for the Captain by default, nothing for the rest.
	// tags.System is admin-only, so it only ever reaches an admin owner.
	TierSystem = "system"
)

// TierOf maps a connector's DefaultTags to its tier; System wins.
func TierOf(defaults []tool.DefaultTag) string {
	tier := TierConnector
	for _, d := range defaults {
		switch d.Name {
		case tags.System.Name:
			return TierSystem
		case tags.Platform.Name:
			tier = TierPlatform
		}
	}
	return tier
}

// ReachItem is one connector of the owner's catalog as the scope needs it.
type ReachItem struct {
	Key  string
	Tier string
}

// Reach is the owner's catalog keyed by connector id. Grants and tier
// defaults both stop at it: a connector the owner no longer sees grants
// nothing, and a grant kept for it applies again once access returns.
type Reach map[string]ReachItem

// ToolPrefix marks a grant on one of wick's own MCP tools rather than a
// connector instance ("tool:wick_schedule_message").
const ToolPrefix = "tool:"

// PlatformTools are wick's own MCP tools that work on the agent's session
// (schedule, todo, ask, title, …). Each is a Platform-tier entry of its own
// so it can be switched off per agent; it only knows on and off (LevelAll /
// LevelOff). Catalog tools (wick_list, wick_get, wick_execute, …) are not
// here: they are always on and what they return is already scoped.
var PlatformTools = []string{
	"wick_schedule_message", "todo", "ask_user", "wick_set_title",
	"wick_session_info", "wick_context", "wick_usage", "wick_compact",
	"wick_cli_token", "wick_session_workspace",
}

// IsPlatformTool reports whether name is one of PlatformTools.
func IsPlatformTool(name string) bool {
	for _, t := range PlatformTools {
		if t == name {
			return true
		}
	}
	return false
}

// toolGrantID is the grant id of a PlatformTools entry.
func toolGrantID(name string) string { return ToolPrefix + name }

// featureKeys maps the old per-agent feature switches with a server side
// to what replaced them: off grants on these connector keys / tools.
var featureKeys = []struct {
	off   func(Features) bool
	set   func(*Features)
	clear func(*Features)
	key   string // connector key, "" for a tool
	tool  string
}{
	{func(f Features) bool { return !f.Notes }, func(f *Features) { f.Notes = true }, func(f *Features) { f.Notes = false }, "notes", ""},
	{func(f Features) bool { return !f.Tickets }, func(f *Features) { f.Tickets = true }, func(f *Features) { f.Tickets = false }, "tickets", ""},
	{func(f Features) bool { return !f.Source }, func(f *Features) { f.Source = true }, func(f *Features) { f.Source = false }, "source", ""},
	{func(f Features) bool { return !f.Subagents }, func(f *Features) { f.Subagents = true }, func(f *Features) { f.Subagents = false }, "sub-agents", ""},
	{func(f Features) bool { return !f.Schedule }, func(f *Features) { f.Schedule = true }, func(f *Features) { f.Schedule = false }, "", "wick_schedule_message"},
}

// MigrateFeatures turns the old Notes/Tickets/Source/Sub-agents/Schedule
// switches, when off, into LevelOff grants on the connectors (or tool)
// that replaced them, and switches the feature back on. A connector that
// already has a grant keeps it. A feature whose connector reach does not
// list (nil reach, or the owner no longer sees it) stays off as it was, so
// featureGates still applies it. Idempotent: a second run changes nothing.
func MigrateFeatures(f Features, grants []ConnectorGrant, reach Reach) (Features, []ConnectorGrant, bool) {
	has := map[string]bool{}
	for _, g := range grants {
		has[g.ConnectorID] = true
	}
	out := append([]ConnectorGrant(nil), grants...)
	changed := false
	for _, fk := range featureKeys {
		if !fk.off(f) {
			continue
		}
		var ids []string
		if fk.tool != "" {
			ids = []string{toolGrantID(fk.tool)}
		} else {
			for id, it := range reach {
				if it.Key == fk.key {
					ids = append(ids, id)
				}
			}
		}
		if len(ids) == 0 {
			continue
		}
		for _, id := range ids {
			if !has[id] {
				out = append(out, ConnectorGrant{ConnectorID: id, Level: LevelOff})
				has[id] = true
			}
		}
		fk.set(&f)
		changed = true
	}
	return f, out, changed
}

// isToolGrant reports whether id names a PlatformTools entry.
func isToolGrant(id string) bool {
	return strings.HasPrefix(id, ToolPrefix) && IsPlatformTool(strings.TrimPrefix(id, ToolPrefix))
}

// EffectiveFeatures is p's features with the access-backed ones (Notes,
// Tickets, Source, Sub-agents, Schedule) switched off when the agent's
// resolved level on every matching entry is LevelOff, so the chat rail
// hides a tab whose tool the agent cannot use. reach nil leaves the
// stored switches as they are.
func EffectiveFeatures(p entity.AgentPersona, reach Reach) Features {
	f, _, _ := MigrateFeatures(DecodeFeatures(p.Features), DecodeGrants(p.AllowedConnectors), reach)
	if reach == nil || p.Disabled {
		return f
	}
	s := ScopeOf(p, reach)
	off := func(key, tool string) bool {
		if tool != "" {
			return s.Level(toolGrantID(tool)) == LevelOff
		}
		seen := false
		for id, it := range reach {
			if it.Key != key {
				continue
			}
			seen = true
			if s.Level(id) != LevelOff {
				return false
			}
		}
		return seen
	}
	for _, fk := range featureKeys {
		if !fk.off(f) && off(fk.key, fk.tool) {
			fk.clear(&f)
		}
	}
	return f
}

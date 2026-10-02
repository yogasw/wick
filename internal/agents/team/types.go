// Package team is the Agents app's data layer: the agents a user owns,
// the connector access each one is handed, and the resolver the MCP layer
// asks "which agent is this session, and what may it reach?".
//
// An agent is a thin row (entity.AgentPersona) over a project. The text a
// person reads as "the persona" — name, icon, description, system prompt,
// provider — lives in the project's meta.json; this package only adds the
// handle, the Captain flag and the access checklist.
package team

import "encoding/json"

// Grant levels for ConnectorGrant.Level.
const (
	// LevelAll permits every op the owner may run on the connector.
	LevelAll = "all"
	// LevelRead permits only ops that do not declare themselves
	// destructive.
	LevelRead = "read"
	// LevelPick permits exactly the ops listed in ConnectorGrant.Ops.
	LevelPick = "pick"
)

// Run-as modes for entity.AgentPersona.RunAs.
const (
	// RunAsCaller runs a turn with the access of the human who triggered
	// it, falling back to the owner when no human did.
	RunAsCaller = "caller"
	// RunAsOwner runs every turn with the owner's access, whoever
	// triggered it.
	RunAsOwner = "owner"
)

// NormalizeRunAs maps a stored value to a mode. Empty (a row saved before
// the column existed) reads as RunAsCaller; anything unknown also does,
// since caller is the mode that never hands a person more than their own.
func NormalizeRunAs(v string) string {
	if v == RunAsOwner {
		return RunAsOwner
	}
	return RunAsCaller
}

// ConnectorGrant is one connector instance on an agent's checklist.
type ConnectorGrant struct {
	ConnectorID string `json:"connector_id"`
	// Accounts narrows which identities the agent may run the connector
	// as. Empty = every account the owner sees; "" inside the list is the
	// instance's own identity (the bot).
	Accounts []string `json:"accounts"`
	// Level is LevelAll, LevelRead or LevelPick. Anything else reads as
	// LevelRead, so a malformed row fails towards less access.
	Level string `json:"level"`
	// Ops is the allow-list used when Level is LevelPick.
	Ops []string `json:"ops"`
}

// Features switches the conversation rail panels an agent's chat shows.
type Features struct {
	Source    bool `json:"source"`
	Schedule  bool `json:"schedule"`
	Browser   bool `json:"browser"`
	Subagents bool `json:"subagents"`
	Tickets   bool `json:"tickets"`
	Notes     bool `json:"notes"`
	Todos     bool `json:"todos"`
	Files     bool `json:"files"`
	Workspace bool `json:"workspace"`
	Process   bool `json:"process"`
}

// DefaultFeatures is what a new agent starts with: every panel except the
// browser, which is the one most agents never need and the costliest to
// keep open.
func DefaultFeatures() Features {
	return Features{
		Source: true, Schedule: true, Subagents: true, Tickets: true,
		Notes: true, Todos: true, Files: true, Workspace: true, Process: true,
	}
}

// Avatar is the agent's drawn identity in the roster.
type Avatar struct {
	// Shape is one of AvatarShapes.
	Shape string `json:"shape"`
	// Color is a CSS color, normally a #rrggbb hex.
	Color string `json:"color"`
}

// AvatarShapes lists the shapes the avatar component can draw.
var AvatarShapes = []string{"circle", "squircle", "triangle", "diamond"}

// DefaultAvatar is used when a row carries none.
func DefaultAvatar() Avatar { return Avatar{Shape: "circle", Color: "#6366f1"} }

// NormalizeAvatar replaces an unknown shape or an empty color with the
// default, so the UI never receives a value it cannot draw.
func NormalizeAvatar(a Avatar) Avatar {
	d := DefaultAvatar()
	ok := false
	for _, s := range AvatarShapes {
		if a.Shape == s {
			ok = true
			break
		}
	}
	if !ok {
		a.Shape = d.Shape
	}
	if a.Color == "" {
		a.Color = d.Color
	}
	return a
}

// DecodeGrants parses entity.AgentPersona.AllowedConnectors. A malformed
// value decodes to no grants: deny by default rather than guess.
func DecodeGrants(raw string) []ConnectorGrant {
	out := []ConnectorGrant{}
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []ConnectorGrant{}
	}
	return out
}

// EncodeGrants is the inverse of DecodeGrants; nil encodes as "[]".
func EncodeGrants(g []ConnectorGrant) string {
	if len(g) == 0 {
		return "[]"
	}
	b, err := json.Marshal(g)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// DecodeFeatures parses entity.AgentPersona.Features. An empty or
// malformed value reads as DefaultFeatures, because "{}" is also what a
// row created before features existed carries.
func DecodeFeatures(raw string) Features {
	if raw == "" || raw == "{}" {
		return DefaultFeatures()
	}
	f := DefaultFeatures()
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return DefaultFeatures()
	}
	return f
}

// EncodeFeatures is the inverse of DecodeFeatures.
func EncodeFeatures(f Features) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// DecodeAvatar parses entity.AgentPersona.Avatar, normalized.
func DecodeAvatar(raw string) Avatar {
	var a Avatar
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &a)
	}
	return NormalizeAvatar(a)
}

// EncodeAvatar is the inverse of DecodeAvatar.
func EncodeAvatar(a Avatar) string {
	b, _ := json.Marshal(NormalizeAvatar(a))
	return string(b)
}

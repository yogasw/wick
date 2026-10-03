// Package team is the Agents app's data layer: the agents a user owns,
// the connector access each one is handed, and the resolver the MCP layer
// asks "which agent is this session, and what may it reach?".
//
// An agent is a thin row (entity.AgentPersona) over a project. The text a
// person reads as "the persona" — name, icon, description, system prompt,
// provider — lives in the project's meta.json; this package only adds the
// handle, the Captain flag and the access checklist.
package team

import (
	"encoding/json"
	"hash/fnv"
	"slices"
)

// Grant levels for ConnectorGrant.Level.
const (
	// LevelAll permits every op the owner may run on the connector.
	LevelAll = "all"
	// LevelRead permits only ops that do not declare themselves
	// destructive.
	LevelRead = "read"
	// LevelPick permits exactly the ops listed in ConnectorGrant.Ops.
	LevelPick = "pick"
	// LevelOff is an explicit "no": it overrides a tier default (see
	// TierPlatform) the way the other levels override it upwards.
	LevelOff = "off"
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
	// Level is LevelAll, LevelRead, LevelPick or LevelOff. Anything else
	// reads as LevelRead, so a malformed row fails towards less access.
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
	// Kind picks the renderer: "" is the classic avatar, AvatarKindBlob the
	// blob mascot. Absent in rows written before blobs existed, so those
	// keep decoding as classic.
	Kind string `json:"kind,omitempty"`
	// Shape is one of AvatarShapes, or of BlobShapes for a blob.
	Shape string `json:"shape"`
	// Color is a CSS color, normally a #rrggbb hex.
	Color string `json:"color"`
	// Expression is one of BlobExpressions; only a blob has one.
	Expression string `json:"expression,omitempty"`
}

// AvatarShapes lists the shapes the avatar component can draw.
var AvatarShapes = []string{"circle", "squircle", "triangle", "diamond"}

// AvatarKindBlob is Avatar.Kind for the blob mascot.
const AvatarKindBlob = "blob"

// BlobShapes and BlobExpressions mirror BLOB_SHAPES / BLOB_EXPRESSIONS in
// fe/common/avatar/src/blob/core/types.ts.
var BlobShapes = []string{
	"circle", "pebble", "squircle", "capsule", "triangle", "cloud",
	"droplet", "flame", "medal", "acorn", "jellyfish", "clover",
}

var BlobExpressions = []string{
	"neutral", "attentive", "surprised", "excited", "happy", "angry",
	"sad", "suspicious", "curious", "proud", "shy", "unimpressed",
}

// DefaultAvatar is used when a row carries none.
func DefaultAvatar() Avatar { return Avatar{Shape: "circle", Color: "#6366f1"} }

// AvatarColors is the swatch palette, in the same order as the UI's
// AVATAR_COLORS (fe/common/avatar/src/shape.ts).
var AvatarColors = []string{"#6366f1", "#27b199", "#0ea5e9", "#f59e0b", "#ef4444", "#ec4899", "#8b5cf6", "#64748b"}

// DefaultAvatarFor is a new agent's starting look: colour and shape picked
// by the 32-bit FNV-1a hash of the handle, the same function as the UI's
// defaultAvatarFor, so a handle always gets the same avatar on both sides.
func DefaultAvatarFor(handle string) Avatar {
	h := fnv.New32a()
	_, _ = h.Write([]byte(handle))
	n := h.Sum32()
	nc := uint32(len(AvatarColors))
	return Avatar{
		Shape: AvatarShapes[(n/nc)%uint32(len(AvatarShapes))],
		Color: AvatarColors[n%nc],
	}
}

// NormalizeAvatar replaces an unknown kind, shape or expression, or an
// empty color, with the default, so the UI never receives a value it
// cannot draw. An unknown kind falls back to classic.
func NormalizeAvatar(a Avatar) Avatar {
	d := DefaultAvatar()
	if a.Kind == AvatarKindBlob {
		if !slices.Contains(BlobShapes, a.Shape) {
			a.Shape = BlobShapes[0]
		}
		if !slices.Contains(BlobExpressions, a.Expression) {
			a.Expression = BlobExpressions[0]
		}
	} else {
		a.Kind, a.Expression = "", ""
		if !slices.Contains(AvatarShapes, a.Shape) {
			a.Shape = d.Shape
		}
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

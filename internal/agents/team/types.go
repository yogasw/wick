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
	"net/url"
	"regexp"
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

// Access modes for entity.AgentPersona.AccessMode.
const (
	// AccessChoose limits the agent to its checklist (grants, tier
	// defaults, include-new).
	AccessChoose = "choose"
	// AccessOwner ("Same as me") opens every Connectors-tier connector the
	// owner reaches, new ones included, with write ops. Platform and System
	// rows still follow their tier defaults and overrides.
	AccessOwner = "owner"
)

// NormalizeAccessMode maps a stored value to a mode. Empty (a row saved
// before the column existed) and anything unknown read as AccessChoose,
// the mode that never widens what an agent had.
func NormalizeAccessMode(v string) string {
	if v == AccessOwner {
		return AccessOwner
	}
	return AccessChoose
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
	// Events overrides the event → pose table (fe/common/avatar/src/events.ts),
	// keyed by one of AvatarEvents. Absent = the defaults.
	Events map[string]AvatarEventPose `json:"events,omitempty"`
	// Image is the uploaded picture's crop. The bytes live in their own
	// table (entity.AgentAvatarImage); V and Aspect are copied from that
	// row by the server, never taken from the client. It is drawn only
	// for AvatarKindImage but kept under the other kinds too, so switching
	// to Classic and back does not lose the upload.
	Image *AvatarImage `json:"image,omitempty"`
}

// AvatarImage is where an image avatar's picture sits in its frame:
// Fit picks what Zoom 0 (automatic) means, X/Y move it in percent of the
// frame, Zoom scales it and Crop shapes the frame. Every value is clamped
// (clampAvatarImage).
type AvatarImage struct {
	V      string  `json:"v"`
	Aspect float64 `json:"aspect"`
	Fit    string  `json:"fit"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Zoom   float64 `json:"zoom"`
	// Crop is the frame's shape, one of AvatarCrops (default round).
	Crop string `json:"crop"`
	// Src is the picture's versioned URL, filled in only on the way out
	// (WithImageSrc); never stored, never read from a request.
	Src string `json:"src,omitempty"`
}

// AvatarEventPose is one row of Avatar.Events: a different pose for the
// event, or Off to let it fall back to the plain cues. An empty State
// keeps the default; Expression absent keeps the default, "" means the
// agent's own expression.
type AvatarEventPose struct {
	State      string  `json:"state,omitempty"`
	Expression *string `json:"expression,omitempty"`
	Off        bool    `json:"off,omitempty"`
}

// AvatarEvents mirrors AVATAR_EVENTS in fe/common/avatar/src/events.ts.
var AvatarEvents = []string{"read", "run", "write", "delegate", "ask_user", "remote_wait", "error", "done", "compact"}

// BlobStates mirrors STATES in fe/common/avatar/src/blob/core/types.ts.
var BlobStates = []string{
	"idle", "thinking", "wink", "wide", "alert", "notify", "exclaim",
	"sleep", "play", "orbit", "swirl", "burst", "comet",
}

// ImagePoses mirrors IMAGE_POSES in fe/common/avatar/src/image.ts: the
// image avatar's own poses. An event row may name one of these or a blob
// state; the blob draws its default for an image-only pose.
var ImagePoses = []string{
	"idle", "wide", "scan", "thinking", "type", "orbit", "send",
	"wait", "alert", "fail", "notify", "swirl", "squeeze",
}

// AvatarShapes lists the shapes the avatar component can draw.
var AvatarShapes = []string{"circle", "squircle", "triangle", "diamond"}

// AvatarKindBlob is Avatar.Kind for the blob mascot.
const AvatarKindBlob = "blob"

// AvatarKindImage is Avatar.Kind for an uploaded picture.
const AvatarKindImage = "image"

var avatarVersionRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

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
	// An image avatar without a stored picture's version has nothing to
	// draw: it falls back to classic.
	if a.Image != nil && !avatarVersionRe.MatchString(a.Image.V) {
		a.Image = nil
	}
	if a.Image != nil {
		im := clampAvatarImage(*a.Image)
		im.Src = ""
		a.Image = &im
	}
	if a.Kind == AvatarKindImage && a.Image == nil {
		a.Kind = ""
	}
	if a.Kind == AvatarKindImage {
		a.Expression = ""
		if !slices.Contains(AvatarShapes, a.Shape) {
			a.Shape = d.Shape
		}
	} else if a.Kind == AvatarKindBlob {
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
	a.Events = normalizeAvatarEvents(a.Events)
	return a
}

// normalizeAvatarEvents keeps the rows the UI can draw: a known event with
// Off, a known state (blob state or image pose) or a known (or empty = own) expression. Anything else
// is dropped, and a table with nothing left is nil.
func normalizeAvatarEvents(in map[string]AvatarEventPose) map[string]AvatarEventPose {
	out := map[string]AvatarEventPose{}
	for k, p := range in {
		if !slices.Contains(AvatarEvents, k) {
			continue
		}
		if p.Off {
			out[k] = AvatarEventPose{Off: true}
			continue
		}
		var row AvatarEventPose
		if slices.Contains(BlobStates, p.State) || slices.Contains(ImagePoses, p.State) {
			row.State = p.State
		}
		if p.Expression != nil && (*p.Expression == "" || slices.Contains(BlobExpressions, *p.Expression)) {
			e := *p.Expression
			row.Expression = &e
		}
		if row.State != "" || row.Expression != nil {
			out[k] = row
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// AvatarForCreate is the avatar a NEW agent may start with: it has no
// stored picture yet, so any image crop is dropped and an "image" kind
// becomes classic. Every create path goes through it.
func AvatarForCreate(a Avatar) Avatar {
	a.Image = nil
	return NormalizeAvatar(a)
}

// WithImageSrc fills in the picture's URL under base (the Agents tool
// path) for agent id, so every surface that draws the avatar gets it
// with no per-call-site wiring. A copy: a is not changed.
func WithImageSrc(a Avatar, base, id string) Avatar {
	if a.Image == nil || id == "" {
		return a
	}
	im := *a.Image
	im.Src = base + "/api/team/agents/" + url.PathEscape(id) + "/avatar/image?v=" + im.V
	a.Image = &im
	return a
}

// EncodeAvatar is the inverse of DecodeAvatar.
func EncodeAvatar(a Avatar) string {
	b, _ := json.Marshal(NormalizeAvatar(a))
	return string(b)
}

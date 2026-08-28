package view

import (
	"encoding/json"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// AgentsLayoutVM carries sidebar data for the full-screen Claude-style shell.
type AgentsLayoutVM struct {
	Base             string
	ActivePage       string
	SidebarIDs       []string
	SidebarSessions  map[string]session.Session
	SidebarLifecycle map[string]SessionLifecycleVM
	SidebarLabels    map[string]string // session id → first user message preview
	ActiveSessionID  string
	IdleTimeoutMs    int64
	// Projects powers the sidebar Projects section + per-row project
	// chips. Keyed by project id; ProjectList is the display order.
	Projects    map[string]project.Project
	ProjectList []string
	// SidebarOwner is the sidebar session list's scope: "me" (default)
	// shows only the caller's sessions, "all" everything they may see.
	// The two hrefs re-render the CURRENT page with ?sb= set — the toggle
	// is a pair of plain links, no script.
	SidebarOwner        string
	SidebarOwnerMeHref  string
	SidebarOwnerAllHref string
	// ScopedProjectID, when set, highlights the active project row +
	// drives the scoped breadcrumb.
	ScopedProjectID string
	// PinnedProjectID is the current user's pinned (personal-default)
	// project id, shown with a 📌 in the sidebar. Empty = none.
	PinnedProjectID string
	// FullBleed=true skips the layout's default px-6 py-6 padding
	// wrapper so the page can paint edge-to-edge. The workflow
	// editor needs the full viewport for its canvas; padded pages
	// (sessions, presets, …) leave this false.
	FullBleed bool
	// ShellAssetURL is the hashed bundle URL for the agents-shell island
	// (fe/agents/shell). AgentsLayout emits a <script type="module"> for
	// this URL so every agents page gets pin + drag-to-move sidebar
	// behaviors. Empty when the bundle has not been built yet (dev
	// machine before npm run build).
	ShellAssetURL string
	// AirouterVisible controls the "AI Router" sidebar entry. True when the
	// master switch is on AND the caller may access it (admin) — everyone
	// loses it when the AI routers are disabled.
	AirouterVisible bool
}

// ProjectName returns the display name for a project id, or the id
// itself when unknown. Used by sidebar / session rows.
func (vm AgentsLayoutVM) ProjectName(id string) string {
	if id == "" {
		return ""
	}
	if p, ok := vm.Projects[id]; ok {
		return p.Meta.Name
	}
	return id
}

// NewSessionHref is the "New session" nav target. When the sidebar is
// scoped to a project, it carries `?project=<id>` so the compose form
// auto-selects that project (mockup ②).
func (vm AgentsLayoutVM) NewSessionHref() string {
	if vm.ScopedProjectID != "" {
		return vm.Base + "/?project=" + vm.ScopedProjectID
	}
	return vm.Base + "/"
}

// ProjectIcon returns the emoji icon for a project id (📁 fallback).
func (vm AgentsLayoutVM) ProjectIcon(id string) string {
	if p, ok := vm.Projects[id]; ok && p.Meta.Icon != "" {
		return p.Meta.Icon
	}
	return "📁"
}

// ProviderChoiceVM is one healthy provider row — what the New Session
// picker offers. Disabled / unprobed / version-failed providers
// never reach the UI.
type ProviderChoiceVM struct {
	Type    string
	Name    string
	Version string
	// UsesAIRouter is true when this instance routes through the embedded
	// AI Router — the composer marks it with a badge.
	UsesAIRouter bool
	// Models lists this instance's selectable models — currently only
	// populated for wick (its custom-model registry). Empty for every
	// other provider type: the composer picker only descends to a 3rd
	// "model" level when there's more than one enabled entry here.
	Models []ModelChoiceVM
}

// ModelChoiceVM is one selectable model under a wick provider instance.
type ModelChoiceVM struct {
	ID      string
	Label   string
	Default bool
	// Desc is a short human description shown under the model name in the
	// picker (like the CLIs' own /model menu). Empty when unknown.
	Desc string
	// Live marks a LIVE model SET rather than a single model: the picker shows
	// it as an expandable row (a 4th level) resolved by this row's ID — the
	// vendor filter itself stays server-side and is never exposed to the UI.
	Live bool
	// Caps is the vendor's raw "capabilities" object for this model (verbatim
	// JSON), used by the picker to render capability chips. Populated only for
	// live-set drill rows (they come from live discovery); nil for plain
	// registered models, which don't store caps. Raw so any vendor key survives.
	Caps json.RawMessage
}

// SessionLifecycleVM is the per-row lifecycle snapshot the sessions
// list table renders. PID + LastActiveMs feed the countdown ring;
// Lifecycle is the colour key.
type SessionLifecycleVM struct {
	Lifecycle    string
	PID          int
	LastActiveMs int64
	// SubAgent is the busiest lifecycle among this session's sub-agents
	// ("working" / "spawning"), or "" when none of them is doing anything.
	// Rolled up from the pool because a child runs under its OWN session
	// id, which never appears in the sidebar — without this the row goes
	// dark the moment the leader idles, even though work continues.
	SubAgent string
}

// ProviderCapVM is the used / effective-max slot count for one provider
// instance, shown on its card as "<Used> / <Max>" — or "<Used> / ∞" when
// Unlimited (no finite cap at provider or global scope).
type ProviderCapVM struct {
	Used      int
	Max       int
	Unlimited bool
}

// LiveProcessVM is one row in the Active Processes panel on the Providers page.
type LiveProcessVM struct {
	SessionID string
	AgentName string
	PID       int
	Lifecycle string // "spawning" | "working" | "idle" | "killed"
	Substate  string
}

// MCPClientStatusVM is one row in the MCP Wick card — one per detected
// MCP client (Claude Desktop, Cursor, Gemini CLI, etc.).
type MCPClientStatusVM struct {
	ID          string // "claude", "cursor", "gemini", "codex", "claude-code"
	Label       string // "Claude Desktop", "Cursor", …
	Detected    bool   // client config dir exists on this host
	Installed   bool   // wick entry present in client's mcpServers
	Blocklisted bool   // user manually uninstalled — skip auto-install
	ConfigPath  string // absolute path to config file (for tooltip)
}

// MCPStatusVM is the aggregate for the MCP Wick card on the Providers page.
type MCPStatusVM struct {
	AppName string
	Clients []MCPClientStatusVM
}

// GateStatusVM is the umbrella "what is the gate doing right now?"
// card on the Providers page. Gate covers two sub-policies — the
// permission prompt and the ask_user MCP tool — so the VM carries
// both, plus the boot-time binary resolution state for the permission
// hook.
type GateStatusVM struct {
	Enabled bool
	Binary  string // absolute path (when enabled)
	Source  string // "sibling" | "embed" | "path" — debug aid
	Reason  string // why disabled, when Enabled=false
	Note    string // human-readable behavior summary; rendered as-is

	// PermissionMode is the active value of GateConfig.PermissionMode
	// ("on" | "bypass"). "bypass" means the spawner strips the hook
	// config and runs unguarded — UI surfaces that as a locked badge
	// so operators can't toggle individual provider hooks (no-op).
	PermissionMode string

	// BypassLocked is true when PermissionMode=="bypass". Retained for
	// templ branches that already key off this flag; equivalent to
	// PermissionMode == "bypass".
	BypassLocked bool
}


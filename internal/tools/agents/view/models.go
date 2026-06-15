package view

import (
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
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
	// ProjectCounts is the number of sessions bound to each project id
	// (sidebar pill). ScopedProjectID, when set, highlights the active
	// project row + drives the scoped breadcrumb.
	ProjectCounts   map[string]int
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
}

// SessionLifecycleVM is the per-row lifecycle snapshot the sessions
// list table renders. PID + LastActiveMs feed the countdown ring;
// Lifecycle is the colour key.
type SessionLifecycleVM struct {
	Lifecycle    string
	PID          int
	LastActiveMs int64
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

// ProviderSpawnDetailVM holds data for one spawn-log file timeline.
type ProviderSpawnDetailVM struct {
	Layout AgentsLayoutVM
	Base   string
	File   provider.SpawnLogFile
	Events []provider.SpawnEvent
	// SessionDeleted is true when the spawn's session no longer exists
	// (deleted since the spawn ran) — the detail page shows a notice and
	// the cwd path is stale.
	SessionDeleted bool
}

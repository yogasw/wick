package view

import (
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
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
	// FullBleed=true skips the layout's default px-6 py-6 padding
	// wrapper so the page can paint edge-to-edge. The workflow
	// editor needs the full viewport for its canvas; padded pages
	// (sessions, presets, …) leave this false.
	FullBleed bool
}

// OverviewVM holds data for the Overview page. SessionIDs is the
// active-only subset (spawning/working/idle) — Killed sessions live
// in /sessions, not on the Overview. Queued is the per-session FIFO
// snapshot — operators can kill a queue entry that's been waiting
// too long.
type OverviewVM struct {
	Layout        AgentsLayoutVM
	Base          string
	Active        int
	QueueLen      int
	PoolMax       int
	SessionIDs    []string
	Sessions      map[string]session.Session
	Lifecycle     map[string]SessionLifecycleVM
	IdleTimeoutMs int64
	Queued        []QueuedEntryVM
}

// QueuedEntryVM is one row in the queue panel. WaitingMs drives a
// "waiting Ns" label so operators see how stale the entry is.
type QueuedEntryVM struct {
	SessionID string
	AgentName string
	WaitingMs int64
}

// SessionsListVM holds data for the Sessions list page. Lifecycle is
// keyed by session ID so each row can render the live badge — empty
// means no live entry in the pool (badge falls back to "killed" /
// no-agent).
type SessionsListVM struct {
	Layout        AgentsLayoutVM
	Base          string
	IDs           []string
	Sessions      map[string]session.Session
	Labels        map[string]string // id → first user message preview
	Workspaces    map[string]workspace.Workspace
	WorkspaceList []string
	PresetList    []string
	Providers     []ProviderChoiceVM
	Lifecycle     map[string]SessionLifecycleVM
	IdleTimeoutMs int64
	Page          int
	HasNext       bool
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

// SessionsTableVM feeds the reusable sessions list table component.
// The full /sessions page sets ShowPaging=true; the Overview "Active
// Sessions" panel sets ShowPaging=false and uses a tighter EmptyText.
type SessionsTableVM struct {
	Base          string
	IDs           []string
	Sessions      map[string]session.Session
	Lifecycle     map[string]SessionLifecycleVM
	IdleTimeoutMs int64
	EmptyText     string
	ShowPaging    bool
	Page          int
	HasNext       bool
}

// TurnEventVM is one tool/thinking event within an assistant turn.
type TurnEventVM struct {
	Type      string // "tool_use" | "tool_result" | "thinking"
	ToolName  string
	ToolInput string
	ToolUseID string
	IsError   bool
	Text      string
}

// TurnVM is one conversation turn for the UI.
type TurnVM struct {
	Role      string // "user" | "assistant" | "system"
	Agent     string
	Text      string
	Truncated bool
	Time      time.Time
	Events    []TurnEventVM
}

// SessionDetailVM holds data for the Session detail page.
//
// Lifecycle / PID / LastActiveMs / IdleTimeoutMs feed the realtime
// status badge: the server emits the snapshot at render time and JS
// updates it from SSE events thereafter.
type SessionDetailVM struct {
	Layout         AgentsLayoutVM
	Base           string
	Session        session.Session
	Tab            string // "conversation" | "commands" | "raw"
	Turns          []TurnVM
	CmdLines       []string
	Lifecycle      string
	PID            int
	LastActiveMs   int64
	IdleTimeoutMs  int64
	Gate           GateStatusVM
	Providers       []ProviderChoiceVM
	ActiveProvider  string
	WorkspaceList   []string
	ActiveWorkspace string
}

// NewSessionComposeVM feeds the ChatGPT-style compose page that
// gathers provider/preset/workspace + first message before any
// session is persisted. The session is created server-side only when
// the form posts back with a non-empty message.
type NewSessionComposeVM struct {
	Layout          AgentsLayoutVM
	Base            string
	Providers       []ProviderChoiceVM
	Presets         []string
	Workspaces      []string
	DefaultProvider string
	DefaultPreset   string
	Message         string // round-tripped on validation error
	Error           string
}

// WorkspacesVM holds data for the Workspaces page.
type WorkspacesVM struct {
	Layout        AgentsLayoutVM
	Base          string
	WorkspaceList []string
	Workspaces    map[string]workspace.Workspace
	PresetList    []string
}

// PresetsVM holds data for the Presets list page.
type PresetsVM struct {
	Layout AgentsLayoutVM
	Base   string
	Names  []string
}

// PresetDetailVM holds data for the Preset editor page.
type PresetDetailVM struct {
	Layout AgentsLayoutVM
	Base   string
	Name   string
	Body   string
}

// ProvidersVM holds data for the Providers page — runtime instance
// statuses, recent spawn log files, and live pool capacity. Spawns
// is the current page slice; Page/HasNext drive the pager.
type ProvidersVM struct {
	Layout        AgentsLayoutVM
	Base          string
	Statuses      []provider.Status
	Spawns        []provider.SpawnLogFile
	Page          int
	HasNext       bool
	PoolActive    int
	PoolQueueLen  int
	PoolMax       int
	SupportedKeys []string
	Gate          GateStatusVM
	AutoRescan    bool
	MCP           MCPStatusVM
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
}

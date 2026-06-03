package view

import (
	"strconv"
	"time"

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

// ScopedFolder returns the folder path shown in the scoped header chip:
// the custom path, or "managed" for managed projects. Empty when not
// scoped or project unknown.
func (vm AgentsLayoutVM) ScopedFolder() string {
	if vm.ScopedProjectID == "" {
		return ""
	}
	if p, ok := vm.Projects[vm.ScopedProjectID]; ok {
		if p.Meta.CustomPath != "" {
			return p.Meta.CustomPath
		}
		return "managed"
	}
	return ""
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
	Projects      map[string]project.Project
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
	// Label (first user message) + Project name make the queue list
	// readable + searchable on the Overview page.
	Label   string
	Project string
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
	Projects      map[string]project.Project
	ProjectList   []string
	PresetList    []string
	Providers     []ProviderChoiceVM
	Lifecycle     map[string]SessionLifecycleVM
	IdleTimeoutMs int64
	Page          int
	HasNext       bool
	// ScopedProjectID, when set, marks the project the sidebar/list is
	// currently scoped to (drives the breadcrumb + header chip).
	ScopedProjectID string
	// Composer is rendered at the top of the scoped project landing
	// (Claude-style compose box). Only used when ScopedProjectID != "".
	Composer ComposerVM
}

// ProjectName resolves a project id to its display name (list VM helper).
func (vm SessionsListVM) ProjectName(id string) string {
	if id == "" {
		return ""
	}
	if p, ok := vm.Projects[id]; ok {
		return p.Meta.Name
	}
	return id
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
	Projects      map[string]project.Project
	Lifecycle     map[string]SessionLifecycleVM
	IdleTimeoutMs int64
	EmptyText     string
	ShowPaging    bool
	Page          int
	HasNext       bool
}

// ProjectName resolves a project id to its display name (table VM helper).
func (vm SessionsTableVM) ProjectName(id string) string {
	if id == "" {
		return ""
	}
	if p, ok := vm.Projects[id]; ok {
		return p.Meta.Name
	}
	return id
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
	Role        string // "user" | "assistant" | "system"
	Agent       string
	Provider    string // "type/name" — snapshot from the turn that produced it
	Text        string
	Truncated   bool
	Time        time.Time
	Events      []TurnEventVM
	Attachments []AttachmentVM
}

// AttachmentVM is one user-uploaded file rendered under the user
// bubble. URL is the GET path served by sessionUploadServe; IsImage
// gates inline thumbnail rendering vs the generic file chip.
type AttachmentVM struct {
	Name    string
	URL     string
	MIME    string
	Size    int64
	IsImage bool
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
	Providers      []ProviderChoiceVM
	ActiveProvider string
	// Projects feeds the "Move to project" picker on the detail page;
	// ActiveProjectID is the session's current binding.
	Projects        map[string]project.Project
	ProjectList     []string
	ActiveProjectID string
}

// ProjectName resolves a project id to its display name (detail VM helper).
func (vm SessionDetailVM) ProjectName(id string) string {
	if id == "" {
		return ""
	}
	if p, ok := vm.Projects[id]; ok {
		return p.Meta.Name
	}
	return id
}

// Compose-form dropdown styling. When the page is scoped to a project,
// provider/preset/project dropdowns render "inherited" (green) per
// mockup state ②; otherwise neutral.
const (
	nsSelectBase      = "rounded-lg border px-2.5 py-1.5 text-xs focus:border-green-500 focus:outline-none cursor-pointer"
	nsSelectInherited = "border-green-400 dark:border-green-700 bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-300 font-semibold"
	nsSelectNeutral   = "border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-900 dark:text-white-100"
)

// ComposerVM drives the shared compose card (composerCard) used both on
// the standalone New Session page and embedded at the top of a scoped
// project landing (Claude-style: compose box + chats below).
type ComposerVM struct {
	Base             string
	Providers        []ProviderChoiceVM
	Presets          []string
	Projects         []ProjectChoiceVM
	DefaultProvider  string
	DefaultPreset    string
	ScopedProjectID  string // explicit project scope → green "inherited" styling
	DefaultProjectID string // soft default pre-select (operator setting)
	Message          string // round-tripped on validation error
	Scoped           bool   // green inherited dropdowns
	// ShowProjectPicker shows the project dropdown. False on a project
	// landing (already in the project) — the binding is sent as a hidden
	// field instead.
	ShowProjectPicker bool
}

// SelectedProjectID is the project the composer pre-selects.
func (vm ComposerVM) SelectedProjectID() string {
	if vm.ScopedProjectID != "" {
		return vm.ScopedProjectID
	}
	return vm.DefaultProjectID
}

// ProjectChoiceVM is one project row offered by the New Session picker /
// move menu. Defaults drive the compose-form prefill.
type ProjectChoiceVM struct {
	ID              string
	Name            string
	Icon            string
	Description     string
	DefaultPreset   string
	DefaultProvider string
	SystemAddon     string
}

// NewSessionComposeVM feeds the ChatGPT-style compose page that
// gathers provider/preset/project + first message before any
// session is persisted. The session is created server-side only when
// the form posts back with a non-empty message.
type NewSessionComposeVM struct {
	Layout          AgentsLayoutVM
	Base            string
	Providers       []ProviderChoiceVM
	Presets         []string
	Projects        []ProjectChoiceVM
	DefaultProvider string
	DefaultPreset   string
	// ScopedProjectID locks the picker to the active project when the
	// compose page is opened from a scoped sidebar (green "inherited" UI).
	ScopedProjectID string
	// DefaultProjectID is the operator's configured default project
	// (Settings → default_project_id). When the page is NOT explicitly
	// scoped, the picker pre-selects this so new sessions land in the
	// preferred project by default. No green styling — it's a soft default.
	DefaultProjectID string
	Message          string // round-tripped on validation error
	Error            string
}

// SelectedProjectID is the project the picker should pre-select: the
// explicit scope wins, else the operator's configured default.
func (vm NewSessionComposeVM) SelectedProjectID() string {
	if vm.ScopedProjectID != "" {
		return vm.ScopedProjectID
	}
	return vm.DefaultProjectID
}

// ScopedProject returns the active project choice + ok when the compose
// page is scoped. Drives the "New session in 📁 X" heading and the
// green "inherited" dropdown styling (mockup state ②).
func (vm NewSessionComposeVM) ScopedProject() (ProjectChoiceVM, bool) {
	if vm.ScopedProjectID == "" {
		return ProjectChoiceVM{}, false
	}
	for _, p := range vm.Projects {
		if p.ID == vm.ScopedProjectID {
			return p, true
		}
	}
	return ProjectChoiceVM{}, false
}

// fmtCount renders an int as a string for templ text nodes.
func fmtCount(n int) string { return strconv.Itoa(n) }

// pinTitle is the tooltip for the project pin toggle.
func pinTitle(pinned bool) string {
	if pinned {
		return "This is your default project — click to unpin"
	}
	return "Pin as your default project (opens here automatically)"
}

// PinnedSessionVM is one pinned-session row on the project settings page.
type PinnedSessionVM struct {
	ID    string
	Label string
}

// ProjectSettingsVM drives the full project settings page (mockup ④) —
// a real navigable page, not a modal. IsNew=true renders the create
// form (empty, no delete / pinned / meta-preview).
type ProjectSettingsVM struct {
	Layout          AgentsLayoutVM
	Base            string
	IsNew           bool
	ID              string
	Name            string
	Icon            string
	Description     string
	CustomPath      string
	Managed         bool // CustomPath == ""
	IsDefault       bool // built-in default project (cannot delete)
	DefaultPreset   string
	DefaultProvider string
	SystemAddon     string
	ChatCount       int
	CreatedAt       string
	PresetList      []string
	Pinned          []PinnedSessionVM
	MetaJSON        string
	Action          string // form POST target
}

// BackHref is where the settings ← link / Cancel returns to: the
// project's own landing for an existing project, else All chats.
func (vm ProjectSettingsVM) BackHref() string {
	if !vm.IsNew && vm.ID != "" {
		return vm.Base + "/sessions?project=" + vm.ID
	}
	return vm.Base + "/sessions"
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

// SkillSyncVM holds the current state of skill directories for the sync card on Providers page.
type SkillSyncVM struct {
	Dirs       []string
	Files      []SkillFileVM
	LastResult string
}

// SkillFileVM is one row in skills tables.
type SkillFileVM struct {
	Name    string
	IsDir   bool
	InDirs  []string
	Missing []string
}

// SkillsPageVM is the view model for the dedicated Skills page.
type SkillsPageVM struct {
	Layout AgentsLayoutVM
	Base   string
	Dirs   []string
	Files  []SkillFileVM
	Flash  string
	Error  string
}

// SkillDetailVM is the view model for a single skill file viewer.
type SkillDetailVM struct {
	Layout     AgentsLayoutVM
	Base       string
	Filename   string
	Content    string
	SourcePath string
	InDirs     []string
}

// SkillFolderVM is the view model for the folder explorer page.
type SkillFolderVM struct {
	Layout     AgentsLayoutVM
	Base       string
	FolderName string
	Entries    []SkillFileVM
	InDirs     []string
	Missing    []string
}

// SkillProviderFolderVM is the folder explorer scoped to one provider dir.
type SkillProviderFolderVM struct {
	Layout       AgentsLayoutVM
	Base         string
	Provider     string // dir label e.g. "claude"
	FolderName   string
	Entries      []SkillFileVM
	AllProviders []string // all known dir labels for tab switching
}

// SkillProviderFileVM is the file viewer scoped to one provider dir.
type SkillProviderFileVM struct {
	Layout       AgentsLayoutVM
	Base         string
	Provider     string
	FolderName   string
	Filename     string
	Content      string
	SourcePath   string
	AllProviders []string // for tab switching
	HasFile      map[string]bool // provider label → file exists
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

package view

import (
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// OverviewVM holds data for the Overview page. SessionIDs is the
// active-only subset (spawning/working/idle) — Killed sessions live
// in /sessions, not on the Overview. Queued is the per-session FIFO
// snapshot — operators can kill a queue entry that's been waiting
// too long.
type OverviewVM struct {
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
	Base          string
	IDs           []string
	Sessions      map[string]session.Session
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

// TurnVM is one conversation turn for the UI.
type TurnVM struct {
	Role      string // "user" | "assistant" | "system"
	Agent     string
	Text      string
	Truncated bool
	Time      time.Time
}

// SessionDetailVM holds data for the Session detail page.
//
// Lifecycle / PID / LastActiveMs / IdleTimeoutMs feed the realtime
// status badge: the server emits the snapshot at render time and JS
// updates it from SSE events thereafter.
type SessionDetailVM struct {
	Base          string
	Session       session.Session
	Tab           string // "conversation" | "commands" | "raw"
	Turns         []TurnVM
	CmdLines      []string
	Lifecycle     string
	PID           int
	LastActiveMs  int64
	IdleTimeoutMs int64
}

// WorkspacesVM holds data for the Workspaces page.
type WorkspacesVM struct {
	Base          string
	WorkspaceList []string
	Workspaces    map[string]workspace.Workspace
	PresetList    []string
}

// PresetsVM holds data for the Presets list page.
type PresetsVM struct {
	Base  string
	Names []string
}

// PresetDetailVM holds data for the Preset editor page.
type PresetDetailVM struct {
	Base string
	Name string
	Body string
}

// ProvidersVM holds data for the Providers page — runtime instance
// statuses, recent spawn log files, and live pool capacity. Spawns
// is the current page slice; Page/HasNext drive the pager.
type ProvidersVM struct {
	Base          string
	Statuses      []provider.Status
	Spawns        []provider.SpawnLogFile
	Page          int
	HasNext       bool
	PoolActive    int
	PoolQueueLen  int
	PoolMax       int
	SupportedKeys []string
}

// ProviderSpawnDetailVM holds data for one spawn-log file timeline.
type ProviderSpawnDetailVM struct {
	Base   string
	File   provider.SpawnLogFile
	Events []provider.SpawnEvent
}

package view

import (
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// OverviewVM holds data for the Overview page.
type OverviewVM struct {
	Base       string
	Active     int
	QueueLen   int
	PoolMax    int
	ActiveList []ActiveAgentVM
	QueueList  []QueuedAgentVM
	SessionIDs []string
	Sessions   map[string]session.Session
}

// ActiveAgentVM is the public snapshot of one running agent in the pool.
type ActiveAgentVM struct {
	SessionID string
	AgentName string
}

// QueuedAgentVM is the public snapshot of one queued request.
type QueuedAgentVM struct {
	SessionID string
	AgentName string
	WaitingMs int64
}

// SessionsListVM holds data for the Sessions list page.
type SessionsListVM struct {
	Base          string
	IDs           []string
	Sessions      map[string]session.Session
	Workspaces    map[string]workspace.Workspace
	WorkspaceList []string
	PresetList    []string
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
type SessionDetailVM struct {
	Base     string
	Session  session.Session
	Tab      string // "conversation" | "commands" | "raw"
	Turns    []TurnVM
	CmdLines []string
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
// statuses, recent spawn log files, and live pool capacity.
type ProvidersVM struct {
	Base          string
	Statuses      []provider.Status
	Spawns        []provider.SpawnLogFile
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

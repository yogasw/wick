package view

import (
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// OverviewVM holds data for the Overview page.
type OverviewVM struct {
	Base       string
	Active     int
	QueueLen   int
	SessionIDs []string
	Sessions   map[string]session.Session
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

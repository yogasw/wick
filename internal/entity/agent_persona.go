package entity

import "time"

// AgentPersona is one agent in the Agents app: a named, chat-able identity
// owned by one user and backed by a project.
//
// The persona text itself (name, icon, description, system prompt,
// provider) is NOT stored here — it is read from the project the agent
// points at, so switching ProjectID switches the persona and there is no
// copy that can go stale. This row only carries what a project has no
// place for: the handle, the tagline, the Captain flag, and the access the
// owner hands the agent.
//
// Access is a NARROWING list over the owner's own reach. AllowedConnectors
// can never grant a connector, account or operation the owner cannot use
// themselves; the MCP layer intersects it with the owner's visibility on
// every call (see team.Scope).
//
// Each row is one agent in a user's Team (package internal/agents/team).
// The AgentPersona name and the agent_personas table are kept as-is so
// existing data needs no migration.
type AgentPersona struct {
	ID          string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	OwnerUserID string `gorm:"type:varchar(64);not null;index:idx_persona_owner_handle,unique,priority:1" json:"owner_user_id"`
	// Handle is the @mention name, lowercase-kebab, unique per owner.
	Handle    string `gorm:"type:varchar(64);not null;index:idx_persona_owner_handle,unique,priority:2" json:"handle"`
	ProjectID string `gorm:"type:varchar(64);not null;default:''" json:"project_id"`
	// Kind is "" for an agent wick runs itself and "a2a-remote" for an
	// agent of another system reached over A2A (package a2aremote), whose
	// project only holds its transcript.
	Kind string `gorm:"type:varchar(16);not null;default:''" json:"kind"`
	// Tagline is the short label people know the agent by ("The Critic",
	// "Log Hunter"), shown beside its name. "" = none. A project has no
	// field for it, so it lives on the row.
	Tagline string `gorm:"type:varchar(64);not null;default:''" json:"tagline"`
	// IsCaptain marks the owner's main agent. At most one per owner; the
	// store enforces it on save.
	IsCaptain bool `gorm:"not null;default:false" json:"is_captain"`

	// AllowedConnectors is a JSON array of team.ConnectorGrant. Empty
	// array = no connector at all (deny by default).
	AllowedConnectors string `gorm:"type:text;not null;default:'[]'" json:"allowed_connectors"`
	// IncludeNewConnectors grants, with read-only ops, every connector the
	// owner gains later without editing the checklist.
	IncludeNewConnectors bool `gorm:"not null;default:false" json:"include_new_connectors"`
	// AccessMode is team.AccessChoose (the checklist above) or
	// team.AccessOwner ("Same as me": every connector the owner reaches,
	// with write ops). The stored checklist is kept either way.
	AccessMode string `gorm:"type:varchar(16);not null;default:'choose'" json:"access_mode"`
	// Features is a JSON object of team.Features (which conversation
	// rail panels the agent's chat shows).
	Features string `gorm:"type:text;not null;default:'{}'" json:"features"`
	// AllowedNativeTools is a JSON array of the provider-native tools the
	// agent may call (team.NativeTools). "" is a row saved before the
	// column existed and keeps every tool on, so an upgrade changes no
	// running agent; a new agent is stored with team.DefaultNativeTools.
	AllowedNativeTools string `gorm:"type:text;not null;default:''" json:"allowed_native_tools"`
	// BashRules is a JSON array of team.BashRule: the commands Bash may
	// run without asking. Anything else goes to the approval gate.
	BashRules string `gorm:"type:text;not null;default:'[]'" json:"bash_rules"`
	// DisabledSkills is a JSON array of skill names the agent's spawns
	// leave out of the skill catalog.
	DisabledSkills string `gorm:"type:text;not null;default:'[]'" json:"disabled_skills"`
	// Avatar is a JSON object of team.Avatar (shape + color).
	Avatar string `gorm:"type:text;not null;default:'{}'" json:"avatar"`

	// RunAs picks whose connector access the agent's spawns run with:
	// "caller" (the human who triggered the turn, the owner when none did)
	// or "owner" (always the owner). See team.SpawnIdentity.
	RunAs string `gorm:"type:varchar(16);not null;default:'caller'" json:"run_as"`
	// UseGlobalPrompt makes the agent's spawns carry the operator's
	// global system_prompt instead of system_prompt_team. On for a
	// project converted into an agent, whose channel rules (Slack format,
	// identity) live in that prompt; off for a new agent.
	UseGlobalPrompt bool `gorm:"not null;default:false" json:"use_global_prompt"`

	// MentionFrom is who may hand this agent a turn over the Team link:
	// "all" (every agent of the owner), "captain", "list" (MentionAllow)
	// or "off". A person's @mention is never filtered by it. "" = all.
	// See teamlink.Peer.AcceptsFrom.
	MentionFrom string `gorm:"type:varchar(16);not null;default:'all'" json:"mention_from"`
	// MentionAllow is a JSON array of agent ids, read when MentionFrom is
	// "list".
	MentionAllow string `gorm:"type:text;not null;default:'[]'" json:"mention_allow"`
	// MaxHops caps the agent-to-agent turns of an exchange this agent
	// takes part in (1..teamlink.MaxHopsCeiling); the smallest cap of the
	// agents involved wins. 0 = teamlink.DefaultMaxHops.
	MaxHops int `gorm:"not null;default:0" json:"max_hops"`

	// ManageAgents lets the agent run the agents.* ops (list, create,
	// edit persona, propose access) on its owner's other agents. nil =
	// never set: on for the Captain, off for everyone else (see
	// team.ManagesAgents). A sub-agent never inherits it.
	ManageAgents *bool `json:"manage_agents"`
	// CaptainCan is a JSON object of team.CaptainCan: what the Captain
	// may do to THIS agent. "{}" reads as the defaults.
	CaptainCan string `gorm:"type:text;not null;default:'{}'" json:"captain_can"`

	Disabled bool `gorm:"not null;default:false" json:"disabled"`
	// SessionPolicy is a JSON object of team.SessionPolicy: how the
	// agent's main chat is compacted. "{}" = provider default.
	SessionPolicy string `gorm:"type:text;not null;default:'{}'" json:"session_policy"`
	// SuggestedPrompts is a JSON array of team.SuggestedPrompt (at most
	// four): the prompt chips of the agent's Slack agent view and of an
	// empty web chat.
	SuggestedPrompts string `gorm:"type:text;not null;default:'[]'" json:"suggested_prompts"`
	// AllowProviderSwitch lets the agent's chat pick another provider or
	// model from the composer. nil = never set: see
	// team.AllowsProviderSwitch for the default.
	AllowProviderSwitch *bool `json:"allow_provider_switch"`
	// LastReadAt is when the owner last opened the agent's chat in the
	// Team app; activity on the main session after it reads as unread.
	// nil = never opened. Only the owner chats an agent from the app, so
	// one column is the per-(user, agent) read mark. Written by
	// Store.MarkRead alone — a settings save never touches it.
	LastReadAt *time.Time `json:"last_read_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// TableName pins the table name so a rename of the struct cannot move it.
func (AgentPersona) TableName() string { return "agent_personas" }

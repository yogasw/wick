package entity

import "time"

// AgentPersona is one agent in the Agents app: a named, chat-able identity
// owned by one user and backed by a project.
//
// The persona text itself (name, icon, description, system prompt,
// provider) is NOT stored here — it is read from the project the agent
// points at, so switching ProjectID switches the persona and there is no
// copy that can go stale. This row only carries what a project has no
// place for: the handle, the Captain flag, and the access the owner hands
// the agent.
//
// Access is a NARROWING list over the owner's own reach. AllowedConnectors
// can never grant a connector, account or operation the owner cannot use
// themselves; the MCP layer intersects it with the owner's visibility on
// every call (see team.Scope).
//
// Each row is one member of a user's Team (package internal/agents/team).
// The AgentPersona name and the agent_personas table are kept as-is so
// existing data needs no migration.
type AgentPersona struct {
	ID          string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	OwnerUserID string `gorm:"type:varchar(64);not null;index:idx_persona_owner_handle,unique,priority:1" json:"owner_user_id"`
	// Handle is the @mention name, lowercase-kebab, unique per owner.
	Handle    string `gorm:"type:varchar(64);not null;index:idx_persona_owner_handle,unique,priority:2" json:"handle"`
	ProjectID string `gorm:"type:varchar(64);not null;default:''" json:"project_id"`
	// IsCaptain marks the owner's main agent. At most one per owner; the
	// store enforces it on save.
	IsCaptain bool `gorm:"not null;default:false" json:"is_captain"`

	// AllowedConnectors is a JSON array of team.ConnectorGrant. Empty
	// array = no connector at all (deny by default).
	AllowedConnectors string `gorm:"type:text;not null;default:'[]'" json:"allowed_connectors"`
	// IncludeNewConnectors grants, with read-only ops, every connector the
	// owner gains later without editing the checklist.
	IncludeNewConnectors bool `gorm:"not null;default:false" json:"include_new_connectors"`
	// Features is a JSON object of team.Features (which conversation
	// rail panels the agent's chat shows).
	Features string `gorm:"type:text;not null;default:'{}'" json:"features"`
	// Avatar is a JSON object of team.Avatar (shape + color).
	Avatar string `gorm:"type:text;not null;default:'{}'" json:"avatar"`

	Disabled  bool      `gorm:"not null;default:false" json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName pins the table name so a rename of the struct cannot move it.
func (AgentPersona) TableName() string { return "agent_personas" }

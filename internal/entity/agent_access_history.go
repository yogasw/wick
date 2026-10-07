package entity

import "time"

// AgentAccessHistory is one change of an agent's connector access: a save
// in Settings › Access, or a change the Captain proposed through
// agents.set_access. A proposal is written "pending" with the approval
// card's id and settles to "applied" or "declined" when the owner clicks.
type AgentAccessHistory struct {
	ID          string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	AgentID     string `gorm:"type:varchar(64);not null;index" json:"agent_id"`
	OwnerUserID string `gorm:"type:varchar(64);not null;index" json:"owner"`
	// Actor is who made the change, as a chip names them: a person's
	// name, or "@handle" for an agent.
	Actor string `gorm:"type:varchar(128);not null;default:''" json:"actor"`
	// ActorAgentID is the proposing agent, "" for a person.
	ActorAgentID string `gorm:"type:varchar(64);not null;default:''" json:"actor_agent_id"`
	// Change is a JSON team.AccessChange: the readable diff and, for a
	// proposal, the grants it would apply.
	Change string `gorm:"type:text;not null;default:'{}'" json:"change"`
	// Status is "pending", "applied" or "declined".
	Status string `gorm:"type:varchar(16);not null;default:'applied'" json:"status"`
	// ApprovalID is the approval_request card's id; "" for a direct save.
	ApprovalID string `gorm:"type:varchar(64);not null;default:'';index" json:"approval_id"`
	// SessionID is the session the approval card sits in.
	SessionID string `gorm:"type:varchar(64);not null;default:''" json:"session_id"`
	// DecidedBy names the person who accepted or declined a proposal.
	DecidedBy string    `gorm:"type:varchar(128);not null;default:''" json:"decided_by"`
	At        time.Time `gorm:"not null;index" json:"at"`
}

// TableName pins the table name.
func (AgentAccessHistory) TableName() string { return "agent_access_history" }

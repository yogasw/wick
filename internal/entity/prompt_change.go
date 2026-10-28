package entity

import "time"

// PromptChange is one change of a managed system prompt (package
// internal/agents/promptguard): an operator prompt config row or one
// user's Team instructions. An applied row is a version of that prompt;
// a change proposed over MCP is written "pending" with the approval
// card's id and settles to "applied" or "declined" when an admin clicks.
type PromptChange struct {
	ID string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	// Target names the prompt: "agents/<config key>" for an operator
	// prompt, "team/<user id>" for one user's Team instructions.
	Target string `gorm:"type:varchar(128);not null;index" json:"target"`
	// Version is the prompt's version number once applied, 1 up; 0 while
	// pending and for a declined proposal.
	Version int `gorm:"not null;default:0;index" json:"version"`
	// Value is the full prompt text this change sets.
	Value string `gorm:"type:text;not null;default:''" json:"value"`
	// Status is "pending", "applied" or "declined".
	Status string `gorm:"type:varchar(16);not null;default:'applied'" json:"status"`
	// Via is how the change came: "ui" (a person saved it), "approval"
	// (an MCP proposal an admin accepted), "baseline" (the value found
	// when history started).
	Via string `gorm:"type:varchar(16);not null;default:''" json:"via"`
	// BaseVersion is the version a proposal was made against; accepting
	// it is refused once the prompt moved past that version.
	BaseVersion int `gorm:"not null;default:0" json:"base_version"`
	// RollbackOf is the version a rollback proposal restores, 0 otherwise.
	RollbackOf int `gorm:"not null;default:0" json:"rollback_of"`
	// Actor names who made or proposed the change; ActorUserID is their
	// wick user, ActorAgent the "@handle" of the agent that proposed it.
	Actor       string `gorm:"type:varchar(128);not null;default:''" json:"actor"`
	ActorUserID string `gorm:"type:varchar(64);not null;default:''" json:"actor_user_id"`
	ActorAgent  string `gorm:"type:varchar(128);not null;default:''" json:"actor_agent"`
	Reason      string `gorm:"type:text;not null;default:''" json:"reason"`
	// SessionID is the session the approval card sits in.
	SessionID string `gorm:"type:varchar(64);not null;default:''" json:"session_id"`
	// DecidedBy names the admin who accepted or declined a proposal.
	DecidedBy string     `gorm:"type:varchar(128);not null;default:''" json:"decided_by"`
	At        time.Time  `gorm:"not null;index" json:"at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// TableName pins the table name.
func (PromptChange) TableName() string { return "prompt_changes" }

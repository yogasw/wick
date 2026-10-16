package entity

import "time"

// AgentShare lets one other wick user chat with a Team agent. The
// recipient may open the agent from their roster, chat 1:1 and @mention
// it; every setting stays the owner's. Turns run with the owner's
// connector access narrowed by the agent's checklist, while the
// conversations are the recipient's own. See internal/agents/team/share.go.
type AgentShare struct {
	ID      string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	AgentID string `gorm:"type:varchar(64);not null;uniqueIndex:idx_agent_share_pair" json:"agent_id"`
	// SharedWithUserID is the recipient, matched by user id only.
	SharedWithUserID string `gorm:"type:varchar(64);not null;uniqueIndex:idx_agent_share_pair;index" json:"shared_with_user_id"`
	// CreatedBy is who shared it: the owner, or an admin acting for them.
	CreatedBy string    `gorm:"type:varchar(64);not null;default:''" json:"created_by"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	// LastReadAt is when the recipient last opened their chat with the
	// agent — the recipient's own unread mark, apart from the owner's.
	LastReadAt *time.Time `json:"last_read_at"`
	// HistoryVisible is the owner's "Recipients can view chat history"
	// choice for this share. nil follows the agent type's default (on for
	// a built-in agent, off for a remote one).
	HistoryVisible *bool `json:"history_visible"`
}

// TableName pins the table name.
func (AgentShare) TableName() string { return "agent_shares" }

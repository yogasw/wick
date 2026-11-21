package entity

import "time"

// AgentRBAC is one revision of a Team agent's role document: who gets
// which role, and what each role may borrow of the owner's connectors.
// The newest revision of an agent is the one in force; older rows are
// its history, so a change can be rolled back by writing an old document
// again. A table of its own on purpose: agent_personas is never altered,
// because adding a column to an existing table left stale cached query
// plans behind the DB pooler.
type AgentRBAC struct {
	ID      string `gorm:"type:text;primaryKey"`
	AgentID string `gorm:"type:text;not null;uniqueIndex:idx_agent_rbac_rev,priority:1"`
	Rev     int    `gorm:"not null;uniqueIndex:idx_agent_rbac_rev,priority:2"`
	// Doc is the role document as JSON (team.RBACDoc).
	Doc       string    `gorm:"type:text;not null"`
	UpdatedBy string    `gorm:"type:text"`
	Reason    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name.
func (AgentRBAC) TableName() string { return "agent_rbac" }

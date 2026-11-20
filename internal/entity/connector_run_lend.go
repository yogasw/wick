package entity

import "time"

// ConnectorRunLend records who really asked for a connector run that went
// out on a lend. The run row itself carries the lender as UserID (the call
// ran with the lender's access and accounts), which alone cannot tell two
// people in one shared thread apart. One row per lent run, keyed by the
// run id; runs that were not lent have none. A separate table on purpose:
// connector_runs is never altered, because adding a column to an existing
// table left stale cached query plans behind the DB pooler.
type ConnectorRunLend struct {
	RunID string `gorm:"type:text;primaryKey"`
	// OnBehalfOf is the lender: whose access and accounts the run used.
	OnBehalfOf string `gorm:"type:text;not null;index:idx_run_lend_lender"`
	// CallerUserID is the person who actually asked: the session owner, or
	// the token holder when there is no session.
	CallerUserID string `gorm:"type:text;index:idx_run_lend_caller"`
	// AgentID is the Team agent whose lend applied, when known.
	AgentID   string    `gorm:"type:text;index:idx_run_lend_agent"`
	SessionID string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name.
func (ConnectorRunLend) TableName() string { return "connector_run_lends" }

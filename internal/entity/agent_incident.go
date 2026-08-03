package entity

import "time"

// Incident status values. Strings so rows stay self-describing for
// ad-hoc queries, like every other status in this schema.
//
// "escalated" is deliberately distinct from "closed": an investigation
// that ran out of iterations, runtime or ideas is a different thing from
// one somebody finished, and the difference is what tells an operator
// whether to look.
const (
	IncidentInvestigating = "investigating"
	IncidentConfirmed     = "confirmed"
	IncidentEscalated     = "escalated"
	IncidentClosed        = "closed"
)

// IncidentStatuses is the complete set, in the order a UI should show it.
var IncidentStatuses = []string{
	IncidentInvestigating,
	IncidentConfirmed,
	IncidentEscalated,
	IncidentClosed,
}

// IsTerminalIncidentStatus reports whether an investigation has stopped.
func IsTerminalIncidentStatus(s string) bool {
	return s == IncidentConfirmed || s == IncidentEscalated || s == IncidentClosed
}

// AgentIncident is what one delegation tree has established so far.
//
// Created LAZILY: a tree that never records evidence and never touches
// the incident op leaves no row. Auto-creating at spawn would put an
// incident behind every delegation tree in the product, most of which are
// code reviews; requiring an explicit "open" would put a step in front of
// a model that will sometimes skip it, leaving every later write to
// handle "no incident".
type AgentIncident struct {
	ID string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	// RootID ties the incident to a delegation tree. Unique: one
	// investigation per conversation, and the index is what makes
	// concurrent lazy creation safe.
	RootID      string `gorm:"type:varchar(64);not null;uniqueIndex" json:"root_id"`
	ProjectID   string `gorm:"type:varchar(64);not null;default:'';index" json:"project_id"`
	TriggeredBy string `gorm:"type:varchar(128);not null;default:'';index" json:"triggered_by"`

	Status string `gorm:"type:varchar(32);not null;index" json:"status"`
	// Iteration counts completed checker rounds.
	Iteration int    `gorm:"not null;default:0" json:"iteration"`
	Title     string `gorm:"type:text;not null;default:''" json:"title"`
	UserIssue string `gorm:"type:text;not null;default:''" json:"user_issue"`
	Summary   string `gorm:"type:text;not null;default:''" json:"summary"`
	// StopReason records WHY an investigation stopped. A loop that ends
	// without saying why is indistinguishable from one still running.
	StopReason string `gorm:"type:text;not null;default:''" json:"stop_reason"`

	// Embedded JSON collections: small, always read whole, never queried
	// by element. Evidence is NOT one of these — it is appended
	// concurrently by many delegations and deduplicated across them,
	// which is exactly what makes it a table instead.
	Hypotheses      string `gorm:"type:text;not null;default:'[]'" json:"hypotheses"`
	MissingEvidence string `gorm:"type:text;not null;default:'[]'" json:"missing_evidence"`
	NextActions     string `gorm:"type:text;not null;default:'[]'" json:"next_actions"`
	ClientContext   string `gorm:"type:text;not null;default:'{}'" json:"client_context"`

	// EvidenceAtRoundStart snapshots the evidence count when the current
	// round began, so "did this round establish anything new?" is a
	// subtraction rather than a judgement call.
	EvidenceAtRoundStart int `gorm:"not null;default:0" json:"evidence_at_round_start"`
	// DryRounds counts consecutive completed rounds that added no
	// evidence. Two in a row stops the loop.
	DryRounds int `gorm:"not null;default:0" json:"dry_rounds"`

	FinalSummary string    `gorm:"type:text;not null;default:''" json:"final_summary"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (AgentIncident) TableName() string { return "agent_incidents" }

// AgentEvidence is one verifiable excerpt an investigator quoted.
//
// Deduplicated per incident by fingerprint, and by a DATABASE CONSTRAINT
// rather than a read-then-write: two investigators finishing at the same
// moment would both pass a "does this exist?" check and both insert.
type AgentEvidence struct {
	ID string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	// IncidentID + Fingerprint is the dedup key. The same log line found
	// by two agents is one piece of evidence, and the second finder is
	// not an error.
	IncidentID  string `gorm:"type:varchar(64);not null;index:idx_evidence_dedup,unique" json:"incident_id"`
	Fingerprint string `gorm:"type:varchar(64);not null;index:idx_evidence_dedup,unique" json:"fingerprint"`
	// DelegationID and Role record WHO found it, so a contradiction can be
	// traced back to the agent that reported each side of it.
	DelegationID string    `gorm:"type:varchar(64);not null;index" json:"delegation_id"`
	Role         string    `gorm:"type:varchar(128);not null;default:''" json:"role"`
	Kind         string    `gorm:"type:varchar(32);not null" json:"kind"`
	Source       string    `gorm:"type:text;not null;default:''" json:"source"`
	Excerpt      string    `gorm:"type:text;not null;default:''" json:"excerpt"`
	CreatedAt    time.Time `json:"created_at"`
}

func (AgentEvidence) TableName() string { return "agent_evidence" }

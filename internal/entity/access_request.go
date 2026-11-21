package entity

import "time"

// AccessRequest is one batch of connector actions an agent asked a human
// to approve because its own access could not run them (a "read only"
// grant, a role that may only read, ...). The agent files it with
// wick_request_access and ends its turn; nothing waits on it. A human
// opens the card (Slack, web), decides each action on its own, and wick
// runs the approved ones with the approver's access.
type AccessRequest struct {
	ID string `gorm:"type:varchar(36);primaryKey"`
	// SessionID is the conversation that asked; its result is delivered
	// back there.
	SessionID string `gorm:"type:text;index:idx_access_req_session"`
	// ProjectID is the session's project, for the project's audit list.
	ProjectID string `gorm:"type:text;not null;default:'';index:idx_access_req_project"`
	// AgentID is the Team agent that asked, when known.
	AgentID string `gorm:"type:text;index:idx_access_req_agent"`
	// AgentName is the agent's display name at the time of the request.
	AgentName string `gorm:"type:text"`
	// RequesterUserID is the wick user the session belongs to.
	RequesterUserID string `gorm:"type:text;index:idx_access_req_requester"`
	// AskedBy is the person whose message the agent was answering when it
	// asked, as the channel resolved it (JSON: id, name, channel, wick
	// user). Empty when no person was behind the turn (a schedule, another
	// agent). Recorded for audit and for rule scripts; never typed by the
	// agent.
	AskedBy string `gorm:"type:text"`
	// Reason is the agent's own words. Shown labelled as an agent claim,
	// never as a fact.
	Reason string `gorm:"type:text"`
	// Status: pending until every item is decided, then done; cancelled
	// when the agent withdrew it or the session went away.
	Status string `gorm:"type:varchar(16);not null;index:idx_access_req_status"`
	// Origin is where the card was posted, as JSON (channel, chat, thread,
	// instance, message ts). Empty for a request with no chat surface.
	Origin    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name.
func (AccessRequest) TableName() string { return "access_requests" }

// AccessRequestItem is one action inside an AccessRequest: an operation on
// one connector instance with exact parameters. The parameters are hashed:
// an approval holds for those parameters only.
type AccessRequestItem struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	RequestID   string `gorm:"type:varchar(36);not null;index:idx_access_item_req"`
	Idx         int    `gorm:"not null"`
	ConnectorID string `gorm:"type:text;not null"`
	// ConnectorKey and labels are copied at request time so the card
	// still reads right after a rename.
	ConnectorKey   string `gorm:"type:text"`
	ConnectorLabel string `gorm:"type:text"`
	OpKey          string `gorm:"type:text;not null"`
	OpName         string `gorm:"type:text"`
	AccountID      string `gorm:"type:text"`
	// Params is the exact JSON object the action runs with.
	Params     string `gorm:"type:text"`
	ParamsHash string `gorm:"type:varchar(64);not null"`
	// Kind is read, write, send or delete. send and delete cannot be
	// approved "always"; delete only "once".
	Kind string `gorm:"type:varchar(16);not null"`
	// Status: pending, running, ok, error, denied.
	Status string `gorm:"type:varchar(16);not null"`
	// Mode the approver picked: once, hour, session, always ("" while
	// pending or when denied).
	Mode string `gorm:"type:varchar(16)"`
	// OnceOnly: this action can only be approved once (a "require" rule
	// matched, or it names no clear target), never for a while.
	OnceOnly  bool       `gorm:"not null;default:false"`
	DecidedBy string     `gorm:"type:text"`
	DecidedAt *time.Time `gorm:""`
	// Note is the approver's reason when denying, or the rule that decided.
	Note   string `gorm:"type:text"`
	RunID  string `gorm:"type:text"`
	Result string `gorm:"type:text"`
	Error  string `gorm:"type:text"`
}

// TableName pins the table name.
func (AccessRequestItem) TableName() string { return "access_request_items" }

// AccessGrant is a standing approval left by a "session" or "always"
// decision: the same operation on the same connector instance runs again
// without a new card, as the approver, for the session (ScopeType
// session) or for the agent (ScopeType agent). Revoked grants keep their
// row for the audit.
type AccessGrant struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	ConnectorID string `gorm:"type:text;not null;index:idx_access_grant_lookup"`
	OpKey       string `gorm:"type:text;not null;index:idx_access_grant_lookup"`
	ScopeType   string `gorm:"type:varchar(16);not null"`
	ScopeID     string `gorm:"type:text;not null;index:idx_access_grant_scope"`
	// Target is what the approved action touched (its target parameters,
	// e.g. "page_id=p1"), so a standing grant covers the same op on the
	// same target only. When the action named no target, Target is
	// "params:<hash>": exactly the approved parameters again.
	Target string `gorm:"type:text;not null;default:''"`
	// ProjectID is the project of the session the grant was made in.
	ProjectID  string    `gorm:"type:text;not null;default:'';index:idx_access_grant_project"`
	ApproverID string    `gorm:"type:text;not null"`
	ItemID     string    `gorm:"type:text"`
	CreatedAt  time.Time `gorm:"not null"`
	// ExpiresAt ends a time-boxed grant ("1 hour"); nil lasts as long as
	// its scope (the session).
	ExpiresAt *time.Time `gorm:"index:idx_access_grant_expiry"`
	RevokedAt *time.Time `gorm:""`
	RevokedBy string     `gorm:"type:text"`
}

// TableName pins the table name.
func (AccessGrant) TableName() string { return "access_grants" }

// AccessRule decides an action that would otherwise need a card: deny it,
// always ask (require), or run it at once (auto_approve) as the rule's
// owner. Doc holds the rule as JSON (see accessreq.RuleDoc). Every save
// bumps Rev and writes an AccessRuleRevision.
type AccessRule struct {
	ID string `gorm:"type:varchar(36);primaryKey"`
	// OwnerID is who an auto-approved action runs as. Only someone who can
	// run the matched connectors themselves makes an auto_approve rule
	// useful: the action runs with their access.
	OwnerID string `gorm:"type:text;not null;index:idx_access_rule_owner"`
	// ProjectID scopes the rule to the sessions of one project; "" is a
	// global rule (admins only).
	ProjectID string `gorm:"type:text;not null;default:'';index:idx_access_rule_project"`
	Name      string `gorm:"type:text;not null"`
	// Effect: deny, require, auto_approve.
	Effect string `gorm:"type:varchar(16);not null"`
	// Simulate keeps an auto_approve rule from acting: a match is only
	// recorded as "would auto-approve".
	Simulate  bool      `gorm:"default:false"`
	Disabled  bool      `gorm:"default:false"`
	Doc       string    `gorm:"type:text;not null"`
	Rev       int       `gorm:"not null"`
	UpdatedBy string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name.
func (AccessRule) TableName() string { return "access_rules" }

// AccessRuleRevision is one saved version of an AccessRule.
type AccessRuleRevision struct {
	ID        string    `gorm:"type:varchar(36);primaryKey"`
	RuleID    string    `gorm:"type:varchar(36);not null;index:idx_access_rule_rev"`
	Rev       int       `gorm:"not null"`
	Doc       string    `gorm:"type:text"`
	By        string    `gorm:"type:text"`
	Reason    string    `gorm:"type:text"`
	Deleted   bool      `gorm:"default:false"`
	CreatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name.
func (AccessRuleRevision) TableName() string { return "access_rule_revisions" }

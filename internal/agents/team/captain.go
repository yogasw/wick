package team

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// ManageAgentsKey is the connector key of the agents.* ops (package
// internal/connectors/team-agents). The scope keeps it switched off for
// every agent without ManageAgents, so the ops never reach tools/list.
const ManageAgentsKey = "agents"

// Access history statuses (entity.AgentAccessHistory.Status).
const (
	AccessPending  = "pending"
	AccessApplied  = "applied"
	AccessDeclined = "declined"
)

// HistoryLimit is how many access changes Settings › Access shows.
const HistoryLimit = 20

// CaptainCan is what the Captain may do to one agent. Access is off by
// default: even when on, an access change is only ever a proposal the
// owner approves.
type CaptainCan struct {
	Persona  bool `json:"persona"`
	Access   bool `json:"access"`
	Routines bool `json:"routines"`
}

// DefaultCaptainCan is persona and routines on, access off.
func DefaultCaptainCan() CaptainCan { return CaptainCan{Persona: true, Routines: true} }

// DecodeCaptainCan parses entity.AgentPersona.CaptainCan over the
// defaults, so a key the row lacks keeps its default. Malformed = the
// defaults.
func DecodeCaptainCan(raw string) CaptainCan {
	c := DefaultCaptainCan()
	if raw == "" {
		return c
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return DefaultCaptainCan()
	}
	return c
}

// EncodeCaptainCan is the inverse of DecodeCaptainCan.
func EncodeCaptainCan(c CaptainCan) string {
	b, _ := json.Marshal(c)
	return string(b)
}

// ManagesAgents is p's effective "Manage other agents" permission: the
// stored value, else on for the Captain only.
func ManagesAgents(p entity.AgentPersona) bool {
	if p.ManageAgents != nil {
		return *p.ManageAgents
	}
	return p.IsCaptain
}

// AccessChange is entity.AgentAccessHistory.Change.
type AccessChange struct {
	// Diff is the readable list: "+Notion (read)", "-Slack",
	// "Loki: read → all".
	Diff []string `json:"diff"`
	// Grants is the checklist a proposal applies on accept.
	Grants []ConnectorGrant `json:"grants,omitempty"`
}

// ErrNotPending is returned when an access proposal is no longer pending
// (decided already, or never existed).
var ErrNotPending = errors.New("team: access change is not pending")

// RecordAccess writes one history row, minting its id and time when
// absent.
func (s *Store) RecordAccess(ctx context.Context, h *entity.AgentAccessHistory, ch AccessChange) error {
	if h.ID == "" {
		h.ID = uuid.New().String()
	}
	if h.At.IsZero() {
		h.At = time.Now().UTC()
	}
	if h.Status == "" {
		h.Status = AccessApplied
	}
	b, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	h.Change = string(b)
	return s.db.WithContext(ctx).Create(h).Error
}

// AccessHistory returns agentID's latest changes, newest first.
func (s *Store) AccessHistory(ctx context.Context, agentID string, limit int) ([]entity.AgentAccessHistory, error) {
	var out []entity.AgentAccessHistory
	err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).
		Order("at DESC").Order("id DESC").Limit(limit).Find(&out).Error
	return out, err
}

// PendingAccess returns the pending proposal behind approvalID.
func (s *Store) PendingAccess(ctx context.Context, approvalID string) (entity.AgentAccessHistory, AccessChange, error) {
	var h entity.AgentAccessHistory
	var ch AccessChange
	if approvalID == "" {
		return h, ch, ErrNotPending
	}
	err := s.db.WithContext(ctx).Where("approval_id = ? AND status = ?", approvalID, AccessPending).First(&h).Error
	if err != nil {
		return h, ch, ErrNotPending
	}
	_ = json.Unmarshal([]byte(h.Change), &ch)
	return h, ch, nil
}

// SettleAccess moves a pending proposal to status. Conditional on it
// still being pending, so two clicks cannot both apply it.
func (s *Store) SettleAccess(ctx context.Context, id, status, decidedBy string) error {
	res := s.db.WithContext(ctx).Model(&entity.AgentAccessHistory{}).
		Where("id = ? AND status = ?", id, AccessPending).
		Updates(map[string]any{"status": status, "decided_by": decidedBy})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotPending
	}
	return nil
}

// OwnerCatalogFunc returns userID's own connector catalog with no agent
// scope applied (connectors.AgentCatalog).
type OwnerCatalogFunc func(ctx context.Context, userID string) ([]connectors.CatalogEntry, error)

// SetOwnerCatalog wires the owner-catalog lookup used to check what a
// Captain proposes. Set at boot.
func (s *Service) SetOwnerCatalog(f OwnerCatalogFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.ownerCatalog = f
	s.mu.Unlock()
}

// OwnerCatalog is userID's own catalog; an error when it is not wired.
func (s *Service) OwnerCatalog(ctx context.Context, userID string) ([]connectors.CatalogEntry, error) {
	s.mu.Lock()
	f := s.ownerCatalog
	s.mu.Unlock()
	if f == nil {
		return nil, errors.New("team: owner catalog is not wired")
	}
	return f(ctx, userID)
}

// ErrNotManager is returned when a session may not run the agents.* ops.
var ErrNotManager = errors.New(`only a Team agent's own chat with "Manage other agents" on can manage other agents`)

// ManagerFor returns the agent of sessionID when that session is the
// agent's OWN (not a sub-agent delegated under it), the agent is enabled
// and has ManageAgents on. Anything else is ErrNotManager.
func (s *Service) ManagerFor(ctx context.Context, sessionID string) (entity.AgentPersona, error) {
	agentID, direct := s.directAgent(sessionID)
	if agentID == "" || !direct {
		return entity.AgentPersona{}, ErrNotManager
	}
	p, err := s.Get(ctx, agentID)
	if err != nil || p.Disabled || !ManagesAgents(p) {
		return entity.AgentPersona{}, ErrNotManager
	}
	return p, nil
}

// ManageAgentsBlock is the Team-block addendum of an agent with "Manage
// other agents" on. Kept to a few lines: the ops' own descriptions carry
// the details.
const ManageAgentsBlock = `## Managing your Team
- You may manage your owner's other agents through the Team agents connector (key agents): list (who is doing what), create, update_persona, set_access.
- A new agent starts with NO access. Access is never yours to grant: set_access only files a request the owner accepts or declines on a card, and it can never exceed the owner's own access.
- Each agent decides what you may touch (Settings › Captain); a refusal means the owner switched it off — tell them, do not work around it.`

// manageAgentsBlock is ManageAgentsBlock for an agent that has the
// permission, "" otherwise.
func manageAgentsBlock(p entity.AgentPersona) string {
	if !ManagesAgents(p) || p.Disabled {
		return ""
	}
	return "\n\n" + ManageAgentsBlock
}

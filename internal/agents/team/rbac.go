package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Roles ("By role" access, AccessRoles): who is chatting decides what of
// the owner's connectors they may borrow through the agent. Two roles are
// fixed: RoleOwner (the agent's owner, everything; a rule may name another
// person owner too, a co-owner who borrows everything the same way, but
// only the real owner changes roles) and RoleGuest (anyone no rule names,
// bots and unknown callers: nothing of their own, only the connectors the
// lend table lends to guest by name). Roles in between are the owner's
// own, ranked 1–99.
//
// A role only narrows. A person keeps their own access to a connector
// (within the agent's checklist) whatever their role, except a guest, who
// gets nothing. What a role changes is the lend: whether a person who
// lacks a connector may run it on the owner's account, and at what level.
const (
	RoleOwner = "owner"
	RoleGuest = "guest"
)

// Rule matches: a wick user by id, or anyone the agent is shared with.
const (
	MatchUser   = "user"
	MatchShared = "shared"
)

// RBACDoc is an agent's role document. The newest revision in agent_rbac
// is in force; with none, DefaultRBACDoc is.
type RBACDoc struct {
	// Roles are the owner's roles between owner and guest, by name.
	Roles map[string]RBACRole `json:"roles"`
	// Rules map callers to roles. First match wins; no match is guest.
	Rules []RBACRule `json:"rules"`
	// Lend is, per connector instance id, the level each role borrows on
	// the owner's account. A role missing from a connector's row borrows
	// at its own default (RBACRole.Lend). A connector missing from Lend is
	// not lent to anyone.
	Lend map[string]map[string]RBACLevel `json:"lend"`
}

// RBACRole is one role's settings.
type RBACRole struct {
	// Rank orders roles (1–99): owner is 100, guest 0.
	Rank int `json:"rank"`
	// Lend is the role's default level on a lent connector: off, read or
	// all.
	Lend string `json:"lend"`
	// WriteNeedsApproval stops write ops on a lend for this role until
	// the owner approves them. The approval flow is not built yet, so for
	// now such a write is refused with a message saying so.
	WriteNeedsApproval bool `json:"write_needs_approval,omitempty"`
}

// RBACRule maps callers to a role.
type RBACRule struct {
	// Match is MatchUser (Value = wick user id) or MatchShared (anyone the
	// agent is shared with; Value unused).
	Match string `json:"match"`
	Value string `json:"value,omitempty"`
	Role  string `json:"role"`
}

// RBACLevel is a lend level: "off", "read", "all", or {"level":"pick",
// "ops":[...]} in JSON.
type RBACLevel struct {
	Level string
	Ops   []string
}

// MarshalJSON writes a plain level as a string and a pick as an object.
func (l RBACLevel) MarshalJSON() ([]byte, error) {
	if l.Level == LevelPick {
		return json.Marshal(struct {
			Level string   `json:"level"`
			Ops   []string `json:"ops"`
		}{LevelPick, l.Ops})
	}
	return json.Marshal(l.Level)
}

// UnmarshalJSON reads either shape.
func (l *RBACLevel) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*l = RBACLevel{Level: s}
		return nil
	}
	var o struct {
		Level string   `json:"level"`
		Ops   []string `json:"ops"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*l = RBACLevel{Level: o.Level, Ops: o.Ops}
	return nil
}

// DefaultRBACDoc is what an agent switched to By role starts with: two
// roles, everyone the agent is shared with is a member, nothing lent.
func DefaultRBACDoc() RBACDoc {
	return RBACDoc{
		Roles: map[string]RBACRole{
			"trusted": {Rank: 70, Lend: LevelAll},
			"member":  {Rank: 40, Lend: LevelRead, WriteNeedsApproval: true},
		},
		Rules: []RBACRule{{Match: MatchShared, Role: "member"}},
		Lend:  map[string]map[string]RBACLevel{},
	}
}

var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

// Validate reports what is wrong with d, nil when it can be saved. It
// never fixes anything: a document is stored as written or not at all.
func (d RBACDoc) Validate() error {
	var errs []string
	for name, r := range d.Roles {
		switch {
		case name == RoleOwner || name == RoleGuest:
			errs = append(errs, fmt.Sprintf("role %q is built in and cannot be defined", name))
		case !roleNameRe.MatchString(name):
			errs = append(errs, fmt.Sprintf("role %q: use a-z, 0-9 or '-', starting with a letter, up to 24 chars", name))
		}
		if r.Rank < 1 || r.Rank > 99 {
			errs = append(errs, fmt.Sprintf("role %q: rank must be 1-99", name))
		}
		if r.Lend != LevelOff && r.Lend != LevelRead && r.Lend != LevelAll {
			errs = append(errs, fmt.Sprintf("role %q: lend must be off, read or all", name))
		}
	}
	known := func(role string) bool { _, ok := d.Roles[role]; return ok || role == RoleGuest || role == RoleOwner }
	for i, r := range d.Rules {
		switch r.Match {
		case MatchUser:
			if strings.TrimSpace(r.Value) == "" {
				errs = append(errs, fmt.Sprintf("rule %d: a user rule needs the user id in value", i+1))
			}
		case MatchShared:
		default:
			errs = append(errs, fmt.Sprintf("rule %d: match must be user or shared", i+1))
		}
		if !known(r.Role) {
			errs = append(errs, fmt.Sprintf("rule %d: unknown role %q", i+1, r.Role))
		}
	}
	for cid, row := range d.Lend {
		if strings.TrimSpace(cid) == "" {
			errs = append(errs, "lend: empty connector id")
		}
		for role, lv := range row {
			if _, ok := d.Roles[role]; !ok && role != RoleGuest {
				errs = append(errs, fmt.Sprintf("lend %s: unknown role %q (owner borrows everything already and is never listed)", cid, role))
			}
			switch lv.Level {
			case LevelOff, LevelRead, LevelAll:
			case LevelPick:
				if len(lv.Ops) == 0 {
					errs = append(errs, fmt.Sprintf("lend %s/%s: pick needs at least one op", cid, role))
				}
			default:
				errs = append(errs, fmt.Sprintf("lend %s/%s: level must be off, read, all or pick", cid, role))
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return errors.New(strings.Join(errs, "; "))
}

// RoleFor is the role a caller gets: owner for the agent's owner, else
// the first rule that matches, else guest. shared says whether the agent
// is shared with the caller; it is only asked when a shared rule is
// reached. An empty caller (unknown, or wick's own internal principal) is
// a guest: failing to know who is asking must never widen access.
func (d RBACDoc) RoleFor(ownerID, callerID string, shared func() bool) string {
	if callerID == "" || callerID == internalPrincipal {
		return RoleGuest
	}
	if callerID == ownerID {
		return RoleOwner
	}
	for _, r := range d.Rules {
		switch r.Match {
		case MatchUser:
			if r.Value == callerID {
				return d.knownRole(r.Role)
			}
		case MatchShared:
			if shared != nil && shared() {
				return d.knownRole(r.Role)
			}
		}
	}
	return RoleGuest
}

// knownRole is role, or guest when the document no longer defines it.
func (d RBACDoc) knownRole(role string) string {
	if role == RoleOwner {
		return RoleOwner
	}
	if _, ok := d.Roles[role]; ok {
		return role
	}
	return RoleGuest
}

// LendLevel is what role borrows of connectorID on the owner's account.
// Owner borrows nothing (it is theirs); a connector the document does not
// lend gets off. Guest has no default level: it borrows only where a
// connector's row names guest explicitly.
func (d RBACDoc) LendLevel(role, connectorID string) RBACLevel {
	if role == RoleGuest {
		if lv, set := d.Lend[connectorID][RoleGuest]; set {
			return lv
		}
		return RBACLevel{Level: LevelOff}
	}
	r, ok := d.Roles[role]
	if !ok {
		return RBACLevel{Level: LevelOff}
	}
	row, lent := d.Lend[connectorID]
	if !lent {
		return RBACLevel{Level: LevelOff}
	}
	if lv, set := row[role]; set {
		return lv
	}
	return RBACLevel{Level: r.Lend}
}

// lentIDs are the connectors some role borrows at more than off.
func (d RBACDoc) lentIDs() []string {
	var out []string
	for cid := range d.Lend {
		if d.LendLevel(RoleGuest, cid).Level != LevelOff {
			out = append(out, cid)
			continue
		}
		for role := range d.Roles {
			if d.LendLevel(role, cid).Level != LevelOff {
				out = append(out, cid)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// HasCoOwner reports whether a rule makes someone besides the agent's
// owner an owner: then every connector of the agent can be borrowed.
func (d RBACDoc) HasCoOwner() bool {
	for _, r := range d.Rules {
		if r.Role == RoleOwner {
			return true
		}
	}
	return false
}

// internalPrincipal mirrors mcp.InternalAgentUserID (not imported here):
// wick's own internal token, which is nobody in particular.
const internalPrincipal = "wick-agent-internal"

// RBACRevision is one saved revision, newest first in RBACHistory.
type RBACRevision struct {
	Rev       int       `json:"rev"`
	Doc       RBACDoc   `json:"doc"`
	UpdatedBy string    `json:"updated_by"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// RBAC returns agentID's role document in force and its revision; with
// no revision saved yet, DefaultRBACDoc at rev 0.
func (s *Store) RBAC(ctx context.Context, agentID string) (RBACDoc, int, error) {
	var row entity.AgentRBAC
	err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Order("rev DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DefaultRBACDoc(), 0, nil
	}
	if err != nil {
		return RBACDoc{}, 0, err
	}
	var d RBACDoc
	if err := json.Unmarshal([]byte(row.Doc), &d); err != nil {
		return RBACDoc{}, 0, fmt.Errorf("agent %s rbac rev %d: %w", agentID, row.Rev, err)
	}
	return d, row.Rev, nil
}

// ErrRBACConflict is SetRBAC's answer when the document changed since the
// revision the caller read.
var ErrRBACConflict = errors.New("the roles changed since you read them; read them again and retry")

// SetRBAC validates d and saves it as agentID's next revision. baseRev is
// the revision the change was made from: another save in between returns
// ErrRBACConflict instead of silently overwriting it.
func (s *Store) SetRBAC(ctx context.Context, agentID string, d RBACDoc, baseRev int, by, reason string) (int, error) {
	if err := d.Validate(); err != nil {
		return 0, err
	}
	if d.Lend == nil {
		d.Lend = map[string]map[string]RBACLevel{}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return 0, err
	}
	var rev int
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur entity.AgentRBAC
		e := tx.Where("agent_id = ?", agentID).Order("rev DESC").First(&cur).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if cur.Rev != baseRev {
			return ErrRBACConflict
		}
		rev = cur.Rev + 1
		return tx.Create(&entity.AgentRBAC{
			ID: uuid.NewString(), AgentID: agentID, Rev: rev, Doc: string(raw),
			UpdatedBy: by, Reason: strings.TrimSpace(reason), CreatedAt: time.Now().UTC(),
		}).Error
	})
	if err != nil {
		return 0, err
	}
	return rev, nil
}

// RBACHistory lists agentID's saved revisions, newest first, at most limit.
func (s *Store) RBACHistory(ctx context.Context, agentID string, limit int) ([]RBACRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var rows []entity.AgentRBAC
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Order("rev DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]RBACRevision, 0, len(rows))
	for _, r := range rows {
		var d RBACDoc
		if err := json.Unmarshal([]byte(r.Doc), &d); err != nil {
			continue
		}
		out = append(out, RBACRevision{Rev: r.Rev, Doc: d, UpdatedBy: r.UpdatedBy, Reason: r.Reason, At: r.CreatedAt})
	}
	return out, nil
}

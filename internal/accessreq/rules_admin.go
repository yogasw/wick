package accessreq

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/entity"
)

var errNotRuleOwner = errors.New("only the rule's owner or an admin can do that")

// Rules and facts are only ever changed by a signed-in person (the web
// page), never by an agent tool: a rule that lives where an agent can
// write would let it approve itself.

// RuleView is a rule as the rules page lists it.
type RuleView struct {
	entity.AccessRule
	OwnerName string
	YAML      string
	CanEdit   bool
}

// Rules lists every rule with who may edit it.
func (s *Service) Rules(ctx context.Context, viewerID string) ([]RuleView, error) {
	rules, err := s.store.rules(ctx)
	if err != nil {
		return nil, err
	}
	viewer, _ := s.person(ctx, viewerID)
	out := make([]RuleView, 0, len(rules))
	for _, r := range rules {
		rv := RuleView{AccessRule: r, OwnerName: s.personName(ctx, r.OwnerID)}
		if d, err := ParseRule(r.Doc); err == nil {
			rv.YAML = d.YAML()
		} else {
			rv.YAML = r.Doc
		}
		rv.CanEdit = viewer.IsAdmin || r.OwnerID == viewerID
		out = append(out, rv)
	}
	return out, nil
}

func (s *Service) person(ctx context.Context, id string) (Person, bool) {
	if s.dir == nil || id == "" {
		return Person{}, false
	}
	return s.dir.User(ctx, id)
}

// SaveRule creates (id "") or updates a rule from YAML/JSON. The saver
// becomes the owner of a new rule; an existing rule keeps its owner and
// only its owner or an admin may change it.
func (s *Service) SaveRule(ctx context.Context, byID, id, raw, reason string) (*entity.AccessRule, error) {
	by, ok := s.person(ctx, byID)
	if !ok || !by.Approved {
		return nil, errors.New("only an approved wick user can save rules")
	}
	d, err := ParseRule(raw)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var r *entity.AccessRule
	create := id == ""
	if create {
		r = &entity.AccessRule{ID: uuid.NewString(), OwnerID: byID, CreatedAt: now}
	} else {
		r, err = s.store.rule(ctx, id)
		if err != nil {
			return nil, err
		}
		if r.OwnerID != byID && !by.IsAdmin {
			return nil, errors.New("only the rule's owner or an admin can change it")
		}
	}
	r.Name = strings.TrimSpace(d.Name)
	r.Effect = d.Effect
	r.Simulate = d.Simulate
	r.Disabled = d.Disabled
	r.Doc = d.JSON()
	r.Rev++
	r.UpdatedBy = byID
	r.UpdatedAt = now
	rev := &entity.AccessRuleRevision{ID: uuid.NewString(), RuleID: r.ID, Rev: r.Rev, Doc: r.Doc, By: byID,
		Reason: strings.TrimSpace(reason), CreatedAt: now}
	if err := s.store.saveRule(ctx, r, rev, create); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteRule removes a rule, keeping a final revision for the audit.
func (s *Service) DeleteRule(ctx context.Context, byID, id, reason string) error {
	by, ok := s.person(ctx, byID)
	if !ok || !by.Approved {
		return errors.New("only an approved wick user can delete rules")
	}
	r, err := s.store.rule(ctx, id)
	if err != nil {
		return err
	}
	if r.OwnerID != byID && !by.IsAdmin {
		return errors.New("only the rule's owner or an admin can delete it")
	}
	rev := &entity.AccessRuleRevision{ID: uuid.NewString(), RuleID: r.ID, Rev: r.Rev + 1, Doc: r.Doc, By: byID,
		Reason: strings.TrimSpace(reason), Deleted: true, CreatedAt: s.now()}
	return s.store.deleteRule(ctx, r, rev)
}

// RuleHistory lists a rule's revisions, newest first.
func (s *Service) RuleHistory(ctx context.Context, id string) ([]entity.AccessRuleRevision, error) {
	return s.store.ruleHistory(ctx, id, 20)
}

// SimulateResult is what the rules would decide for one sample action.
type SimulateResult struct {
	Effect    string   `json:"effect"`
	Rule      string   `json:"rule,omitempty"`
	Simulated []string `json:"simulated,omitempty"`
	Kind      string   `json:"kind"`
}

// Simulate evaluates the rules for a sample action without running it.
func (s *Service) Simulate(ctx context.Context, a Action, destructive bool) (SimulateResult, error) {
	if a.ConnectorKey == "" && a.ConnectorID != "" {
		// An instance id resolves to its key; anything else is taken as
		// the key itself ("notion"), for trying a rule before an
		// instance exists.
		a.ConnectorKey = a.ConnectorID
		if s.cat != nil {
			if meta, err := s.cat.Describe(ctx, a.ConnectorID, a.OpKey); err == nil {
				a.ConnectorKey = meta.ConnectorKey
				destructive = meta.Destructive
			}
		}
	}
	if a.Kind == "" {
		a.Kind = ClassifyOp(a.OpKey, destructive)
	}
	v := s.Evaluate(ctx, a)
	out := SimulateResult{Effect: v.Effect, Simulated: v.Simulated, Kind: a.Kind}
	if out.Effect == "" {
		out.Effect = "card"
	}
	if v.Rule != nil {
		out.Rule = v.Rule.Name
	}
	return out, nil
}

// Facts lists every fact.
func (s *Service) Facts(ctx context.Context) ([]entity.AccessFact, error) {
	return s.store.facts(ctx)
}

// SetFact writes one fact. Admin only: an admin's auto_approve rule reads
// facts for every session, so marking an app "staging" is an admin call.
func (s *Service) SetFact(ctx context.Context, byID, fact, subject, value string) error {
	by, ok := s.person(ctx, byID)
	if !ok || !by.IsAdmin {
		return errors.New("only an admin can change facts")
	}
	fact, subject, value = strings.TrimSpace(fact), strings.TrimSpace(subject), strings.TrimSpace(value)
	if fact == "" || subject == "" || value == "" {
		return errors.New("fact, subject and value are required")
	}
	if !strings.Contains(fact, ".") {
		return errors.New(`a fact is named <subject>.<attribute>, e.g. "app.env"`)
	}
	return s.store.setFact(ctx, entity.AccessFact{Fact: fact, Subject: subject, Value: value, UpdatedBy: byID, UpdatedAt: s.now()})
}

// DeleteFact removes one fact. Admin only.
func (s *Service) DeleteFact(ctx context.Context, byID, fact, subject string) error {
	by, ok := s.person(ctx, byID)
	if !ok || !by.IsAdmin {
		return errors.New("only an admin can change facts")
	}
	return s.store.deleteFact(ctx, fact, subject)
}

package accessreq

import (
	"context"

	"github.com/yogasw/wick/internal/entity"
)

// Rules are only ever changed by a signed-in manager of the project (its
// settings), never by an agent tool: a rule that lives where an agent can
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

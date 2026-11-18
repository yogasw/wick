package accessreq

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/entity"
)

// Project-level access: each project keeps its own access-request history
// and its own rules. The HTTP layer lets only the project's managers in;
// the service checks what is left (a global rule is an admin's call).

// ProjectAccessView is what the project's Access requests card reads from
// the database: rules and live grants. The request history lives in the
// sessions' files (see the web layer).
type ProjectAccessView struct {
	Rules     []RuleView           `json:"rules"`
	Grants    []entity.AccessGrant `json:"grants"`
	FactNames []string             `json:"fact_names"`
}

// ProjectAccess lists the project's rules and its live session grants.
func (s *Service) ProjectAccess(ctx context.Context, projectID, viewerID string) (ProjectAccessView, error) {
	out := ProjectAccessView{FactNames: FactNames()}
	rules, err := s.Rules(ctx, viewerID)
	if err != nil {
		return out, err
	}
	for _, r := range rules {
		if r.ProjectID == projectID {
			r.CanEdit = true // the HTTP layer already checked the manager
			out.Rules = append(out.Rules, r)
		}
	}
	out.Grants, _ = s.store.projectGrants(ctx, projectID)
	return out, nil
}

// RequestRetention is how long a finished request stays in the database.
// Its history is kept in the session's files.
const RequestRetention = 30 * 24 * time.Hour

// PruneFinished drops finished requests older than RequestRetention.
func (s *Service) PruneFinished(ctx context.Context) (int64, error) {
	return s.store.pruneFinished(ctx, s.now().Add(-RequestRetention))
}

// SaveProjectRule creates or updates a rule of projectID. byID must be a
// manager of the project (checked by the caller).
func (s *Service) SaveProjectRule(ctx context.Context, byID, projectID, id, raw, reason string) (*entity.AccessRule, error) {
	by, ok := s.person(ctx, byID)
	if !ok || !by.Approved {
		return nil, errors.New("only an approved wick user can save rules")
	}
	if projectID == "" {
		return nil, errors.New("a rule belongs to a project")
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
		if r, err = s.store.rule(ctx, id); err != nil {
			return nil, err
		}
		if r.ProjectID != projectID {
			return nil, errRuleOtherProject
		}
	}
	r.ProjectID = projectID
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

// DeleteProjectRule removes a rule of projectID (or a global one, admin).
func (s *Service) DeleteProjectRule(ctx context.Context, byID, projectID, id, reason string) error {
	by, ok := s.person(ctx, byID)
	if !ok || !by.Approved {
		return errors.New("only an approved wick user can delete rules")
	}
	r, err := s.store.rule(ctx, id)
	if err != nil {
		return err
	}
	if r.ProjectID != projectID || projectID == "" {
		return errRuleOtherProject
	}
	rev := &entity.AccessRuleRevision{ID: uuid.NewString(), RuleID: r.ID, Rev: r.Rev + 1, Doc: r.Doc, By: byID,
		Reason: strings.TrimSpace(reason), Deleted: true, CreatedAt: s.now()}
	return s.store.deleteRule(ctx, r, rev)
}

var errRuleOtherProject = errors.New("this rule belongs to another project")

// SimulateProjectScript runs one rule's script for a sample action, when
// the rule is the project's own or a global one.
func (s *Service) SimulateProjectScript(ctx context.Context, byID, projectID, ruleID string, a Action, viewerID string) (ScriptResult, error) {
	r, err := s.store.rule(ctx, ruleID)
	if err != nil {
		return ScriptResult{}, err
	}
	if r.ProjectID != projectID || projectID == "" {
		return ScriptResult{}, errRuleOtherProject
	}
	return s.simulateScript(ctx, r, a, viewerID)
}

// RevokeProjectGrant revokes a "this session" grant made in projectID.
func (s *Service) RevokeProjectGrant(ctx context.Context, byID, projectID, grantID string) error {
	g, err := s.store.grant(ctx, grantID)
	if err != nil {
		return err
	}
	if g.ProjectID != projectID {
		return errors.New("this grant belongs to another project")
	}
	return s.store.revokeGrant(ctx, grantID, byID)
}

// PersonName is a user's display name for lists ("" when unknown).
func (s *Service) PersonName(ctx context.Context, id string) string { return s.personName(ctx, id) }

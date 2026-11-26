package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/accessreq"
	"github.com/yogasw/wick/internal/agents/project"
)

// Access rules as a requestable action: an agent may propose a project's
// access rule with wick_request_access (tool_id
// conn:wick-access-rules/save_rule), and it is saved only when the
// project's owner (or an admin) approves the card. Rules stay a person's
// decision; the agent only drafts them. There is no other way for an agent
// to write a rule.
const (
	rulesConnectorID = accessreq.RulesConnectorID
	rulesOpSave      = "save_rule"
)

// accessRules is the service that saves rules, set once the access
// service exists (it is built after the catalog).
var accessRules *accessreq.Service

func isRulesAction(connectorID string) bool { return connectorID == rulesConnectorID }

func describeRulesAction(opKey string) (accessreq.ActionMeta, error) {
	if opKey != rulesOpSave {
		return accessreq.ActionMeta{}, fmt.Errorf("operation %q does not exist on %s (only %s)", opKey, rulesConnectorID, rulesOpSave)
	}
	return accessreq.ActionMeta{ConnectorKey: "wick_access_rules", ConnectorLabel: "Access rules (wick)",
		OpName: "Save access rule", Destructive: true}, nil
}

// rulesParams reads save_rule's params: project_id, doc (YAML or JSON),
// rule_id (empty = a new rule) and an optional reason.
func rulesParams(params map[string]any) (projectID, ruleID, doc, reason string, err error) {
	str := func(k string) string {
		v, _ := params[k].(string)
		return strings.TrimSpace(v)
	}
	projectID, ruleID, doc, reason = str("project_id"), str("rule_id"), str("doc"), str("reason")
	if projectID == "" || doc == "" {
		return "", "", "", "", errors.New("save_rule needs project_id and doc (the rule as YAML)")
	}
	if _, perr := accessreq.ParseRule(doc); perr != nil {
		return "", "", "", "", fmt.Errorf("doc is not a valid rule: %w", perr)
	}
	return projectID, ruleID, doc, reason, nil
}

// runRulesAction saves the rule as userID, who must own the project or be
// an admin.
func (a accessCatalog) runRulesAction(ctx context.Context, userID, opKey string, params map[string]any) (string, string, error) {
	if _, err := describeRulesAction(opKey); err != nil {
		return "", "", err
	}
	if accessRules == nil {
		return "", "", errors.New("access rules are not available on this wick")
	}
	pid, rid, doc, reason, err := rulesParams(params)
	if err != nil {
		return "", "", err
	}
	if !a.ownsProject(ctx, userID, pid) {
		return "", "", fmt.Errorf("project %q was not found, or only its owner or an admin can approve a rule for it", pid)
	}
	if reason == "" {
		reason = "proposed by an agent, approved on a card"
	}
	r, err := accessRules.SaveProjectRule(ctx, userID, pid, rid, doc, reason)
	if err != nil {
		return "", "", err
	}
	b, _ := json.Marshal(map[string]any{"rule_id": r.ID, "name": r.Name, "effect": r.Effect, "rev": r.Rev, "project_id": pid})
	return "", string(b), nil
}

// ownsProject reports whether userID may approve a rule for projectID:
// the project must exist, and userID own it or be an admin.
func (a accessCatalog) ownsProject(ctx context.Context, userID, projectID string) bool {
	if userID == "" || projectID == "" {
		return false
	}
	p, err := project.Load(a.layout, projectID)
	if err != nil {
		return false
	}
	if p.Meta.OwnerUserID == userID {
		return true
	}
	u, err := a.users.GetUserByID(ctx, userID)
	return err == nil && u != nil && u.IsAdmin()
}

package api

import (
	"context"
	"strings"
	"testing"
)

const testRuleDoc = "name: prod-card\neffect: require\nwhen:\n  ops: [notes.*]\n"

func TestRulesActionDescribeAndParams(t *testing.T) {
	if !isRulesAction(rulesConnectorID) || isRulesAction("c-notes") {
		t.Fatal("isRulesAction")
	}
	if m, err := describeRulesAction(rulesOpSave); err != nil || !m.Destructive || m.ConnectorKey == "" {
		t.Fatalf("describe = %+v %v", m, err)
	}
	if _, err := describeRulesAction("delete_everything"); err == nil {
		t.Fatal("unknown op described")
	}
	pid, rid, doc, _, err := rulesParams(map[string]any{"project_id": " p1 ", "doc": testRuleDoc})
	if err != nil || pid != "p1" || rid != "" || !strings.Contains(doc, "prod-card") {
		t.Fatalf("params = %q %q %v", pid, rid, err)
	}
	if _, _, _, _, err := rulesParams(map[string]any{"doc": testRuleDoc}); err == nil {
		t.Fatal("missing project accepted")
	}
	if _, _, _, _, err := rulesParams(map[string]any{"project_id": "p1", "doc": "effect: nope"}); err == nil {
		t.Fatal("invalid rule accepted")
	}
}

func TestRulesActionRefusesWithoutService(t *testing.T) {
	prev := accessRules
	accessRules = nil
	t.Cleanup(func() { accessRules = prev })
	if _, _, err := (accessCatalog{}).runRulesAction(context.Background(), "u1", rulesOpSave, map[string]any{"project_id": "p1", "doc": testRuleDoc}); err == nil {
		t.Fatal("ran without the access service")
	}
}

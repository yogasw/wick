package accessreq

import (
	"context"
	"strings"
	"testing"
)

// captainDir is fakeDir with access requests off in every project and a
// Captain auto-approve setting that names owner-a for sess-1 only.
type captainDir struct {
	fakeDir
	on bool
}

func (d captainDir) AccessRequestsOn(string) bool { return false }

func (d captainDir) CaptainRuleOwner(_ context.Context, sid string) string {
	if d.on && sid == "sess-1" {
		return "owner-a"
	}
	return ""
}

func captainEnv(t *testing.T, on bool) *env {
	e := newEnv(t)
	e.cat.ops[RulesConnectorID+".save_rule"] = ActionMeta{ConnectorKey: "wick_access_rules", ConnectorLabel: "Access rules", Destructive: true}
	e.svc.dir = captainDir{fakeDir: e.svc.dir.(fakeDir), on: on}
	return e
}

func TestCaptainPolicyAppliesRuleSaveAsOwner(t *testing.T) {
	e := captainEnv(t, true)
	v, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "add a rule",
		Actions: []ActionInput{act(RulesConnectorID, "save_rule", map[string]any{"project_id": "proj-9", "doc": "name: x"})}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(e.cat.runs) != 1 || e.cat.runs[0].user != "owner-a" || e.cat.runs[0].connector != RulesConnectorID {
		t.Fatalf("runs = %+v, want one save_rule run as owner-a", e.cat.runs)
	}
	if v.Items[0].Status != ItemOK || !strings.Contains(v.Items[0].DecidedBy, CaptainPolicyRuleName) {
		t.Fatalf("item = %+v, want ok, decided by the Captain setting", v.Items[0])
	}
	if v.AutoRan {
		t.Fatal("the card must stay posted for the record")
	}
}

func TestCaptainPolicyCoversRuleSavesOnly(t *testing.T) {
	e := captainEnv(t, true)
	_, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "mixed",
		Actions: []ActionInput{
			act(RulesConnectorID, "save_rule", map[string]any{"project_id": "proj-9", "doc": "name: x"}),
			act("c-notes", "update_page", map[string]any{"page_id": "p1"}),
		}})
	if err != ErrOff {
		t.Fatalf("a request mixing other actions in a project with requests off = %v, want ErrOff", err)
	}
	if len(e.cat.runs) != 0 {
		t.Fatalf("runs = %+v", e.cat.runs)
	}
}

func TestCaptainPolicyOffStaysOff(t *testing.T) {
	e := captainEnv(t, false)
	_, err := e.svc.Create(context.Background(), CreateInput{SessionID: "sess-1", AgentID: "agent-1", Reason: "add a rule",
		Actions: []ActionInput{act(RulesConnectorID, "save_rule", map[string]any{"project_id": "proj-9", "doc": "name: x"})}})
	if err != ErrOff {
		t.Fatalf("setting off = %v, want ErrOff", err)
	}
	if e.svc.CaptainRulesOn(context.Background(), "sess-1") {
		t.Fatal("CaptainRulesOn with the setting off")
	}
}

func TestCaptainPolicyRuleDocIsValid(t *testing.T) {
	d, err := ParseRule(captainPolicyRule("owner-a").Doc)
	if err != nil {
		t.Fatalf("captain policy rule doc does not parse: %v", err)
	}
	if !d.ShowCard {
		t.Fatal("captain policy rule must keep the card")
	}
}

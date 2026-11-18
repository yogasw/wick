package accessreq

import (
	"context"
	"strings"
	"testing"
)

const stagingAuto = `name: staging-auto
effect: auto_approve
when:
  ops: [apps.*]
  kind: [read, write]
  all:
    - fact: app.env
      equals: staging
`

func TestParseRuleValidation(t *testing.T) {
	bad := map[string]string{
		"no name":         "effect: deny\nwhen: {ops: [x.*]}",
		"bad effect":      "name: a\neffect: allow\nwhen: {ops: [x.*]}",
		"no ops":          "name: a\neffect: deny\nwhen: {kind: [write]}",
		"auto no kind":    "name: a\neffect: auto_approve\nwhen: {ops: [x.*]}",
		"auto delete":     "name: a\neffect: auto_approve\nwhen: {ops: [x.*], kind: [write, delete]}",
		"two tests":       "name: a\neffect: deny\nwhen: {ops: [x.*], all: [{fact: app, equals: a, in: [b]}]}",
		"bad kind":        "name: a\neffect: deny\nwhen: {ops: [x.*], kind: [update]}",
		"no fact name":    "name: a\neffect: deny\nwhen: {ops: [x.*], all: [{equals: a}]}",
		"not yaml at all": "{{{",
	}
	for name, raw := range bad {
		if _, err := ParseRule(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	d, err := ParseRule(stagingAuto)
	if err != nil {
		t.Fatalf("good rule refused: %v", err)
	}
	// Round trip through the stored JSON.
	if d2, err := ParseRule(d.JSON()); err != nil || d2.Name != "staging-auto" || len(d2.When.All) != 1 {
		t.Fatalf("json round trip: %+v %v", d2, err)
	}
}

func saveRule(t *testing.T, e *env, by, raw string) {
	t.Helper()
	if _, err := e.svc.SaveRule(context.Background(), by, "", raw, "test"); err != nil {
		t.Fatalf("save rule: %v", err)
	}
}

func TestAutoApproveNeedsTheFact(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	ctx := context.Background()
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	// Unknown app: fail closed.
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("auto-approved without the fact: %q", runAs)
	}
	if err := e.svc.SetFact(ctx, "owner-a", "app.env", "acme-stag", "staging"); err == nil {
		t.Fatal("a non-admin changed a fact")
	}
	if err := e.svc.SetFact(ctx, "admin-z", "app.env", "acme-stag", "staging"); err != nil {
		t.Fatal(err)
	}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "owner-a" {
		t.Fatalf("staging app not auto-approved: %q", runAs)
	}
	// The same rule, a production app.
	a.Params = map[string]string{"app_id": "acme-prod"}
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-prod", "production")
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("production app auto-approved: %q", runAs)
	}
}

func TestDenyBeatsAutoApprove(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	saveRule(t, e, "owner-a", "name: no-apps\neffect: deny\nwhen: {ops: [apps.update_*]}")
	ctx := context.Background()
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-stag", "staging")
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	runAs, _, err := e.svc.Check(ctx, a)
	if err == nil || runAs != "" || !strings.Contains(err.Error(), "no-apps") {
		t.Fatalf("deny should win: %q %v", runAs, err)
	}
	if _, err := e.svc.Create(ctx, CreateInput{SessionID: "sess-1", Actions: []ActionInput{act("c-apps", "update_setting", map[string]any{"app_code": "acme-stag"})}}); err == nil {
		t.Fatal("a denied action was filed")
	}
}

func TestRequireBeatsAutoApprove(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	saveRule(t, e, "owner-a", "name: always-ask\neffect: require\nwhen: {ops: [apps.*]}")
	ctx := context.Background()
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-stag", "staging")
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	if runAs, _, err := e.svc.Check(ctx, a); runAs != "" || err != nil {
		t.Fatalf("require should leave it to a card: %q %v", runAs, err)
	}
}

func TestAutoApproveAtCreateRunsAsRuleOwner(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	ctx := context.Background()
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-stag", "staging")
	v := e.create(t,
		act("c-apps", "update_setting", map[string]any{"app_code": "acme-stag"}),
		act("c-notes", "update_page", map[string]any{"page_id": "p1"}),
	)
	if len(e.cat.runs) != 1 || e.cat.runs[0].user != "owner-a" || e.cat.runs[0].connector != "c-apps" {
		t.Fatalf("runs = %+v", e.cat.runs)
	}
	got, _ := e.svc.ViewFor(ctx, v.ID, "")
	if got.Items[0].Status != ItemOK || got.Items[0].DecidedBy != "rule staging-auto" {
		t.Fatalf("auto item = %+v", got.Items[0])
	}
	if got.Items[1].Status != ItemPending || got.Status != StatusPending {
		t.Fatalf("manual item should still wait: %+v", got.Items[1])
	}
}

func TestSimulateRuleDoesNotAct(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", "simulate: true\n"+stagingAuto)
	ctx := context.Background()
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-stag", "staging")
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("simulate rule acted: %q", runAs)
	}
	res, _ := e.svc.Simulate(ctx, a, true)
	if res.Effect != "card" || len(res.Simulated) != 1 {
		t.Fatalf("simulate = %+v", res)
	}
	v := e.create(t, act("c-apps", "update_setting", map[string]any{"app_code": "acme-stag"}))
	if !strings.Contains(v.Items[0].Note, "would auto-approve") || len(e.cat.runs) != 0 {
		t.Fatalf("note = %q runs = %d", v.Items[0].Note, len(e.cat.runs))
	}
}

func TestNonAdminRuleCoversOnlyOwnSessions(t *testing.T) {
	e := newEnv(t)
	// helper-b's rule must not touch owner-a's session; admin-z's does.
	e.cat.can["helper-b|c-apps"] = true
	saveRule(t, e, "helper-b", strings.Replace(stagingAuto, "staging-auto", "helper-auto", 1))
	ctx := context.Background()
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-stag", "staging")
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("another user's rule applied: %q", runAs)
	}
	a.SessionID = "sess-h"
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "helper-b" {
		t.Fatalf("own rule should apply in own session: %q", runAs)
	}
	saveRule(t, e, "admin-z", strings.Replace(stagingAuto, "staging-auto", "admin-auto", 1))
	a.SessionID = "sess-1"
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "admin-z" {
		t.Fatalf("admin rule should apply everywhere: %q", runAs)
	}
}

func TestRuleEditOnlyByOwnerOrAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r, err := e.svc.SaveRule(ctx, "owner-a", "", stagingAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveRule(ctx, "stranger", r.ID, stagingAuto, ""); err == nil {
		t.Fatal("a stranger edited the rule")
	}
	if _, err := e.svc.SaveRule(ctx, "admin-z", r.ID, strings.Replace(stagingAuto, "write]", "write, send]", 1), "widen"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteRule(ctx, "stranger", r.ID, ""); err == nil {
		t.Fatal("a stranger deleted the rule")
	}
	if err := e.svc.DeleteRule(ctx, "owner-a", r.ID, "done"); err != nil {
		t.Fatal(err)
	}
	h, _ := e.svc.RuleHistory(ctx, r.ID)
	if len(h) != 3 || !h[0].Deleted {
		t.Fatalf("history = %+v", h)
	}
}

func TestAgentMatchByNameOrID(t *testing.T) {
	e := newEnv(t)
	d, _ := ParseRule("name: a\neffect: deny\nwhen: {ops: ['*'], agents: ['@agent-1']}")
	ctx := context.Background()
	if !e.svc.Matches(ctx, d, Action{AgentID: "x", AgentName: "@agent-1", ConnectorKey: "k", OpKey: "o"}) {
		t.Error("name match failed")
	}
	if e.svc.Matches(ctx, d, Action{AgentID: "other", ConnectorKey: "k", OpKey: "o"}) {
		t.Error("matched another agent")
	}
}

// A connector bound to one app (its app_code in config, like a session
// connector) is judged by that app; a parameter naming another app makes
// the subject unknown instead of letting it pass.
func TestAppFromConnectorConfig(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	ctx := context.Background()
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-stag", "staging")
	_ = e.svc.SetFact(ctx, "admin-z", "app.env", "acme-prod", "production")
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Config: map[string]string{"app_code": "acme-stag"}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "owner-a" {
		t.Fatalf("config app_code staging: %q", runAs)
	}
	a.Params = map[string]string{"app_code": "acme-stag"}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "owner-a" {
		t.Fatalf("same app in both: %q", runAs)
	}
	a.Config = map[string]string{"app_code": "acme-prod"}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("param says staging, connector is prod: auto-approved as %q", runAs)
	}
}

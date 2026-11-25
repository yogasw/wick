package accessreq

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const stagingAuto = `name: staging-auto
effect: auto_approve
when:
  ops: [apps.*]
  kind: [read, write]
  all:
    - fact: app
      in: [acme-stag]
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
	if _, err := e.svc.SaveProjectRule(context.Background(), by, "proj-1", "", raw, "test"); err != nil {
		t.Fatalf("save rule: %v", err)
	}
}

func TestAutoApproveNeedsAKnownApp(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	ctx := context.Background()
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite}
	// No app named at all: fail closed.
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("auto-approved without an app: %q", runAs)
	}
	a.Params = map[string]string{"app_code": "acme-stag"}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "owner-a" {
		t.Fatalf("listed app not auto-approved: %q", runAs)
	}
	a.Params = map[string]string{"app_id": "acme-prod"}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("unlisted app auto-approved: %q", runAs)
	}
}

func TestDenyBeatsAutoApprove(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	saveRule(t, e, "owner-a", "name: no-apps\neffect: deny\nwhen: {ops: [apps.update_*]}")
	ctx := context.Background()
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

func TestProjectRuleCoversOnlyItsProject(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto) // proj-1
	ctx := context.Background()
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "owner-a" {
		t.Fatalf("rule should apply in its project: %q", runAs)
	}
	a.SessionID = "sess-h" // proj-2
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("another project's rule applied: %q", runAs)
	}
	a.SessionID = "sess-unknown" // no project
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("a rule applied to a session without a project: %q", runAs)
	}
}

func TestRuleEditOnlyInItsProject(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r, err := e.svc.SaveProjectRule(ctx, "owner-a", "proj-1", "", stagingAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	// Another manager of the same project may change it.
	if _, err := e.svc.SaveProjectRule(ctx, "helper-b", "proj-1", r.ID, strings.Replace(stagingAuto, "write]", "write, send]", 1), "widen"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveProjectRule(ctx, "helper-b", "proj-2", r.ID, stagingAuto, ""); err == nil {
		t.Fatal("edited from another project")
	}
	if _, err := e.svc.SaveProjectRule(ctx, "owner-a", "", "", stagingAuto, ""); err == nil {
		t.Fatal("saved a rule without a project")
	}
	if err := e.svc.DeleteProjectRule(ctx, "helper-b", "proj-2", r.ID, ""); err == nil {
		t.Fatal("deleted from another project")
	}
	if err := e.svc.DeleteProjectRule(ctx, "owner-a", "proj-1", r.ID, "done"); err != nil {
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

func TestAlwaysAskOnlyForRequireOrDeny(t *testing.T) {
	raw := "name: a\neffect: auto_approve\nalways_ask: true\nwhen: {ops: [x.*], kind: [write]}"
	if _, err := ParseRule(raw); err == nil {
		t.Fatal("always_ask accepted on auto_approve")
	}
	if _, err := ParseRule("name: a\neffect: require\nalways_ask: true\nwhen: {ops: [x.*]}"); err != nil {
		t.Fatalf("always_ask on require refused: %v", err)
	}
}

func TestGateHoldsOnlyAlwaysAskRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	other := Action{SessionID: "sess-1", ConnectorID: "c-chat", ConnectorKey: "chat", OpKey: "post_message", Kind: KindSend,
		Params: map[string]string{"channel": "C02OTHER", "text": "hi"}}
	own := other
	own.Params = map[string]string{"channel": "C01OWN", "text": "hi"}

	// A plain require rule only decides refused calls: the gate lets the
	// agent's own call through.
	saveRule(t, e, "owner-a", "name: plain\neffect: require\nwhen:\n  ops: [chat.*]\n  all: [{fact: channel, equals: C02OTHER}]\n")
	if err := e.svc.Gate(ctx, other); err != nil {
		t.Fatalf("plain require gated an allowed call: %v", err)
	}

	saveRule(t, e, "owner-a", "name: outside-own\neffect: require\nalways_ask: true\nwhen:\n  ops: [chat.*]\n  kind: [send]\n  all: [{fact: channel, equals: C02OTHER}]\n")
	err := e.svc.Gate(ctx, other)
	if err == nil || !errors.Is(err, ErrNeedsApproval) || !strings.Contains(err.Error(), "wick_request_access") {
		t.Fatalf("always_ask require did not hold the call: %v", err)
	}
	if err := e.svc.Gate(ctx, own); err != nil {
		t.Fatalf("own channel held: %v", err)
	}
	// Another session's project is not covered.
	far := other
	far.SessionID = "sess-h"
	if err := e.svc.Gate(ctx, far); err != nil {
		t.Fatalf("rule leaked to another project: %v", err)
	}
}

// An always_ask card offers the timed modes, and the grant it leaves
// lets the same call on the same target through the gate.
func TestAlwaysAskCardOffersTimedModesAndGrantPasses(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	saveRule(t, e, "owner-a", "name: notes-ask\neffect: require\nalways_ask: true\nwhen:\n  ops: [notes.update_page]\n")
	a := Action{SessionID: "sess-1", AgentID: "agent-1", ConnectorID: "c-notes", ConnectorKey: "notes", OpKey: "update_page", Kind: KindWrite,
		Params: map[string]string{"page_id": "p1", "status": "done"}}
	if err := e.svc.Gate(ctx, a); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("gate before approval: %v", err)
	}
	v0 := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1", "status": "done"}))
	v, _ := e.svc.ViewFor(ctx, v0.ID, "owner-a")
	if len(v.Items[0].Modes) < 2 {
		t.Fatalf("always_ask card offers %v, want the timed modes too", v.Items[0].Modes)
	}
	if _, res, err := e.svc.Decide(ctx, v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeHour}}); err != nil || len(res) != 1 || !res[0].OK {
		t.Fatalf("decide: %v %+v", err, res)
	}
	if err := e.svc.Gate(ctx, a); err != nil {
		t.Fatalf("gate after an hour grant: %v", err)
	}
	other := a
	other.Params = map[string]string{"page_id": "p2"}
	if err := e.svc.Gate(ctx, other); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("grant leaked to another target: %v", err)
	}
}

type countPub struct{ n int }

func (p *countPub) Publish(context.Context, View) (map[string]string, error) {
	p.n++
	return nil, nil
}

func TestAutoApproveAllRunsWithoutCard(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	pub := &countPub{}
	e.svc.AddPublisher(pub)
	woke := 0
	e.svc.SetWaker(func(context.Context, string, string) error { woke++; return nil })
	v := e.create(t, act("c-apps", "update_setting", map[string]any{"app_code": "acme-stag"}))
	if !v.AutoRan || v.Status != StatusDone || v.Items[0].Status != ItemOK {
		t.Fatalf("view = auto %v status %s item %s", v.AutoRan, v.Status, v.Items[0].Status)
	}
	if len(e.cat.runs) != 1 || e.cat.runs[0].user != "owner-a" {
		t.Fatalf("runs = %+v", e.cat.runs)
	}
	if pub.n != 0 || woke != 0 {
		t.Fatalf("published %d, woke %d: an all-auto request needs no card", pub.n, woke)
	}
}

func TestAutoApprovePartlyStillPostsCard(t *testing.T) {
	e := newEnv(t)
	saveRule(t, e, "owner-a", stagingAuto)
	pub := &countPub{}
	e.svc.AddPublisher(pub)
	v := e.create(t,
		act("c-apps", "update_setting", map[string]any{"app_code": "acme-stag"}),
		act("c-notes", "update_page", map[string]any{"page_id": "p1"}),
	)
	if v.AutoRan || pub.n == 0 {
		t.Fatalf("auto %v published %d: a card is still needed for the manual action", v.AutoRan, pub.n)
	}
}

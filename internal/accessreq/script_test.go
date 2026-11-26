package accessreq

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeRunner answers rule scripts from a Go func and counts runs.
type fakeRunner struct {
	mu    sync.Mutex
	runs  int
	owner string
	fn    func(code string, input map[string]any) (any, error)
}

func (f *fakeRunner) run(_ context.Context, owner, code string, input map[string]any) (json.RawMessage, []string, error) {
	f.mu.Lock()
	f.runs++
	f.owner = owner
	f.mu.Unlock()
	// Round-trip the input like the real runner does.
	raw, _ := json.Marshal(input)
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	out, err := f.fn(code, in)
	if err != nil {
		return nil, []string{"boom"}, err
	}
	b, _ := json.Marshal(out)
	return b, []string{"ran"}, nil
}

func viewerField(in map[string]any, k string) string {
	v, _ := in["viewer"].(map[string]any)
	s, _ := v[k].(string)
	return s
}

func actionParam(in map[string]any, k string) string {
	a, _ := in["action"].(map[string]any)
	p, _ := a["params"].(map[string]any)
	s, _ := p[k].(string)
	return s
}

const scriptAuto = `name: listed-auto
effect: auto_approve
when:
  ops: [apps.*]
  kind: [write]
  script: |
    return input.action.params.app_code === "acme-stag";
`

func TestParseScriptResult(t *testing.T) {
	cases := map[string]ScriptResult{
		`true`:                          {Allow: true},
		`false`:                         {Allow: false},
		`{"allow":true,"reason":"ok"}`:  {Allow: true, Reason: "ok"},
		`{"allow":false,"reason":"no"}`: {Allow: false, Reason: "no"},
	}
	for in, want := range cases {
		got := parseScriptResult(json.RawMessage(in), nil)
		if got.Allow != want.Allow || got.Reason != want.Reason || got.Err != "" {
			t.Errorf("%s = %+v", in, got)
		}
	}
	for _, in := range []string{`"yes"`, `1`, `{"reason":"x"}`, `null`, ``} {
		if got := parseScriptResult(json.RawMessage(in), nil); got.Err == "" || got.Allow {
			t.Errorf("%q must be an error, got %+v", in, got)
		}
	}
	if got := parseScriptResult(nil, errors.New("timeout")); got.Err == "" || got.Allow {
		t.Errorf("runner error = %+v", got)
	}
}

func TestScriptRuleValidation(t *testing.T) {
	bad := map[string]string{
		"approvers no script":  "name: a\neffect: approvers\napprovers: add\nwhen: {ops: [x.*]}",
		"approvers bad mode":   "name: a\neffect: approvers\napprovers: some\nwhen: {ops: [x.*], script: 'return true'}",
		"script too long":      "name: a\neffect: deny\nwhen: {ops: [x.*], script: '" + strings.Repeat("x", MaxScriptBytes+1) + "'}",
		"auto script delete":   "name: a\neffect: auto_approve\nwhen: {ops: [x.*], kind: [delete], script: 'return true'}",
		"auto script no kind":  "name: a\neffect: auto_approve\nwhen: {ops: [x.*], script: 'return true'}",
		"approvers wrong slot": "name: a\neffect: deny\napprovers: add\nwhen: {ops: [x.*], script: 'return true'}",
	}
	for name, raw := range bad {
		if _, err := ParseRule(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseRule(scriptAuto); err != nil {
		t.Fatalf("script auto rule refused: %v", err)
	}
}

func TestScriptAutoApproveAndCache(t *testing.T) {
	e := newEnv(t)
	fr := &fakeRunner{fn: func(_ string, in map[string]any) (any, error) {
		return actionParam(in, "app_code") == "acme-stag", nil
	}}
	e.svc.SetScriptRunner(fr.run)
	saveRule(t, e, "owner-a", scriptAuto)
	ctx := context.Background()
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "owner-a" {
		t.Fatalf("script allowed but not auto-approved: %q", runAs)
	}
	if fr.owner != "owner-a" {
		t.Fatalf("script ran as %q, want the rule owner", fr.owner)
	}
	// Same input again: never cached, the script runs again.
	_, _, _ = e.svc.Check(ctx, a)
	if fr.runs != 2 {
		t.Fatalf("runs = %d, want 2 (no cache)", fr.runs)
	}
	if rc := e.svc.RuleCheckOf(ruleIDNamed(t, e, "listed-auto")); rc.LastOK == nil || rc.LastError != "" {
		t.Fatalf("check = %+v", rc)
	}
	a.Params = map[string]string{"app_code": "acme-prod"}
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("script refused but auto-approved: %q", runAs)
	}
}

func TestScriptFailureNeverAllows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	saveRule(t, e, "owner-a", scriptAuto)
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	// No runner wired.
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("no runner but auto-approved: %q", runAs)
	}
	fr := &fakeRunner{fn: func(string, map[string]any) (any, error) { return nil, errors.New("connector down") }}
	e.svc.SetScriptRunner(fr.run)
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("failing script auto-approved: %q", runAs)
	}
	_, _, _ = e.svc.Check(ctx, a)
	if fr.runs != 2 {
		t.Fatalf("runs = %d, each check must run the script", fr.runs)
	}
	fr.fn = func(string, map[string]any) (any, error) { return "yes", nil }
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("unclear answer auto-approved: %q", runAs)
	}
}

func TestScriptDenyFailsClosedToRequire(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	saveRule(t, e, "admin-z", stagingAuto)
	saveRule(t, e, "admin-z", "name: prod-check\neffect: deny\nwhen:\n  ops: [apps.*]\n  script: 'return false'\n")
	fr := &fakeRunner{fn: func(string, map[string]any) (any, error) { return nil, errors.New("timeout") }}
	e.svc.SetScriptRunner(fr.run)
	a := Action{SessionID: "sess-1", ConnectorID: "c-apps", ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite,
		Params: map[string]string{"app_code": "acme-stag"}}
	// A deny rule whose script cannot run must not let the auto rule through.
	if runAs, _, _ := e.svc.Check(ctx, a); runAs != "" {
		t.Fatalf("broken deny script let auto-approve run: %q", runAs)
	}
}

func approversRule(mode string) string {
	return "name: ops-members\neffect: approvers\napprovers: " + mode + "\nwhen:\n  ops: [notes.*]\n  script: 'return input.viewer.chat_id === \"U-OK\"'\n"
}

type idDir struct {
	fakeDir
	ids map[string]map[string]string
}

func (d idDir) Identities(_ context.Context, id string) map[string]string { return d.ids[id] }

func withIdentities(e *env, ids map[string]map[string]string) {
	e.svc.dir = idDir{fakeDir: e.svc.dir.(fakeDir), ids: ids}
}

func TestApproversAddLetsScriptPassersDecide(t *testing.T) {
	e := newEnv(t)
	e.cat.can["stranger|c-notes"] = true
	withIdentities(e, map[string]map[string]string{"stranger": {"chat_id": "U-OK"}, "helper-b": {"chat_id": "U-NO"}})
	fr := &fakeRunner{fn: func(_ string, in map[string]any) (any, error) {
		return map[string]any{"allow": viewerField(in, "chat_id") == "U-OK", "reason": "not in the ops channel"}, nil
	}}
	e.svc.SetScriptRunner(fr.run)
	saveRule(t, e, "owner-a", approversRule("add"))
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	ctx := context.Background()

	sv, _ := e.svc.ViewFor(ctx, v.ID, "stranger")
	if !sv.Items[0].CanDecide || !sv.ViewerCanDecide {
		t.Fatalf("script passer cannot decide: %+v", sv.Items[0])
	}
	hv, _ := e.svc.ViewFor(ctx, v.ID, "helper-b")
	if hv.Items[0].CanDecide || !strings.Contains(hv.Items[0].LockedReason, "ops-members") {
		t.Fatalf("non-passer view = %+v", hv.Items[0])
	}
	// The default approver keeps their role with an add rule.
	ov, _ := e.svc.ViewFor(ctx, v.ID, "owner-a")
	if !ov.Items[0].CanDecide {
		t.Fatal("owner lost the role under an add rule")
	}
	_, res, _ := e.svc.Decide(ctx, v.ID, "stranger", []Decision{{ItemID: v.Items[0].ID, Mode: ModeOnce}})
	if len(res) != 1 || !res[0].OK || len(e.cat.runs) != 1 || e.cat.runs[0].user != "stranger" {
		t.Fatalf("script passer decide = %+v runs %+v", res, e.cat.runs)
	}
}

func TestApproversOnlyNarrowsDefaultApprovers(t *testing.T) {
	e := newEnv(t)
	withIdentities(e, map[string]map[string]string{"owner-a": {"chat_id": "U-NO"}})
	fr := &fakeRunner{fn: func(_ string, in map[string]any) (any, error) {
		return map[string]any{"allow": viewerField(in, "chat_id") == "U-OK", "reason": "not in the ops channel"}, nil
	}}
	e.svc.SetScriptRunner(fr.run)
	saveRule(t, e, "admin-z", approversRule("only"))
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	ctx := context.Background()
	ov, _ := e.svc.ViewFor(ctx, v.ID, "owner-a")
	if ov.Items[0].CanDecide || !strings.Contains(ov.Items[0].LockedReason, "not in the ops channel") {
		t.Fatalf("only rule did not narrow the owner: %+v", ov.Items[0])
	}
	_, res, _ := e.svc.Decide(ctx, v.ID, "owner-a", []Decision{{ItemID: v.Items[0].ID, Mode: ModeOnce}})
	if len(res) != 1 || res[0].OK || len(e.cat.runs) != 0 {
		t.Fatalf("refused approver ran anyway: %+v", res)
	}
}

// A person signed in by email has no linked chat identity; the id the
// channel saw them click with reaches the script instead.
func TestApproversOnlyUsesClickerIdentityFromContext(t *testing.T) {
	e := newEnv(t)
	e.cat.can["stranger|c-notes"] = true
	withIdentities(e, map[string]map[string]string{"stranger": {"email": "dev@example.com"}})
	fr := &fakeRunner{fn: func(_ string, in map[string]any) (any, error) {
		return map[string]any{"allow": viewerField(in, "chat_id") == "U-OK", "reason": "not in the ops channel"}, nil
	}}
	e.svc.SetScriptRunner(fr.run)
	saveRule(t, e, "admin-z", approversRule("only"))
	v := e.create(t, act("c-notes", "update_page", map[string]any{"page_id": "p1"}))
	ctx := context.Background()

	if sv, _ := e.svc.ViewFor(ctx, v.ID, "stranger"); sv.Items[0].CanDecide {
		t.Fatalf("unlinked viewer passed without a clicker id: %+v", sv.Items[0])
	}
	cctx := WithViewerIdentity(ctx, "chat_id", "U-OK")
	sv, _ := e.svc.ViewFor(cctx, v.ID, "stranger")
	if !sv.Items[0].CanDecide {
		t.Fatalf("clicker id did not reach the script: %+v", sv.Items[0])
	}
	_, res, _ := e.svc.Decide(cctx, v.ID, "stranger", []Decision{{ItemID: v.Items[0].ID, Mode: ModeOnce}})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("clicker decide = %+v", res)
	}
}

func TestWithViewerIdentityOverridesAndKeeps(t *testing.T) {
	ctx := WithViewerIdentity(context.Background(), "a", "1")
	ctx = WithViewerIdentity(ctx, "b", "2")
	ctx = WithViewerIdentity(ctx, "", "x")
	ctx = WithViewerIdentity(ctx, "c", "")
	got := ViewerIdentitiesOf(ctx)
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("identities = %v", got)
	}
}

func TestSimulateScriptReportsResultAndLogs(t *testing.T) {
	e := newEnv(t)
	fr := &fakeRunner{fn: func(string, map[string]any) (any, error) {
		return map[string]any{"allow": true, "reason": "listed"}, nil
	}}
	e.svc.SetScriptRunner(fr.run)
	r, err := e.svc.SaveProjectRule(context.Background(), "owner-a", "proj-1", "", scriptAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	a := Action{ConnectorKey: "apps", OpKey: "update_setting", Kind: KindWrite, Params: map[string]string{"app_code": "x"}}
	res, err := e.svc.SimulateProjectScript(context.Background(), "owner-a", "proj-1", r.ID, a, "")
	if err != nil || !res.Allow || res.Reason != "listed" || len(res.Logs) == 0 {
		t.Fatalf("simulate = %+v %v", res, err)
	}
	if _, err := e.svc.SimulateProjectScript(context.Background(), "stranger", "proj-2", r.ID, a, ""); err == nil {
		t.Fatal("another project ran this project's rule script")
	}
}

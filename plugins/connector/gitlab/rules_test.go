package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestDefaultRulesNeedOneApprovalAndDenyHelm(t *testing.T) {
	rules, err := parseMergeRules(defaultMergeRules)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := matchMergeRule(rules, "acme/web")
	if !ok || r.Mode != "allow" || r.MinApprovals != 1 {
		t.Fatalf("acme/web: %+v %v", r, ok)
	}
	r, _ = matchMergeRule(rules, "platform/deploy-helm")
	if r.Mode != "deny" {
		t.Fatalf("a helm repo must be denied, got %+v", r)
	}
}

func TestMatchMostSpecificWins(t *testing.T) {
	rules, err := parseMergeRules(`[{"repo":"*","mode":"allow"},{"repo":"*helm*","mode":"deny"},{"repo":"ops/special-helm","mode":"allow","min_approvals":"2"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := matchMergeRule(rules, "ops/special-helm"); r.Repo != "ops/special-helm" {
		t.Fatalf("exact path should win, got %q", r.Repo)
	}
	if r, _ := matchMergeRule(rules, "ops/other-helm"); r.Repo != "*helm*" {
		t.Fatalf("*helm* should beat *, got %q", r.Repo)
	}
	if r, _ := matchMergeRule(rules, "ops/api"); r.Repo != "*" {
		t.Fatalf("* should match the rest, got %q", r.Repo)
	}
}

func TestParseRulesRejectsBadRows(t *testing.T) {
	for _, raw := range []string{`not json`, `[{"repo":"x","mode":"maybe"}]`, `[{"repo":"x","min_approvals":"two"}]`, `[{"repo":"[","mode":"allow"}]`} {
		if _, err := parseMergeRules(raw); err == nil {
			t.Fatalf("%s should fail", raw)
		}
	}
}

func TestEvaluateApprovalsAndRequiredApprover(t *testing.T) {
	rule := mergeRule{Repo: "*", Mode: "allow", MinApprovals: 1, RequiredApprovers: []string{"lead", "@absent"}}
	mr := map[string]any{"state": "opened"}
	lead := glUser{ID: "2", Username: "lead", Name: "Team Lead"}
	dev := glUser{ID: "3", Username: "dev", Name: "Dev"}

	v := evaluateMergeRule(rule, "acme/web", mr, []glUser{dev}, []glUser{lead})
	if v.Allowed || !strings.Contains(strings.Join(v.reasons(), ";"), "Team Lead is on this merge request and has not approved") {
		t.Fatalf("lead on the MR without approving must block: %+v", v)
	}
	v = evaluateMergeRule(rule, "acme/web", mr, []glUser{lead}, []glUser{dev})
	if !v.Allowed {
		t.Fatalf("lead approved, absent approver not required: %v", v.reasons())
	}
	v = evaluateMergeRule(rule, "acme/web", mr, nil, []glUser{dev})
	if v.Allowed {
		t.Fatal("zero approvals must block")
	}
	v = evaluateMergeRule(rule, "acme/web", map[string]any{"state": "merged"}, []glUser{lead}, nil)
	if v.Allowed {
		t.Fatal("a merged MR must not pass")
	}
}

func TestParseMRRef(t *testing.T) {
	cases := map[string]struct {
		project string
		iid     int
	}{
		"https://gitlab.example.com/acme/sub/web/-/merge_requests/12":  {"acme/sub/web", 12},
		"https://gitlab.example.com/acme/web/-/merge_requests/7/diffs": {"acme/web", 7},
		"acme/web!3": {"acme/web", 3},
		"acme/web/4": {"acme/web", 4},
		"!5":         {"dflt/repo", 5},
		"6":          {"dflt/repo", 6},
	}
	for in, want := range cases {
		p, iid, err := parseMRRef(in, "dflt/repo")
		if err != nil || p != want.project || iid != want.iid {
			t.Fatalf("%s: got %q %d %v", in, p, iid, err)
		}
	}
	if _, _, err := parseMRRef("!5", ""); err == nil {
		t.Fatal("no project and no default must fail")
	}
}

// fakeMR serves one MR (approved by the given usernames) and records
// whether a merge was attempted.
func fakeMR(t *testing.T, project string, approvedBy []string, merged *bool) *httptest.Server {
	t.Helper()
	base := "/api/v4/projects/" + strings.ReplaceAll(project, "/", "%2F") + "/merge_requests/9"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.EscapedPath(); {
		case p == base && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"iid": 9, "state": "opened",
				"web_url": "https://gitlab.example.com/" + project + "/-/merge_requests/9",
				"author":  map[string]any{"id": 1, "username": "dev"}})
		case p == base+"/approvals":
			var list []any
			for _, u := range approvedBy {
				list = append(list, map[string]any{"user": map[string]any{"id": 5, "username": u, "name": u}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"approved_by": list})
		case p == base+"/participants":
			_, _ = w.Write([]byte(`[]`))
		case p == base+"/merge" && r.Method == http.MethodPut:
			*merged = true
			_ = json.NewEncoder(w).Encode(map[string]any{"iid": 9, "state": "merged"})
		default:
			t.Errorf("unexpected %s %s", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func mergeCtx(t *testing.T, base, project string) *connector.Ctx {
	return connector.NewCtx(t.Context(), "test",
		map[string]string{"base_url": base, "token": "tok", "merge_rules": defaultMergeRules},
		map[string]string{"project": project, "mr_iid": "9"}, http.DefaultClient, nil, nil)
}

func TestMergeRefusedWithoutApproval(t *testing.T) {
	merged := false
	srv := fakeMR(t, "acme/web", nil, &merged)
	defer srv.Close()
	_, err := mergeMR(mergeCtx(t, srv.URL, "acme/web"))
	if err == nil || !strings.Contains(err.Error(), "merge refused by merge_rules") || merged {
		t.Fatalf("expected refusal without merging, got %v merged=%v", err, merged)
	}
}

func TestMergeRefusedForHelmEvenWhenApproved(t *testing.T) {
	merged := false
	srv := fakeMR(t, "ops/deploy-helm", []string{"lead"}, &merged)
	defer srv.Close()
	_, err := mergeMR(mergeCtx(t, srv.URL, "ops/deploy-helm"))
	if err == nil || !strings.Contains(err.Error(), "denied") || merged {
		t.Fatalf("helm must be denied, got %v merged=%v", err, merged)
	}
}

func TestMergeAllowedWithOneApproval(t *testing.T) {
	merged := false
	srv := fakeMR(t, "acme/web", []string{"lead"}, &merged)
	defer srv.Close()
	if _, err := mergeMR(mergeCtx(t, srv.URL, "acme/web")); err != nil || !merged {
		t.Fatalf("expected merge, got %v merged=%v", err, merged)
	}
}

func TestRulesTestRunRendersVerdict(t *testing.T) {
	merged := false
	srv := fakeMR(t, "acme/web", nil, &merged)
	defer srv.Close()
	c := connector.NewCtx(t.Context(), "test",
		map[string]string{"base_url": srv.URL, "token": "tok", "merge_rules": defaultMergeRules},
		map[string]string{"mr": "https://gitlab.example.com/acme/web/-/merge_requests/9"}, http.DefaultClient, nil, nil)
	got, err := rulesTestRun(c)
	if err != nil {
		t.Fatal(err)
	}
	h := got.(map[string]any)["html"].(string)
	if !strings.Contains(h, "MERGE REFUSED") || !strings.Contains(h, "At least 1 approval") || merged {
		t.Fatalf("unexpected panel: %s", h)
	}
}

func TestEditorRoundTrip(t *testing.T) {
	rows := rowsFromConfig(defaultMergeRules)
	rows[0].Approvers, rows[0].Names = []string{"lead"}, []string{"Team Lead"}
	enc, err := encodeRows(append(rows, editorRow{Mode: "allow"}))
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := parseMergeRules(enc)
	if len(rules) != 2 || rules[0].RequiredApprovers[0] != "lead" || rules[1].Mode != "deny" {
		t.Fatalf("round trip lost data: %s", enc)
	}
	if _, err := encodeRows([]editorRow{{Repo: "x", Mode: "maybe"}}); err == nil {
		t.Fatal("an invalid row must not be saved")
	}
	h := renderRulesEditor(rows, editorState{Search: -1})
	for _, want := range []string{"*helm*", "Team Lead", `data-op="rules_editor_save"`} {
		if !strings.Contains(h, want) {
			t.Fatalf("editor missing %q", want)
		}
	}
}

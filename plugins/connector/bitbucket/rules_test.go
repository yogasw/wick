package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
)

const testRules = `[
 {"repo":"*","mode":"allow","min_approvals":"1","required_approvers":"acc-lead"},
 {"repo":"infra-helm","mode":"deny"}
]`

// prJSON builds the slice of GET pullrequests/{id} the rules read.
func prJSON(state string, reviewers []string, approved map[string]bool) map[string]any {
	var rs, ps []any
	for _, id := range reviewers {
		rs = append(rs, map[string]any{"uuid": "{" + id + "}", "account_id": id, "nickname": id, "display_name": "User " + id})
	}
	for id, ok := range approved {
		ps = append(ps, map[string]any{"approved": ok, "user": map[string]any{"uuid": "{" + id + "}", "account_id": id, "nickname": id, "display_name": "User " + id}})
	}
	return map[string]any{"state": state, "reviewers": rs, "participants": ps}
}

func mustRules(t *testing.T, raw string) []mergeRule {
	t.Helper()
	rules, err := parseMergeRules(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return rules
}

func TestMatchMergeRule_SpecificBeatsGlob(t *testing.T) {
	rules := mustRules(t, testRules)
	r, ok := matchMergeRule(rules, "acme", "infra-helm")
	if !ok || r.Mode != "deny" {
		t.Fatalf("infra-helm matched %+v, want the deny row", r)
	}
	r, ok = matchMergeRule(rules, "acme", "client-wa-flow-bot")
	if !ok || r.Repo != "*" {
		t.Fatalf("client-wa-flow-bot matched %+v, want *", r)
	}
	r, ok = matchMergeRule(mustRules(t, `[{"repo":"acme/Infra-Helm","mode":"deny"}]`), "acme", "infra-helm")
	if !ok || r.Mode != "deny" {
		t.Fatal("workspace/slug rows must match case-insensitively")
	}
	if _, ok := matchMergeRule(mustRules(t, `[{"repo":"client-*"}]`), "acme", "infra-helm"); ok {
		t.Fatal("client-* must not match infra-helm")
	}
}

func TestParseMergeRules_FailsClosed(t *testing.T) {
	for _, raw := range []string{`not json`, `[{"repo":"*","mode":"maybe"}]`, `[{"repo":"*","min_approvals":"two"}]`, `[{"repo":"[","mode":"allow"}]`} {
		if _, err := parseMergeRules(raw); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
	rules := mustRules(t, `[{"repo":"x"}]`)
	if rules[0].Mode != "allow" || rules[0].MinApprovals != 0 {
		t.Fatalf("defaults = %+v", rules[0])
	}
}

func TestEvaluateMergeRule(t *testing.T) {
	rule, _ := matchMergeRule(mustRules(t, testRules), "acme", "bot")
	cases := []struct {
		name   string
		pr     map[string]any
		allow  bool
		reason string
	}{
		{"lead reviewer approved", prJSON("OPEN", []string{"acc-lead"}, map[string]bool{"acc-lead": true}), true, ""},
		{"lead reviewer not approved yet", prJSON("OPEN", []string{"acc-lead", "acc-b"}, map[string]bool{"acc-b": true}), false, "User acc-lead is on this pull request and has not approved yet"},
		{"lead not on the PR, one other approval", prJSON("OPEN", []string{"acc-b"}, map[string]bool{"acc-b": true}), true, ""},
		{"no approvals", prJSON("OPEN", []string{"acc-b"}, nil), false, "has 0, approved by nobody"},
		{"already merged", prJSON("MERGED", []string{"acc-lead"}, map[string]bool{"acc-lead": true}), false, "not OPEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evaluateMergeRule(rule, "acme", "bot", tc.pr)
			if v.Allowed != tc.allow {
				t.Fatalf("allowed = %v, checks %+v", v.Allowed, v.Checks)
			}
			if tc.reason != "" && !strings.Contains(strings.Join(v.reasons(), "; "), tc.reason) {
				t.Fatalf("reasons %v, want %q", v.reasons(), tc.reason)
			}
		})
	}
}

func TestParsePRRef(t *testing.T) {
	cases := map[string][3]string{
		"https://bitbucket.org/acme/client-wa-flow-bot/pull-requests/113": {"acme", "client-wa-flow-bot", "113"},
		"https://bitbucket.org/acme/infra-helm/pull-requests/2132/":  {"acme", "infra-helm", "2132"},
		"repo/7":       {"ws", "repo", "7"},
		"other/repo/7": {"other", "repo", "7"},
	}
	for raw, want := range cases {
		ws, repo, id, err := parsePRRef(raw, "ws")
		if err != nil || ws != want[0] || repo != want[1] || want[2] != itoa(id) {
			t.Fatalf("%s → %s/%s/%d (%v), want %v", raw, ws, repo, id, err, want)
		}
	}
	for _, raw := range []string{"", "repo", "repo/x"} {
		if _, _, _, err := parsePRRef(raw, "ws"); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// The guard must stop the merge POST from ever leaving, and a denied repo
// must not even need the PR read.
func TestMergePullRequest_RulesGateTheMerge(t *testing.T) {
	cases := []struct {
		name, repo, pr string
		wantMerge      bool
		wantErr        string
	}{
		{"approved by lead", "bot", `{"state":"OPEN","reviewers":[{"uuid":"{l}","account_id":"acc-lead"}],"participants":[{"approved":true,"user":{"uuid":"{l}","account_id":"acc-lead"}}]}`, true, ""},
		{"lead pending", "bot", `{"state":"OPEN","reviewers":[{"uuid":"{l}","account_id":"acc-lead","display_name":"Lead"}],"participants":[]}`, false, "Lead is on this pull request"},
		{"denied repo", "infra-helm", ``, false, "denied by merge_rules"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, read := false, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/repositories/ws/"+tc.repo+"/pullrequests/9":
					read = true
					_, _ = io.WriteString(w, tc.pr)
				case r.Method == http.MethodPost && r.URL.Path == "/repositories/ws/"+tc.repo+"/pullrequests/9/merge":
					merged = true
					_, _ = io.WriteString(w, `{"id":9,"state":"MERGED"}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
			}))
			defer srv.Close()
			c := connector.NewCtx(context.Background(), "id1",
				map[string]string{"base_url": srv.URL, "default_workspace": "ws", "merge_rules": testRules},
				map[string]string{"repo_slug": tc.repo, "pull_request_id": "9"},
				srv.Client(), nil, nil)
			_, err := mergePullRequest(c)
			if merged != tc.wantMerge {
				t.Fatalf("merged = %v, want %v (err %v)", merged, tc.wantMerge, err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if tc.repo == "infra-helm" && read {
				t.Fatal("a denied repo must be refused without reading the PR")
			}
		})
	}
}

// Without merge_rules the merge behaves exactly as before: no PR read.
func TestMergePullRequest_NoRulesUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":9}`)
	}))
	defer srv.Close()
	c := connector.NewCtx(context.Background(), "id1",
		map[string]string{"base_url": srv.URL, "default_workspace": "ws"},
		map[string]string{"repo_slug": "repo", "pull_request_id": "9"},
		srv.Client(), nil, nil)
	if _, err := mergePullRequest(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The default seeded into a new instance must be readable and carry the two
// agreed examples.
func TestDefaultMergeRules(t *testing.T) {
	var def string
	for _, cfg := range entity.StructToConfigs(Configs{}) {
		if cfg.Key == "merge_rules" {
			def = cfg.Value
		}
	}
	rules := mustRules(t, def)
	if r, _ := matchMergeRule(rules, "acme", "infra-helm"); r.Mode != "deny" {
		t.Fatalf("infra-helm default = %+v", r)
	}
	if r, _ := matchMergeRule(rules, "acme", "client-wa-flow-bot"); r.MinApprovals != 1 || len(r.RequiredApprovers) != 1 {
		t.Fatalf("* default = %+v", r)
	}
}

func TestRulesTestRun_RendersChecklist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"state":"OPEN","reviewers":[],"participants":[]}`)
	}))
	defer srv.Close()
	c := connector.NewCtx(context.Background(), "id1",
		map[string]string{"base_url": srv.URL, "default_workspace": "ws", "merge_rules": testRules},
		map[string]string{"pr": "https://bitbucket.org/ws/bot/pull-requests/3"},
		srv.Client(), nil, nil)
	out, err := rulesTestRun(c)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	h := m["html"].(string)
	if !strings.Contains(h, "MERGE REFUSED") || !strings.Contains(h, "At least 1 approval(s)") {
		t.Fatalf("html missing verdict/checklist: %s", h)
	}
	if m["fields"].(map[string]string)["rules_test_pr"] == "" {
		t.Fatal("the tested PR must be persisted")
	}
}

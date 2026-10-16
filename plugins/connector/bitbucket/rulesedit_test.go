package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func editorCtx(base string, cfg, input map[string]string) *connector.Ctx {
	full := map[string]string{"base_url": base, "default_workspace": "ws"}
	for k, v := range cfg {
		full[k] = v
	}
	return connector.NewCtx(context.Background(), "id1", full, input, http.DefaultClient, nil, nil)
}

// twoRows is what the editor posts for "* allow 1 [lead]" + "integration-helm deny".
func twoRows(extra map[string]string) map[string]string {
	in := map[string]string{
		"row_count": "2",
		"r_repo_0":  "*", "r_mode_0": "allow", "r_min_0": "1", "r_appr_0": "acc-lead", "r_names_0": "Lead Person",
		"r_repo_1": "integration-helm", "r_mode_1": "deny", "r_min_1": "", "r_appr_1": "", "r_names_1": "",
	}
	for k, v := range extra {
		in[k] = v
	}
	return in
}

func savedRules(t *testing.T, out any) []mergeRule {
	t.Helper()
	m := out.(map[string]any)
	f, ok := m["fields"].(map[string]string)
	if !ok {
		t.Fatalf("nothing saved: %v", m["html"])
	}
	return mustRules(t, f["merge_rules"])
}

func TestRulesEditorSave_RoundTrip(t *testing.T) {
	out, err := rulesEditorSave(editorCtx("http://x", nil, twoRows(nil)))
	if err != nil {
		t.Fatal(err)
	}
	rules := savedRules(t, out)
	if len(rules) != 2 || rules[0].MinApprovals != 1 || rules[0].RequiredApprovers[0] != "acc-lead" || rules[1].Mode != "deny" {
		t.Fatalf("rules = %+v", rules)
	}
	raw := out.(map[string]any)["fields"].(map[string]string)["merge_rules"]
	if !strings.Contains(raw, `"approver_names":"Lead Person"`) {
		t.Fatalf("names must be stored for display: %s", raw)
	}
}

func TestRulesEditorSave_InvalidIsNotWritten(t *testing.T) {
	out, _ := rulesEditorSave(editorCtx("http://x", nil, twoRows(map[string]string{"r_min_0": "-3"})))
	m := out.(map[string]any)
	if _, ok := m["fields"]; ok {
		t.Fatal("an invalid rule set must not be saved")
	}
	if !strings.Contains(m["html"].(string), "Not saved") {
		t.Fatal("the editor must say why")
	}
}

func TestRulesEditor_AddRemoveApprover(t *testing.T) {
	out, _ := rulesEditorAddApprover(editorCtx("http://x", nil, twoRows(map[string]string{"browser": "0|acc-b|User B"})))
	rules := savedRules(t, out)
	if strings.Join(rules[0].RequiredApprovers, ",") != "acc-lead,acc-b" {
		t.Fatalf("approvers = %v", rules[0].RequiredApprovers)
	}
	// Adding the same person twice keeps one.
	out, _ = rulesEditorAddApprover(editorCtx("http://x", nil, twoRows(map[string]string{"browser": "0|acc-lead|Lead Person"})))
	if got := savedRules(t, out)[0].RequiredApprovers; len(got) != 1 {
		t.Fatalf("duplicate added: %v", got)
	}
	out, _ = rulesEditorRemoveApprover(editorCtx("http://x", nil, twoRows(map[string]string{"browser": "0|acc-lead"})))
	if got := savedRules(t, out)[0].RequiredApprovers; len(got) != 0 {
		t.Fatalf("approver not removed: %v", got)
	}
}

func TestRulesEditor_AddAndDeleteRow(t *testing.T) {
	out, _ := rulesEditorAddRow(editorCtx("http://x", nil, twoRows(nil)))
	m := out.(map[string]any)
	if _, ok := m["fields"]; ok {
		t.Fatal("an empty new row must not save on its own")
	}
	if !strings.Contains(m["html"].(string), `name="row_count" value="3"`) {
		t.Fatal("new row not rendered")
	}
	out, _ = rulesEditorDeleteRow(editorCtx("http://x", nil, twoRows(map[string]string{"browser": "1"})))
	if rules := savedRules(t, out); len(rules) != 1 || rules[0].Repo != "*" {
		t.Fatalf("rules after delete = %+v", rules)
	}
	// An unfilled row is dropped on save instead of failing it.
	out, _ = rulesEditorSave(editorCtx("http://x", nil, twoRows(map[string]string{"row_count": "3", "r_mode_2": "allow"})))
	if rules := savedRules(t, out); len(rules) != 2 {
		t.Fatalf("rules = %+v", rules)
	}
}

// Search filters workspace members by name or nickname across pages.
func TestRulesEditorSearch_FindsByName(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspaces/ws/members" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `{"values":[{"user":{"account_id":"acc-t","display_name":"Taufik Budi S","nickname":"taufik"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"values":[{"user":{"account_id":"acc-y","display_name":"Yoga Setiawan","nickname":"yoga.setiawan"}}],"next":"`+srv.URL+`/workspaces/ws/members?page=2"}`)
	}))
	defer srv.Close()
	c := connector.NewCtx(context.Background(), "id1", map[string]string{"base_url": srv.URL, "default_workspace": "ws"},
		twoRows(map[string]string{"browser": "0", "q_0": "TAUF"}), srv.Client(), nil, nil)
	out, err := rulesEditorSearch(c)
	if err != nil {
		t.Fatal(err)
	}
	h := out.(map[string]any)["html"].(string)
	if !strings.Contains(h, "Taufik Budi S") || strings.Contains(h, "Yoga Setiawan") {
		t.Fatalf("search result wrong: %s", h)
	}
	if !strings.Contains(h, `data-arg="0|acc-t|Taufik Budi S"`) {
		t.Fatal("Add button must carry row, account_id and name")
	}
	if _, ok := out.(map[string]any)["fields"]; ok {
		t.Fatal("a search must not save anything")
	}
}

// Without read:workspace the search falls back to people on recent PRs.
func TestRulesEditorSearch_FallsBackToRecentPRs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/workspaces/ws/members":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"message":"scope"}}`)
		case "/repositories/ws":
			_, _ = io.WriteString(w, `{"values":[{"slug":"a"},{"slug":"b"}]}`)
		case "/repositories/ws/a/pullrequests":
			_, _ = io.WriteString(w, `{"values":[{"author":{"account_id":"acc-y","display_name":"Yoga Setiawan","nickname":"yoga"},"participants":[{"user":{"account_id":"acc-t","display_name":"Taufik Budi S","nickname":"taufik"}}]}]}`)
		case "/repositories/ws/b/pullrequests":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := connector.NewCtx(context.Background(), "id1", map[string]string{"base_url": srv.URL, "default_workspace": "ws"},
		twoRows(map[string]string{"browser": "0", "q_0": "taufik"}), srv.Client(), nil, nil)
	out, _ := rulesEditorSearch(c)
	h := out.(map[string]any)["html"].(string)
	if !strings.Contains(h, `data-arg="0|acc-t|Taufik Budi S"`) || !strings.Contains(h, "recent pull requests") {
		t.Fatalf("fallback result wrong: %s", h)
	}
}

func TestRulesEditorSearch_BothSourcesFailExplains(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"forbidden"}}`)
	}))
	defer srv.Close()
	c := connector.NewCtx(context.Background(), "id1", map[string]string{"base_url": srv.URL, "default_workspace": "ws"},
		twoRows(map[string]string{"browser": "0", "q_0": "x"}), srv.Client(), nil, nil)
	out, _ := rulesEditorSearch(c)
	if !strings.Contains(out.(map[string]any)["html"].(string), "could not list Bitbucket users") {
		t.Fatal("must say the users could not be listed")
	}
}

// The first render names an approver saved with only an id.
func TestRulesEditor_FillsMissingNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/acc-lead" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"display_name":"Lead Person"}`)
	}))
	defer srv.Close()
	c := connector.NewCtx(context.Background(), "id1",
		map[string]string{"base_url": srv.URL, "default_workspace": "ws", "merge_rules": `[{"repo":"*","mode":"allow","min_approvals":"1","required_approvers":"acc-lead"},{"repo":"integration-helm","mode":"deny"}]`},
		map[string]string{}, srv.Client(), nil, nil)
	out, _ := rulesEditor(c)
	h := out.(map[string]any)["html"].(string)
	if !strings.Contains(h, "Lead Person") {
		t.Fatalf("name not resolved: %s", h)
	}
	if !strings.Contains(h, `<option value="deny" selected>`) {
		t.Fatal("mode must render as a dropdown with the saved value selected")
	}
}

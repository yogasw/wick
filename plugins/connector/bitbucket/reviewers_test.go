package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestParseReviewers(t *testing.T) {
	got, err := parseReviewers(" 5efc131e3404690bae882041, {d1933bcb-9112}\n5efc131e3404690bae882041 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d reviewers, want 2 (duplicate dropped): %v", len(got), got)
	}
	if got[0]["account_id"] != "5efc131e3404690bae882041" || got[1]["uuid"] != "{d1933bcb-9112}" {
		t.Fatalf("unexpected reviewers: %v", got)
	}
}

func TestParseReviewers_RefusesEmailAndBrokenUUID(t *testing.T) {
	for _, raw := range []string{"taufik@qiscus.com", "{broken"} {
		if _, err := parseReviewers(raw); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestValidateCreatePullRequest_Reviewers(t *testing.T) {
	c := prCommentCtx(map[string]string{
		"repo_slug": "repo", "title": "t", "source_branch": "a", "destination_branch": "main",
		"reviewers": "5efc131e3404690bae882041",
	})
	_, body, err := validateCreatePullRequest(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rs, _ := body["reviewers"].([]map[string]any)
	if len(rs) != 1 || rs[0]["account_id"] != "5efc131e3404690bae882041" {
		t.Fatalf("reviewers = %v", body["reviewers"])
	}
}

func TestValidateCreatePullRequest_NoReviewersKeyWhenEmpty(t *testing.T) {
	c := prCommentCtx(map[string]string{"repo_slug": "repo", "title": "t", "source_branch": "a", "destination_branch": "main"})
	_, body, err := validateCreatePullRequest(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := body["reviewers"]; ok {
		t.Fatal("reviewers must be omitted when empty")
	}
}

// The update replaces the reviewer list, so the reviewers already on the PR
// must travel with the new one, and the title must be sent back unchanged.
func TestAddPullRequestReviewers_KeepsExisting(t *testing.T) {
	var put map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repositories/ws/repo/pullrequests/84" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"title":"T","reviewers":[{"uuid":"{a}","account_id":"acc-a"}]}`)
		case http.MethodPut:
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &put)
			_, _ = io.WriteString(w, `{"id":84}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	c := connector.NewCtx(context.Background(), "id1",
		map[string]string{"base_url": srv.URL, "default_workspace": "ws"},
		map[string]string{"repo_slug": "repo", "pull_request_id": "84", "reviewers": "acc-a, acc-b"},
		srv.Client(), nil, nil)
	if _, err := addPullRequestReviewers(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if put["title"] != "T" {
		t.Fatalf("title = %v, want T", put["title"])
	}
	rs, _ := put["reviewers"].([]any)
	if len(rs) != 2 {
		t.Fatalf("reviewers = %v, want existing {a} + acc-b", put["reviewers"])
	}
	first, _ := rs[0].(map[string]any)
	second, _ := rs[1].(map[string]any)
	if first["uuid"] != "{a}" || second["account_id"] != "acc-b" {
		t.Fatalf("reviewers = %v", rs)
	}
}

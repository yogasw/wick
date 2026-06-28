package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestValidatePullRequestAction_BuildsPostURL(t *testing.T) {
	cases := map[string]string{
		"approve":         "/pullrequests/5/approve",
		"request-changes": "/pullrequests/5/request-changes",
	}
	for action, suffix := range cases {
		t.Run(action, func(t *testing.T) {
			c := prCommentCtx(map[string]string{"repo_slug": "repo", "pull_request_id": "5"})
			p, err := validatePullRequestAction(c, action)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", p.Method)
			}
			if !strings.HasSuffix(p.URL, suffix) {
				t.Fatalf("url = %s, want suffix %s", p.URL, suffix)
			}
		})
	}
}

func TestValidatePullRequestAction_RequiresPRID(t *testing.T) {
	c := prCommentCtx(map[string]string{"repo_slug": "repo"})
	if _, err := validatePullRequestAction(c, "approve"); err == nil {
		t.Fatal("expected error when pull_request_id is missing")
	}
}

func TestValidateMergePullRequest_FullBody(t *testing.T) {
	c := prCommentCtx(map[string]string{
		"repo_slug":           "repo",
		"pull_request_id":     "7",
		"merge_strategy":      "squash",
		"message":             "ship it",
		"close_source_branch": "true",
	})
	p, body, err := validateMergePullRequest(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Method != http.MethodPost || !strings.HasSuffix(p.URL, "/pullrequests/7/merge") {
		t.Fatalf("request = %s %s", p.Method, p.URL)
	}
	if body["merge_strategy"] != "squash" {
		t.Fatalf("merge_strategy = %v", body["merge_strategy"])
	}
	if body["message"] != "ship it" {
		t.Fatalf("message = %v", body["message"])
	}
	if body["close_source_branch"] != true {
		t.Fatalf("close_source_branch = %v", body["close_source_branch"])
	}
}

func TestValidateMergePullRequest_OmitsEmptyOptionals(t *testing.T) {
	c := prCommentCtx(map[string]string{"repo_slug": "repo", "pull_request_id": "7"})
	_, body, err := validateMergePullRequest(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := body["merge_strategy"]; ok {
		t.Fatal("merge_strategy must be omitted when empty")
	}
	if _, ok := body["message"]; ok {
		t.Fatal("message must be omitted when empty")
	}
	if body["close_source_branch"] != false {
		t.Fatalf("close_source_branch = %v, want false", body["close_source_branch"])
	}
}

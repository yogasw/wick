package bitbucket

import (
	"context"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func prCommentCtx(input map[string]string) *connector.Ctx {
	return connector.NewCtx(
		context.Background(),
		"id1",
		map[string]string{"base_url": "https://api.bitbucket.org/2.0", "default_workspace": "ws"},
		input,
		nil, nil, nil,
	)
}

func TestValidateCreatePRComment_Inline(t *testing.T) {
	c := prCommentCtx(map[string]string{
		"repo_slug":       "repo",
		"pull_request_id": "5",
		"body":            "hi",
		"inline_path":     "src/main.go",
		"inline_to":       "42",
	})
	_, body, err := validateCreatePullRequestComment(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inline, ok := body["inline"].(map[string]any)
	if !ok {
		t.Fatalf("inline missing: %v", body)
	}
	if inline["path"] != "src/main.go" {
		t.Fatalf("inline.path = %v", inline["path"])
	}
	if inline["to"] != 42 {
		t.Fatalf("inline.to = %v (%T)", inline["to"], inline["to"])
	}
}

func TestValidateCreatePRComment_PlainHasNoInline(t *testing.T) {
	c := prCommentCtx(map[string]string{"repo_slug": "repo", "pull_request_id": "5", "body": "hi"})
	_, body, err := validateCreatePullRequestComment(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := body["inline"]; ok {
		t.Fatal("plain comment must not carry an inline block")
	}
}

func TestValidateCreatePRComment_LineWithoutPathErrors(t *testing.T) {
	c := prCommentCtx(map[string]string{"repo_slug": "repo", "pull_request_id": "5", "body": "hi", "inline_to": "42"})
	if _, _, err := validateCreatePullRequestComment(c); err == nil {
		t.Fatal("expected error when inline_to is set without inline_path")
	}
}

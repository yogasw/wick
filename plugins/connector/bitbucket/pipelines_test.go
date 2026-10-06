package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/conntest"
)

func TestValidateRunPipeline_Body(t *testing.T) {
	c := prCommentCtx(map[string]string{
		"repo_slug": "repo", "branch": "main", "pattern": "deploy",
		"variables": "ENV=prod\nTAG=v1",
	})
	p, body, err := validateRunPipeline(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Method != http.MethodPost || !strings.HasSuffix(p.URL, "/pipelines/") {
		t.Fatalf("request = %s %s", p.Method, p.URL)
	}
	target := body["target"].(map[string]any)
	if target["ref_name"] != "main" || target["selector"].(map[string]any)["pattern"] != "deploy" {
		t.Fatalf("target = %v", target)
	}
	if len(body["variables"].([]map[string]any)) != 2 {
		t.Fatalf("variables = %v", body["variables"])
	}
}

func TestValidateRunPipeline_RequiresBranch(t *testing.T) {
	c := prCommentCtx(map[string]string{"repo_slug": "repo"})
	if _, _, err := validateRunPipeline(c); err == nil {
		t.Fatal("expected error when branch is missing")
	}
}

func TestScopeCovers(t *testing.T) {
	g := map[string]bool{"write:pullrequest:bitbucket": true, "read:repository:bitbucket": true}
	if !scopeCovers(g, "pullrequest", false) || !scopeCovers(g, "pullrequest", true) {
		t.Fatal("write should imply read and write")
	}
	if scopeCovers(g, "repository", true) || scopeCovers(g, "pipeline", false) {
		t.Fatal("unexpected coverage")
	}
}

func TestPermissionStatus_RendersChecklist(t *testing.T) {
	srv := conntest.Server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-OAuth-Scopes", "read:repository:bitbucket, write:pullrequest:bitbucket")
		_, _ = w.Write([]byte(`{}`))
	})
	c := conntest.Ctx(t, map[string]string{
		"base_url": srv.URL, "email": "a@b.c", "api_token": "t", "default_workspace": "ws",
	}, map[string]string{})
	got, err := permissionStatus(c)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := got.(map[string]any)["html"].(string)
	for _, want := range []string{"get_repository", "merge_pull_request", "run_pipeline", "❌", "✅", "allowed"} {
		if !strings.Contains(h, want) {
			t.Fatalf("html missing %q: %s", want, h)
		}
	}
}

func TestPermissionStatus_NeedsWorkspace(t *testing.T) {
	c := conntest.Ctx(t, map[string]string{"base_url": "http://x", "email": "a", "api_token": "t"}, map[string]string{})
	got, _ := permissionStatus(c)
	if !strings.Contains(got.(map[string]any)["html"].(string), "default_workspace") {
		t.Fatal("expected workspace hint")
	}
}

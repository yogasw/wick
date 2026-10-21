package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// fakeGitLab answers the calls the permission check makes. scopes nil =
// a token that does not describe itself (fine-grained or old GitLab);
// denied lists paths answered 403 like a fine-grained token would.
func fakeGitLab(t *testing.T, scopes []string, role int, denied ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.EscapedPath()
		for _, d := range denied {
			if p == d {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"insufficient_granular_scope"}`))
				return
			}
		}
		const pid = "/api/v4/projects/acme%2Fweb"
		switch {
		case p == "/api/v4/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "username": "dev"})
		case p == "/api/v4/projects":
			_, _ = w.Write([]byte(`[]`))
		case p == "/api/v4/personal_access_tokens/self":
			if scopes == nil {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"insufficient_granular_scope"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "bot", "scopes": scopes, "expires_at": "2099-01-01"})
		case p == pid:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "visibility": "private"})
		case p == pid+"/members/all/7":
			if role == 0 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_level": role})
		case strings.HasPrefix(p, pid+"/"):
			if role == 0 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected call %s", p)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func marks(rep *PermissionReport) map[string]string {
	out := map[string]string{}
	for _, o := range rep.Ops {
		out[o.Op] = o.Mark
	}
	return out
}

func TestPermissionsReadAPIScopeDeniesWrites(t *testing.T) {
	srv := fakeGitLab(t, []string{"read_api"}, roleMaintainer)
	defer srv.Close()
	rep, err := evaluatePermissions(newCtx(t, srv.URL, nil), "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	m := marks(rep)
	if m["get_merge_request"] != "✅" || m["create_branch"] != "❌" || m["merge_merge_request"] != "❌" {
		t.Fatalf("unexpected marks: %v", m)
	}
	if rep.Role != "Maintainer" {
		t.Fatalf("role = %q", rep.Role)
	}
}

func TestPermissionsRoleTooLow(t *testing.T) {
	srv := fakeGitLab(t, []string{"api"}, roleReporter)
	defer srv.Close()
	rep, err := evaluatePermissions(newCtx(t, srv.URL, nil), "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	m := marks(rep)
	if m["list_pipelines"] != "✅" || m["add_mr_note"] != "✅" || m["create_branch"] != "❌" || m["retry_pipeline"] != "❌" {
		t.Fatalf("unexpected marks: %v", m)
	}
}

func TestPermissionsNotMemberOfPrivateProject(t *testing.T) {
	srv := fakeGitLab(t, []string{"api"}, 0)
	defer srv.Close()
	rep, err := evaluatePermissions(newCtx(t, srv.URL, nil), "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	m := marks(rep)
	if m["list_projects"] != "✅" || m["get_file"] != "❌" {
		t.Fatalf("unexpected marks: %v", m)
	}
}

func TestPermissionsFineGrainedTokenProbesReads(t *testing.T) {
	srv := fakeGitLab(t, nil, roleDeveloper, "/api/v4/user", "/api/v4/projects/acme%2Fweb/pipelines")
	defer srv.Close()
	rep, err := evaluatePermissions(newCtx(t, srv.URL, nil), "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	m := marks(rep)
	if m["get_me"] != "❌" || m["get_file"] != "✅" || m["get_pipeline"] != "❌" || m["retry_pipeline"] != "❌" || m["create_merge_request"] != "❔" {
		t.Fatalf("unexpected marks: %v", m)
	}
}

func TestPermissionsUnreadableProjectDeniesProjectOps(t *testing.T) {
	srv := fakeGitLab(t, nil, roleDeveloper, "/api/v4/user", "/api/v4/projects/acme%2Fweb")
	defer srv.Close()
	rep, err := evaluatePermissions(newCtx(t, srv.URL, nil), "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	m := marks(rep)
	if m["list_projects"] != "✅" || m["get_project"] != "❌" || m["create_issue"] != "❌" || m["get_file"] != "❌" {
		t.Fatalf("unexpected marks: %v", m)
	}
}

func TestPermissionsOldGitLabLeavesWritesUnknown(t *testing.T) {
	srv := fakeGitLab(t, nil, roleDeveloper)
	defer srv.Close()
	rep, err := evaluatePermissions(newCtx(t, srv.URL, nil), "acme/web")
	if err != nil {
		t.Fatal(err)
	}
	m := marks(rep)
	if m["get_pipeline"] != "✅" || m["create_merge_request"] != "❔" {
		t.Fatalf("unexpected marks: %v", m)
	}
}

func TestPermissionStatusRendersCard(t *testing.T) {
	srv := fakeGitLab(t, []string{"api", "read_api"}, roleDeveloper)
	defer srv.Close()
	c := connector.NewCtx(t.Context(), "test", map[string]string{"base_url": srv.URL, "token": "tok", "default_project": "acme/web"}, nil, http.DefaultClient, nil, nil)
	got, err := permissionStatus(c)
	if err != nil {
		t.Fatal(err)
	}
	h := got.(map[string]any)["html"].(string)
	for _, want := range []string{"allowed", "merge_merge_request", "Developer", "api, read_api", "2099-01-01"} {
		if !strings.Contains(h, want) {
			t.Fatalf("html missing %q: %s", want, h)
		}
	}
}

func TestPermissionsRejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer srv.Close()
	if _, err := evaluatePermissions(newCtx(t, srv.URL, nil), ""); err == nil {
		t.Fatal("a rejected token must fail")
	}
}

func TestPermissionStatusNeedsToken(t *testing.T) {
	c := connector.NewCtx(t.Context(), "test", map[string]string{"base_url": "http://unused"}, nil, http.DefaultClient, nil, nil)
	got, _ := permissionStatus(c)
	if !strings.Contains(got.(map[string]any)["html"].(string), "token") {
		t.Fatal("expected token hint")
	}
}

func TestPermissionStatusTestBoxOverridesProject(t *testing.T) {
	srv := fakeGitLab(t, []string{"api"}, roleDeveloper)
	defer srv.Close()
	c := connector.NewCtx(t.Context(), "test", map[string]string{"base_url": srv.URL, "token": "tok", "default_project": "dev"}, map[string]string{"project": "acme/web"}, http.DefaultClient, nil, nil)
	got, err := permissionStatus(c)
	if err != nil {
		t.Fatal(err)
	}
	h := got.(map[string]any)["html"].(string)
	for _, want := range []string{"Developer", `value="acme/web"`, "Recheck", `data-op="permission_status"`} {
		if !strings.Contains(h, want) {
			t.Fatalf("html missing %q: %s", want, h)
		}
	}
}

func TestProjectHintOnNamespaceOnly(t *testing.T) {
	p := probe{Name: "project", Status: http.StatusNotFound, Error: "404 Not Found"}
	if got := probeText(p); got != "404 Not Found" {
		t.Fatalf("status repeated: %q", got)
	}
	if !strings.Contains(projectHint("dev", p), "namespace") {
		t.Fatal("expected a namespace hint")
	}
	if strings.Contains(projectHint("acme/web", p), "namespace") {
		t.Fatal("a group/repo path is not a namespace")
	}
}

func TestTokenFactsWarnsNearExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	facts := tokenFacts(&PermissionReport{User: "dev", TokenName: "bot", Scopes: []string{"api"}, ExpiresAt: "2026-01-06"}, now)
	if !strings.Contains(strings.Join(facts, "\n"), "⚠") {
		t.Fatalf("expected expiry warning: %v", facts)
	}
}

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// callMe invokes WickMe with the given principal and returns the decoded body.
func callMe(t *testing.T, user *entity.User, tagIDs []string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if user != nil || tagIDs != nil {
		r = r.WithContext(login.WithUser(r.Context(), user, tagIDs))
	}

	var captured string
	rsp := Responder{
		WriteResult: func(_ http.ResponseWriter, _ json.RawMessage, result any) {
			if tr, ok := result.(ToolCallResult); ok && len(tr.Content) > 0 {
				captured = tr.Content[0].Text
			}
		},
		WriteError: func(_ http.ResponseWriter, _ json.RawMessage, _ int, msg string, _ any) {
			t.Fatalf("wick_me returned an error: %s", msg)
		},
	}
	WickMe(httptest.NewRecorder(), r, RPCRequest{ID: json.RawMessage("1")}, rsp)

	var out map[string]any
	if err := json.Unmarshal([]byte(captured), &out); err != nil {
		t.Fatalf("decode %q: %v", captured, err)
	}
	return out
}

// TestWickMe_ReportsResolvedPrincipal covers the point of the tool: an agent
// asking "who am I acting for" must get the server's answer, including the
// tag set that governs connector visibility.
func TestWickMe_ReportsResolvedPrincipal(t *testing.T) {
	out := callMe(t, &entity.User{
		ID: "user-a", Name: "Ada", Email: "ada@example.com",
		Role: entity.RoleUser, Approved: true,
	}, []string{"tag-1", "tag-2"})

	if out["authenticated"] != true {
		t.Fatalf("authenticated = %v, want true", out["authenticated"])
	}
	for k, want := range map[string]any{
		"user_id": "user-a", "name": "Ada",
		"email": "ada@example.com", "role": "user",
	} {
		if out[k] != want {
			t.Errorf("%s = %v, want %v", k, out[k], want)
		}
	}
	if out["is_admin"] != false || out["is_system"] != false {
		t.Errorf("plain user flagged admin/system: %v %v", out["is_admin"], out["is_system"])
	}
	tags, _ := out["filter_tag_ids"].([]any)
	if len(tags) != 2 || tags[0] != "tag-1" {
		t.Errorf("filter_tag_ids = %v, want [tag-1 tag-2]", out["filter_tag_ids"])
	}
}

// TestWickMe_AdminAndSystemAreDistinguishable keeps the two "privileged"
// answers apart: a real admin is a human, the synthetic principal is not. An
// agent that cannot tell them apart would report a cron run as a person.
func TestWickMe_AdminAndSystemAreDistinguishable(t *testing.T) {
	admin := callMe(t, &entity.User{
		ID: "user-b", Name: "Root", Email: "root@example.com",
		Role: entity.RoleAdmin, Approved: true,
	}, nil)
	if admin["is_admin"] != true {
		t.Errorf("admin is_admin = %v, want true", admin["is_admin"])
	}
	if admin["is_system"] != false {
		t.Errorf("real admin flagged as system")
	}

	sys := callMe(t, &entity.User{
		ID: internalAgentUserID, Name: "wick agent (internal)",
		Role: entity.RoleAdmin, Approved: true,
	}, nil)
	if sys["is_system"] != true {
		t.Errorf("synthetic principal not flagged is_system")
	}
}

// TestWickMe_UnauthenticatedSaysSo covers local stdio, which carries no
// principal. Reporting a blank user as authenticated would be worse than
// saying there is none.
func TestWickMe_UnauthenticatedSaysSo(t *testing.T) {
	out := callMe(t, nil, nil)
	if out["authenticated"] != false {
		t.Fatalf("authenticated = %v, want false", out["authenticated"])
	}
	if _, present := out["user_id"]; present {
		t.Errorf("unauthenticated response leaked a user_id: %v", out["user_id"])
	}
}

// TestWickMe_LocalCLIFallbackIsLabelled covers the stdio transport
// (`wick mcp serve`), which has no request, no bearer token and no session: it
// binds to the first admin on the machine because wick_enc_ tokens are keyed
// HKDF(masterKey, salt=user.ID) and a synthetic salt would be undecryptable.
//
// That principal is "whoever ran the CLI", not someone who authenticated, and
// wick_me must say so — otherwise an agent reports a machine-local fallback as
// a verified human.
func TestWickMe_LocalCLIFallbackIsLabelled(t *testing.T) {
	SetLocalCLIPrincipal("admin-1")
	t.Cleanup(func() { SetLocalCLIPrincipal("") })

	local := callMe(t, &entity.User{
		ID: "admin-1", Name: "Root", Email: "root@example.com",
		Role: entity.RoleAdmin, Approved: true,
	}, nil)
	if local["is_local_cli"] != true {
		t.Errorf("stdio principal not flagged is_local_cli: %v", local)
	}
	if local["identity_source"] != "local-cli-fallback" {
		t.Errorf("identity_source = %v, want local-cli-fallback", local["identity_source"])
	}

	// A DIFFERENT user in the same process is a real authenticated caller.
	other := callMe(t, &entity.User{
		ID: "user-a", Name: "Ada", Email: "ada@example.com",
		Role: entity.RoleUser, Approved: true,
	}, nil)
	if other["is_local_cli"] != false {
		t.Errorf("authenticated user flagged as local CLI: %v", other)
	}
	if other["identity_source"] != "token" {
		t.Errorf("identity_source = %v, want token", other["identity_source"])
	}
}

// TestWickMe_HTTPServerHasNoLocalFlag: with no stdio principal recorded (the
// HTTP server case), every caller is token-authenticated.
func TestWickMe_HTTPServerHasNoLocalFlag(t *testing.T) {
	SetLocalCLIPrincipal("")
	out := callMe(t, &entity.User{
		ID: "user-a", Name: "Ada", Role: entity.RoleUser, Approved: true,
	}, nil)
	if out["is_local_cli"] != false || out["identity_source"] != "token" {
		t.Fatalf("HTTP caller mislabelled: %v", out)
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// scriptFixture is a session in a project whose folder holds scripts/rows.ts,
// plus a session with no project.
func scriptFixture(t *testing.T, src string) (layout agentconfig.Layout, sid, bare string) {
	t.Helper()
	layout = agentconfig.NewLayout(t.TempDir())
	if _, err := project.Create(layout, project.CreateOptions{ID: "proj-1", Name: "Reports", OwnerUserID: "u1"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	dir, err := project.ResolvePath(layout, "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "rows.ts"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, o := range []session.CreateOptions{
		{ID: "sess-1", ProjectID: "proj-1", Origin: session.OriginUI, UserID: "u1"},
		{ID: "sess-2", Origin: session.OriginUI, UserID: "u1"},
	} {
		if _, err := session.Create(context.Background(), layout, o); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	return layout, "sess-1", "sess-2"
}

func TestRunCodeSource(t *testing.T) {
	layout, sid, bare := scriptFixture(t, "return 1")

	if got, err := runCodeSource(layout, sid, map[string]any{"code": "return 2"}); err != nil || got != "return 2" {
		t.Fatalf("inline: %q %v", got, err)
	}
	if got, err := runCodeSource(layout, sid, map[string]any{"script_path": "scripts/rows.ts"}); err != nil || got != "return 1" {
		t.Fatalf("script_path: %q %v", got, err)
	}

	cases := []struct {
		name, sid string
		args      map[string]any
		want      string
	}{
		{"both", sid, map[string]any{"code": "x", "script_path": "scripts/rows.ts"}, "exactly one"},
		{"neither", sid, map[string]any{}, "exactly one"},
		{"escape", sid, map[string]any{"script_path": "../x.ts"}, "no .."},
		{"absolute", sid, map[string]any{"script_path": "/etc/hosts"}, "relative"},
		{"missing", sid, map[string]any{"script_path": "scripts/gone.ts"}, "not found"},
		{"no session", "", map[string]any{"script_path": "scripts/rows.ts"}, "needs a session"},
		{"no project", bare, map[string]any{"script_path": "scripts/rows.ts"}, "no project folder"},
	}
	for _, c := range cases {
		if _, err := runCodeSource(layout, c.sid, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want %q", c.name, err, c.want)
		}
	}
}

func TestWickRunCodeScriptPath(t *testing.T) {
	srv := httptest.NewServer(&stubRunner{results: map[int64]chan json.RawMessage{}})
	t.Cleanup(srv.Close)
	prev := CodeModeRunners
	CodeModeRunners = func() []codemode.Runner {
		return []codemode.Runner{{Key: "codemode_stub", Langs: []string{"ts"}, Transport: func() (http.RoundTripper, error) { return stubRewrite{base: srv.URL}, nil }}}
	}
	t.Cleanup(func() { CodeModeRunners = prev })

	exec := func(c *connector.Ctx) (any, error) { return map[string]string{"from": "file"}, nil }
	svc := newVWSessionSvcWith(t, connector.Module{
		Meta:       connector.Meta{Key: "acme", Name: "Acme", Description: "test", Fixed: true},
		Operations: []connector.Category{connector.Cat("Main", "", connector.Op("list_rows", "List", "list rows", struct{}{}, exec, wickdocs.Docs{}))},
	})
	rows, err := svc.ListByKey(context.Background(), "acme")
	if err != nil || len(rows) == 0 {
		t.Fatalf("no acme row: %v", err)
	}
	layout, sid, _ := scriptFixture(t, "CALL conn:"+rows[0].ID+"/list_rows")
	user := &entity.User{ID: "u1", Role: entity.RoleAdmin, Approved: true}

	var got ToolCallResult
	rsp := Responder{WriteResult: func(_ http.ResponseWriter, _ json.RawMessage, result any) { got = result.(ToolCallResult) }}
	call := func(args map[string]any) {
		got = ToolCallResult{}
		r := httptest.NewRequest("POST", "/mcp", nil)
		WickRunCode(httptest.NewRecorder(), r, RPCRequest{}, rsp, svc, layout, args, user, nil)
	}

	call(map[string]any{"script_path": "scripts/rows.ts", "session_id": sid})
	if got.IsError || len(got.Content) == 0 || !strings.Contains(got.Content[0].Text, `\"from\":\"file\"`) && !strings.Contains(got.Content[0].Text, `"from":"file"`) {
		t.Fatalf("script_path run: %+v", got)
	}

	call(map[string]any{"script_path": "../rows.ts", "session_id": sid})
	if !got.IsError || !strings.Contains(got.Content[0].Text, "no ..") {
		t.Fatalf("escape must be refused: %+v", got)
	}
}

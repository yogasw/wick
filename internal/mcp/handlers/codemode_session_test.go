package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// The CLI channel's run: script_path resolves in the session's own project
// folder, every call runs as the given user, and a bad source is refused
// before the runner is asked.
func TestRunCodeInSession(t *testing.T) {
	srv := httptest.NewServer(&stubRunner{results: map[int64]chan json.RawMessage{}})
	t.Cleanup(srv.Close)
	prev := CodeModeRunners
	CodeModeRunners = func() []codemode.Runner {
		return []codemode.Runner{{Key: "codemode_stub", Langs: []string{"ts"}, Transport: func() (http.RoundTripper, error) { return stubRewrite{base: srv.URL}, nil }}}
	}
	t.Cleanup(func() { CodeModeRunners = prev })

	exec := func(c *connector.Ctx) (any, error) { return map[string]string{"caller": c.CallerUserID()}, nil }
	svc := newVWSessionSvcWith(t, connector.Module{
		Meta:       connector.Meta{Key: "acme", Name: "Acme", Description: "test", Fixed: true},
		Operations: []connector.Category{connector.Cat("Main", "", connector.Op("list_rows", "List", "list rows", struct{}{}, exec, wickdocs.Docs{}))},
	})
	rows, err := svc.ListByKey(context.Background(), "acme")
	if err != nil || len(rows) == 0 {
		t.Fatalf("no acme row: %v", err)
	}
	tool := "conn:" + rows[0].ID + "/list_rows"
	layout, sid, bare := scriptFixture(t, "CALL "+tool)
	user := &entity.User{ID: "u-cli", Role: entity.RoleAdmin, Approved: true}

	res, err := RunCodeInSession(context.Background(), svc, layout, SessionCodeRun{ScriptPath: "scripts/rows.ts"}, sid, user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !strings.Contains(string(res.Result), `"caller":"u-cli"`) || len(res.Calls) != 1 {
		t.Fatalf("script_path run: %+v %s", res, res.Result)
	}

	res, err = RunCodeInSession(context.Background(), svc, layout, SessionCodeRun{Code: "CALL " + tool}, bare, user, nil)
	if err != nil || !res.OK {
		t.Fatalf("inline run in a session without a project: err=%v res=%+v", err, res)
	}

	for name, run := range map[string]struct {
		sid string
		run SessionCodeRun
	}{
		"nothing":    {sid, SessionCodeRun{}},
		"both":       {sid, SessionCodeRun{Code: "x", ScriptPath: "scripts/rows.ts"}},
		"escape":     {sid, SessionCodeRun{ScriptPath: "../rows.ts"}},
		"no project": {bare, SessionCodeRun{ScriptPath: "scripts/rows.ts"}},
	} {
		_, err := RunCodeInSession(context.Background(), svc, layout, run.run, run.sid, user, nil)
		if !errors.Is(err, ErrCodeSource) {
			t.Errorf("%s: err=%v, want ErrCodeSource", name, err)
		}
	}
}

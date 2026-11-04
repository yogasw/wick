package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
)

func TestCodeRunRow(t *testing.T) {
	started := time.Now().Add(-time.Second)
	cases := []struct {
		name string
		res  *codemode.Result
		err  error
		want entity.CodeModeRunStatus
	}{
		{"ok", &codemode.Result{OK: true, RunID: "run-1"}, nil, entity.CodeModeRunOK},
		{"script error", &codemode.Result{Error: "TypeError: x is undefined"}, nil, entity.CodeModeRunError},
		{"timeout", &codemode.Result{Error: "script timeout after 30s while waiting for conn:a/b"}, nil, entity.CodeModeRunTimeout},
		{"crash", &codemode.Result{Error: "the runner stopped before the script finished (crashed)"}, nil, entity.CodeModeRunCrashed},
		{"not started", nil, errors.New("runner busy"), entity.CodeModeRunNotStarted},
	}
	for _, c := range cases {
		row := codeRunRow(codeRunOrigin{via: entity.CodeModeViaMCP}, "TS", "s1", started, c.res, c.err)
		if row.Status != c.want {
			t.Errorf("%s: status %q, want %q", c.name, row.Status, c.want)
		}
		if row.Script != "inline" || row.Lang != "ts" || row.SessionID != "s1" {
			t.Errorf("%s: row %+v", c.name, row)
		}
	}

	res := &codemode.Result{OK: true, RunID: "run-2", Runner: "goja", DurationMS: 42, QueuedMS: 7,
		Calls: []codemode.Call{{OK: true}, {OK: false}, {OK: false}}}
	row := codeRunRow(codeRunOrigin{via: entity.CodeModeViaWatch, script: " watches/a.ts "}, "", "", started, res, nil)
	if row.ID != "run-2" || row.Calls != 3 || row.CallErrors != 2 || row.DurationMs != 42 || row.QueuedMs != 7 || row.Script != "watches/a.ts" || row.Runner != "goja" {
		t.Fatalf("row %+v", row)
	}
}

func TestCodeRunOrigin(t *testing.T) {
	if o := codeRunOriginOf(context.Background()); o.via != entity.CodeModeViaWorkflow {
		t.Fatalf("default via %q", o.via)
	}
	ctx := WithCodeRunOrigin(context.Background(), entity.CodeModeViaCLI, "scripts/x.ts")
	if o := codeRunOriginOf(ctx); o.via != entity.CodeModeViaCLI || o.script != "scripts/x.ts" {
		t.Fatalf("origin %+v", o)
	}
}

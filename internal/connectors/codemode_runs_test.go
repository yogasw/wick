package connectors

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func TestCodeRunsAudit(t *testing.T) {
	ctx := context.Background()
	repo := NewRepo(newSQLite(t))
	now := time.Now()
	runs := []entity.CodeModeRun{
		{ID: "r1", UserID: "u1", ProjectID: "p1", Via: entity.CodeModeViaMCP, Script: "inline", Status: entity.CodeModeRunOK, Calls: 3, DurationMs: 100, StartedAt: now.Add(-time.Minute)},
		{ID: "r2", UserID: "u1", ProjectID: "p1", Via: entity.CodeModeViaWatch, Script: "watches/a.ts", Status: entity.CodeModeRunError, ErrorMsg: "boom", Calls: 2, CallErrors: 1, DurationMs: 300, StartedAt: now.Add(-2 * time.Minute)},
		{ID: "r3", UserID: "u2", ProjectID: "p2", Via: entity.CodeModeViaCLI, Script: "watches/a.ts", Status: entity.CodeModeRunTimeout, ErrorMsg: "script timeout after 2s", DurationMs: 2000, StartedAt: now.Add(-3 * time.Minute)},
		{ID: "old", UserID: "u2", Via: entity.CodeModeViaMCP, Script: "inline", Status: entity.CodeModeRunOK, StartedAt: now.AddDate(0, 0, -30)},
	}
	for i := range runs {
		if err := repo.RecordCodeRun(ctx, &runs[i]); err != nil {
			t.Fatalf("record %s: %v", runs[i].ID, err)
		}
	}

	list, err := repo.ListCodeRuns(ctx, CodeRunFilter{UserID: "u1"}, 10, 0)
	if err != nil || len(list) != 2 || list[0].ID != "r1" {
		t.Fatalf("list by user: %v %+v", err, list)
	}
	if n, _ := repo.CountCodeRuns(ctx, CodeRunFilter{Status: "failed"}); n != 2 {
		t.Fatalf("failed count = %d, want 2", n)
	}
	if n, _ := repo.CountCodeRuns(ctx, CodeRunFilter{ProjectID: "p2", Via: "cli"}); n != 1 {
		t.Fatalf("project+via count = %d, want 1", n)
	}

	from := now.Add(-time.Hour)
	s, err := repo.SummariseCodeRuns(ctx, CodeRunFilter{From: &from})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if s.Total != 3 || s.OK != 1 || s.Failed != 2 || s.Calls != 5 || s.CallErrors != 1 {
		t.Fatalf("summary counts: %+v", s)
	}
	if s.ByStatus["timeout"] != 1 || s.ByStatus["error"] != 1 {
		t.Fatalf("by status: %+v", s.ByStatus)
	}
	if len(s.TopScripts) == 0 || s.TopScripts[0].Key != "watches/a.ts" || s.TopScripts[0].Runs != 2 || s.TopScripts[0].Failed != 2 {
		t.Fatalf("top scripts: %+v", s.TopScripts)
	}
	if len(s.TopUsers) == 0 || s.TopUsers[0].Key != "u1" {
		t.Fatalf("top users: %+v", s.TopUsers)
	}
	if len(s.TopErrors) != 2 {
		t.Fatalf("top errors: %+v", s.TopErrors)
	}

	n, err := repo.PurgeCodeRunsOlderThan(ctx, now.AddDate(0, 0, -7))
	if err != nil || n != 1 {
		t.Fatalf("purge = %d, %v; want 1", n, err)
	}
}

package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

func callWickTodo(t *testing.T, args map[string]any) ToolCallResult {
	t.Helper()
	var got ToolCallResult
	WickTodo(httptest.NewRecorder(), httptest.NewRequest("POST", "/mcp", nil), RPCRequest{}, captureResponder(t, &got), agentconfig.Layout{}, args)
	return got
}

func TestWickTodo_AcceptsItemsWithoutID(t *testing.T) {
	got := callWickTodo(t, map[string]any{
		"items": []map[string]any{
			{"step": "do a", "status": "completed"},
			{"step": "do b", "status": "pending"},
		},
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	if len(got.Content) == 0 || !strings.Contains(got.Content[0].Text, "(1/2 done)") {
		t.Errorf("expected done count in output, got %+v", got)
	}
}

func TestWickTodo_AcceptsItemsWithID(t *testing.T) {
	got := callWickTodo(t, map[string]any{
		"items": []map[string]any{
			{"id": "1", "step": "do a", "status": "in_progress"},
		},
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	if len(got.Content) == 0 || !strings.Contains(got.Content[0].Text, "do a") {
		t.Errorf("expected step text in output, got %+v", got)
	}
}

func TestWickTodo_AcceptsTitleAndDescription(t *testing.T) {
	got := callWickTodo(t, map[string]any{
		"items": []map[string]any{
			{"id": "1", "title": "Build login form", "description": "Wire the auth screen end to end.", "status": "in_progress"},
		},
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	if len(got.Content) == 0 || !strings.Contains(got.Content[0].Text, "Build login form") {
		t.Errorf("expected title text in output, got %+v", got)
	}
}

func TestWickTodo_TitleTakesPrecedenceOverDeprecatedStep(t *testing.T) {
	got := callWickTodo(t, map[string]any{
		"items": []map[string]any{
			{"title": "New label", "step": "Old label", "status": "pending"},
		},
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	text := got.Content[0].Text
	if !strings.Contains(text, "New label") || strings.Contains(text, "Old label") {
		t.Errorf("expected title to win over deprecated step, got %+v", got)
	}
}

func TestWickTodo_RendersNestedSubsteps(t *testing.T) {
	got := callWickTodo(t, map[string]any{
		"items": []map[string]any{
			{
				"title":  "Build login form",
				"status": "in_progress",
				"substeps": []map[string]any{
					{"step": "Install deps", "status": "completed"},
					{"step": "Wire validation", "status": "in_progress"},
					{"step": "Style form", "status": "pending"},
				},
			},
		},
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	text := got.Content[0].Text
	for _, want := range []string{"Install deps", "Wire validation", "Style form"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected substep %q in output, got %+v", want, got)
		}
	}
	// The parent task is 1 of 1 in the top-level count — substeps don't
	// count toward the top-level done-count (they're their own nested
	// progress, tracked client-side).
	if !strings.Contains(text, "(0/1 done)") {
		t.Errorf("expected top-level count to ignore substeps, got %+v", got)
	}
}

func TestWickTodo_ItemWithoutTitleOrStepRejectedByRequiredFieldButSchemaAllowsMissingSubsteps(t *testing.T) {
	// Substeps are optional — a simple task must still work with none.
	got := callWickTodo(t, map[string]any{
		"items": []map[string]any{
			{"title": "Simple task", "status": "completed"},
		},
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	if !strings.Contains(got.Content[0].Text, "(1/1 done)") {
		t.Errorf("expected simple task without substeps to work, got %+v", got)
	}
}

func TestWickTodo_GoalOpenWritesLatch(t *testing.T) {
	dir := t.TempDir()
	layout := agentconfig.NewLayout(dir)
	// session id "s1" → layout.SessionDir
	sid := "s1"
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("X-Wick-Session-Id", sid)
	var got ToolCallResult
	WickTodo(httptest.NewRecorder(), req, RPCRequest{}, captureResponder(t, &got), layout, map[string]any{
		"items": []map[string]any{{"title": "step a", "status": "in_progress"}},
		"goal":  "find 3 houses",
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	text := got.Content[0].Text
	if !strings.Contains(text, "[goal] OPEN") {
		t.Errorf("expected OPEN marker, got %q", text)
	}
	if !session.HasOpenGoal(layout, sid) {
		t.Fatal("expected open goal on disk")
	}
}

func TestWickTodo_GoalDoneReleases(t *testing.T) {
	dir := t.TempDir()
	layout := agentconfig.NewLayout(dir)
	sid := "s1"
	_ = session.OpenGoal(layout, sid, "x", "")
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("X-Wick-Session-Id", sid)
	var got ToolCallResult
	WickTodo(httptest.NewRecorder(), req, RPCRequest{}, captureResponder(t, &got), layout, map[string]any{
		"items":     []map[string]any{{"title": "step a", "status": "completed"}},
		"goal_done": true,
	})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	if session.HasOpenGoal(layout, sid) {
		t.Fatal("expected goal closed")
	}
	if !strings.Contains(got.Content[0].Text, "[goal] DONE") {
		t.Errorf("expected DONE marker, got %q", got.Content[0].Text)
	}
}

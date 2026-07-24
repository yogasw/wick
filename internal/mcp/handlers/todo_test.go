package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func callWickTodo(t *testing.T, args map[string]any) ToolCallResult {
	t.Helper()
	var got ToolCallResult
	WickTodo(httptest.NewRecorder(), RPCRequest{}, captureResponder(t, &got), args)
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

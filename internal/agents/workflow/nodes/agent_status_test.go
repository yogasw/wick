package nodes

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func TestParseAgentStatus(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantStatus string
	}{
		{"clean json", `{"status":"done","summary":"all good"}`, "done"},
		{"trailing json", "Some reasoning here.\n{\"status\":\"blocked\",\"summary\":\"no access\"}", "blocked"},
		{"uppercase", `{"status":"DONE"}`, "done"},
		{"plain question", "I think we should ask the user first?", ""},
		{"no status key", `{"foo":"bar"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := parseAgentStatus(tc.text)
			if got != tc.wantStatus {
				t.Fatalf("parseAgentStatus(%q) status = %q, want %q", tc.text, got, tc.wantStatus)
			}
		})
	}
}

func TestFinalizeAgent_NoRequireStatus(t *testing.T) {
	out, err := finalizeAgent(workflow.Node{}, "hello", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Fields["text"] != "hello" {
		t.Fatalf("text field not set: %v", out.Fields)
	}
}

func TestFinalizeAgent_RequireStatusDone(t *testing.T) {
	out, err := finalizeAgent(workflow.Node{RequireStatus: true}, `{"status":"done","summary":"ok"}`, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Verdict != "done" {
		t.Fatalf("verdict = %q, want done", out.Verdict)
	}
	if out.Fields["status"] != "done" {
		t.Fatalf("status field missing: %v", out.Fields)
	}
}

func TestFinalizeAgent_RequireStatusBlockedFails(t *testing.T) {
	_, err := finalizeAgent(workflow.Node{RequireStatus: true}, `{"status":"blocked","summary":"no connector"}`, map[string]any{})
	if err == nil {
		t.Fatalf("expected error for blocked status")
	}
}

func TestFinalizeAgent_RequireStatusMissingFails(t *testing.T) {
	_, err := finalizeAgent(workflow.Node{RequireStatus: true}, "I have a question for you.", map[string]any{})
	if err == nil {
		t.Fatalf("expected error when no status JSON present")
	}
}

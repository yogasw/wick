package canvas

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func TestApplyNodePatch_UnknownKeyReturnsError(t *testing.T) {
	n := workflow.Node{ID: "n1", Label: "original"}
	err := applyNodePatch(&n, map[string]any{"typo_field": "val"})
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "typo_field") {
		t.Errorf("expected error to contain key name, got: %s", err.Error())
	}
}

func TestApplyNodePatch_KnownKeySucceeds(t *testing.T) {
	n := workflow.Node{ID: "n1", Label: "original"}
	err := applyNodePatch(&n, map[string]any{"label": "updated"})
	if err != nil {
		t.Fatalf("expected no error, got: %s", err)
	}
	if n.Label != "updated" {
		t.Errorf("expected label 'updated', got %q", n.Label)
	}
}

func TestApplyNodePatch_MultipleUnknownKeys(t *testing.T) {
	n := workflow.Node{ID: "n1"}
	err := applyNodePatch(&n, map[string]any{"bad_key": "x", "another_bad": "y"})
	if err == nil {
		t.Fatal("expected error for unknown keys, got nil")
	}
	if !strings.Contains(err.Error(), "another_bad") || !strings.Contains(err.Error(), "bad_key") {
		t.Errorf("expected error to list both unknown keys, got: %s", err.Error())
	}
}

func TestApplyNodePatch_EmptyPatch(t *testing.T) {
	n := workflow.Node{ID: "n1", Label: "unchanged"}
	err := applyNodePatch(&n, map[string]any{})
	if err != nil {
		t.Fatalf("expected no error for empty patch, got: %s", err)
	}
	if n.Label != "unchanged" {
		t.Errorf("expected label unchanged, got %q", n.Label)
	}
}

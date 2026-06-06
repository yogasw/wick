package entity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkflowVersionJSONKeys(t *testing.T) {
	b, err := json.Marshal(WorkflowVersion{
		ID:         7,
		WorkflowID: "wf1",
		Kind:       "draft",
		Body:       "x",
		Message:    "m",
		CreatedBy:  "u",
		CreatedAt:  time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{`"id"`, `"workflow_id"`, `"kind"`, `"body"`, `"message"`, `"created_by"`, `"created_at"`} {
		if !strings.Contains(s, key) {
			t.Errorf("missing json key %s in %s", key, s)
		}
	}
}

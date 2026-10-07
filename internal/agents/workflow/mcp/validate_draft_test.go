package mcp

import (
	"strings"
	"testing"
)

// TestOps_ValidateReadsDraft: a fix that only exists in the draft must clear
// the warning — workflow_validate is the pre-publish check, so it has to look
// at what the next publish will promote, not the live copy.
func TestOps_ValidateReadsDraft(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	id := "valdraft"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id, ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	hasTriggerWarn := func() bool {
		for _, w := range ops.Validate(id).Warnings {
			if strings.HasPrefix(w.Path, "triggers[0].description") {
				return true
			}
		}
		return false
	}
	if !hasTriggerWarn() {
		t.Fatal("published copy has no trigger description: expected a warning")
	}
	w := sampleWF(id)
	w.Triggers[0].Description = "Run manual buat test.\nKenapa: contoh."
	if err := svc.SaveDraft(id, w); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if hasTriggerWarn() {
		t.Error("draft has the description but Validate still warns — it read the published copy")
	}
}

package userconfig

import (
	"encoding/json"
	"testing"
)

// TestUserModelEntry_UnmarshalBothForms verifies the migration path: a config
// written with the legacy plain-string model list ("opus") and one written
// with the current object form ({"id":"opus","desc":"…"}) both decode.
func TestUserModelEntry_UnmarshalBothForms(t *testing.T) {
	// Legacy: array of bare strings.
	var legacy []UserModelEntry
	if err := json.Unmarshal([]byte(`["opus","sonnet"]`), &legacy); err != nil {
		t.Fatalf("legacy form: %v", err)
	}
	if len(legacy) != 2 || legacy[0].ID != "opus" || legacy[0].Desc != "" || legacy[1].ID != "sonnet" {
		t.Errorf("legacy decode wrong: %+v", legacy)
	}

	// Current: array of objects with id + desc.
	var cur []UserModelEntry
	if err := json.Unmarshal([]byte(`[{"id":"opus","desc":"best"},{"id":"haiku"}]`), &cur); err != nil {
		t.Fatalf("object form: %v", err)
	}
	if len(cur) != 2 || cur[0].ID != "opus" || cur[0].Desc != "best" || cur[1].ID != "haiku" || cur[1].Desc != "" {
		t.Errorf("object decode wrong: %+v", cur)
	}

	// Round-trip the object form stays object form (marshal emits id/desc).
	b, err := json.Marshal(cur)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back []UserModelEntry
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if back[0].ID != "opus" || back[0].Desc != "best" {
		t.Errorf("round-trip lost data: %+v", back)
	}
}

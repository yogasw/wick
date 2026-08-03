package agents

import (
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestProfileToItemDecodesJSONColumns(t *testing.T) {
	got := profileToItem(entity.AgentProfile{
		Key:                "researcher",
		AllowedTagIDs:      `["t1","t2"]`,
		AllowedNativeTools: `["WebSearch"]`,
	})
	if !reflect.DeepEqual(got.AllowedTagIDs, []string{"t1", "t2"}) {
		t.Fatalf("tags = %v", got.AllowedTagIDs)
	}
	if !reflect.DeepEqual(got.AllowedNativeTools, []string{"WebSearch"}) {
		t.Fatalf("native tools = %v", got.AllowedNativeTools)
	}
}

// Empty and malformed JSON columns must decode to an empty slice, never
// nil: the admin UI binds these to checkbox lists and a null would render
// as "no field" rather than "nothing selected".
func TestProfileToItemEmptyAndMalformedColumns(t *testing.T) {
	for _, raw := range []string{"", "[]", "{oops"} {
		got := profileToItem(entity.AgentProfile{AllowedTagIDs: raw})
		if got.AllowedTagIDs == nil || len(got.AllowedTagIDs) != 0 {
			t.Fatalf("raw %q decoded to %v, want empty non-nil slice", raw, got.AllowedTagIDs)
		}
	}
}

func TestEncodeJSONStringsRoundTrip(t *testing.T) {
	if got := encodeJSONStrings(nil); got != "[]" {
		t.Fatalf("nil encoded to %q, want []", got)
	}
	if got := decodeJSONStrings(encodeJSONStrings([]string{"a", "b"})); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("round trip gave %v", got)
	}
}

// The scope split is the whole permission model, and the save and delete
// paths both route through this one function — so it is tested directly
// rather than through two HTTP handlers that could drift apart.
func TestCanMutateProfileScope(t *testing.T) {
	allow := func(allowed string) func(string) bool {
		return func(id string) bool { return id == allowed }
	}
	deny := func(string) bool { return false }

	tests := []struct {
		name         string
		projectID    string
		isAdmin      bool
		allowProject func(string) bool
		want         bool
	}{
		{"admin may edit a global role", "", true, deny, true},
		{"non-admin may not edit a global role", "", false, allow("proj-abc"), false},
		{"project access is enough for a project role", "proj-abc", false, allow("proj-abc"), true},
		{"no project access means no project role", "proj-abc", false, allow("proj-other"), false},
		// Admin-ness must not be a back door into a project the admin
		// cannot otherwise see: allowProject already grants admins what
		// they are due, so the global branch must not leak into this one.
		{"admin still goes through project access", "proj-abc", true, deny, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canMutateProfileScope(tt.projectID, tt.isAdmin, tt.allowProject); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// The scope has to survive the DTO round trip, or every profile saved
// from the project tab would land in the global scope instead.
func TestProfileToItemCarriesScope(t *testing.T) {
	got := profileToItem(entity.AgentProfile{Key: "db-migrator", ProjectID: "proj-abc"})
	if got.ProjectID != "proj-abc" {
		t.Fatalf("project_id = %q, want proj-abc", got.ProjectID)
	}
	if global := profileToItem(entity.AgentProfile{Key: "researcher"}); global.ProjectID != "" {
		t.Fatalf("a global role must report an empty scope, got %q", global.ProjectID)
	}
}

// can_delegate must be impossible to set on a provider that cannot use
// MCP tools — otherwise the profile saves cleanly and then fails at
// delegation time with a confusing runtime error.
func TestLeaderCapabilityIsProviderGated(t *testing.T) {
	if !leaderCapableProviders["claude"] {
		t.Fatal("claude must be leader-capable")
	}
	if leaderCapableProviders["gemini"] {
		t.Fatal("gemini is not verified for MCP tool use; it must not be leader-capable by default")
	}
}

// Lock is the whole point of the feature, and save + delete both route
// through the same decision — so it is tested as a pure function rather
// than through two HTTP handlers that could drift apart.
func TestLockGuardSave(t *testing.T) {
	locked := &entity.AgentProfile{Key: "reviewer", Locked: true}
	open := &entity.AgentProfile{Key: "reviewer"}

	if unlockOnly, err := lockGuardSave(nil, false); unlockOnly || err != nil {
		t.Fatalf("create: unlockOnly=%v err=%v", unlockOnly, err)
	}
	if unlockOnly, err := lockGuardSave(open, false); unlockOnly || err != nil {
		t.Fatalf("unlocked role: unlockOnly=%v err=%v", unlockOnly, err)
	}
	// Editing a locked role while leaving it locked is the case the whole
	// feature exists to refuse.
	if _, err := lockGuardSave(locked, true); err == nil {
		t.Fatal("a locked role accepted an edit")
	}
	// Unticking Locked is the one save a locked role accepts, and it is
	// accepted ALONE: unlockOnly tells the handler to keep every stored
	// field, so an unlock cannot smuggle an edit through with it.
	unlockOnly, err := lockGuardSave(locked, false)
	if err != nil {
		t.Fatalf("unlock refused: %v", err)
	}
	if !unlockOnly {
		t.Fatal("unlock did not report unlockOnly")
	}
}

func TestProfileToItemCarriesLocked(t *testing.T) {
	if got := profileToItem(entity.AgentProfile{Locked: true}); !got.Locked {
		t.Fatal("locked did not survive profileToItem")
	}
}

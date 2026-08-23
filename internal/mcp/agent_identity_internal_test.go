package mcp

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// TestAgentIdentity_Resolve covers the in-process (wick provider) path, which
// has no HTTP hop and therefore no bearer token: the principal is passed
// directly instead. The zero value must keep the old synthetic-admin
// behaviour so ownerless spawns (cron, system jobs, legacy sessions) do not
// lose their tools.
func TestAgentIdentity_Resolve(t *testing.T) {
	// Zero value: no resolved owner -> synthetic admin, no tag filtering.
	user, tags := AgentIdentity{}.resolve()
	if user == nil || user.ID != InternalAgentUserID || !user.IsAdmin() {
		t.Fatalf("zero identity = %+v, want synthetic admin", user)
	}
	if tags != nil {
		t.Fatalf("zero identity tags = %v, want nil (no filtering)", tags)
	}

	// A real owner: identity and tags both follow the human, so connector
	// visibility matches what that user may reach.
	real := &entity.User{ID: "user-a", Role: entity.RoleUser, Approved: true}
	user, tags = AgentIdentity{User: real, TagIDs: []string{"t1", "t2"}}.resolve()
	if user.ID != "user-a" || user.IsAdmin() {
		t.Fatalf("resolved user = %+v, want plain user-a", user)
	}
	if len(tags) != 2 || tags[0] != "t1" {
		t.Fatalf("resolved tags = %v, want [t1 t2]", tags)
	}

	// An admin owner keeps admin: the in-process agent acts for that human.
	admin := &entity.User{ID: "user-b", Role: entity.RoleAdmin, Approved: true}
	user, _ = AgentIdentity{User: admin}.resolve()
	if !user.IsAdmin() {
		t.Fatal("admin owner lost admin on the in-process path")
	}
}

// TestAgentIdentity_DistinctOwnersDoNotCollapse is the in-process counterpart
// of the token-swap proof: two owners must resolve to two principals, or the
// wick provider repeats the bug this change fixes.
func TestAgentIdentity_DistinctOwnersDoNotCollapse(t *testing.T) {
	a, aTags := AgentIdentity{
		User:   &entity.User{ID: "user-a", Role: entity.RoleUser, Approved: true},
		TagIDs: []string{"tag-a"},
	}.resolve()
	b, bTags := AgentIdentity{
		User:   &entity.User{ID: "user-b", Role: entity.RoleAdmin, Approved: true},
		TagIDs: []string{"tag-b"},
	}.resolve()

	if a.ID == b.ID {
		t.Fatal("two owners collapsed to one principal")
	}
	if aTags[0] == bTags[0] {
		t.Fatal("two owners share a tag set — visibility would not differ")
	}
}

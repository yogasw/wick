package delegation

import (
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func profileWithTags(tags string) *entity.AgentProfile {
	return &entity.AgentProfile{Key: "p", AllowedTagIDs: tags}
}

// The security core: a profile narrows, never widens. If this ever
// regresses, an admin-authored profile becomes a privilege-escalation
// path for any user who can call it.
func TestEffectiveTagsNeverWidensBeyondUser(t *testing.T) {
	userTags := []string{"a", "b"}

	got := EffectiveTags(userTags, profileWithTags(`["a","b","c","admin-only"]`))
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v — profile must not grant tags the user lacks", got, want)
	}
}

func TestEffectiveTagsEmptyAllowListInheritsUserTags(t *testing.T) {
	userTags := []string{"a", "b"}
	for _, raw := range []string{"", "[]"} {
		got := EffectiveTags(userTags, profileWithTags(raw))
		if !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("allowed=%q: got %v, want full inheritance", raw, got)
		}
	}
}

func TestEffectiveTagsNarrows(t *testing.T) {
	got := EffectiveTags([]string{"a", "b", "c"}, profileWithTags(`["b"]`))
	if !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("got %v, want [b]", got)
	}
}

// A user with no tags must end up with no tags, regardless of what the
// profile lists — the intersection has nothing to draw from.
func TestEffectiveTagsUserWithNoTagsGetsNothing(t *testing.T) {
	if got := EffectiveTags(nil, profileWithTags(`["a","b"]`)); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// A corrupt ACL column must fail closed (inherit-only), never open.
func TestEffectiveTagsMalformedAllowListFailsClosed(t *testing.T) {
	got := EffectiveTags([]string{"a"}, profileWithTags(`{"not":"an array"}`))
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("got %v; malformed ACL must not widen beyond user tags", got)
	}
}

func TestCanInterrupt(t *testing.T) {
	d := &entity.AgentDelegation{TriggeredBy: "user-1"}

	tests := []struct {
		name    string
		actor   string
		isAdmin bool
		want    bool
	}{
		{"triggering user may stop", "user-1", false, true},
		{"admin may stop", "user-9", true, true},
		{"unrelated user may not stop", "user-2", false, false},
		{"anonymous may not stop", "", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanInterrupt(d, tc.actor, tc.isAdmin); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVisibleProfilesHidesDisabledAndUnreachable(t *testing.T) {
	profiles := []entity.AgentProfile{
		{Key: "open", AllowedTagIDs: "[]"},
		{Key: "reachable", AllowedTagIDs: `["a"]`},
		{Key: "unreachable", AllowedTagIDs: `["secret"]`},
		{Key: "off", AllowedTagIDs: "[]", Disabled: true},
	}

	got := VisibleProfiles(profiles, []string{"a"}, false)
	var keys []string
	for _, p := range got {
		keys = append(keys, p.Key)
	}
	if !reflect.DeepEqual(keys, []string{"open", "reachable"}) {
		t.Fatalf("got %v, want [open reachable]", keys)
	}
}

// Disabled profiles stay hidden even from admins — disabled means "not
// in service", which is not an access question.
func TestVisibleProfilesHidesDisabledFromAdmins(t *testing.T) {
	profiles := []entity.AgentProfile{{Key: "off", Disabled: true}}
	if got := VisibleProfiles(profiles, nil, true); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

func TestCanAgentStopOnlyInsideItsOwnTree(t *testing.T) {
	d := &entity.AgentDelegation{RootID: "root1", Handle: "worker"}
	if !CanAgentStop(d, "root1") {
		t.Fatal("an agent must be able to stop a peer in its own tree")
	}
	if CanAgentStop(d, "root2") {
		t.Fatal("stopping across trees must be refused")
	}
	if CanAgentStop(nil, "root1") {
		t.Fatal("nil delegation must not be stoppable")
	}
	if CanAgentStop(d, "") {
		t.Fatal("a caller with no tree must not be able to stop anything")
	}
}

func TestNarrowTagsCannotWidenBeyondTheCaller(t *testing.T) {
	caller := []string{"tag-a", "tag-b"}

	if got := NarrowTags([]string{"tag-a"}, caller); len(got) != 1 || got[0] != "tag-a" {
		t.Fatalf("a held tag must survive: %v", got)
	}
	// The whole reason an agent may pick tags at all.
	if got := NarrowTags([]string{"tag-a", "tag-zzz"}, caller); len(got) != 1 || got[0] != "tag-a" {
		t.Fatalf("a tag the caller does not hold must be dropped: %v", got)
	}
	if got := NarrowTags(nil, caller); got != nil {
		t.Fatalf("no request means inherit everything, not an empty set: %v", got)
	}
	if got := NarrowTags([]string{"tag-a", "tag-a"}, caller); len(got) != 1 {
		t.Fatalf("duplicates must collapse: %v", got)
	}
}

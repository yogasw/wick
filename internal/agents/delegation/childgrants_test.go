package delegation

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// The narrowing has to be READABLE at the moment the child's process is
// spawned, because that is when its MCP credential is minted. Handing a
// token to the runner was not enough — the pool ignores it and mints its
// own — so this asserts the grant is published by the time StartAgent runs,
// and gone once the delegation ends.
func TestChildGrantIsPublishedWhileTheChildRuns(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done"},
		{Type: event.Done},
	}}
	grants := NewChildGrants()
	runner := &fakeRunner{}

	var atSpawn ChildGrant
	var okAtSpawn bool
	var childSession string
	runner.onStart = func(spec ChildSpec) {
		childSession = spec.SessionID
		atSpawn, okAtSpawn = grants.Get(spec.SessionID)
	}

	s, _, _ := runService(t, stream, runner)
	s.Children = grants

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q", res.Status)
	}

	if !okAtSpawn {
		t.Fatal("no grant published when the child started — its credential would be minted with the user's full tags")
	}
	if atSpawn.UserID != "user-1" {
		t.Fatalf("grant user = %q, want the triggering human", atSpawn.UserID)
	}
	if len(atSpawn.TagIDs) != 1 || atSpawn.TagIDs[0] != "a" {
		t.Fatalf("grant tags = %v, want the effective (narrowed) set", atSpawn.TagIDs)
	}

	if _, still := grants.Get(childSession); still {
		t.Fatal("grant outlived the run; a reused session id would inherit it")
	}
}

func TestChildGrantsCopyAndNilSafety(t *testing.T) {
	var nilGrants *ChildGrants
	nilGrants.Set("s", "u", []string{"a"}) // must not panic
	if _, ok := nilGrants.Get("s"); ok {
		t.Fatal("nil registry answered a grant")
	}
	nilGrants.Clear("s")

	g := NewChildGrants()
	tags := []string{"a", "b"}
	g.Set("s", "u", tags)
	tags[0] = "mutated"
	got, ok := g.Get("s")
	if !ok || got.TagIDs[0] != "a" {
		t.Fatalf("stored grant aliased the caller's slice: %v", got.TagIDs)
	}
	got.TagIDs[1] = "mutated"
	again, _ := g.Get("s")
	if again.TagIDs[1] != "b" {
		t.Fatalf("returned grant aliased the stored slice: %v", again.TagIDs)
	}
}

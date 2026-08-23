package tickets

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/pkg/connector"
)

// ctxWith builds a Ctx carrying the given caller and inputs.
func ctxWith(caller string, in map[string]string) *connector.Ctx {
	c := connector.NewCtx(context.Background(), "", nil, in, nil, nil, nil)
	c.SetCallerUserID(caller)
	return c
}

// TestResolveAssigneeFilter_MineResolvesServerSide is the security property.
//
// "my tickets" must resolve to the caller from the credential, never from an id
// the model supplies. Otherwise a model could read another person's queue just
// by passing their id, and the prompt would need to know an id at all.
func TestResolveAssigneeFilter_MineResolvesServerSide(t *testing.T) {
	got, err := resolveAssigneeFilter(ctxWith("user-ada", map[string]string{"mine": "true"}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "user-ada" {
		t.Fatalf("filter = %q, want the caller's id", got)
	}
}

// TestResolveAssigneeFilter_MineNeedsAHuman: a cron or system run has no caller.
// Listing everything there would answer a different question than the one asked,
// so it errors and names the way out.
func TestResolveAssigneeFilter_MineNeedsAHuman(t *testing.T) {
	_, err := resolveAssigneeFilter(ctxWith("", map[string]string{"mine": "true"}))
	if err == nil {
		t.Fatal("mine=true with no caller silently listed everything")
	}
	if !strings.Contains(err.Error(), "assignee=") {
		t.Errorf("error should name the alternative: %v", err)
	}
}

// TestResolveAssigneeFilter_ConflictIsRefused: given both, picking either one
// silently answers the wrong question, so neither is picked.
func TestResolveAssigneeFilter_ConflictIsRefused(t *testing.T) {
	_, err := resolveAssigneeFilter(ctxWith("user-ada", map[string]string{"mine": "true", "assignee": "user-bob"}))
	if err == nil {
		t.Fatal("mine=true + a different assignee was accepted")
	}

	// Agreeing values are not a conflict — the caller just said it twice.
	got, err := resolveAssigneeFilter(ctxWith("user-ada", map[string]string{"mine": "true", "assignee": "user-ada"}))
	if err != nil || got != "user-ada" {
		t.Fatalf("agreeing values rejected: got %q err %v", got, err)
	}
}

// TestResolveAssigneeFilter_ExplicitAndEmpty covers the other two shapes.
func TestResolveAssigneeFilter_ExplicitAndEmpty(t *testing.T) {
	got, err := resolveAssigneeFilter(ctxWith("user-ada", map[string]string{"assignee": "user-bob"}))
	if err != nil || got != "user-bob" {
		t.Fatalf("explicit assignee = %q err %v", got, err)
	}
	// No filter at all: everyone's tickets.
	got, err = resolveAssigneeFilter(ctxWith("user-ada", nil))
	if err != nil || got != "" {
		t.Fatalf("no filter = %q err %v, want empty", got, err)
	}
}

// TestMatchesAssignee_UnassignedIsDistinctFromNoFilter pins the sentinel.
//
// An empty filter means "don't filter"; unassigned means "only tickets with
// nobody on them". Collapsing the two would turn a request for orphaned work
// into a request for everything.
func TestMatchesAssignee_UnassignedIsDistinctFromNoFilter(t *testing.T) {
	owned := ticket.Ticket{Assignee: "user-ada"}
	orphan := ticket.Ticket{Assignee: ""}
	blankish := ticket.Ticket{Assignee: "   "}

	// No filter: everything passes.
	for _, tk := range []ticket.Ticket{owned, orphan} {
		if !matchesAssignee(tk, "") {
			t.Errorf("empty filter rejected %+v", tk)
		}
	}

	// unassigned: only the orphan, and whitespace counts as unassigned.
	if matchesAssignee(owned, unassignedFilter) {
		t.Error("unassigned matched an owned ticket")
	}
	if !matchesAssignee(orphan, unassignedFilter) {
		t.Error("unassigned did not match an orphan")
	}
	if !matchesAssignee(blankish, unassignedFilter) {
		t.Error("whitespace assignee should count as unassigned")
	}

	// A specific user matches only their own.
	if !matchesAssignee(owned, "user-ada") {
		t.Error("owner filter did not match their ticket")
	}
	if matchesAssignee(owned, "user-bob") {
		t.Error("owner filter matched somebody else's ticket")
	}
}

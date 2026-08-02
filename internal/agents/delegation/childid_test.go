package delegation

import (
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

const testDelegationID = "3f1c9a70-2b44-4d6e-9d0e-5a1c8e2f77b1"

// The child's ID carries its parent's, which is the whole basis of the
// nested folder resolution — Layout does the path math from the ID alone.
func TestChildSessionIDCarriesItsParent(t *testing.T) {
	got := childSessionIDFor("a1b2c3d4-root", testDelegationID)

	if parent := agentconfig.ParentOfSubSession(got); parent != "a1b2c3d4-root" {
		t.Fatalf("parent of %q = %q, want %q", got, parent, "a1b2c3d4-root")
	}
	if err := storage.ValidateSessionID(got); err != nil {
		t.Fatalf("derived id is not a legal session id: %v", err)
	}
}

// The segment is derived from the delegation UUID rather than freshly
// random, so redispatching the same delegation reuses its folder instead
// of abandoning the transcript already written there.
func TestChildSessionIDIsStablePerDelegation(t *testing.T) {
	a := childSessionIDFor("root", testDelegationID)
	b := childSessionIDFor("root", testDelegationID)
	if a != b {
		t.Fatalf("same delegation gave two ids: %q vs %q", a, b)
	}
	if c := childSessionIDFor("root", "9c7e1f20-1111-2222-3333-444455556666"); c == a {
		t.Fatalf("different delegations collided on %q", a)
	}
}

// Short segments are not cosmetic: nesting with full UUIDs pushes a deep
// session's thinking-trace path past the Windows path limit.
func TestChildSessionIDSegmentIsShort(t *testing.T) {
	id := childSessionIDFor("root", testDelegationID)
	seg := id[len("root"+agentconfig.SubSessionSep):]
	if len(seg) != childSegRunes {
		t.Fatalf("segment %q is %d chars, want %d", seg, len(seg), childSegRunes)
	}
	if seg != "3f1c9a702b44" {
		t.Fatalf("segment = %q, want the dash-stripped uuid prefix", seg)
	}
}

// Nothing dispatches a parentless delegation today, but the request shape
// allows one; it must not produce an id that opens with the separator.
func TestChildSessionIDWithoutParentStaysFlat(t *testing.T) {
	got := childSessionIDFor("", testDelegationID)
	if want := "sub-" + testDelegationID; got != want {
		t.Fatalf("parentless child id = %q, want %q", got, want)
	}
	if parent := agentconfig.ParentOfSubSession(got); parent != "" {
		t.Fatalf("parentless child claims parent %q", parent)
	}
}

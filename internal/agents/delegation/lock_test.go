package delegation

import (
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// A create has no existing row, and an unlocked role is ordinary. Only a
// locked row refuses — and the refusal has to say where the way out is,
// because an agent told nothing but "no" retries the same payload.
func TestCheckMutable(t *testing.T) {
	if err := CheckMutable(nil); err != nil {
		t.Fatalf("create refused: %v", err)
	}
	if err := CheckMutable(&entity.AgentProfile{Key: "reviewer"}); err != nil {
		t.Fatalf("unlocked role refused: %v", err)
	}

	err := CheckMutable(&entity.AgentProfile{Key: "reviewer", Locked: true})
	if err == nil {
		t.Fatal("locked role accepted a mutation")
	}
	if !errors.Is(err, ErrProfileLocked) {
		t.Fatalf("err = %v, want it to wrap ErrProfileLocked", err)
	}
	if !strings.Contains(err.Error(), "reviewer") {
		t.Fatalf("err = %q, want it to name the role", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unlock") {
		t.Fatalf("err = %q, want it to say how to unlock", err)
	}
}

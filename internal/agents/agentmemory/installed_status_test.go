package agentmemory

import (
	"context"
	"testing"
)

// A daemon somebody else started must not be taken as proof that wick has the
// binary. Status.Installed used to be `installed || running`, and on a host
// where an ai-memory was already listening — but not on PATH — that made the
// panel hide the Install control while every CLI-backed read failed with
// "executable file not found in $PATH". The one action that fixes it was
// nowhere on the page, because a process on a port had answered a question
// nobody asked it.
func TestStatusInstalledMeansTheBinaryNotAPortAnswering(t *testing.T) {
	be := testBackend(&fakeData{})

	// No binary anywhere.
	prev := resolveOnPath
	resolveOnPath = func(string) (string, error) { return "", errNoBin }
	t.Cleanup(func() { resolveOnPath = prev })

	st := be.Mgr.Status(context.Background())
	if st.Installed {
		t.Fatal("Installed is true with no binary — a reachable daemon is not an installed CLI")
	}
	if st.State != "not-installed" {
		t.Fatalf("state = %q, want not-installed", st.State)
	}
}

type noBinErr struct{}

func (noBinErr) Error() string { return "not found" }

var errNoBin = noBinErr{}

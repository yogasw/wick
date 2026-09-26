package agentmemory

import (
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Turning Agent Memory ON for a project makes nothing record: the enabling
// switch is the provider instance's, and a project's own switch only narrows
// (projectpolicy.go). These pin the state the panel reads to say so — and the
// one case it must NOT claim, which is a provider list wick could not read.

func TestProviderMemoryCountsWiredAndRecording(t *testing.T) {
	withInstances(t, []provider.Instance{
		{Type: provider.TypeClaude, Name: "enginer", UseAgentMemory: true, AgentMemoryCapture: true},
		// Recall only: it reads the store and writes nothing back.
		{Type: provider.TypeCodex, Name: "codex", UseAgentMemory: true},
		// Pointed at another backend, and still recording — the question is
		// "will any agent record here", not "is this bucket wired".
		{Type: provider.TypeClaude, Name: "other", UseAgentMemory: true, AgentMemoryProvider: "elsewhere", AgentMemoryCapture: true},
		{Type: provider.TypeClaude, Name: "off"},
	}, nil)

	st := ProviderMemory()
	if !st.Known {
		t.Fatal("the provider list read fine, so the state is known")
	}
	if st.Instances != 3 || st.Recording != 2 {
		t.Fatalf("3 instances use memory and 2 of them capture: %+v", st)
	}
	// Named so the card can say where it is already on, in the same
	// "type/name" form the rest of the panel uses.
	if got := strings.Join(st.Names, ", "); got != "claude/enginer, claude/other, codex" {
		t.Fatalf("names should be labelled and sorted, got %q", got)
	}
}

func TestProviderMemoryNothingWired(t *testing.T) {
	withInstances(t, []provider.Instance{
		{Type: provider.TypeClaude, Name: "claude"},
		{Type: provider.TypeCodex, Name: "codex"},
	}, nil)

	st := ProviderMemory()
	if !st.Known || st.Instances != 0 || st.Recording != 0 || len(st.Names) != 0 {
		t.Fatalf("nothing uses memory, and that is a KNOWN nothing: %+v", st)
	}
}

// An unreadable config is an unknown, not an empty one. Reporting it as "no
// instance is switched on" would put a fix-this-now sentence on the card of
// every project on a host whose provider file simply failed to load.
func TestProviderMemoryUnreadableIsNotEmpty(t *testing.T) {
	withInstances(t, nil, errors.New("providers.json: permission denied"))

	st := ProviderMemory()
	if st.Known {
		t.Fatalf("a failed read must not be reported as a known state: %+v", st)
	}
}

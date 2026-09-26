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

// The shape this host actually has (read from ~/.support-tools/config.json,
// 2026-09-26): five claude instances with NO memory fields at all, and one
// codex instance with use_agent_memory true and no capture flag.
//
// It is pinned because the answer it produces is the one the user sees, and
// the two branches next to each other say opposite things. "No instance has it
// on" tells someone to go and switch it on; "on but not capturing" tells them
// the switch they are looking for is capture. Getting that wrong sends them to
// the wrong screen.
func TestProviderMemoryMirrorsThisHostsConfig(t *testing.T) {
	withInstances(t, []provider.Instance{
		{Type: provider.TypeClaude, Name: "enginer"},
		{Type: provider.TypeClaude, Name: "claude_waba"},
		{Type: provider.TypeClaude, Name: "enginer_sonet_medium"},
		{Type: provider.TypeClaude, Name: "claude_waba_sonet"},
		{Type: provider.TypeClaude, Name: "claude_support_ent"},
		{Type: provider.TypeCodex, Name: "codex", UseAgentMemory: true, AgentMemoryProvider: "ai-memory"},
		{Type: provider.TypeGemini, Name: "gemini"},
		{Type: provider.TypeWick, Name: "wick"},
	}, nil)

	st := ProviderMemory()
	if !st.Known {
		t.Fatal("the provider list read fine, so the state is known")
	}
	// One instance uses it; none of them capture, because codex carries no
	// agent_memory_capture field. That is "on but not recording", NOT "no
	// instance has it on".
	if st.Instances != 1 || st.Recording != 0 {
		t.Fatalf("one instance on, none capturing: %+v", st)
	}
	if len(st.Names) != 1 || st.Names[0] != "codex" {
		t.Fatalf("the instance is named so the card can point at it: %+v", st.Names)
	}
}

// A disabled instance cannot spawn, so its toggle is not a fact about what is
// recording. Counting it would put "Agent Memory is on for codex" on a card
// while codex never runs.
func TestProviderMemoryIgnoresDisabledInstances(t *testing.T) {
	withInstances(t, []provider.Instance{
		{Type: provider.TypeCodex, Name: "codex", UseAgentMemory: true, AgentMemoryCapture: true, Disabled: true},
		{Type: provider.TypeClaude, Name: "enginer", UseAgentMemory: true},
	}, nil)

	st := ProviderMemory()
	if st.Instances != 1 || st.Recording != 0 || len(st.Names) != 1 || st.Names[0] != "claude/enginer" {
		t.Fatalf("a disabled instance must not count: %+v", st)
	}
}

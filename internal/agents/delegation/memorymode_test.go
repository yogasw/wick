package delegation

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestNormalizeMemoryMode(t *testing.T) {
	cases := []struct {
		name             string
		requested, deflt string
		want             string
	}{
		{"nothing said anywhere", "", "", MemorySummary},
		{"caller wins over role", MemoryNone, MemoryFull, MemoryNone},
		{"role default when the caller says nothing", "", MemoryChunks, MemoryChunks},
		{"junk from the caller falls through to the role", "vibes", MemoryFull, MemoryFull},
		{"junk everywhere lands on the default", "vibes", "hunches", MemorySummary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeMemoryMode(tc.requested, tc.deflt); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidMemoryMode(t *testing.T) {
	for _, ok := range []string{"", MemoryNone, MemorySummary, MemoryChunks, MemoryFull} {
		if !ValidMemoryMode(ok) {
			t.Fatalf("%q must be valid", ok)
		}
	}
	if ValidMemoryMode("vibes") {
		t.Fatal("an unknown mode must not be accepted")
	}
}

// doneSibling builds a finished delegation, optionally with a reported
// envelope.
func doneSibling(role, prose, summary string) entity.AgentDelegation {
	d := entity.AgentDelegation{
		ProfileKey: role, Status: entity.DelegationDone, Result: prose,
	}
	if summary != "" {
		d.ResultJSON = `{"summary":"` + summary + `","confidence":"high","structured":true}`
	}
	return d
}

// The whole point of the default mode: an agent spawned late must not
// re-derive what the earlier ones already established.
func TestMemoryPayloadStateSummary(t *testing.T) {
	got := MemoryPayload(MemorySummary, []entity.AgentDelegation{
		doneSibling("log-investigator", "long write-up", "401s spike at 10:02"),
		doneSibling("docs-investigator", "the runbook says retry\nsecond line", ""),
	}, nil)
	want := "What the other agents in this conversation have established (snapshot at spawn):\n" +
		"- log-investigator (done): 401s spike at 10:02\n" +
		"- docs-investigator (done): the runbook says retry\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

// An unfinished sibling has established nothing yet.
func TestMemoryPayloadSkipsUnfinishedSiblings(t *testing.T) {
	running := entity.AgentDelegation{ProfileKey: "coder", Status: entity.DelegationRunning}
	if got := MemoryPayload(MemorySummary, []entity.AgentDelegation{running}, nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestMemoryPayloadNoHistoryIsEmpty(t *testing.T) {
	sibs := []entity.AgentDelegation{doneSibling("log-investigator", "x", "y")}
	if got := MemoryPayload(MemoryNone, sibs, nil); got != "" {
		t.Fatalf("no_history returned %q", got)
	}
}

// relevant_chunks means "the caller curates"; wick contributes nothing of
// its own, and the context field it did supply is appended elsewhere.
func TestMemoryPayloadRelevantChunksAddsNothing(t *testing.T) {
	sibs := []entity.AgentDelegation{doneSibling("log-investigator", "x", "y")}
	if got := MemoryPayload(MemoryChunks, sibs, nil); got != "" {
		t.Fatalf("relevant_chunks returned %q", got)
	}
}

func TestMemoryPayloadFullHistoryCarriesWholeResults(t *testing.T) {
	got := MemoryPayload(MemoryFull, []entity.AgentDelegation{
		doneSibling("log-investigator", "line one\nline two", "short summary"),
	}, nil)
	if !strings.Contains(got, "line one\nline two") {
		t.Fatalf("full_history dropped the body:\n%s", got)
	}
	// It must carry the RESULT, not the summary — that is the difference
	// between full_history and state_summary.
	if strings.Contains(got, "short summary") {
		t.Fatalf("full_history rendered the summary instead of the result:\n%s", got)
	}
}

// A long sibling contributes one line like every other, or a "summary"
// stops being one.
func TestStateSummaryTruncatesALongSibling(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := MemoryPayload(MemorySummary, []entity.AgentDelegation{
		doneSibling("log-investigator", long, ""),
	}, nil)
	if !strings.Contains(got, "…") {
		t.Fatalf("a 500-char sibling was not truncated:\n%s", got)
	}
	if len([]rune(got)) > 400 {
		t.Fatalf("summary line is %d runes, far past the cap", len([]rune(got)))
	}
}

// The whole point of the store: an agent spawned in iteration 3 must not
// re-derive what iterations 1 and 2 established, nor go hunting for
// evidence somebody already recorded as missing.
func TestStateSummaryCarriesIncidentState(t *testing.T) {
	inc := &entity.AgentIncident{
		Hypotheses:      `["signature mismatch on webhook retry"]`,
		MissingEvidence: `["the signature header from a failing request"]`,
	}
	got := MemoryPayload(MemorySummary, nil, inc)
	want := "current hypotheses: signature mismatch on webhook retry\n" +
		"missing evidence: the signature header from a failing request\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

// A conversation with no investigation must read exactly as it did
// before the incident store existed.
func TestStateSummaryWithoutAnIncidentIsUnchanged(t *testing.T) {
	sibs := []entity.AgentDelegation{doneSibling("log-investigator", "x", "found it")}
	withNil := MemoryPayload(MemorySummary, sibs, nil)
	withEmpty := MemoryPayload(MemorySummary, sibs, &entity.AgentIncident{})
	if withNil != withEmpty {
		t.Fatalf("an empty incident changed the payload:\n%q\nvs\n%q", withNil, withEmpty)
	}
	if strings.Contains(withNil, "hypotheses") {
		t.Fatalf("an empty header was printed:\n%s", withNil)
	}
}

// A malformed column costs a line of prompt, not a spawn.
func TestIncidentStateToleratesJunkColumns(t *testing.T) {
	inc := &entity.AgentIncident{Hypotheses: "not json", MissingEvidence: "{"}
	if got := MemoryPayload(MemorySummary, nil, inc); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// A tree with nothing finished must produce no header — an empty
// "what the others established" heading reads as a lost result.
func TestMemoryPayloadIsEmptyWithNoSiblings(t *testing.T) {
	if got := MemoryPayload(MemorySummary, nil, nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := MemoryPayload(MemoryFull, nil, nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

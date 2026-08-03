package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// gateService builds a service whose runner FAILS the test if anything
// spawns — the gate must be mechanical, and a spawn here would mean it
// asked a model whether a string was empty.
func gateService(t *testing.T) (*Service, *Repo) {
	t.Helper()
	s, r := serialService(t, &scriptedStream{})
	s.Waker = &fakeWaker{}
	// A follow-up needs a transport to reach the sub-agent — without one
	// the gate would silently swallow every re-ask.
	s.Steerer = &recordingSteerer{}
	s.Runner = &spawnBansRunner{t: t}
	return s, r
}

type spawnBansRunner struct{ t *testing.T }

func (f *spawnBansRunner) EnsureChildSession(context.Context, string, string, string, string) error {
	return nil
}
func (f *spawnBansRunner) StartAgent(context.Context, ChildSpec) error {
	f.t.Fatal("the intake gate spawned an agent; whether a string is empty is not a judgement")
	return nil
}
func (f *spawnBansRunner) KillAgent(string, string) error    { return nil }
func (f *spawnBansRunner) PartialText(string, string) string { return "" }

// gateFixture seeds a tree with one live sub-agent to gate.
func gateFixture(t *testing.T, r *Repo) *entity.AgentDelegation {
	t.Helper()
	ctx := context.Background()
	root := seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	root.Handle = entity.LeaderHandle
	root.Blocked = true
	if err := r.SaveDelegationForTest(ctx, root); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	row := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	row.Handle = "log-investigator"
	row.ProfileKey = "log-investigator"
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	return row
}

// A claim nobody else can check cannot be recorded. It earns exactly one
// follow-up, then it is dropped.
func TestGateReAsksOnceThenDrops(t *testing.T) {
	s, r := gateService(t)
	ctx := context.Background()
	row := gateFixture(t, r)

	bad := &ResultEnvelope{Structured: true, Evidence: []Evidence{
		{Kind: EvidenceLog, Source: "", Excerpt: "401 signature_invalid"},
	}}

	first := s.GateEnvelope(ctx, row, bad)
	if !first.ReAsked {
		t.Fatal("a sourceless item did not earn a follow-up")
	}
	if len(first.Dropped) != 0 {
		t.Fatalf("dropped %d items on the first failure; the agent has not answered yet", len(first.Dropped))
	}

	msgs, err := r.ListThread(ctx, "root-1", 10)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "no source") {
		t.Fatalf("the follow-up does not name what was missing: %+v", msgs)
	}

	// Second time round the same failure is dropped, not argued about.
	second := s.GateEnvelope(ctx, row, bad)
	if second.ReAsked {
		t.Fatal("the gate asked twice; a turn budget is not for arguing about formatting")
	}
	if len(second.Dropped) != 1 {
		t.Fatalf("dropped %d, want 1", len(second.Dropped))
	}
	msgs, _ = r.ListThread(ctx, "root-1", 10)
	if len(msgs) != 1 {
		t.Fatalf("a second follow-up was sent: %+v", msgs)
	}
}

func TestGateRejectsAnExcerptlessItem(t *testing.T) {
	s, r := gateService(t)
	ctx := context.Background()
	row := gateFixture(t, r)

	v := s.GateEnvelope(ctx, row, &ResultEnvelope{Structured: true, Evidence: []Evidence{
		{Kind: EvidenceLog, Source: "loki: app=abc", Excerpt: "  "},
	}})
	if !v.ReAsked {
		t.Fatal("an item with no excerpt was accepted")
	}
	msgs, _ := r.ListThread(ctx, "root-1", 10)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "no excerpt") {
		t.Fatalf("the follow-up does not name the problem: %+v", msgs)
	}
}

// Well-formed items are not held hostage by a malformed sibling.
func TestGateKeepsTheGoodItems(t *testing.T) {
	s, r := gateService(t)
	ctx := context.Background()
	row := gateFixture(t, r)
	row.IntakeReasked = true // past its one follow-up
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	v := s.GateEnvelope(ctx, row, &ResultEnvelope{Structured: true, Evidence: []Evidence{
		{Kind: EvidenceLog, Source: "loki: app=abc", Excerpt: "401"},
		{Kind: EvidenceLog, Source: "", Excerpt: "500"},
	}})
	if len(v.Accepted) != 1 || v.Accepted[0].Excerpt != "401" {
		t.Fatalf("accepted = %+v, want the well-formed item", v.Accepted)
	}
	if len(v.Dropped) != 1 {
		t.Fatalf("dropped = %+v, want the malformed one", v.Dropped)
	}
}

// A finding with nothing behind it is not rejected — the agent may be
// right — but the checker has to be told, or it weighs a plausible
// sentence like a verified one.
func TestGateFlagsFindingsWithNoEvidence(t *testing.T) {
	s, r := gateService(t)
	ctx := context.Background()
	row := gateFixture(t, r)

	v := s.GateEnvelope(ctx, row, &ResultEnvelope{
		Structured: true,
		Findings:   []string{"the signature is stale"},
	})
	if !v.Unsupported {
		t.Fatal("findings with no evidence were not flagged")
	}
	if v.ReAsked {
		t.Fatal("a finding with no evidence is not a malformed report; nothing to re-ask for")
	}
}

// Prose beats nothing — B already decided that — but the caller is told
// the fields were never asserted.
func TestGateAcceptsAnUnstructuredEnvelopeAndSaysSo(t *testing.T) {
	s, r := gateService(t)
	ctx := context.Background()
	row := gateFixture(t, r)

	v := s.GateEnvelope(ctx, row, FallbackEnvelope("some prose"))
	if !v.Unstructured {
		t.Fatal("a reconstructed envelope was not flagged")
	}
	if v.ReAsked || len(v.Dropped) != 0 {
		t.Fatalf("an unstructured envelope was treated as malformed: %+v", v)
	}
}

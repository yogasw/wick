package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// Without a cap a sub-agent turns its token budget into a database
// problem by quoting a whole log file.
func TestClampCapsEvidence(t *testing.T) {
	e := &ResultEnvelope{Confidence: "HIGH"}
	for i := 0; i < 30; i++ {
		e.Evidence = append(e.Evidence, Evidence{
			Kind: EvidenceLog, Source: "loki: app=abc",
			Excerpt: strings.Repeat("x", 10_000),
		})
	}
	e.Clamp()

	if len(e.Evidence) != maxEvidenceItems {
		t.Fatalf("items = %d, want %d", len(e.Evidence), maxEvidenceItems)
	}
	if len(e.Evidence[0].Excerpt) != maxExcerptBytes {
		t.Fatalf("excerpt = %d bytes, want %d", len(e.Evidence[0].Excerpt), maxExcerptBytes)
	}
	if e.Confidence != ConfidenceHigh {
		t.Fatalf("confidence = %q, want it case-normalised to %q", e.Confidence, ConfidenceHigh)
	}
}

// A decimal is not a confidence level. Saying "unknown" is honest;
// mapping 0.8 onto "high" is a guess presented as a reading.
func TestClampNormalisesUnrecognisedConfidence(t *testing.T) {
	for _, raw := range []string{"0.8", "", "quite sure", "HIGHLY"} {
		e := &ResultEnvelope{Confidence: raw}
		e.Clamp()
		if e.Confidence != ConfidenceUnknown {
			t.Fatalf("%q → %q, want %q", raw, e.Confidence, ConfidenceUnknown)
		}
	}
}

func TestFallbackEnvelopeIsMarkedUnstructured(t *testing.T) {
	e := FallbackEnvelope("  the webhook signature was stale  ")
	if e.Structured {
		t.Fatal("a reconstructed envelope must not claim to be structured")
	}
	if e.Confidence != ConfidenceUnknown {
		t.Fatalf("confidence = %q, want unknown", e.Confidence)
	}
	if e.Summary != "the webhook signature was stale" {
		t.Fatalf("summary = %q", e.Summary)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 0)

	in := &ResultEnvelope{
		Summary:    "401s spike at 10:02",
		Findings:   []string{"signature header missing on retries"},
		Confidence: ConfidenceMedium,
		Structured: true,
		Evidence: []Evidence{{
			Kind: EvidenceLog, Source: "loki: app=abc", Excerpt: "401 signature_invalid",
		}},
		RecommendedNextTasks: []NextTask{{
			Role: "code-investigator", Task: "read the retry path", Reason: "the header is dropped somewhere",
		}},
	}
	if err := r.SaveResultJSON(ctx, "d1", in); err != nil {
		t.Fatalf("save: %v", err)
	}

	row, err := r.Get(ctx, "d1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	out := EnvelopeOf(row)
	if out == nil {
		t.Fatal("envelope did not survive the round trip")
	}
	if out.Summary != in.Summary || out.Confidence != ConfidenceMedium || !out.Structured {
		t.Fatalf("scalar fields lost: %+v", out)
	}
	if len(out.Evidence) != 1 || out.Evidence[0].Excerpt != "401 signature_invalid" {
		t.Fatalf("evidence lost: %+v", out.Evidence)
	}
	if len(out.RecommendedNextTasks) != 1 || out.RecommendedNextTasks[0].Role != "code-investigator" {
		t.Fatalf("next tasks lost: %+v", out.RecommendedNextTasks)
	}
}

// A supervisor is better served by prose with no structure than by a
// delegation that fails because a column holds junk.
func TestEnvelopeOfToleratesJunk(t *testing.T) {
	if got := EnvelopeOf(&entity.AgentDelegation{ResultJSON: "not json"}); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
	if got := EnvelopeOf(&entity.AgentDelegation{}); got != nil {
		t.Fatalf("got %+v, want nil for an empty column", got)
	}
	if got := EnvelopeOf(nil); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

// A sub-agent that forgets report_result still produces a usable result.
// Failing the run instead would punish the LEADER for the sub-agent's
// omission and throw away work already paid for.
func TestDoneResultFallsBackToProse(t *testing.T) {
	s, r := serialService(t, &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "the retry drops the signature header"},
		{Type: event.Done},
	}})
	ctx := context.Background()

	res, err := s.Run(ctx, baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Envelope == nil {
		t.Fatal("a finished delegation must always carry an envelope")
	}
	if res.Envelope.Structured {
		t.Fatal("a reconstructed envelope must say so")
	}
	if res.Envelope.Confidence != ConfidenceUnknown {
		t.Fatalf("confidence = %q, want unknown", res.Envelope.Confidence)
	}
	if res.Envelope.Summary != "the retry drops the signature header" {
		t.Fatalf("summary = %q", res.Envelope.Summary)
	}
	// Persisted, not just returned: collect and the panel must see the
	// same answer as the caller who was there when it finished.
	row, _ := r.Get(ctx, res.DelegationID)
	stored := EnvelopeOf(row)
	if stored == nil || stored.Summary != res.Envelope.Summary {
		t.Fatalf("fallback not persisted: %+v", stored)
	}
}

// A reported envelope must survive the run: overwriting it with the
// closing message would discard the fields the sub-agent asserted.
func TestDoneResultKeepsAReportedEnvelope(t *testing.T) {
	reported := &ResultEnvelope{
		Summary: "401 spike traced to the retry path", Confidence: ConfidenceHigh, Structured: true,
	}
	s, r := serialService(t, &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done — see my report"},
		{Type: event.Done},
	}})
	ctx := context.Background()

	// Stand in for the op: the row exists before the turn ends, so the
	// envelope is already stored when doneResult runs.
	runner := s.Runner.(*fakeRunner)
	runner.onStart = func(spec ChildSpec) {
		row, err := r.FindByChildSession(ctx, spec.SessionID)
		if err != nil || row == nil {
			return
		}
		_ = r.SaveResultJSON(ctx, row.ID, reported)
	}

	res, err := s.Run(ctx, baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Envelope == nil || !res.Envelope.Structured {
		t.Fatalf("envelope = %+v, want the reported one", res.Envelope)
	}
	if res.Envelope.Summary != reported.Summary {
		t.Fatalf("summary = %q, want the reported summary", res.Envelope.Summary)
	}
	// The prose is still there — the envelope is additive.
	if res.Result != "done — see my report" {
		t.Fatalf("result = %q, want the closing message untouched", res.Result)
	}
}

func TestCollectCarriesTheEnvelope(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()

	row := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	row.Mode = ModeAsync
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := r.SaveResultJSON(ctx, "d1", &ResultEnvelope{
		Summary: "s", Confidence: ConfidenceLow, Structured: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Collect(ctx, "d1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got.Envelope == nil || got.Envelope.Confidence != ConfidenceLow {
		t.Fatalf("envelope = %+v", got.Envelope)
	}
}

// Clamping happens at the storage boundary so every writer obeys the same
// bounds without remembering to.
func TestSaveResultJSONClampsBeforeStoring(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 0)

	e := &ResultEnvelope{Confidence: "0.9"}
	for i := 0; i < 25; i++ {
		e.Evidence = append(e.Evidence, Evidence{Kind: EvidenceLog, Source: "s", Excerpt: "x"})
	}
	if err := r.SaveResultJSON(ctx, "d1", e); err != nil {
		t.Fatalf("save: %v", err)
	}

	row, _ := r.Get(ctx, "d1")
	out := EnvelopeOf(row)
	if len(out.Evidence) != maxEvidenceItems {
		t.Fatalf("stored %d items, want %d", len(out.Evidence), maxEvidenceItems)
	}
	if out.Confidence != ConfidenceUnknown {
		t.Fatalf("stored confidence = %q, want unknown", out.Confidence)
	}
}

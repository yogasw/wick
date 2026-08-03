package delegation

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// Dedup is a database constraint rather than a read-then-write, because
// two investigators finishing at the same moment would both pass a "does
// this exist?" check and both insert.
func TestEvidenceDedupIndexHolds(t *testing.T) {
	r := testRepo(t)
	db := r.DB()

	first := entity.AgentEvidence{ID: "e1", IncidentID: "i1", Fingerprint: "f1", Kind: EvidenceLog}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("first insert: %v", err)
	}
	dup := entity.AgentEvidence{ID: "e2", IncidentID: "i1", Fingerprint: "f1", Kind: EvidenceLog}
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("a duplicate fingerprint was accepted; the dedup index is not doing its job")
	}
	// The same fingerprint in a DIFFERENT incident is a different fact.
	other := entity.AgentEvidence{ID: "e3", IncidentID: "i2", Fingerprint: "f1", Kind: EvidenceLog}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("cross-incident insert refused: %v", err)
	}
}

// One incident per tree, whoever gets there first.
func TestEnsureForRootIsIdempotentUnderConcurrency(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)

	const workers = 8
	ids := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inc, err := s.EnsureForRoot(ctx, "root-1")
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = inc.ID
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("worker %d saw incident %q, worker 0 saw %q — two were created", i, id, ids[0])
		}
	}
}

// The lazy contract: a tree that recorded nothing has no row.
func TestIncidentForRootIsNilWhenNothingRecorded(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)

	inc, err := s.IncidentForRoot(ctx, "root-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if inc != nil {
		t.Fatalf("got %+v, want nil — nothing has been recorded yet", inc)
	}
}

// The same log line found by two investigators is ONE piece of evidence,
// and the second finder corroborated rather than erred.
func TestIngestSameEvidenceFromTwoDelegationsOnce(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	first := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	second := seedDelegation(t, r, "d2", "root-1", entity.DelegationDone, 1)

	shared := &ResultEnvelope{Evidence: []Evidence{{
		Kind: EvidenceLog, Source: "loki: app=abc", Excerpt: "401 signature_invalid",
	}}}

	n, err := s.IngestEvidence(ctx, first, shared)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if n != 1 {
		t.Fatalf("first ingest added %d, want 1", n)
	}
	n, err = s.IngestEvidence(ctx, second, shared)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if n != 0 {
		t.Fatalf("second ingest added %d, want 0 — it is the same fact", n)
	}

	inc, _ := s.IncidentForRoot(ctx, "root-1")
	rows, err := s.ListEvidence(ctx, inc.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored %d rows, want 1", len(rows))
	}
}

// Findings are interpretations. Merging four agents' interpretations
// without a checker is how a supervisor ends up confidently wrong.
func TestIngestStoresEvidenceButNotFindings(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	row := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)

	n, err := s.IngestEvidence(ctx, row, &ResultEnvelope{
		Findings: []string{"the signature is stale"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 0 {
		t.Fatalf("added %d rows for a findings-only envelope, want 0", n)
	}
	// And no incident was conjured for it: the lazy contract holds.
	inc, _ := s.IncidentForRoot(ctx, "root-1")
	if inc != nil {
		t.Fatalf("an incident was created with nothing quoted: %+v", inc)
	}
}

// A finding must be traceable back to the agent that reported it, or a
// contradiction cannot be attributed to either side.
func TestIngestRecordsProvenanceAndLinksTheDelegation(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	row := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	row.ProfileKey = "log-investigator"
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := s.IngestEvidence(ctx, row, &ResultEnvelope{Evidence: []Evidence{{
		Kind: EvidenceLog, Source: "loki: app=abc", Excerpt: "401",
	}}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	inc, _ := s.IncidentForRoot(ctx, "root-1")
	rows, _ := s.ListEvidence(ctx, inc.ID)
	if len(rows) != 1 || rows[0].DelegationID != "d1" || rows[0].Role != "log-investigator" {
		t.Fatalf("provenance lost: %+v", rows)
	}

	stored, _ := r.Get(ctx, "d1")
	if stored.IncidentID != inc.ID {
		t.Fatalf("delegation not linked to its incident: %q", stored.IncidentID)
	}
}

// A supervisor must be able to add a hypothesis without restating
// everything else it knows.
func TestUpdateIncidentPatchesOnlyGivenFields(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)

	summary := "401s on the webhook"
	if _, err := s.UpdateIncident(ctx, "root-1", IncidentPatch{Summary: &summary}); err != nil {
		t.Fatalf("first patch: %v", err)
	}
	hyp := `["signature mismatch on retry"]`
	got, err := s.UpdateIncident(ctx, "root-1", IncidentPatch{Hypotheses: &hyp})
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if got.Summary != summary {
		t.Fatalf("summary = %q, want it untouched", got.Summary)
	}
	if got.Hypotheses != hyp {
		t.Fatalf("hypotheses = %q", got.Hypotheses)
	}
}

// A closed incident refuses writes rather than silently overwriting the
// summary somebody closed it with.
func TestCloseThenUpdateIsRefused(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	if _, err := s.EnsureForRoot(ctx, "root-1"); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if _, err := s.CloseIncident(ctx, "root-1", "root cause was a stale signature"); err != nil {
		t.Fatalf("close: %v", err)
	}
	summary := "actually something else"
	if _, err := s.UpdateIncident(ctx, "root-1", IncidentPatch{Summary: &summary}); !errors.Is(err, ErrIncidentClosed) {
		t.Fatalf("update after close: %v, want ErrIncidentClosed", err)
	}
	if _, err := s.CloseIncident(ctx, "root-1", "again"); !errors.Is(err, ErrIncidentClosed) {
		t.Fatalf("second close: %v, want ErrIncidentClosed", err)
	}
}

// Closing with nothing to say leaves the record useless to whoever reads
// it next.
func TestCloseRequiresAFinalSummary(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	if _, err := s.EnsureForRoot(ctx, "root-1"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := s.CloseIncident(ctx, "root-1", "   "); err == nil {
		t.Fatal("an incident was closed with an empty final summary")
	}
}

// Evidence reaches the store by finishing a delegation, not by anyone
// remembering to file it.
func TestFinishedDelegationIngestsItsEvidence(t *testing.T) {
	s, r := serialService(t, &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done"},
		{Type: event.Done},
	}})
	ctx := context.Background()

	runner := s.Runner.(*fakeRunner)
	runner.onStart = func(spec ChildSpec) {
		row, err := r.FindByChildSession(ctx, spec.SessionID)
		if err != nil || row == nil {
			return
		}
		_ = r.SaveResultJSON(ctx, row.ID, &ResultEnvelope{
			Summary: "401s at 10:02", Confidence: ConfidenceHigh, Structured: true,
			Evidence: []Evidence{{Kind: EvidenceLog, Source: "loki: app=abc", Excerpt: "401 signature_invalid"}},
		})
	}

	res, err := s.Run(ctx, baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	inc, err := s.IncidentForRoot(ctx, res.DelegationID)
	if err != nil {
		t.Fatalf("incident: %v", err)
	}
	if inc == nil {
		t.Fatal("finishing with quoted evidence recorded no incident")
	}
	rows, _ := s.ListEvidence(ctx, inc.ID)
	if len(rows) != 1 || rows[0].Excerpt != "401 signature_invalid" {
		t.Fatalf("evidence = %+v", rows)
	}
}

// A delegation that quoted nothing must not leave an incident behind —
// most delegation trees are code reviews, not investigations.
func TestFinishedDelegationWithoutEvidenceLeavesNoIncident(t *testing.T) {
	s, _ := serialService(t, &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "looks fine to me"},
		{Type: event.Done},
	}})
	ctx := context.Background()

	res, err := s.Run(ctx, baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	inc, err := s.IncidentForRoot(ctx, res.DelegationID)
	if err != nil {
		t.Fatalf("incident: %v", err)
	}
	if inc != nil {
		t.Fatalf("an ordinary delegation created an incident: %+v", inc)
	}
}

// A new incident starts investigating, scoped to the tree's project and
// the human who triggered it.
func TestEnsureForRootCopiesTheTreesScope(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	root := seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	root.ProjectID = "proj-abc"
	root.TriggeredBy = "user-7"
	if err := r.SaveDelegationForTest(ctx, root); err != nil {
		t.Fatalf("seed: %v", err)
	}

	inc, err := s.EnsureForRoot(ctx, "root-1")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if inc.Status != entity.IncidentInvestigating {
		t.Fatalf("status = %q, want investigating", inc.Status)
	}
	if inc.ProjectID != "proj-abc" || inc.TriggeredBy != "user-7" {
		t.Fatalf("scope not copied: %+v", inc)
	}
}

package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

/* Regression for the T1/error_max_turns data loss: a sub-agent that worked
   through 48 tool-driven steps streamed almost no text, so when the provider
   killed it (error_max_turns) the recorded result was its EARLY narration —
   and the only trace of the real position, last_report ("step 48 of 80"),
   never reached the leader. 48 steps of work reported lost. */

func seedRunningRow(t *testing.T, r *Repo, id, lastReport string) {
	t.Helper()
	row := &entity.AgentDelegation{
		ID:             id,
		RootID:         "root-" + id,
		ChildSessionID: "child-" + id,
		Status:         entity.DelegationRunning,
		LastReport:     lastReport,
	}
	if err := r.Create(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if lastReport != "" {
		if err := r.SaveProgress(context.Background(), id, lastReport); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnrichAppendsFreshLastReportToPartialResult(t *testing.T) {
	s, r, _ := newService(t)
	seedRunningRow(t, r, "d1", "Unsur 48/80: Kadmium (Cd)")

	out := s.enrichWithLastReport(context.Background(), "d1", "early narration only")

	if !strings.Contains(out, "early narration only") {
		t.Fatalf("partial text lost: %q", out)
	}
	if !strings.Contains(out, "Unsur 48/80") {
		t.Fatalf("last_report not appended — the only trace of 48 steps of work is dropped: %q", out)
	}
}

func TestEnrichUsesLastReportWhenNothingWasStreamed(t *testing.T) {
	s, r, _ := newService(t)
	seedRunningRow(t, r, "d2", "Unsur 48/80: Kadmium (Cd)")

	out := s.enrichWithLastReport(context.Background(), "d2", "")

	if !strings.Contains(out, "Unsur 48/80") {
		t.Fatalf("empty stream must fall back to last_report: %q", out)
	}
	if !strings.Contains(out, "not an answer") {
		t.Fatalf("fallback must be labelled as unfinished, not presented as the answer: %q", out)
	}
}

func TestEnrichLeavesResultAloneWhenReportAlreadyIncluded(t *testing.T) {
	s, r, _ := newService(t)
	seedRunningRow(t, r, "d3", "Unsur 48/80")

	in := "finished; final position Unsur 48/80 reached"
	if out := s.enrichWithLastReport(context.Background(), "d3", in); out != in {
		t.Fatalf("report already present must not be appended twice: %q", out)
	}
}

func TestEnrichNoReportNoChange(t *testing.T) {
	s, r, _ := newService(t)
	seedRunningRow(t, r, "d4", "")

	if out := s.enrichWithLastReport(context.Background(), "d4", "partial"); out != "partial" {
		t.Fatalf("no report on the row must leave the result untouched: %q", out)
	}
}

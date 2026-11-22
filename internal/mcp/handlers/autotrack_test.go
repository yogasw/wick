package handlers

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/pkg/connector"
)

func testTrack() connector.Track {
	return connector.Track{
		StatusOp: "get_job",
		Params: map[string]string{
			"space":  "in.space?",
			"repo":   "in.repo",
			"job_id": "out.uuid",
			"mode":   "full",
		},
		StatePath:  "state.result.name",
		Done:       []string{"SUCCESSFUL"},
		Failed:     []string{"FAILED"},
		DetailPath: "state.name",
		Label:      "job {in.repo} #{out.number}",
	}
}

func TestPlanTrackResolvesParamsAndSteps(t *testing.T) {
	op := connector.Operation{Key: "start_job", Name: "Start Job"}
	p, err := planTrack(testTrack(), op, "c-1", "acc-9",
		map[string]any{"repo": "acme"}, `{"uuid":"{u-1}","number":7}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.steps) != 2 {
		t.Fatalf("steps = %d", len(p.steps))
	}
	st := p.steps[0]
	if st.ToolID != "conn:c-1/get_job@acc-9" {
		t.Errorf("tool_id = %q", st.ToolID)
	}
	if st.Params["job_id"] != "{u-1}" || st.Params["repo"] != "acme" || st.Params["mode"] != "full" {
		t.Errorf("params = %v", st.Params)
	}
	if _, ok := st.Params["space"]; ok {
		t.Errorf("optional missing param must be left out: %v", st.Params)
	}
	ck := p.steps[1]
	if ck.Kind != schedule.StepKindCheck || len(ck.Rules) != 1 || len(ck.FailRules) != 1 {
		t.Fatalf("check step = %+v", ck)
	}
	if ck.Extract[schedule.WatchTodoStateKey] != "state.result.name" || ck.Extract[schedule.WatchTodoDetailKey] != "state.name" {
		t.Errorf("extract = %v", ck.Extract)
	}
	if p.title != "job acme #7" {
		t.Errorf("title = %q", p.title)
	}
	if !strings.HasPrefix(p.item, "track-") {
		t.Errorf("item = %q", p.item)
	}
	if err := schedule.ValidateSteps(p.steps); err != nil {
		t.Errorf("steps must pass watch validation: %v", err)
	}
}

func TestPlanTrackMissingRequiredSource(t *testing.T) {
	op := connector.Operation{Key: "start_job"}
	if _, err := planTrack(testTrack(), op, "c-1", "", map[string]any{"repo": "acme"}, `{"number":1}`); err == nil || !strings.Contains(err.Error(), "out.uuid") {
		t.Fatalf("want out.uuid error, got %v", err)
	}
	if _, err := planTrack(testTrack(), op, "c-1", "", map[string]any{"repo": "acme"}, `not json`); err == nil {
		t.Fatal("non-JSON result must be reported")
	}
}

func TestPlanTrackDefaultTitle(t *testing.T) {
	tr := testTrack()
	tr.Label = ""
	tr.Params = map[string]string{"job_id": "out.uuid"}
	p, err := planTrack(tr, connector.Operation{Key: "start_job", Name: "Start Job"}, "c-1", "", nil, `{"uuid":"x1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.title != "Start Job x1" {
		t.Errorf("title = %q", p.title)
	}
}

func TestPopNoTrack(t *testing.T) {
	in := map[string]any{"a": 1, "no_track": true}
	out, skip := popNoTrack(in)
	if !skip || len(out) != 1 || out["a"] != 1 {
		t.Fatalf("got %v %v", out, skip)
	}
	if _, ok := in["no_track"]; !ok {
		t.Error("caller's map must not be mutated")
	}
	out, skip = popNoTrack(map[string]any{"a": 1})
	if skip || len(out) != 1 {
		t.Fatalf("got %v %v", out, skip)
	}
	if _, skip = popNoTrack(map[string]any{"no_track": "TRUE"}); !skip {
		t.Error("string true must opt out")
	}
}

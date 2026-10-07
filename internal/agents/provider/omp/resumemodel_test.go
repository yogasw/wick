package omp

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// stubModelFns fixes what the shared rules answer: explicit = the live
// Default model rule, own = the instance's own model.
func stubModelFns(t *testing.T, explicit, own string) {
	t.Helper()
	pe, po := explicitDefaultFn, ownModelFn
	explicitDefaultFn = func(context.Context, provider.Instance) string { return explicit }
	ownModelFn = func(context.Context, provider.Instance) string { return own }
	t.Cleanup(func() { explicitDefaultFn, ownModelFn = pe, po })
}

// modelSent is the --model the omp argv ends up with ("" = none): wick's
// default rule (defaultModelArgs) and then buildArgs, as Spawn runs them.
func modelSent(ins provider.Instance, opt provider.SpawnOptions, resume, cache string, extra []string) string {
	opt.Instance = &ins
	if m := defaultModelArgs(context.Background(), ins, opt, provider.OMPProfile(ins), resume, cache, extra); m != nil {
		extra = append(append([]string{}, extra...), m...)
	}
	args := buildArgs(ins, withResume(opt, resume), "", "", extra)
	if i := slices.Index(args, "--model"); i >= 0 {
		return args[i+1]
	}
	return ""
}

// One row per "condition → --model" line of the omp table.
func TestOMPModelTable(t *testing.T) {
	t.Cleanup(provider.SetModelStateDirForTest(t.TempDir())) // no evidence: ProvenDefault is ""
	live := provider.Instance{Type: provider.TypeOMP, Name: "tbl-live", LiveModels: true, LiveModelDefault: "openai-codex/gpt-5.6-luna",
		OMPConfig: &provider.OMPConfig{Profile: "wick-b"}}
	liveNoDefault := live
	liveNoDefault.LiveModelDefault = ""
	router := live
	router.UseAIRouter = true
	foreignPath := "/h/.omp/profiles/wick-a/agent/sessions/-w/x_sid.jsonl"

	rows := []struct {
		name          string
		ins           provider.Instance
		opt           provider.SpawnOptions
		resume        string
		writer        string // recorded last writer of sid ("" = none)
		extra         []string
		explicit, own string // what the shared rules answer
		want          string
	}{
		{"session pin wins", live, provider.SpawnOptions{ModelID: "openai-codex/gpt-5.2"}, "", "", nil, "openai-codex/gpt-5.6-luna", "", "openai-codex/gpt-5.2"},
		{"AI router: no --model", router, provider.SpawnOptions{ModelID: "openai-codex/gpt-5.2"}, "", "", nil, "openai-codex/gpt-5.6-luna", "", ""},
		{"--model in extra args stays", live, provider.SpawnOptions{}, "", "", []string{"--model", "x/y"}, "openai-codex/gpt-5.6-luna", "", "x/y"},
		{"live + chosen Default model: always sent", live, provider.SpawnOptions{}, "", "", nil, "openai-codex/gpt-5.6-luna", "", "openai-codex/gpt-5.6-luna"},
		{"live + Default, resuming own transcript", live, provider.SpawnOptions{ResumeID: "sid"}, "sid", "wick-b", nil, "openai-codex/gpt-5.6-luna", "", "openai-codex/gpt-5.6-luna"},
		{"live, no Default, no evidence, fresh: omp default", liveNoDefault, provider.SpawnOptions{}, "", "", nil, "", "openai-codex/first", ""},
		{"no Default, resume another profile's transcript: own model", liveNoDefault, provider.SpawnOptions{ResumeID: "sid"}, foreignPath, "", nil, "", "openai-codex/first", "openai-codex/first"},
		{"no Default, A→B→A back on A after B wrote: own model", liveNoDefault, provider.SpawnOptions{ResumeID: "sid"}, "sid", "wick-a", nil, "", "openai-codex/first", "openai-codex/first"},
		{"no Default, resume own last turn: omp keeps it", liveNoDefault, provider.SpawnOptions{ResumeID: "sid"}, "sid", "wick-b", nil, "", "openai-codex/first", ""},
		{"no Default, foreign resume, own model unknown: nothing", liveNoDefault, provider.SpawnOptions{ResumeID: "sid"}, foreignPath, "", nil, "", "", ""},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			stubModelFns(t, r.explicit, r.own)
			cache := filepath.Join(t.TempDir(), ".omp")
			if r.writer != "" {
				noteWriter(cache, r.writer, "sid")
			}
			if got := modelSent(r.ins, r.opt, r.resume, cache, r.extra); got != r.want {
				t.Fatalf("--model = %q, want %q", got, r.want)
			}
		})
	}
}

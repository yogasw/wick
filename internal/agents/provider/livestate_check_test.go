//go:build livestate

package provider

import (
	"os"
	"path/filepath"
	"testing"
)

// The real omp/yoga case on a COPY of the host's model-state and the
// profile's own `omp models --json` (LIVESTATE_DIR): gpt-5.5 must come
// out marked and never the default.
func TestLiveStateYoga(t *testing.T) {
	dir := os.Getenv("LIVESTATE_DIR")
	if dir == "" {
		t.Skip("LIVESTATE_DIR not set")
	}
	t.Cleanup(SetModelStateDirForTest(filepath.Join(dir, "model-state")))
	out, err := os.ReadFile(filepath.Join(dir, "yoga-models.json"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := parseOMPModels(out)
	if err != nil {
		t.Fatal(err)
	}
	ins := Instance{Type: TypeOMP, Name: "yoga", LiveModels: true, LiveModelDefault: os.Getenv("LIVE_DEFAULT"), OMPConfig: &OMPConfig{Profile: "wick-yoga"}}
	for _, r := range EffectiveLiveModels(ins, all) {
		if r.ID == "openai-codex/gpt-5.5" || r.Default {
			t.Logf("%s unavailable=%v default=%v", r.ID, r.Unavailable, r.Default)
		}
		if r.ID == "openai-codex/gpt-5.5" && (!r.Unavailable || r.Default) {
			t.Fatalf("gpt-5.5 offered as usable: %+v", r)
		}
	}
}

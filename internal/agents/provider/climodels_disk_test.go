package provider

import (
	"context"
	"testing"
	"time"
)

// The live list survives a restart: a fresh process (empty memory) serves
// the list the last one stored, without harvesting or running the CLI —
// only Refresh does that. A file from another profile is not used.
func TestCLIModelsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	prevDir := cliModelsDir
	cliModelsDir = func() string { return dir }
	t.Cleanup(func() { cliModelsDir = prevDir })
	reset := func() {
		cliModelsMu.Lock()
		cliModelsCache = map[string]cliModelsEntry{}
		cliModelsMu.Unlock()
	}
	reset()
	t.Cleanup(reset)

	harvests := 0
	RegisterModelHarvester(TypeOMP, func(context.Context, Instance) ([]ModelSeed, error) {
		harvests++
		return []ModelSeed{{ID: "openai-codex/gpt-6-luna"}}, nil
	})
	t.Cleanup(func() { RegisterModelHarvester(TypeOMP, nil) })
	ins := Instance{Type: TypeOMP, Name: "yoga", OMPConfig: &OMPConfig{Profile: "wick-yoga"}}

	if m, _, _ := CachedCLIModels(context.Background(), ins, false); len(m) != 1 || harvests != 1 {
		t.Fatalf("first read: %v, harvests %d", m, harvests)
	}
	reset() // restart: memory gone, file stays
	m, at, err := CachedCLIModels(context.Background(), ins, false)
	if err != nil || len(m) != 1 || m[0].ID != "openai-codex/gpt-6-luna" || at.IsZero() {
		t.Fatalf("after restart: %v %v %v", m, at, err)
	}
	time.Sleep(50 * time.Millisecond) // a background harvest would have run by now
	if harvests != 1 {
		t.Fatalf("restart re-read the list (%d harvests); the file should serve it", harvests)
	}

	reset()
	other := Instance{Type: TypeOMP, Name: "yoga", OMPConfig: &OMPConfig{Profile: "wick-other"}}
	if e, ok := cliModelsLookup(other, cliModelsKey(other)); ok {
		t.Fatalf("a file of another profile was used: %+v", e)
	}

	InvalidateCLIModels(ins)
	reset()
	if _, ok := cliModelsLookup(ins, cliModelsKey(ins)); ok {
		t.Fatal("an invalidated list came back from disk")
	}
}

package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func liveTestInstance(t *testing.T) Instance {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "opencode")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	return Instance{
		Type: TypeOpencode, Name: "oc-live", Binary: bin, ModelSelect: true, LiveModels: true,
		OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()},
	}
}

func stubCLIModels(t *testing.T, out string, err error) *int {
	t.Helper()
	calls := 0
	prev := cliModelsRunner
	cliModelsRunner = func(ctx context.Context, bin string, args, env []string) ([]byte, error) {
		calls++
		return []byte(out), err
	}
	t.Cleanup(func() {
		cliModelsRunner = prev
		cliModelsMu.Lock()
		cliModelsCache = map[string]cliModelsEntry{}
		cliModelsMu.Unlock()
	})
	return &calls
}

// Event-driven, no timer: a read never runs the CLI (a cold cache with no
// harvest stays empty: "click Refresh"); only an explicit refresh does, at
// most once per cliModelsMinRefresh; a fetched list does not expire.
func TestCachedCLIModelsReadsNeverExec(t *testing.T) {
	ins := liveTestInstance(t)
	calls := stubCLIModels(t, "openai/gpt-5.5\nopenai/gpt-5.5-mini\n", nil)
	now := time.Unix(1000, 0)
	cliModelsNow = func() time.Time { return now }
	t.Cleanup(func() { cliModelsNow = time.Now })

	if m, _, err := CachedCLIModels(context.Background(), ins, false); err != nil || len(m) != 0 || *calls != 0 {
		t.Fatalf("cold read: %v %v calls=%d", m, err, *calls)
	}
	if m, _, err := CachedCLIModels(context.Background(), ins, true); err != nil || len(m) != 2 || *calls != 1 {
		t.Fatalf("refresh: %v %v calls=%d", m, err, *calls)
	}
	now = now.Add(24 * time.Hour) // no TTL: still served
	for i := 0; i < 3; i++ {
		if m, _, _ := CachedCLIModels(context.Background(), ins, false); len(m) != 2 {
			t.Fatalf("read after a day: %v", m)
		}
	}
	if *calls != 1 {
		t.Fatalf("reads exec'd: %d calls", *calls)
	}
	CachedCLIModels(context.Background(), ins, true)
	if *calls != 2 {
		t.Fatalf("refresh did not exec: %d", *calls)
	}
	CachedCLIModels(context.Background(), ins, true) // inside the minimum interval
	if *calls != 2 {
		t.Fatalf("refresh inside the minimum interval exec'd: %d", *calls)
	}
}

// A cold read is filled by the type's harvester (files / running server),
// never the CLI; Peek starts that harvest, not a CLI run.
func TestCachedCLIModelsColdReadHarvests(t *testing.T) {
	ins := liveTestInstance(t)
	calls := stubCLIModels(t, "x/y\n", nil)
	harvested := 0
	RegisterModelHarvester(ins.Type, func(context.Context, Instance) ([]ModelSeed, error) {
		harvested++
		return []ModelSeed{{ID: "openai/from-files"}}, nil
	})
	t.Cleanup(func() { RegisterModelHarvester(ins.Type, nil) })
	m, at, err := CachedCLIModels(context.Background(), ins, false)
	if err != nil || len(m) != 1 || m[0].ID != "openai/from-files" || at.IsZero() || *calls != 0 {
		t.Fatalf("cold read: %v %v %v calls=%d", m, at, err, *calls)
	}
	if _, src := CLIModelsInfo(ins); src != harvestSource[ins.Type] {
		t.Fatalf("source %q", src)
	}
	CachedCLIModels(context.Background(), ins, false)
	if harvested != 1 {
		t.Fatalf("harvested %d times for an unchanged account", harvested)
	}
}

func TestCachedCLIModelsFailedRefreshKeepsList(t *testing.T) {
	ins := liveTestInstance(t)
	stubCLIModels(t, "openai/gpt-5.5\n", nil)
	now := time.Unix(1000, 0)
	cliModelsNow = func() time.Time { return now }
	t.Cleanup(func() { cliModelsNow = time.Now })
	CachedCLIModels(context.Background(), ins, true)
	cliModelsRunner = func(ctx context.Context, bin string, args, env []string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	now = now.Add(cliModelsMinRefresh)
	m, _, err := CachedCLIModels(context.Background(), ins, true)
	if err == nil || len(m) != 1 {
		t.Fatalf("want last good list + error, got %v %v", m, err)
	}
}

func TestFilterLiveModelsHostedAndFilter(t *testing.T) {
	ins := liveTestInstance(t)
	all := []ModelSeed{{ID: "opencode/big-pickle"}, {ID: "opencode-go/kimi"}, {ID: "openai/gpt-5.5"}, {ID: "openai/gpt-5.5-mini"}, {ID: "anthropic/claude-sonnet"}, {ID: "google/gemini-3"}}
	ids := func(ms []ModelSeed) string {
		var s []string
		for _, m := range ms {
			s = append(s, m.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(FilterLiveModels(ins, all)); strings.Contains(got, "opencode/") {
		t.Fatalf("hosted leaked without opt-in: %s", got)
	}
	ins.LiveModelFilter = "claude|gpt !mini"
	if got := ids(FilterLiveModels(ins, all)); got != "openai/gpt-5.5,anthropic/claude-sonnet" {
		t.Fatalf("filter: %s", got)
	}
	// logged into opencode Zen → hosted models are the account itself
	ins.LiveModelFilter = ""
	os.MkdirAll(filepath.Join(ins.OpencodeConfig.DataDir, "opencode"), 0o700)
	os.WriteFile(filepath.Join(ins.OpencodeConfig.DataDir, "opencode", "auth.json"), []byte(`{"opencode":{"type":"api"}}`), 0o600)
	if got := ids(FilterLiveModels(ins, all)); !strings.HasPrefix(got, "opencode/big-pickle") {
		t.Fatalf("zen login should allow hosted: %s", got)
	}
}

func TestLiveDefaultModelAndEffectiveModels(t *testing.T) {
	ins := liveTestInstance(t)
	stubCLIModels(t, "openai/gpt-5.5\nanthropic/claude-sonnet\nopencode/big-pickle\n", nil)
	// Nothing known yet: no CLI run; the typed Default (none) applies.
	if got := LiveDefaultModel(context.Background(), ins); got != "" {
		t.Fatalf("cold: %q", got)
	}
	CachedCLIModels(context.Background(), ins, true) // the user's Refresh
	if got := LiveDefaultModel(context.Background(), ins); got != "openai/gpt-5.5" {
		t.Fatalf("first match: %q", got)
	}
	ins.LiveModelDefault = "anthropic/claude-sonnet"
	if got := LiveDefaultModel(context.Background(), ins); got != "anthropic/claude-sonnet" {
		t.Fatalf("pin: %q", got)
	}
	em := ins.EffectiveModels()
	if len(em) != 2 || em[0].ID != "anthropic/claude-sonnet" {
		t.Fatalf("effective models (pin first, no hosted): %v", em)
	}
	ins.LiveModelDefault = "gone/model"
	if got := LiveDefaultModel(context.Background(), ins); got != "openai/gpt-5.5" {
		t.Fatalf("vanished pin falls back to first: %q", got)
	}
	ins.LiveModels = false
	if got := LiveDefaultModel(context.Background(), ins); got != "" {
		t.Fatalf("live off: %q", got)
	}
}

// A finished turn harvests the instance's list (files / running server) in
// the background — no CLI — and a refusal does too.
func TestTurnEndHarvestsModels(t *testing.T) {
	RegisterModelSets(TypeOMP, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOMP, nil) })
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	calls := stubCLIModels(t, "x/y\n", nil)
	ins := Instance{Type: TypeOMP, Name: "harvest-turn", LiveModels: true, OMPConfig: &OMPConfig{Profile: "wick-h"}}
	harvested := make(chan struct{}, 4)
	RegisterModelHarvester(TypeOMP, func(context.Context, Instance) ([]ModelSeed, error) {
		harvested <- struct{}{}
		return []ModelSeed{{ID: "openai-codex/gpt-5.6-luna"}}, nil
	})
	t.Cleanup(func() { RegisterModelHarvester(TypeOMP, nil) })
	w := newModelTurnWatch(&ins, "openai-codex/gpt-5.6-luna")
	w.observe(`{"type":"agent_end","messages":[]}`)
	select {
	case <-harvested:
	case <-time.After(2 * time.Second):
		t.Fatal("turn end did not harvest")
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(PeekCLIModels(ins)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m := PeekCLIModels(ins); len(m) != 1 || *calls != 0 {
		t.Fatalf("cache %v, CLI calls %d", m, *calls)
	}
}

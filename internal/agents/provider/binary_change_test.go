package provider

import (
	"os"
	"path/filepath"
	"testing"
)

// A binary change on a live-models instance drops the cached list AND
// reads it again once; an unchanged binary, a failed probe or live models
// off never runs the CLI for it.
func TestPersistFromStatusRefreshesModelsOnBinaryChange(t *testing.T) {
	setIsolatedAppName(t)
	var refreshed []string
	prev := refreshCLIModelsAfterBinaryChange
	refreshCLIModelsAfterBinaryChange = func(ins Instance) { refreshed = append(refreshed, ins.Name) }
	t.Cleanup(func() { refreshCLIModelsAfterBinaryChange = prev })

	dir := t.TempDir()
	oldBin := filepath.Join(dir, "v1", "omp")
	newBin := filepath.Join(dir, "v2", "omp")
	for _, p := range []string{oldBin, newBin} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	live := Instance{Type: TypeOMP, Name: "live", LiveModels: true}
	ok := func(ins Instance, bin string) Status {
		return Status{Instance: ins, Path: bin, PathFound: true, Version: "omp/1.0.0"}
	}

	persistFromStatus(TypeOMP, "live", ok(live, oldBin))
	if len(refreshed) != 0 {
		t.Fatalf("first probe refreshed: %v", refreshed)
	}
	persistFromStatus(TypeOMP, "live", ok(live, oldBin))
	if len(refreshed) != 0 {
		t.Fatalf("unchanged binary refreshed: %v", refreshed)
	}
	persistFromStatus(TypeOMP, "live", ok(live, newBin))
	if len(refreshed) != 1 {
		t.Fatalf("binary change: refreshed %v, want once", refreshed)
	}

	// Broken new binary: nothing to list models with.
	bad := ok(live, oldBin)
	bad.VersionErr = "exec failed"
	persistFromStatus(TypeOMP, "live", bad)
	if len(refreshed) != 1 {
		t.Fatalf("failed probe refreshed: %v", refreshed)
	}

	// Live models off: the CLI list is not used, so it is not read.
	off := Instance{Type: TypeOMP, Name: "off"}
	persistFromStatus(TypeOMP, "off", ok(off, oldBin))
	persistFromStatus(TypeOMP, "off", ok(off, newBin))
	if len(refreshed) != 1 {
		t.Fatalf("live models off refreshed: %v", refreshed)
	}
}

func TestSpawnBinary(t *testing.T) {
	if got := SpawnBinary("/fixed/omp", nil); got != "/fixed/omp" {
		t.Errorf("no fn: %q", got)
	}
	if got := SpawnBinary("/fixed/omp", func() string { return "/v2/omp" }); got != "/v2/omp" {
		t.Errorf("fn wins: %q", got)
	}
	if got := SpawnBinary("/fixed/omp", func() string { return "" }); got != "/fixed/omp" {
		t.Errorf("empty fn falls back: %q", got)
	}
}

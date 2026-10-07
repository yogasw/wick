package opencode

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// mkStore makes a data folder that holds an opencode DB.
func mkStore(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode", "opencode.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

type syncCall struct {
	args []string
	xdg  string
}

// fakeSync records every opencode call with the XDG_DATA_HOME it got.
func fakeSync(t *testing.T, fail string) *[]syncCall {
	t.Helper()
	var calls []syncCall
	prev := syncRunner
	syncRunner = func(_ context.Context, _, _ string, env []string, args ...string) ([]byte, error) {
		c := syncCall{args: args}
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "XDG_DATA_HOME="); ok {
				c.xdg = v
			}
		}
		calls = append(calls, c)
		if args[0] == fail {
			return nil, os.ErrPermission
		}
		if args[0] == "export" {
			return []byte("Exporting session: " + args[1] + "\n{\"info\":{\"id\":\"" + args[1] + "\"},\"messages\":[]}"), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { syncRunner = prev })
	return &calls
}

func TestSyncSource(t *testing.T) {
	base := t.TempDir()
	a, b := mkStore(t, filepath.Join(base, "a")), mkStore(t, filepath.Join(base, "b"))
	cases := []struct {
		name   string
		owners map[string]string
		resume string
		target string
		want   string
	}{
		{"writer is another folder", map[string]string{"ses_1": a}, "ses_1", b, a},
		{"last writer is the target", map[string]string{"ses_1": b}, "ses_1", b, ""},
		{"id unknown, last spawn folder", map[string]string{"": a}, "ses_1", b, a},
		{"no resume", map[string]string{"": a}, "", b, ""},
		{"writer unknown", map[string]string{}, "ses_1", b, ""},
		{"writer folder gone", map[string]string{"ses_1": filepath.Join(base, "gone")}, "ses_1", b, ""},
	}
	for _, c := range cases {
		if got := syncSource(c.owners, c.resume, c.target); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// Moving to B exports with A's env and imports with B's, keeps the id,
// and records B; the next turn on B copies nothing; back on A copies B's
// newer turns into A.
func TestCarryHistoryRoundTrip(t *testing.T) {
	base := t.TempDir()
	a, b := mkStore(t, filepath.Join(base, "a")), mkStore(t, filepath.Join(base, "b"))
	state := filepath.Join(base, "sess", ".opencode-wick")
	calls := fakeSync(t, "")

	carryHistory(context.Background(), "opencode", base, state, "", a) // fresh on A
	if len(*calls) != 0 {
		t.Fatalf("fresh spawn copied: %v", *calls)
	}
	carryHistory(context.Background(), "opencode", base, state, "ses_1", b)
	want := []syncCall{{[]string{"export", "ses_1"}, a}, {nil, b}}
	if len(*calls) != 2 || !slices.Equal((*calls)[0].args, want[0].args) || (*calls)[0].xdg != a ||
		(*calls)[1].args[0] != "import" || (*calls)[1].xdg != b {
		t.Fatalf("A→B calls = %+v", *calls)
	}
	if f := (*calls)[1].args[1]; filepath.Dir(f) != state {
		t.Fatalf("export file %q outside the session state dir", f)
	} else if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("export file left behind")
	}
	if got := readOwners(state)["ses_1"]; got != b {
		t.Fatalf("owner = %q, want B", got)
	}
	carryHistory(context.Background(), "opencode", base, state, "ses_1", b)
	if len(*calls) != 2 {
		t.Fatalf("second turn on B copied again: %v", *calls)
	}
	carryHistory(context.Background(), "opencode", base, state, "ses_1", a)
	if len(*calls) != 4 || (*calls)[2].xdg != b || (*calls)[3].xdg != a {
		t.Fatalf("B→A calls = %+v", *calls)
	}
}

// A failed copy keeps the old writer, so the next turn tries again, and
// the id is left for the resume to fail on visibly.
func TestCarryHistoryFailedImport(t *testing.T) {
	base := t.TempDir()
	a, b := mkStore(t, filepath.Join(base, "a")), mkStore(t, filepath.Join(base, "b"))
	state := filepath.Join(base, ".opencode-wick")
	writeOwners(state, map[string]string{"ses_1": a})
	fakeSync(t, "import")
	carryHistory(context.Background(), "opencode", base, state, "ses_1", b)
	if got := readOwners(state)["ses_1"]; got != a {
		t.Fatalf("owner = %q after a failed copy, want A", got)
	}
}

// An id that could name a file outside the state dir is never exported,
// and the owners file is private like the export beside it.
func TestCarryHistoryRefusesPathID(t *testing.T) {
	base := t.TempDir()
	a, b := mkStore(t, filepath.Join(base, "a")), mkStore(t, filepath.Join(base, "b"))
	state := filepath.Join(base, ".opencode-wick")
	writeOwners(state, map[string]string{"../../x": a})
	calls := fakeSync(t, "")
	carryHistory(context.Background(), "opencode", base, state, "../../x", b)
	if len(*calls) != 0 {
		t.Fatalf("path-like id exported: %+v", *calls)
	}
	for _, f := range []string{state, filepath.Join(state, ownersFile)} {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode %v, want private", f, fi.Mode().Perm())
		}
	}
}

// A PWD inherited from wick's own environment is overridden by the
// workspace; getenv-style readers take the last entry.
func TestPinPWD(t *testing.T) {
	env := pinPWD([]string{"PWD=/repo/pkg", "A=1"}, "/work")
	last := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PWD="); ok {
			last = v
		}
	}
	if last != "/work" {
		t.Fatalf("PWD = %q, want /work (env %q)", last, env)
	}
	if got := pinPWD([]string{"A=1"}, ""); len(got) != 1 {
		t.Fatalf("empty dir changed env: %q", got)
	}
}

// opencode's registered carrier: a source folder with a DB carries by copy;
// one without says why not.
func TestCarrySwitchRegistered(t *testing.T) {
	base := t.TempDir()
	a := provider.Instance{Type: provider.TypeOpencode, Name: "a", OpencodeConfig: &provider.OpencodeConfig{DataDir: mkStore(t, filepath.Join(base, "a"))}}
	gone := provider.Instance{Type: provider.TypeOpencode, Name: "g", OpencodeConfig: &provider.OpencodeConfig{DataDir: filepath.Join(base, "g")}}
	if c := carrySwitch(a, provider.Instance{}, "ses_1"); !c.Copied || c.Reason != "" {
		t.Fatalf("source with DB: %+v", c)
	}
	if c := carrySwitch(gone, provider.Instance{}, "ses_1"); c.Reason == "" {
		t.Fatal("missing source DB carried")
	}
}

// storeEnv is exactly the env a spawn in that folder gets.
func TestStoreEnvMatchesSpawnEnv(t *testing.T) {
	dir := t.TempDir()
	want, _ := provider.OpencodeEnv(provider.Instance{Type: provider.TypeOpencode, Name: "x", OpencodeConfig: &provider.OpencodeConfig{DataDir: dir}})
	if got := storeEnv(dir); !slices.Equal(got, want) || !slices.Contains(got, "XDG_DATA_HOME="+dir) {
		t.Fatalf("storeEnv %q, spawn env %q", got, want)
	}
}

// export/import run through the helper guard ("opencode-export" /
// "opencode-import").
func TestSyncRunnerUsesHelperGuard(t *testing.T) {
	var labels []string
	t.Cleanup(provider.ObserveHelpersForTest(func(label string, _ *provider.Instance) { labels = append(labels, label) }))
	bin := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '{}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := syncRunner(context.Background(), bin, t.TempDir(), storeEnv(t.TempDir()), "export", "ses_1"); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != "opencode-export" {
		t.Fatalf("labels = %v", labels)
	}
}

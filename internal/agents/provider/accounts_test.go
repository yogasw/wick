package provider

import (
	"path/filepath"
	"testing"
)

func TestDefaultOMPProfile(t *testing.T) {
	cases := map[string]string{
		"omp":      "wick-omp",
		"My Omp_2": "wick-my-omp_2",
		"  ":       "wick-default",
		"a/b..c":   "wick-a-b..c",
	}
	for in, want := range cases {
		if got := DefaultOMPProfile(in); got != want || !ValidOMPProfile(got) {
			t.Errorf("DefaultOMPProfile(%q) = %q (valid=%v), want %q", in, got, ValidOMPProfile(got), want)
		}
	}
	long := DefaultOMPProfile("x123456789012345678901234567890123456789012345678901234567890123456789")
	if len(long) > 64 || !ValidOMPProfile(long) {
		t.Errorf("long profile %q", long)
	}
}

func TestAccountStoreSurvivesRename(t *testing.T) {
	t.Setenv("WICK_DATA_DIR", t.TempDir())
	ins := Instance{Type: TypeOMP, Name: "first"}
	applyAccountConfig(&ins, "", "")
	p, _ := accountConfigToUser(ins)
	if p != "wick-first" {
		t.Fatal(p)
	}
	// Rename: persisted value comes back, not a re-derived one.
	renamed := Instance{Type: TypeOMP, Name: "second"}
	applyAccountConfig(&renamed, p, "")
	if OMPProfile(renamed) != "wick-first" {
		t.Fatalf("rename moved account: %q", OMPProfile(renamed))
	}

	oc := Instance{Type: TypeOpencode, Name: "oc"}
	applyAccountConfig(&oc, "", "")
	_, d := accountConfigToUser(oc)
	if filepath.Base(d) != "oc" || filepath.Base(filepath.Dir(d)) != "opencode" {
		t.Fatalf("default dir %q", d)
	}
	env, err := OpencodeEnv(oc)
	if err != nil || env[0] != "XDG_DATA_HOME="+d {
		t.Fatalf("env %v %v", env, err)
	}
	if f, _ := OpencodeAuthFile(oc); f != filepath.Join(d, "opencode", "auth.json") {
		t.Fatal(f)
	}
	if _, err := DefaultOpencodeDataDir("../x"); err == nil {
		t.Error("path-escaping name accepted")
	}
}

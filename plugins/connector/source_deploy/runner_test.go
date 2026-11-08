//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeScript puts a bash script on disk and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "deploy.sh")
	if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitDone(t *testing.T, svc *Service, id string) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := svc.Get(id, 50)
		if err != nil {
			t.Fatal(err)
		}
		if st.State != StateRunning {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s still running", id)
	return Status{}
}

func TestDeployRunsScriptWithProfileArgs(t *testing.T) {
	svc := New(t.TempDir())
	script := writeScript(t, `echo "== build $1 $2"; echo "args:$*"`)
	if _, err := svc.SaveProfile(Profile{Name: "dev", Script: script, Args: []string{"--ref", "develop"}}); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Start(context.Background(), StartOptions{ExtraArgs: []string{"--force"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "dev" {
		t.Fatalf("profile = %q, want the default dev", r.Profile)
	}
	st := waitDone(t, svc, r.ID)
	if st.State != StateSucceeded || st.ExitCode == nil || *st.ExitCode != 0 {
		t.Fatalf("state=%s exit=%v tail=%s", st.State, st.ExitCode, st.Tail)
	}
	if st.Stage != "build --ref develop" {
		t.Fatalf("stage = %q", st.Stage)
	}
	if !strings.Contains(st.Tail, "args:--ref develop --force") {
		t.Fatalf("tail = %q", st.Tail)
	}
}

func TestDeployFailureKeepsExitCode(t *testing.T) {
	svc := New(t.TempDir())
	script := writeScript(t, `echo "== test"; echo boom >&2; exit 3`)
	if _, err := svc.SaveProfile(Profile{Name: "x", Script: script}); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st := waitDone(t, svc, r.ID)
	if st.State != StateFailed || *st.ExitCode != 3 || !strings.Contains(st.Tail, "boom") {
		t.Fatalf("state=%s exit=%v tail=%q", st.State, st.ExitCode, st.Tail)
	}
}

func TestOneRunAtATimeAndCancel(t *testing.T) {
	svc := New(t.TempDir())
	script := writeScript(t, `echo "== wait"; sleep 30`)
	if _, err := svc.SaveProfile(Profile{Name: "slow", Script: script}); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Start(context.Background(), StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(context.Background(), StartOptions{}); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("second start: %v", err)
	}
	c, err := svc.Cancel("")
	if err != nil {
		t.Fatal(err)
	}
	if c.State != StateCanceled {
		t.Fatalf("state = %s", c.State)
	}
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(r.PID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(r.PID) {
		t.Fatalf("pid %d survived cancel", r.PID)
	}
}

func TestURLScriptIsDownloadedAndHashed(t *testing.T) {
	svc := New(t.TempDir())
	var got string
	svc.Fetch = func(_ context.Context, url string) ([]byte, error) {
		got = url
		return []byte("echo fetched\n"), nil
	}
	r, err := svc.Start(context.Background(), StartOptions{}) // no profile: the default script
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultScriptURL+".sh" {
		t.Fatalf("fetched %q", got)
	}
	if len(r.ScriptSHA) != 64 {
		t.Fatalf("sha = %q", r.ScriptSHA)
	}
	if st := waitDone(t, svc, r.ID); st.State != StateSucceeded || !strings.Contains(st.Tail, "fetched") {
		t.Fatalf("state=%s tail=%q", st.State, st.Tail)
	}
}

func TestProfilesDefaultMovesOnDelete(t *testing.T) {
	svc := New(t.TempDir())
	script := writeScript(t, "true")
	for _, n := range []string{"a", "b"} {
		if _, err := svc.SaveProfile(Profile{Name: n, Script: script}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SaveProfile(Profile{Name: "b", Script: script, Default: true}); err != nil {
		t.Fatal(err)
	}
	ps, _ := svc.Profiles()
	if len(ps) != 2 || ps[0].Name != "b" || !ps[0].Default || ps[1].Default {
		t.Fatalf("profiles = %+v", ps)
	}
	if err := svc.DeleteProfile("b"); err != nil {
		t.Fatal(err)
	}
	ps, _ = svc.Profiles()
	if len(ps) != 1 || !ps[0].Default {
		t.Fatalf("after delete = %+v", ps)
	}
	if _, err := svc.SaveProfile(Profile{Name: "c", Script: "http://insecure.example/x.sh"}); err == nil {
		t.Fatal("http script URL accepted")
	}
}

func TestParseArgs(t *testing.T) {
	a, _ := ParseArgs(`["--ref","my branch"]`)
	b, _ := ParseArgs(" --ref  develop ")
	if len(a) != 2 || a[1] != "my branch" || len(b) != 2 || b[1] != "develop" {
		t.Fatalf("a=%q b=%q", a, b)
	}
	if _, err := ParseArgs("[nope"); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

// TestMain keeps the wrapper's completion callback out of the run logs the
// tests read: by default it is this test binary, which would print its
// usage there.
func TestMain(m *testing.M) {
	notifyExe = func() string { return "true" }
	os.Exit(m.Run())
}

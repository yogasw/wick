package omp

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"context"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/cliserver"
	"sync"
)

// writeTranscript makes an omp transcript for id in profile's store.
func writeTranscript(t *testing.T, root, profile, id string) string {
	t.Helper()
	dir := filepath.Join(root, "profiles", profile, "agent", "sessions", "-w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "2026-09-30T00-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindSessionPath(t *testing.T) {
	root := t.TempDir()
	waba := writeTranscript(t, root, "wick-waba", "01a0f26e-9894-7246-a018-a646ed52ee9b")
	writeTranscript(t, root, "wick-yoga", "01a0ef79-own")

	// Found in another profile, full id and by prefix.
	if got := findSessionPath(root, "wick-yoga", "01a0f26e-9894-7246-a018-a646ed52ee9b"); got != waba {
		t.Fatalf("full id: got %q want %q", got, waba)
	}
	if got := findSessionPath(root, "wick-yoga", "01a0f26e"); got != waba {
		t.Fatalf("prefix: got %q want %q", got, waba)
	}
	// The running profile has it: the id stays as it is.
	if got := findSessionPath(root, "wick-yoga", "01a0ef79-own"); got != "" {
		t.Fatalf("own profile: got %q", got)
	}
	// Nowhere, empty, or something that is not an id.
	for _, id := range []string{"missing", "", "../x", "*"} {
		if got := findSessionPath(root, "wick-yoga", id); got != "" {
			t.Fatalf("%q: got %q", id, got)
		}
	}
}

// Two profiles holding the id (a copied store): the newest file wins.
func TestFindSessionPathNewest(t *testing.T) {
	root := t.TempDir()
	old := writeTranscript(t, root, "a", "sid")
	newer := writeTranscript(t, root, "b", "sid")
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, past, past)
	if got := findSessionPath(root, "c", "sid"); got != newer {
		t.Fatalf("got %q want %q", got, newer)
	}
}

func TestResumeArgCachesPath(t *testing.T) {
	root, cache := t.TempDir(), filepath.Join(t.TempDir(), ".omp")
	p := writeTranscript(t, root, "wick-waba", "sid-1")
	if got := resumeArg(root, "wick-yoga", "sid-1", cache); got != p {
		t.Fatalf("first: %q", got)
	}
	if _, err := os.Stat(filepath.Join(cache, resumePathsFile)); err != nil {
		t.Fatalf("path not cached: %v", err)
	}
	// The cached path is used without a scan: move the profiles away and
	// the cache still answers while the file exists.
	if m := readResumePaths(cache); m["sid-1"] != p {
		t.Fatalf("cache = %v", m)
	}
	// Back on the owning profile the id is enough.
	if got := resumeArg(root, "wick-waba", "sid-1", cache); got != "sid-1" {
		t.Fatalf("owner: %q", got)
	}
	// Gone everywhere: the id goes to omp, which reports the miss.
	_ = os.Remove(p)
	if got := resumeArg(root, "wick-yoga", "sid-1", cache); got != "sid-1" {
		t.Fatalf("missing: %q", got)
	}
	if got := resumeArg(root, "wick-yoga", "", cache); got != "" {
		t.Fatalf("empty: %q", got)
	}
}

// A planted cache entry outside every omp store is ignored: the path
// handed to --resume is only ever one a scan could have found.
func TestResumeArgIgnoresPlantedCache(t *testing.T) {
	root, cache := t.TempDir(), filepath.Join(t.TempDir(), ".omp")
	planted := filepath.Join(t.TempDir(), "2026-09-30T00-00-00-000Z_sid-2.jsonl")
	if err := os.WriteFile(planted, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeResumePaths(cache, map[string]string{"sid-2": planted})
	if got := resumeArg(root, "wick-yoga", "sid-2", cache); got != "sid-2" {
		t.Fatalf("planted path used: %q", got)
	}
	real := writeTranscript(t, root, "wick-waba", "sid-2")
	if got := resumeArg(root, "wick-yoga", "sid-2", cache); got != real {
		t.Fatalf("real transcript: %q, want %q", got, real)
	}
}

func TestOMPRoot(t *testing.T) {
	if got := ompRoot("/h", ""); got != "/h/.omp" {
		t.Fatal(got)
	}
	if got := ompRoot("/h", "/abs/cfg"); got != "/abs/cfg" {
		t.Fatal(got)
	}
}

// RPC mode: --resume carries the path, and the process key ignores it, so
// a path and an id for the same session reuse one process.
func TestRPCArgsResumeByPath(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, Name: "yoga"}
	base := provider.SpawnOptions{Workspace: "/w", Instance: &ins}
	opt := withResume(base, "/r/.omp/profiles/wick-waba/agent/sessions/-w/x_sid.jsonl")
	args := buildRPCArgs(ins, opt, "", "", nil)
	i := slices.Index(args, "--resume")
	if i < 0 || args[i+1] != opt.ResumeID {
		t.Fatalf("argv = %q", args)
	}
	byID := buildRPCArgs(ins, withResume(base, "sid"), "", "", nil)
	if rpcKey("i", "s", "", "/omp", "/w", nil, args) != rpcKey("i", "s", "", "/omp", "/w", nil, byID) {
		t.Fatal("rpcKey depends on the --resume value")
	}
}

// omp's registered carrier: reachable in any profile → carries; nowhere →
// the reason the switch notice shows.
func TestCarryHistoryRegistered(t *testing.T) {
	home := t.TempDir()
	prev := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = prev })
	t.Setenv("PI_CONFIG_DIR", "")
	writeTranscript(t, filepath.Join(home, ".omp"), "wick-waba", "omp-1")
	yoga := provider.Instance{Type: provider.TypeOMP, Name: "yoga", OMPConfig: &provider.OMPConfig{Profile: "wick-yoga"}}
	if c := carryHistory(provider.Instance{}, yoga, "omp-1"); c.Reason != "" || c.Copied {
		t.Fatalf("reachable: %+v", c)
	}
	if c := carryHistory(provider.Instance{}, yoga, "omp-gone"); c.Reason == "" {
		t.Fatal("unreachable transcript carried")
	}
}

// omp's registered yielder: an idle RPC process of another session goes
// when a spawn needs room; the spawning session's own stays, and so does
// one with a turn leased.
func TestIdleRPCYieldsToOtherSpawn(t *testing.T) {
	prev := rpcServers
	rpcServers = newRPCManager()
	t.Cleanup(func() { rpcServers.Shutdown(); rpcServers = prev })
	start := func(context.Context) (*rpcConn, error) {
		done := make(chan struct{})
		var once sync.Once
		return &rpcConn{pid: 1, done: done, kill: func() { once.Do(func() { close(done) }) }}, nil
	}
	acquire := func(inst, sid string) *cliserver.Lease[*rpcConn] {
		l, err := rpcServers.Acquire(context.Background(), cliserver.Spec{Instance: inst, Group: inst + "/" + sid, Key: inst + sid, Idle: time.Hour, Turns: 1}, start)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	acquire("waba", "s-other").Release()
	acquire("yoga", "s-me").Release()
	busy := acquire("waba", "s-busy")
	defer busy.Release()
	if n := provider.YieldIdleServers("s-me", "yoga"); n != 1 {
		t.Fatalf("stopped %d, want 1 (waba/s-other)", n)
	}
	if _, ok := rpcServers.LiveInGroup("yoga/s-me"); !ok {
		t.Fatal("the spawning session's own RPC process was stopped")
	}
	if _, ok := rpcServers.LiveInGroup("waba/s-busy"); !ok {
		t.Fatal("an RPC process with a turn leased was stopped")
	}
}

// auth-broker token goes through the helper guard ("omp-broker-token").
func TestBrokerTokenUsesHelperGuard(t *testing.T) {
	home := t.TempDir() // no token file: the CLI path
	prevHome := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = prevHome })
	var labels []string
	t.Cleanup(provider.ObserveHelpersForTest(func(label string, _ *provider.Instance) { labels = append(labels, label) }))
	bin := filepath.Join(t.TempDir(), "omp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"token\":\"t\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := brokerToken(context.Background(), brokerSpec{bin: bin, profile: "wick-owner"}); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != "omp-broker-token" {
		t.Fatalf("labels = %v", labels)
	}
}

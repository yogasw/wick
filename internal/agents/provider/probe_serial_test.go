package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/userconfig"
)

// probeScript writes an executable stand-in for a CLI's --version.
func probeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Two omp instances are never probed at the same time: the second probe
// finds the first one's lock dir gone, or it reports an overlap.
func TestHeavyProbesNeverOverlap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	dir := t.TempDir()
	lock, overlap := filepath.Join(dir, "lock"), filepath.Join(dir, "overlap")
	body := "mkdir " + lock + " 2>/dev/null || touch " + overlap + "\nsleep 0.3\nrmdir " + lock + " 2>/dev/null\necho 'omp 18.4.4'"
	var wg sync.WaitGroup
	for _, name := range []string{"waba", "yoga"} {
		bin := probeScript(t, t.TempDir(), "omp", body)
		ins := Instance{Type: TypeOMP, Name: "probe-" + name, Binary: bin}
		wg.Add(1)
		go func() { defer wg.Done(); _ = Probe(context.Background(), ins) }()
	}
	wg.Wait()
	if _, err := os.Stat(overlap); err == nil {
		t.Fatal("two omp --version probes ran at the same time")
	}
}

// Light probes (claude/codex/gemini) stay parallel: each waits for the
// other's marker, which only appears if both run at once.
func TestLightProbesStayParallel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	wait := func(mine, other string) string {
		return "touch " + mine + "\ni=0\nwhile [ ! -e " + other + " ] && [ $i -lt 30 ]; do sleep 0.1; i=$((i+1)); done\n[ -e " + other + " ] && echo '1.0.0 (Claude Code)' || { echo serial; exit 1; }"
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string
	for _, c := range []struct{ name, mine, other string }{{"one", a, b}, {"two", b, a}} {
		bin := probeScript(t, t.TempDir(), "claude", wait(c.mine, c.other))
		ins := Instance{Type: TypeClaude, Name: "probe-" + c.name, Binary: bin}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st := Probe(context.Background(), ins); st.VersionErr != "" {
				mu.Lock()
				errs = append(errs, st.VersionErr)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("light probes were serialized: %v", errs)
	}
}

// The version is re-probed only when the binary's fingerprint (resolved
// path, size, mtime) changes — never on age alone, never for a failed
// probe of an unchanged binary (that waits for an explicit Rescan).
func TestProbeFingerprintCache(t *testing.T) {
	bin := probeScript(t, t.TempDir(), "omp", "echo 1")
	ins := Instance{Type: TypeOMP, Name: "fresh", Binary: bin}
	now := time.Now()
	ok := userconfig.ProviderStatus{Path: bin, PathFound: true, Version: "18.4.4",
		VersionAt: now.Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano), Fingerprint: BinaryFingerprint(bin)}
	if !probeStillFresh(ins, ok, now) {
		t.Fatal("unchanged binary re-probed because the probe is old")
	}
	failed := ok
	failed.Version, failed.VersionErr = "", "exit 1"
	if probeNeedsRefresh(ins, failed, now) {
		t.Fatal("a failed probe of an unchanged binary re-probes on every render")
	}
	moved := ok
	moved.Path = bin + "-old"
	if !probeNeedsRefresh(ins, moved, now) {
		t.Fatal("binary resolving elsewhere not re-probed")
	}
	// Updated binary (size changes): fingerprint differs → re-probe.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'omp 18.5.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !probeNeedsRefresh(ins, ok, now) {
		t.Fatal("updated binary not re-probed")
	}
	// Legacy entry (no fingerprint): the old 24h / mtime rule.
	legacy := ok
	legacy.Fingerprint = ""
	legacy.VersionAt = now.Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	if !probeNeedsRefresh(ins, legacy, now) {
		t.Fatal("legacy stale entry kept")
	}
}

// Fingerprint follows symlinks: an updated target changes it.
func TestBinaryFingerprintFollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir := t.TempDir()
	v1 := probeScript(t, dir, "omp-1", "echo 1")
	link := filepath.Join(dir, "omp")
	if err := os.Symlink(v1, link); err != nil {
		t.Fatal(err)
	}
	a := BinaryFingerprint(link)
	v2 := probeScript(t, dir, "omp-2", "echo 22")
	_ = os.Remove(link)
	_ = os.Symlink(v2, link)
	if b := BinaryFingerprint(link); a == "" || a == b {
		t.Fatalf("fingerprint did not follow the link: %q %q", a, b)
	}
}

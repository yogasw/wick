package agentmemory

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The one test that actually reaches GitHub, and it is opt-in:
//
//	WICK_LIVE_INSTALL=1 WICK_LIVE_DIR=/some/scratch go test -run TestLiveInstall ./internal/agents/agentmemory/
//
// It is skipped in every normal run — a gate that downloads 15 MiB from a
// third party is a gate that fails on someone else's outage. It exists
// because the asset NAMES are the one thing a fake server cannot verify: they
// are the upstream project's convention, and the day it renames them, this is
// the test that says so.
//
// Last run 2026-09-25 on linux/amd64: picked ai-memory-linux-x86_64.tar.gz
// from v2.4.0, checksum 590f75dd…, installed a 36.8 MiB binary that reports
// "ai-memory 2.4.0".
func TestLiveInstallAgainstGitHub(t *testing.T) {
	if os.Getenv("WICK_LIVE_INSTALL") != "1" {
		t.Skip("set WICK_LIVE_INSTALL=1 to reach GitHub")
	}
	dir := os.Getenv("WICK_LIVE_DIR")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := releaseInstall{
		repo: "akitaonrails/ai-memory", bin: "ai-memory", dest: dir,
		goos: hostGOOS(), goarch: hostGOARCH(), api: githubAPIBase,
	}.run(ctx)
	t.Log("\n" + out)
	if err != nil {
		t.Fatalf("live install: %v", err)
	}
	withBinDir(t, dir)
	p, err := ResolveBackendBin("ai-memory")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	v, err := exec.CommandContext(ctx, p, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("run --version: %v (%s)", err, v)
	}
	t.Logf("installed binary reports: %s", strings.TrimSpace(string(v)))
}

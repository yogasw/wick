package wick

import (
	"context"
	"strings"
	"testing"
	"time"
)

// tool_shell_bg_test.go covers background shells: run_in_background spawn,
// shell_output polling, shell_kill, the per-session cap, and teardown
// reaping. Skips when bash isn't resolvable (same policy as the
// foreground suite).

func newBgTC(t *testing.T) toolContext {
	t.Helper()
	if _, err := resolveBash(); err != nil {
		t.Skipf("bash not available on this host: %v", err)
	}
	ws := t.TempDir()
	return toolContext{Workspace: ws, Bg: newBgRegistry(ws)}
}

// waitBgStatus polls shell_output until status leaves "running" or the
// deadline passes. Returns the final output text.
func waitBgStatus(t *testing.T, tc toolContext, id string, within time.Duration) string {
	t.Helper()
	out := shellOutputTool(tc)
	deadline := time.Now().Add(within)
	var last string
	for time.Now().Before(deadline) {
		res, isErr := out.handler(context.Background(), map[string]any{"bash_id": id})
		if isErr {
			t.Fatalf("shell_output errored: %s", res)
		}
		last = res
		if !strings.Contains(res, "status: running") {
			return res
		}
		time.Sleep(50 * time.Millisecond)
	}
	return last
}

func TestBg_StartReturnsIDImmediately(t *testing.T) {
	tc := newBgTC(t)
	sh := shellTool(tc)
	start := time.Now()
	res, isErr := sh.handler(context.Background(), map[string]any{
		"command":           "sleep 3",
		"run_in_background": true,
	})
	elapsed := time.Since(start)
	if isErr {
		t.Fatalf("background start errored: %s", res)
	}
	if !strings.Contains(res, "bash_") {
		t.Errorf("want a bash_id in the reply, got %q", res)
	}
	// Must NOT block for the 3s sleep — the whole point of background.
	if elapsed > time.Second {
		t.Errorf("background start blocked %v (should return immediately)", elapsed)
	}
}

func TestBg_OutputCapturedAndExitCode(t *testing.T) {
	tc := newBgTC(t)
	sh := shellTool(tc)
	res, _ := sh.handler(context.Background(), map[string]any{
		"command":           "echo hello-bg && exit 3",
		"run_in_background": true,
	})
	id := extractBashID(t, res)

	final := waitBgStatus(t, tc, id, 10*time.Second)
	if !strings.Contains(final, "hello-bg") {
		t.Errorf("output not captured, got %q", final)
	}
	if !strings.Contains(final, "status: exited") {
		t.Errorf("want status exited, got %q", final)
	}
	if !strings.Contains(final, "exit_code: 3") {
		t.Errorf("want exit_code 3, got %q", final)
	}
}

func TestBg_Kill(t *testing.T) {
	tc := newBgTC(t)
	sh := shellTool(tc)
	res, _ := sh.handler(context.Background(), map[string]any{
		"command":           "sleep 30",
		"run_in_background": true,
	})
	id := extractBashID(t, res)

	kill := shellKillTool(tc)
	kres, isErr := kill.handler(context.Background(), map[string]any{"bash_id": id})
	if isErr {
		t.Fatalf("shell_kill errored: %s", kres)
	}

	final := waitBgStatus(t, tc, id, 8*time.Second)
	if strings.Contains(final, "status: running") {
		t.Errorf("shell still running after kill: %q", final)
	}
	if !strings.Contains(final, "status: killed") {
		t.Errorf("want status killed, got %q", final)
	}
}

func TestBg_OutputUnknownID(t *testing.T) {
	tc := newBgTC(t)
	out := shellOutputTool(tc)
	res, isErr := out.handler(context.Background(), map[string]any{"bash_id": "bash_nope"})
	if !isErr {
		t.Errorf("unknown bash_id should error, got ok: %q", res)
	}
}

func TestBg_KillIdempotent(t *testing.T) {
	tc := newBgTC(t)
	sh := shellTool(tc)
	res, _ := sh.handler(context.Background(), map[string]any{
		"command":           "echo quick",
		"run_in_background": true,
	})
	id := extractBashID(t, res)
	waitBgStatus(t, tc, id, 8*time.Second) // let it finish

	kill := shellKillTool(tc)
	kres, isErr := kill.handler(context.Background(), map[string]any{"bash_id": id})
	if isErr {
		t.Errorf("killing a finished shell should still succeed, got: %s", kres)
	}
}

func TestBg_MaxProcsCap(t *testing.T) {
	tc := newBgTC(t)
	sh := shellTool(tc)
	// Fill the registry with long-lived shells.
	for i := 0; i < maxBgProcs; i++ {
		res, isErr := sh.handler(context.Background(), map[string]any{
			"command":           "sleep 30",
			"run_in_background": true,
		})
		if isErr {
			t.Fatalf("start %d errored: %s", i, res)
		}
	}
	// One past the cap must fail.
	res, isErr := sh.handler(context.Background(), map[string]any{
		"command":           "sleep 30",
		"run_in_background": true,
	})
	if !isErr {
		t.Errorf("start past cap should error, got ok: %q", res)
	}
	if !strings.Contains(res, "too many background shells") {
		t.Errorf("want a cap message, got %q", res)
	}
	tc.Bg.killAll()
}

func TestBg_KillAllReapsRunning(t *testing.T) {
	tc := newBgTC(t)
	sh := shellTool(tc)
	res, _ := sh.handler(context.Background(), map[string]any{
		"command":           "sleep 30",
		"run_in_background": true,
	})
	id := extractBashID(t, res)

	tc.Bg.killAll()

	final := waitBgStatus(t, tc, id, 8*time.Second)
	if strings.Contains(final, "status: running") {
		t.Errorf("killAll left a shell running: %q", final)
	}
}

func TestBg_NilRegistryReportsUnavailable(t *testing.T) {
	if _, err := resolveBash(); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	tc := toolContext{Workspace: t.TempDir()} // Bg == nil
	sh := shellTool(tc)
	res, isErr := sh.handler(context.Background(), map[string]any{
		"command":           "echo x",
		"run_in_background": true,
	})
	if !isErr || !strings.Contains(res, "not available") {
		t.Errorf("nil registry should report unavailable, got isErr=%v %q", isErr, res)
	}
}

// extractBashID pulls the bash_id token out of a background-start reply.
func extractBashID(t *testing.T, reply string) string {
	t.Helper()
	i := strings.Index(reply, "bash_")
	if i < 0 {
		t.Fatalf("no bash_id in reply: %q", reply)
	}
	// bash_ + 8 hex chars.
	end := i + len("bash_") + 8
	if end > len(reply) {
		end = len(reply)
	}
	return reply[i:end]
}

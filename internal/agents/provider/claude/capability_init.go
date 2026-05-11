package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/agents/capability"
	"github.com/yogasw/wick/internal/agents/gate"
)

// init wires this provider into the capability registries. Loaded
// transitively whenever something imports provider/claude/ — the gate
// binary via blank import in cmd/gate, the spawn factory via direct
// use of claude.Spawner, etc. Self-registration keeps "what claude
// can do" co-located with the rest of the claude integration.
func init() {
	capability.Register("claude", capability.Capability{
		HookSupported:  true,
		InterceptScope: "bash+edit+mcp",
	})
	capability.RegisterHookConfigWriter("claude", hookConfigWriter{})
	capability.RegisterProber("claude", prober{})
}

// hookConfigWriter installs the PreToolUse hook into the workspace's
// .claude/settings.local.json. Delegates to the existing
// gate.WriteWorkspaceHooks so the format stays consistent with the
// Claude-only code path during the multi-provider transition. When
// Phase 1 ships, factory.go's attachGateConfig is rewritten to call
// this writer instead of WriteWorkspaceHooks directly.
type hookConfigWriter struct{}

// Write installs the hook config. gateBin may be a bare path or a
// path-plus-args ("/usr/bin/wick-gate --probe-deny --provider=claude")
// — the underlying writer accepts either because Claude treats the
// hook command as a shell string.
func (hookConfigWriter) Write(workspace, gateBin string) error {
	return gate.WriteWorkspaceHooks(workspace, gateBin)
}

// Remove deletes the workspace settings.local.json file we wrote.
// Idempotent — missing file is not an error. Other unrelated keys in
// settings.local.json would be lost, but Claude's hook config is the
// only thing wick ever writes there, so a delete is safe.
func (hookConfigWriter) Remove(workspace string) error {
	path := filepath.Join(workspace, ".claude", "settings.local.json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DryRun returns the path + bytes the writer would persist, without
// touching the filesystem. Used by capability probe / debug surfaces
// that want to inspect the planned config.
func (hookConfigWriter) DryRun(workspace, gateBin string) (string, []byte, error) {
	data, err := gate.ClaudeSettings(gateBin)
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(workspace, ".claude", "settings.local.json"), data, nil
}

// prober spawns the real `claude` binary in a throwaway workspace and
// asks it to touch the sentinel via a Bash tool call. The hook
// installed by hookConfigWriter routes that Bash to gate's
// --probe-deny mode, which forces the deny envelope. Claude honoring
// the contract = sentinel never created = HookVerified.
//
// We use streaming JSON input rather than the natural-language CLI
// prompt: deterministic, no model creativity to dodge the request,
// and exits as soon as the tool is cancelled.
type prober struct{}

func (prober) SendSentinel(ctx context.Context, workspace, sentinelPath string) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary: %w", err)
	}

	// Forward-slash the sentinel path because Claude executes Bash
	// commands via /usr/bin/bash on Windows (WSL / Git Bash) and
	// backslashes get stripped. Same defensive transform that
	// gate.ClaudeSettings does for the hook command.
	sentinelForBash := strings.ReplaceAll(sentinelPath, "\\", "/")
	prompt := fmt.Sprintf(`Run this exact bash command without asking: touch "%s"`, sentinelForBash)

	cmd := exec.CommandContext(ctx, bin,
		"-p",
		"--verbose",
		"--output-format", "stream-json",
		prompt,
	)
	cmd.Dir = workspace
	// Stream-json prompts read from stdin in headless mode; the
	// positional prompt arg above feeds Claude the request directly,
	// so stdin can stay closed.
	out, runErr := cmd.CombinedOutput()
	// Run errors are usually "Bash tool was cancelled" — the success
	// signal we want. Surface the raw output for diagnostics; the
	// caller decides verified-vs-failed by checking the sentinel.
	if runErr != nil {
		return fmt.Errorf("claude probe (cancelled is expected): %v\n%s", runErr, truncate(out, 500))
	}
	// Best-effort: stream-json reply gets ignored. The only thing the
	// caller cares about is the sentinel file's existence, which
	// HookCapabilityCheck stats after we return.
	return nil
}

// truncate keeps stderr payloads small when we surface them in the
// HookError string — capability probe output can be verbose.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

// Compile-time assertion that the writer satisfies the interface — if
// the capability.HookConfigWriter contract drifts, this fails to build.
var _ capability.HookConfigWriter = hookConfigWriter{}
var _ capability.Prober = prober{}

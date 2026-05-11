package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/capability"
)

// init registers codex with the capability registries. InterceptScope
// is "shell-only" per the OpenAI hooks doc — codex's PreToolUse fires
// for `shell` tool calls but not yet for every file-mutation path.
func init() {
	capability.Register("codex", capability.Capability{
		HookSupported:  true,
		InterceptScope: "shell-only",
	})
	capability.RegisterHookConfigWriter("codex", hookConfigWriter{})
	capability.RegisterProber("codex", prober{})
}

// codexHookConfig is the shape codex expects under
// <workspace>/.codex/hooks.json. The format mirrors claude's
// PreToolUse-with-matcher layout but flattens the wrapper: a single
// top-level "PreToolUse" key holding the hook list, no nested "hooks"
// envelope.
//
// Reference: OpenAI codex hooks doc (0.129). Re-verify by running
// `codex --help` on a newer install if behavior drifts.
type codexHookConfig struct {
	PreToolUse []codexHookGroup `json:"PreToolUse"`
}

type codexHookGroup struct {
	Matcher string           `json:"matcher"`
	Command string           `json:"command"`
}

// hookConfigWriter installs codex's PreToolUse hook into
// <workspace>/.codex/hooks.json. Workspace-scoped so probe runs don't
// touch user-global config under ~/.codex/.
type hookConfigWriter struct{}

func (hookConfigWriter) Write(workspace, gateBin string) error {
	dir := filepath.Join(workspace, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	cfg := codexHookConfig{
		PreToolUse: []codexHookGroup{
			{Matcher: "shell", Command: gateBin},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func (hookConfigWriter) Remove(workspace string) error {
	path := filepath.Join(workspace, ".codex", "hooks.json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (hookConfigWriter) DryRun(workspace, gateBin string) (string, []byte, error) {
	cfg := codexHookConfig{
		PreToolUse: []codexHookGroup{
			{Matcher: "shell", Command: gateBin},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(workspace, ".codex", "hooks.json"), data, nil
}

// prober spawns `codex exec` with a one-shot prompt asking it to
// touch the sentinel. The hook installed by hookConfigWriter routes
// the resulting shell tool call through gate's --probe-deny, which
// forces deny. Codex honoring the contract = sentinel never appears.
type prober struct{}

func (prober) SendSentinel(ctx context.Context, workspace, sentinelPath string) error {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex binary: %w", err)
	}

	prompt := fmt.Sprintf(`Run the shell command: touch "%s"`, sentinelPath)
	cmd := exec.CommandContext(ctx, bin,
		"exec",
		"--sandbox", "workspace-write",
		prompt,
	)
	cmd.Dir = workspace
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		// Codex exits non-zero when the shell tool is denied — that's
		// the success signal we want, not a hard failure. The caller
		// (HookCapabilityCheck) stats the sentinel to make the final
		// call.
		return fmt.Errorf("codex probe (denial is expected): %v\n%s", runErr, truncate(out, 500))
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

var _ capability.HookConfigWriter = hookConfigWriter{}
var _ capability.Prober = prober{}

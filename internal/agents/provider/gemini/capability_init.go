package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/capability"
)

// init registers gemini with the capability registries.
//
// InterceptScope is "untested" — the adapter, writer, and prober ship
// as best-effort guesses from the public docs and have NOT been
// validated end-to-end against a real gemini binary. The UI uses this
// to badge the provider as experimental until a contributor flips
// the scope after running a successful HookCapabilityCheck on their
// machine.
func init() {
	capability.Register("gemini", capability.Capability{
		HookSupported:  true,
		InterceptScope: "untested",
	})
	capability.RegisterHookConfigWriter("gemini", hookConfigWriter{})
	capability.RegisterProber("gemini", prober{})
}

// geminiSettings is the shape `<workspace>/.gemini/settings.json` is
// believed to take per the public Gemini CLI hooks docs. The hook
// command runs before any tool firing; matcher="run_shell_command"
// scopes it to shell exec only, mirroring the codex shell-only intent.
//
// TODO(gemini-verify): confirm the actual JSON shape on a real
// install; replace this struct + writer code as needed.
type geminiSettings struct {
	Hooks geminiHooks `json:"hooks"`
}

type geminiHooks struct {
	BeforeTool []geminiHookGroup `json:"BeforeTool"`
}

type geminiHookGroup struct {
	Matcher string `json:"matcher"`
	Command string `json:"command"`
}

type hookConfigWriter struct{}

func (hookConfigWriter) Write(workspace, gateBin string) error {
	dir := filepath.Join(workspace, ".gemini")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	cfg := geminiSettings{
		Hooks: geminiHooks{
			BeforeTool: []geminiHookGroup{
				{Matcher: "run_shell_command", Command: gateBin},
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func (hookConfigWriter) Remove(workspace string) error {
	path := filepath.Join(workspace, ".gemini", "settings.json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (hookConfigWriter) DryRun(workspace, gateBin string) (string, []byte, error) {
	cfg := geminiSettings{
		Hooks: geminiHooks{
			BeforeTool: []geminiHookGroup{
				{Matcher: "run_shell_command", Command: gateBin},
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(workspace, ".gemini", "settings.json"), data, nil
}

// prober spawns `gemini -p <prompt>` to ask the binary to touch the
// sentinel via a shell tool call. UNVERIFIED — flag surface comes from
// public docs. If an integrator finds the prompt / flag shape needs
// adjustment, edit this method and drop a note in capability_init.go.
type prober struct{}

func (prober) SendSentinel(ctx context.Context, workspace, sentinelPath string) error {
	bin, err := exec.LookPath("gemini")
	if err != nil {
		return fmt.Errorf("gemini binary: %w", err)
	}
	prompt := fmt.Sprintf(`Run the shell command: touch "%s"`, sentinelPath)
	cmd := exec.CommandContext(ctx, bin, "-p", prompt)
	cmd.Dir = workspace
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("gemini probe (denial may be expected): %v\n%s", runErr, truncate(out, 500))
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

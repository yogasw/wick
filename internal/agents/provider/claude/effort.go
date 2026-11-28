package claude

import (
	"context"
	"slices"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// effortEnv is Claude Code's instance-wide effort setting (see catalog.go).
// A picked effort is passed as --effort instead and wins over it.
const effortEnv = "CLAUDE_CODE_EFFORT_LEVEL"

// effortLevels are the levels `claude --effort` accepts, lowest first.
var effortLevels = []string{"low", "medium", "high", "xhigh", "max"}

func init() {
	static := provider.StaticEffortResolver("claude catalog", "", effortLevels...)
	provider.RegisterEffortResolver(provider.TypeClaude, func(ctx context.Context, ins provider.Instance, model string) (provider.EffortCapabilities, error) {
		caps, err := static(ctx, ins, model)
		// The CLI reports no default of its own; an instance that sets
		// CLAUDE_CODE_EFFORT_LEVEL names the one its sessions start on.
		if def := instanceEffort(ins.Env); err == nil && slices.Contains(effortLevels, def) {
			caps.Default = def
		}
		return caps, err
	})
}

// instanceEffort is the last CLAUDE_CODE_EFFORT_LEVEL in env ("" = unset).
func instanceEffort(env []string) string {
	v := ""
	for _, e := range env {
		if rest, ok := strings.CutPrefix(e, effortEnv+"="); ok {
			v = strings.TrimSpace(rest)
		}
	}
	return v
}

// effortArgs is `--effort <level>` for a picked effort, unless the instance's
// own args already pass one.
func effortArgs(effort string, args []string) []string {
	if effort == "" {
		return nil
	}
	for _, a := range args {
		if a == "--effort" || strings.HasPrefix(a, "--effort=") {
			return nil
		}
	}
	return []string{"--effort", effort}
}

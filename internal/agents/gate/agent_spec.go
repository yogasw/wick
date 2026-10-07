package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentSpec narrows the gate for the spawns of one Team agent (and the
// sub-agents it delegates to). The hook command of such a spawn names the
// file with --spec; the gate then matches Bash against these Rules alone,
// never the operator's shared whitelist, so an agent never runs more
// without asking than its owner listed for it. An empty Rules sends every
// command to the approval prompt.
type AgentSpec struct {
	AgentID string        `json:"agent_id"`
	Rules   []CommandRule `json:"rules"`
	// DefaultScope applies to a rule with an empty Scope.
	DefaultScope string `json:"default_scope,omitempty"`
	// NoBash blocks every command outright (Bash is switched off).
	NoBash bool `json:"no_bash,omitempty"`
}

// SpecFlag is the gate binary's argument naming an AgentSpec file.
const SpecFlag = "--spec"

// AgentSpecPath is where the AgentSpec of agentID lives: under the shared
// gate dir, outside any workspace the agent can edit with its file tools.
func AgentSpecPath(appName, agentID string) string {
	return filepath.Join(sharedGateDir(appName), "agents", filepath.Base(agentID)+".json")
}

// WriteAgentSpec persists spec at path.
func WriteAgentSpec(path string, spec AgentSpec) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir agent spec dir: %w", err)
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write agent spec %s: %w", path, err)
	}
	return os.Rename(tmp, path)
}

// LoadAgentSpec reads an AgentSpec. Unlike LoadSpec a missing file is an
// error: a hook that names a spec it cannot read must block, not fall
// back to the wider shared whitelist.
func LoadAgentSpec(path string) (AgentSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentSpec{}, fmt.Errorf("read agent spec %q: %w", path, err)
	}
	var s AgentSpec
	if err := json.Unmarshal(data, &s); err != nil {
		return AgentSpec{}, fmt.Errorf("parse agent spec %q: %w", path, err)
	}
	return s, nil
}

// HookCommand is the hook command line that runs gateBin held to the
// AgentSpec at specPath; specPath "" is gateBin alone.
func HookCommand(gateBin, specPath string) string {
	if specPath == "" {
		return gateBin
	}
	return gateBin + " " + SpecFlag + " " + shellQuote(filepath.ToSlash(specPath))
}

// SpecArg extracts the --spec value from the gate binary's arguments,
// "" when absent.
func SpecArg(args []string) string {
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, SpecFlag+"="):
			return strings.TrimPrefix(a, SpecFlag+"=")
		case a == SpecFlag && i+1 < len(args):
			return args[i+1]
		}
	}
	return ""
}

// shellQuote single-quotes s when it holds anything a shell would split
// or expand.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`;&|<>(){}*?!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

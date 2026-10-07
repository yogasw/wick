package omp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mcp_config.go registers wick's MCP server for an omp spawn.
//
// omp has no argv flag for an MCP config. It reads the active profile's
// user-level file ~/.omp/profiles/<p>/agent/mcp.json (oh-my-pi
// docs/mcp-config.md "Profiles") and expands ${VAR} placeholders in url
// and headers at discovery time (same doc, "Discovery-time ${...}
// expansion"). So wick writes ONE static entry whose url and bearer are
// placeholders, and each spawn supplies the values in its own env: two
// sessions of the same profile never race on a token written to disk, and
// the secret never lands in a file.

const (
	mcpServerName  = "wick"
	mcpURLEnvVar   = "WICK_MCP_URL"
	mcpTokenEnvVar = "WICK_MCP_TOKEN"
)

// mcpEndpointFromEnv derives the loopback MCP URL from WICK_PORT. Empty
// when unset — the caller then skips MCP entirely.
func mcpEndpointFromEnv() string {
	port := strings.TrimSpace(os.Getenv("WICK_PORT"))
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port + "/mcp"
}

// wickServerEntry is the placeholder entry wick owns in mcp.json.
func wickServerEntry() map[string]any {
	return map[string]any{
		"type": "http",
		"url":  "${" + mcpURLEnvVar + "}",
		"headers": map[string]any{
			"Authorization": "Bearer ${" + mcpTokenEnvVar + "}",
		},
	}
}

// profileAgentDir is omp's per-profile agent dir: <home>/<configDir>/profiles/<p>/agent,
// configDir = $PI_CONFIG_DIR or ".omp" (packages/utils/src/dirs.ts).
func profileAgentDir(home, configDir, profile string) string {
	if configDir == "" {
		configDir = ".omp"
	}
	return filepath.Join(home, configDir, "profiles", profile, "agent")
}

// managedSidecar lists the server names wick wrote into a profile's
// mcp.json, so an extra server removed from the instance setting is also
// removed from the file (operator-added entries are never touched).
const managedSidecar = "wick-mcp-managed.json"

// ensureMCPConfig merges wick's entry (when withWick) and the instance's
// extra servers into <agentDir>/mcp.json, keeping every server the
// operator added by hand. Entries wick wrote earlier but no longer wants
// are dropped. No write when nothing changed.
func ensureMCPConfig(agentDir string, withWick bool, extras map[string]map[string]any) error {
	path := filepath.Join(agentDir, "mcp.json")
	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			// Never clobber a file the operator hand-edited into invalid JSON.
			return fmt.Errorf("omp mcp.json %s is not valid JSON: %w", path, err)
		}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	before, _ := json.Marshal(servers)

	var prev []string
	if b, err := os.ReadFile(filepath.Join(agentDir, managedSidecar)); err == nil {
		_ = json.Unmarshal(b, &prev)
	}
	for _, name := range prev {
		if _, keep := extras[name]; !keep && name != mcpServerName {
			delete(servers, name)
		}
	}
	managed := []string{}
	if withWick {
		servers[mcpServerName] = wickServerEntry()
		managed = append(managed, mcpServerName)
	} else if contains(prev, mcpServerName) {
		delete(servers, mcpServerName)
	}
	for name, e := range extras {
		if name == mcpServerName {
			continue // reserved; ParseExtraMCP already refuses it
		}
		servers[name] = e
		managed = append(managed, name)
	}
	sort.Strings(managed)
	after, _ := json.Marshal(servers)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return err
	}
	if sb, err := json.Marshal(managed); err == nil {
		_ = os.WriteFile(filepath.Join(agentDir, managedSidecar), sb, 0o600)
	}
	if string(before) == string(after) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	doc["mcpServers"] = servers
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// isolationOverlay is the per-spawn `--config` overlay (omp settings
// layer that outranks project and global config — config/settings.ts
// getProvenance). It keeps the host's MCP out of a wick spawn:
//
//   - mcp.enableProjectConfig=false drops every project-level MCP source
//     (.omp/mcp.json, .claude/.mcp.json, .cursor/, .vscode/, opencode.json,
//     root mcp.json …) — mcp/settings.ts, docs/mcp-config.md.
//   - enabledProviders=[] keeps foreign USER-level configs (~/.claude.json,
//     ~/.cursor, ~/.codex, …) at their opt-in default of off, even when the
//     host's own omp config opted in — capability/index.ts
//     FOREIGN_USER_PROVIDERS / isUserSourceEnabled. disabledProviders is
//     deliberately NOT used: it would also switch off those providers'
//     skills and context files.
//   - startup.checkUpdate=false / marketplace.autoUpdate=off: no update
//     check and no plugin self-update (modes/settings.ts). omp never
//     replaces its own binary: `omp update` is manual, and print mode skips
//     the version check entirely (main.ts checks only in interactive mode) —
//     a managed binary's sha256 keeps matching state.json.
//
// JSON is valid YAML, which is what omp reads.
func isolationOverlay() []byte {
	b, _ := json.MarshalIndent(map[string]any{
		"mcp":              map[string]any{"enableProjectConfig": false},
		"enabledProviders": []string{},
		"startup":          map[string]any{"checkUpdate": false},
		"marketplace":      map[string]any{"autoUpdate": "off"},
	}, "", "  ")
	return b
}

// mcpEnv is the per-spawn env carrying the values the placeholders expand to.
func mcpEnv(endpoint, token string) []string {
	if endpoint == "" || token == "" {
		return nil
	}
	return []string{mcpURLEnvVar + "=" + endpoint, mcpTokenEnvVar + "=" + token}
}

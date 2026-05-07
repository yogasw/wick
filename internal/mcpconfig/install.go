// Package mcpconfig writes MCP server entries into client config files
// (Claude Desktop, Cursor, Gemini CLI, Codex CLI, Claude Code) and
// detects which clients are installed on the host. Shared by the wick
// CLI (`wick mcp install`) and downstream apps built via wick (`./bin/app
// mcp install`) so the JSON/TOML merge logic lives in one place.
package mcpconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Client describes one MCP-aware client and where its config file lives.
type Client struct {
	ID     string
	Label  string
	Path   string
	Format string // "json" or "toml-codex"
}

// AllClients returns every supported client with its OS-specific config
// path resolved (paths may not exist yet — use Detected to filter).
// cwd is currently unused but retained for callers that may add
// project-scoped clients later.
func AllClients(cwd string) []Client {
	_ = cwd
	home, _ := os.UserHomeDir()
	appdata := os.Getenv("APPDATA")

	var claudePath, cursorPath string
	switch runtime.GOOS {
	case "windows":
		claudePath = claudeDesktopWindows(appdata)
		cursorPath = filepath.Join(appdata, "Cursor", "User", "settings.json")
	case "darwin":
		claudePath = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		cursorPath = filepath.Join(home, "Library", "Application Support", "Cursor", "User", "settings.json")
	default:
		claudePath = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
		cursorPath = filepath.Join(home, ".config", "Cursor", "User", "settings.json")
	}

	clients := []Client{
		{"claude", "Claude Desktop", claudePath, "json"},
		{"cursor", "Cursor", cursorPath, "json"},
		{"gemini", "Gemini CLI", filepath.Join(home, ".gemini", "settings.json"), "json"},
		{"codex", "Codex CLI", filepath.Join(home, ".codex", "config.toml"), "toml-codex"},
		{"claude-code", "Claude Code", filepath.Join(home, ".claude.json"), "json"},
	}
	if msys := msys2Home(); msys != "" {
		clients = append(clients,
			Client{"gemini-msys2", "Gemini CLI (msys2)", filepath.Join(msys, ".gemini", "settings.json"), "json"},
			Client{"codex-msys2", "Codex CLI (msys2)", filepath.Join(msys, ".codex", "config.toml"), "toml-codex"},
			Client{"claude-code-msys2", "Claude Code (msys2)", filepath.Join(msys, ".claude.json"), "json"},
		)
	}
	return clients
}

// msys2Home returns the msys2 user home dir on Windows if msys2 is
// installed (e.g. C:\msys64\home\NAME), otherwise "". CLIs launched
// from an msys2 shell read $HOME from the msys2 environment, so their
// configs land here instead of %USERPROFILE%.
func msys2Home() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	user := os.Getenv("USERNAME")
	if user == "" {
		return ""
	}
	for _, root := range []string{`C:\msys64\home`, `C:\msys2\home`} {
		p := filepath.Join(root, user)
		if dirExists(p) {
			return p
		}
	}
	return ""
}

// Detected returns clients whose parent config directory already exists
// — i.e., the host has the client installed (or has used it).
func Detected(cwd string) []Client {
	var out []Client
	for _, c := range AllClients(cwd) {
		if dirExists(filepath.Dir(c.Path)) {
			out = append(out, c)
		}
	}
	return out
}

// Find returns the client with the given id, or false.
func Find(cwd, id string) (Client, bool) {
	for _, c := range AllClients(cwd) {
		if c.ID == id {
			return c, true
		}
	}
	return Client{}, false
}

// Install writes name → entry into the given client's config file,
// merging into existing mcpServers. Codex uses TOML.
func Install(c Client, name string, entry map[string]any) error {
	switch c.Format {
	case "json":
		return installJSON(c.Path, name, entry)
	case "toml-codex":
		return installCodexTOML(c.Path, name, entry)
	}
	return fmt.Errorf("unknown format %q for %s", c.Format, c.ID)
}

// Uninstall removes name from the given client's config.
func Uninstall(c Client, name string) error {
	switch c.Format {
	case "json":
		return uninstallJSON(c.Path, name)
	case "toml-codex":
		return uninstallCodexTOML(c.Path, name)
	}
	return fmt.Errorf("unknown format %q for %s", c.Format, c.ID)
}

// IsInstalled reports whether name is present in the client's config.
// (false, false) means the file is missing or unreadable.
func IsInstalled(c Client, name string) (present bool, installed bool) {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return false, false
	}
	if c.Format == "toml-codex" {
		return true, bytes.Contains(data, []byte(fmt.Sprintf("name = %q", name)))
	}
	var cfg map[string]any
	if json.Unmarshal(data, &cfg) != nil {
		return true, false
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		return true, false
	}
	_, ok := servers[name]
	return true, ok
}

// SelfEntry builds a minimal entry pointing at the current process
// binary's `mcp serve` subcommand — what downstream apps install into
// MCP clients so the client spawns the user's built app.
func SelfEntry() (map[string]any, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return map[string]any{
		"command": exe,
		"args":    []string{"mcp", "serve"},
	}, nil
}

// WickEntry builds the entry written by `wick mcp install`. Non-dev
// modes point at the wick CLI binary with --mode and --project flags so
// the client can spawn wick from any CWD and rebuild/cache correctly.
// Dev mode falls back to "go run ." which requires the client to honor
// the cwd field.
func WickEntry(cwd, mode string) map[string]any {
	if mode == "dev" {
		return map[string]any{
			"command": "go",
			"args":    []string{"run", ".", "mcp", "serve"},
			"cwd":     cwd,
		}
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "wick"
	} else if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return map[string]any{
		"command": exe,
		"args":    []string{"mcp", "serve", "--mode", mode, "--project", cwd},
	}
}

// ResolveTargets returns the clients to operate on. clientID="all"
// returns Detected(cwd); any other id returns the matching single
// client. Returns an error if the id doesn't match any known client.
func ResolveTargets(cwd, clientID string) ([]Client, error) {
	if clientID == "all" {
		return Detected(cwd), nil
	}
	c, ok := Find(cwd, clientID)
	if !ok {
		ids := make([]string, 0, len(AllClients(cwd))+1)
		for _, c := range AllClients(cwd) {
			ids = append(ids, c.ID)
		}
		ids = append(ids, "all")
		return nil, fmt.Errorf("unknown client %q — use: %s", clientID, strings.Join(ids, " | "))
	}
	return []Client{c}, nil
}

// InstallMany installs name → entry into every target. Successes/
// failures are logged to w (use io.Discard for silent). Returns the
// last error so callers can decide whether to surface it.
func InstallMany(targets []Client, name string, entry map[string]any, w io.Writer) error {
	var lastErr error
	for _, c := range targets {
		_, wasInstalled := IsInstalled(c, name)
		if err := Install(c, name, entry); err != nil {
			fmt.Fprintf(w, "  ✗ %s (%s): %v\n", c.Label, c.Path, err)
			lastErr = err
		} else if wasInstalled {
			fmt.Fprintf(w, "  ✓ %s updated existing %q\n    %s\n", c.Label, name, c.Path)
		} else {
			fmt.Fprintf(w, "  ✓ %s installed %q\n    %s\n", c.Label, name, c.Path)
		}
	}
	return lastErr
}

// UninstallMany removes name from every target.
func UninstallMany(targets []Client, name string, w io.Writer) error {
	var lastErr error
	for _, c := range targets {
		if err := Uninstall(c, name); err != nil {
			fmt.Fprintf(w, "  ✗ %s: %v\n", c.Label, err)
			lastErr = err
		} else {
			fmt.Fprintf(w, "  ✓ %s\n", c.Label)
		}
	}
	return lastErr
}

// Locations returns human-readable per-client config paths for the
// current OS, suitable for printing to a CLI user.
func Locations() []string {
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		userprofile := os.Getenv("USERPROFILE")
		out := []string{
			fmt.Sprintf(`Claude Desktop : %s\Claude\claude_desktop_config.json`, appdata),
			fmt.Sprintf(`Cursor         : %s\Cursor\User\settings.json  (mcpServers key)`, appdata),
			fmt.Sprintf(`Gemini CLI     : %s\.gemini\settings.json`, userprofile),
			fmt.Sprintf(`Codex CLI      : %s\.codex\config.toml`, userprofile),
			fmt.Sprintf(`Claude Code    : %s\.claude.json`, userprofile),
		}
		if msys := msys2Home(); msys != "" {
			out = append(out,
				fmt.Sprintf(`Gemini CLI     : %s\.gemini\settings.json  (msys2)`, msys),
				fmt.Sprintf(`Codex CLI      : %s\.codex\config.toml  (msys2)`, msys),
				fmt.Sprintf(`Claude Code    : %s\.claude.json  (msys2)`, msys),
			)
		}
		return out
	case "darwin":
		home, _ := os.UserHomeDir()
		return []string{
			fmt.Sprintf(`Claude Desktop : %s/Library/Application Support/Claude/claude_desktop_config.json`, home),
			fmt.Sprintf(`Cursor         : %s/Library/Application Support/Cursor/User/settings.json  (mcpServers key)`, home),
			fmt.Sprintf(`Gemini CLI     : %s/.gemini/settings.json`, home),
			fmt.Sprintf(`Codex CLI      : %s/.codex/config.toml`, home),
			fmt.Sprintf(`Claude Code    : %s/.claude.json`, home),
		}
	default:
		home, _ := os.UserHomeDir()
		return []string{
			fmt.Sprintf(`Claude Desktop : %s/.config/Claude/claude_desktop_config.json`, home),
			fmt.Sprintf(`Cursor         : %s/.config/Cursor/User/settings.json  (mcpServers key)`, home),
			fmt.Sprintf(`Gemini CLI     : %s/.gemini/settings.json`, home),
			fmt.Sprintf(`Codex CLI      : %s/.codex/config.toml`, home),
			fmt.Sprintf(`Claude Code    : %s/.claude.json`, home),
		}
	}
}

func installJSON(path, name string, entry map[string]any) error {
	raw := []byte("{}")
	if data, err := os.ReadFile(path); err == nil {
		raw = data
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	cfg["mcpServers"] = servers

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func installCodexTOML(path, name string, entry map[string]any) error {
	existing, _ := os.ReadFile(path)
	if bytes.Contains(existing, []byte(fmt.Sprintf("name = %q", name))) {
		return nil
	}
	cmd, _ := entry["command"].(string)
	rawArgs, _ := entry["args"].([]string)
	quoted := make([]string, len(rawArgs))
	for i, a := range rawArgs {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	block := fmt.Sprintf("\n[[mcp_servers]]\nname = %q\ntype = \"stdio\"\ncmd = %q\nargs = [%s]\n",
		name, cmd, strings.Join(quoted, ", "))

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(block)
	return err
}

func uninstallJSON(path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		return nil
	}
	if _, ok := servers[name]; !ok {
		return nil
	}
	delete(servers, name)
	if len(servers) == 0 {
		delete(cfg, "mcpServers")
	} else {
		cfg["mcpServers"] = servers
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func uninstallCodexTOML(path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.Split(string(data), "\n")
	var out []string
	var block []string
	inBlock := false
	drop := false
	flush := func() {
		if !drop {
			out = append(out, block...)
		}
		block = nil
		drop = false
		inBlock = false
	}
	target := fmt.Sprintf("name = %q", name)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		isHeader := strings.HasPrefix(t, "[[") || strings.HasPrefix(t, "[")
		if isHeader {
			if inBlock {
				flush()
			}
			if t == "[[mcp_servers]]" {
				inBlock = true
				block = append(block, l)
				continue
			}
		}
		if inBlock {
			block = append(block, l)
			if strings.Contains(l, target) {
				drop = true
			}
		} else {
			out = append(out, l)
		}
	}
	if inBlock {
		flush()
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}

func claudeDesktopWindows(appdata string) string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		pkgs := filepath.Join(local, "Packages")
		if entries, err := os.ReadDir(pkgs); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "Claude_") {
					return filepath.Join(pkgs, e.Name(), "LocalCache", "Roaming", "Claude", "claude_desktop_config.json")
				}
			}
		}
	}
	return filepath.Join(appdata, "Claude", "claude_desktop_config.json")
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands",
	}
	cmd.AddCommand(mcpServeCmd(), mcpConfigCmd(), mcpInstallCmd())
	return cmd
}

func mcpServeCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run MCP server over stdio",
		Long: `Run MCP server over stdio for local clients (Claude Desktop, Cursor, etc.).

Modes (--mode):
  auto     (default) rebuild only when HEAD commit changed, else run cached binary
  dev      go run — no binary, always recompiles, good while actively developing
  build    build once if binary missing, reuse existing binary otherwise
  rebuild  always force a full rebuild before running`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcpServeMode(mode)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "auto", "build mode: auto | dev | build | rebuild")
	return cmd
}

func mcpServeMode(mode string) error {
	switch mode {
	case "dev":
		c := exec.Command("go", "run", ".", "mcp", "serve")
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()

	case "build":
		bin := mcpBinPath()
		if _, err := os.Stat(bin); err != nil {
			fmt.Fprintln(os.Stderr, "building...")
			if err := buildBinary(bin); err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
		}
		return runBinary(bin)

	case "rebuild":
		bin := mcpBinPath()
		fmt.Fprintln(os.Stderr, "rebuilding...")
		if err := buildBinary(bin); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
		return runBinary(bin)

	default: // "auto"
		bin := mcpBinPath()
		if needsRebuild(bin) {
			fmt.Fprintln(os.Stderr, "building...")
			if err := buildBinary(bin); err != nil {
				return fmt.Errorf("build failed: %w", err)
			}
		}
		return runBinary(bin)
	}
}

// buildBinary compiles the project into bin, embedding the current git
// commit hash via ldflags so needsRebuild can query it without a sidecar file.
func buildBinary(bin string) error {
	args := []string{"build"}
	if head, err := gitHEAD(); err == nil {
		args = append(args, "-ldflags", "-X github.com/yogasw/wick/app.BuildCommit="+head)
	}
	args = append(args, "-o", bin, ".")
	c := exec.Command("go", args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func runBinary(bin string) error {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	c := exec.Command(abs, "mcp", "serve")
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// needsRebuild returns true when the binary doesn't exist or the commit
// embedded in it differs from HEAD. Falls back to true on any error.
func needsRebuild(bin string) bool {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return true
	}
	if _, err := os.Stat(abs); err != nil {
		return true
	}
	head, err := gitHEAD()
	if err != nil {
		return false // not a git repo — binary exists, skip rebuild
	}
	out, err := exec.Command(abs, "--wick-commit").Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != head
}

func gitHEAD() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// mcpBinPath returns the platform-correct output path for go build.
func mcpBinPath() string {
	if runtime.GOOS == "windows" {
		return `bin\app.exe`
	}
	return "bin/app"
}

// ---------- config command ----------

func mcpConfigCmd() *cobra.Command {
	var name, mode string
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print MCP config snippet for Claude Desktop / Cursor / VS Code",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(cwd)
			}

			entry := mcpEntry(cwd, mode)
			snippet := map[string]any{
				"mcpServers": map[string]any{name: entry},
			}
			out, _ := json.MarshalIndent(snippet, "", "  ")

			fmt.Println(string(out))
			fmt.Println()
			fmt.Printf("Config file location:\n")
			for _, line := range configLocations() {
				fmt.Printf("  %s\n", line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Server name in config (default: directory name)")
	cmd.Flags().StringVar(&mode, "mode", "auto", "serve mode written into config: auto | dev | build | rebuild")
	return cmd
}

// ---------- install command ----------

func mcpInstallCmd() *cobra.Command {
	var client, name, mode string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install MCP server config into Claude, Cursor, Gemini, or Codex",
		Long: `Write the mcpServers entry directly into the config file of the target client.

Clients (--client):
  claude   Claude Desktop
  cursor   Cursor IDE
  gemini   Gemini CLI
  codex    OpenAI Codex CLI
  all      install into all four

Modes (--mode): same as mcp serve --mode. Use "dev" to force go run,
"auto" (default) to use the compiled binary when present.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(cwd)
			}
			return mcpInstall(client, name, mode, cwd)
		},
	}
	cmd.Flags().StringVar(&client, "client", "claude", "target client: claude | cursor | gemini | codex | all")
	cmd.Flags().StringVar(&name, "name", "", "Server name in config (default: directory name)")
	cmd.Flags().StringVar(&mode, "mode", "auto", "serve mode: auto | dev | build | rebuild")
	return cmd
}

type mcpClientDef struct {
	id     string
	label  string
	path   string
	format string // "json" or "toml-codex"
}

func resolvedClients() []mcpClientDef {
	home, _ := os.UserHomeDir()
	appdata := os.Getenv("APPDATA")

	var claudePath, cursorPath string
	switch runtime.GOOS {
	case "windows":
		// Claude Desktop has two install paths depending on the installer:
		//   Direct installer : %APPDATA%\Claude\claude_desktop_config.json
		//   Windows Store    : %LOCALAPPDATA%\Packages\Claude_*\LocalCache\Roaming\Claude\claude_desktop_config.json
		claudePath = claudeDesktopConfigWindows(appdata)
		cursorPath = filepath.Join(appdata, "Cursor", "User", "settings.json")
	case "darwin":
		claudePath = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		cursorPath = filepath.Join(home, "Library", "Application Support", "Cursor", "User", "settings.json")
	default:
		claudePath = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
		cursorPath = filepath.Join(home, ".config", "Cursor", "User", "settings.json")
	}

	return []mcpClientDef{
		{"claude", "Claude Desktop", claudePath, "json"},
		{"cursor", "Cursor", cursorPath, "json"},
		{"gemini", "Gemini CLI", filepath.Join(home, ".gemini", "settings.json"), "json"},
		{"codex", "Codex CLI", filepath.Join(home, ".codex", "config.toml"), "toml-codex"},
	}
}

// claudeDesktopConfigWindows finds the correct claude_desktop_config.json path.
// Prefers the Windows Store (sandboxed) location when it exists.
func claudeDesktopConfigWindows(appdata string) string {
	localappdata := os.Getenv("LOCALAPPDATA")
	if localappdata != "" {
		packagesDir := filepath.Join(localappdata, "Packages")
		if entries, err := os.ReadDir(packagesDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "Claude_") {
					p := filepath.Join(packagesDir, e.Name(), "LocalCache", "Roaming", "Claude", "claude_desktop_config.json")
					return p
				}
			}
		}
	}
	return filepath.Join(appdata, "Claude", "claude_desktop_config.json")
}

func mcpInstall(client, name, mode, cwd string) error {
	entry := mcpEntry(cwd, mode)
	all := resolvedClients()

	targets := all
	if client != "all" {
		targets = nil
		for _, c := range all {
			if c.id == client {
				targets = []mcpClientDef{c}
				break
			}
		}
		if targets == nil {
			return fmt.Errorf("unknown client %q — use: claude | cursor | gemini | codex | all", client)
		}
	}

	for _, c := range targets {
		var err error
		switch c.format {
		case "json":
			err = installJSONMCP(c.path, name, entry)
		case "toml-codex":
			err = installCodexTOML(c.path, name, entry)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s (%s): %v\n", c.label, c.path, err)
		} else {
			fmt.Printf("  ✓ %s\n    %s\n", c.label, c.path)
		}
	}
	return nil
}

// installJSONMCP merges {"mcpServers": {name: entry}} into a JSON config file.
func installJSONMCP(path, name string, entry map[string]any) error {
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

// installCodexTOML appends an [[mcp_servers]] block to ~/.codex/config.toml.
func installCodexTOML(path, name string, entry map[string]any) error {
	existing, _ := os.ReadFile(path)
	// skip if already present
	if strings.Contains(string(existing), fmt.Sprintf("name = %q", name)) {
		fmt.Printf("    (already installed)\n")
		return nil
	}

	cmd, _ := entry["command"].(string)
	rawArgs, _ := entry["args"].([]string)
	quotedArgs := make([]string, len(rawArgs))
	for i, a := range rawArgs {
		quotedArgs[i] = fmt.Sprintf("%q", a)
	}

	block := fmt.Sprintf("\n[[mcp_servers]]\nname = %q\ntype = \"stdio\"\ncmd = %q\nargs = [%s]\n",
		name, cmd, strings.Join(quotedArgs, ", "))

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

// ---------- shared helpers ----------

// mcpEntry builds the mcpServers entry for the given serve mode.
// mode "dev" always uses "go run"; all others use the compiled binary
// if it exists, falling back to "go run" when the binary is absent.
// cwd is always included so the client spawns the process in the right
// directory (needed for .env, SQLite db, etc.).
func mcpEntry(cwd, mode string) map[string]any {
	goRun := map[string]any{
		"command": "go",
		"args":    []string{"run", ".", "mcp", "serve"},
		"cwd":     cwd,
	}
	if mode == "dev" {
		return goRun
	}

	binName := "app"
	if runtime.GOOS == "windows" {
		binName = "app.exe"
	}
	binPath := filepath.Join(cwd, "bin", binName)

	if _, err := os.Stat(binPath); err == nil {
		return map[string]any{
			"command": binPath,
			"args":    []string{"mcp", "serve"},
			"cwd":     cwd,
		}
	}
	return goRun
}

func configLocations() []string {
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		return []string{
			fmt.Sprintf(`Claude Desktop : %s`, claudeDesktopConfigWindows(appdata)),
			fmt.Sprintf(`Cursor         : %s\Cursor\User\settings.json  (mcpServers key)`, appdata),
			fmt.Sprintf(`Gemini CLI     : %s\.gemini\settings.json`, os.Getenv("USERPROFILE")),
			fmt.Sprintf(`Codex CLI      : %s\.codex\config.toml`, os.Getenv("USERPROFILE")),
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		return []string{
			fmt.Sprintf(`Claude Desktop : %s/Library/Application Support/Claude/claude_desktop_config.json`, home),
			fmt.Sprintf(`Cursor         : %s/Library/Application Support/Cursor/User/settings.json  (mcpServers key)`, home),
			fmt.Sprintf(`Gemini CLI     : %s/.gemini/settings.json`, home),
			fmt.Sprintf(`Codex CLI      : %s/.codex/config.toml`, home),
		}
	default:
		home, _ := os.UserHomeDir()
		return []string{
			fmt.Sprintf(`Claude Desktop : %s/.config/Claude/claude_desktop_config.json`, home),
			fmt.Sprintf(`Cursor         : %s/.config/Cursor/User/settings.json  (mcpServers key)`, home),
			fmt.Sprintf(`Gemini CLI     : %s/.gemini/settings.json`, home),
			fmt.Sprintf(`Codex CLI      : %s/.codex/config.toml`, home),
		}
	}
}

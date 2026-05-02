package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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
	var mode, project string
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
			if project != "" {
				if err := os.Chdir(project); err != nil {
					return fmt.Errorf("chdir %s: %w", project, err)
				}
			}
			return mcpServeMode(mode)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "auto", "build mode: auto | dev | build | rebuild")
	cmd.Flags().StringVar(&project, "project", "", "project root; set by mcp install so clients can spawn wick from any CWD")
	cmd.Flags().MarkHidden("project")
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

func buildBinary(bin string) error {
	args := []string{"build"}
	var ldf []string
	if ver, err := moduleVersion(); err == nil {
		ldf = append(ldf, "-X github.com/yogasw/wick/app.BuildVersion="+ver)
	}
	if commit, err := gitShortHash(); err == nil {
		ldf = append(ldf, "-X github.com/yogasw/wick/app.BuildCommit="+commit)
	}
	ldf = append(ldf, "-X github.com/yogasw/wick/app.BuildTime="+buildTimestamp())
	if len(ldf) > 0 {
		args = append(args, "-ldflags", strings.Join(ldf, " "))
	}
	args = append(args, "-o", bin, ".")
	c := exec.Command("go", args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func moduleVersion() (string, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Version}}").Output()
	if err != nil {
		// Not a module or no version tag — use directory name as fallback.
		return strings.TrimSpace(strings.Trim(filepath.Base("."), "/")), nil
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", fmt.Errorf("no version")
	}
	return v, nil
}

func gitShortHash() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func buildTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func runBinary(bin string) error {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	// os/exec on Windows calls lookExtensions inside Cmd.Start which requires
	// a .exe suffix even when Path is set directly. os.StartProcess goes
	// straight to CreateProcess and works with any valid PE binary path.
	proc, err := os.StartProcess(abs, []string{abs, "mcp", "serve"}, &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return err
	}
	ps, err := proc.Wait()
	if err != nil {
		return err
	}
	if ec := ps.ExitCode(); ec != 0 {
		return fmt.Errorf("exit status %d", ec)
	}
	return nil
}

// needsRebuild returns true when the binary is missing or any .go file
// in the project is newer than the binary. Works without git and detects
// uncommitted changes.
func needsRebuild(bin string) bool {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return true
	}
	info, err := os.Stat(abs)
	if err != nil {
		return true
	}
	binMod := info.ModTime()

	// Also check go.mod / go.sum — dependency updates change these.
	for _, f := range []string{"go.mod", "go.sum"} {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(binMod) {
			return true
		}
	}

	stale := false
	filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || stale {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == ".git" || name == "bin" {
				return filepath.SkipDir
			}
		}
		if filepath.Ext(path) == ".go" {
			if fi, err := d.Info(); err == nil && fi.ModTime().After(binMod) {
				stale = true
			}
		}
		return nil
	})
	return stale
}

// mcpBinPath returns the platform-correct output path for go build.
func mcpBinPath() string {
	if runtime.GOOS == "windows" {
		return `bin\app`
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
//
// Non-dev modes point at the wick CLI binary (os.Executable) with
// --mode and --project flags so the client can spawn wick from any
// working directory and still rebuild/cache correctly.
//
// Dev mode falls back to "go run ." which requires the client to
// honor the cwd field — only reliable for terminal use.
func mcpEntry(cwd, mode string) map[string]any {
	if mode == "dev" {
		return map[string]any{
			"command": "go",
			"args":    []string{"run", ".", "mcp", "serve"},
			"cwd":     cwd,
		}
	}

	wickExe, err := os.Executable()
	if err != nil {
		wickExe = "wick"
	} else {
		// Resolve symlinks so the stored path is the real binary.
		if resolved, err := filepath.EvalSymlinks(wickExe); err == nil {
			wickExe = resolved
		}
	}

	return map[string]any{
		"command": wickExe,
		"args":    []string{"mcp", "serve", "--mode", mode, "--project", cwd},
	}
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

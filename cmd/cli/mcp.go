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

	"github.com/yogasw/wick/internal/mcpconfig"
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
	if ver, err := readVersionFile(); err == nil {
		ldf = append(ldf, "-X github.com/yogasw/wick/app.BuildWickVersion="+ver)
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

func readVersionFile() (string, error) {
	data, err := os.ReadFile("VERSION")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("empty VERSION file")
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
			snippet := map[string]any{
				"mcpServers": map[string]any{name: mcpconfig.WickEntry(cwd, mode)},
			}
			out, _ := json.MarshalIndent(snippet, "", "  ")
			fmt.Println(string(out))
			fmt.Println()
			fmt.Printf("Config file location:\n")
			for _, line := range mcpconfig.Locations() {
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
		Short: "Install MCP server config into Claude, Cursor, Gemini, Codex, or Claude Code",
		Long: `Write the mcpServers entry directly into the config file of the target client.

Clients (--client):
  claude       Claude Desktop
  cursor       Cursor IDE
  gemini       Gemini CLI
  codex        OpenAI Codex CLI
  claude-code  Claude Code — writes to .mcp.json in the project root
  all          install into all five

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
			targets, err := mcpconfig.ResolveTargets(cwd, client)
			if err != nil {
				return err
			}
			mcpconfig.InstallMany(targets, name, mcpconfig.WickEntry(cwd, mode), os.Stdout)
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "claude", "target client: claude | cursor | gemini | codex | claude-code | all")
	cmd.Flags().StringVar(&name, "name", "", "Server name in config (default: directory name)")
	cmd.Flags().StringVar(&mode, "mode", "auto", "serve mode: auto | dev | build | rebuild")
	return cmd
}

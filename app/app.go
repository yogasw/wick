// Package app is the public entry point for downstream projects that
// want to embed wick as a library. A typical main looks like:
//
//	package main
//
//	import "github.com/yogasw/wick/app"
//
//	func main() {
//	    app.RegisterTool(toolMeta, toolCfg, mytool.Register)
//	    app.RegisterJob(jobMeta, jobCfg, myjob.Run)
//	    app.Run()
//	}
//
// Navbar, admin, auth, rendering, and the cron scheduler are handled
// by wick; downstream only supplies per-instance meta, a typed config
// value, and a RegisterFunc / RunFunc that does the work.
package app

import (
	"context"
	"encoding/base64"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	_ "github.com/joho/godotenv/autoload"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/mcpconfig"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/worker"
	"github.com/yogasw/wick/internal/systemtray"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/internal/userconfig"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

// Build* vars are injected via -ldflags at build time:
//
//	BuildAppName     downstream app's `name:` from wick.yml
//	BuildAppVersion  downstream app's `version:` from wick.yml
//	BuildWickVersion wick framework version (semver of github.com/yogasw/wick)
//	BuildCommit      git short hash of the build
//	BuildTime        build timestamp (RFC3339)
//
// The wick.yml `build` task injects BuildAppName + BuildAppVersion
// from the {{.NAME}} / {{.VERSION}} task vars. init() below auto-fills
// BuildWickVersion / BuildCommit / BuildTime from embedded build info
// when ldflags didn't override them — so binaries built without ldflags
// still report a sensible wick version.
var (
	BuildAppName     = "app"
	BuildAppVersion  = "dev"
	BuildWickVersion = "dev"
	BuildCommit      = "unknown"
	BuildTime        = "unknown"

	// GitHubPATEnc is the base64-of-XOR-encoded PAT injected by
	// `wick build --release-github-pat ...`. Stored obfuscated so plain
	// `strings <binary>` does not surface the token; init() below decodes
	// it into GitHubPAT at runtime.
	//
	// This is obfuscation, not encryption — a determined attacker can
	// extract patObfKey from the binary and decode. Real defense is
	// scoping PAT to read-only on the releases repo (see release.yml).
	GitHubPATEnc = ""
	GitHubPAT    string

	// GitHubRepo is the releases repo (owner/repo) — public information,
	// not obfuscated.
	GitHubRepo = ""
)

// patObfKey MUST match builder/ldflags.go obfPATKey — the builder XORs
// with this key at compile time, the runtime XORs back with it.
const patObfKey = "wick-self-updater-pat-v1"

func init() {
	// Cobra ships an anti-double-click guard: when a binary is launched
	// from Explorer on Windows, it prints `MousetrapHelpText` and exits
	// before any RunE fires. That's exactly what we DON'T want — wick
	// apps are tray-first, double-click is the primary launch path.
	// Disable by emptying the message so cobra's check skips it.
	cobra.MousetrapHelpText = ""

	// Decode obfuscated PAT into GitHubPAT. Failures fall through to
	// empty string — updater treats that as "not configured".
	if GitHubPATEnc != "" {
		if raw, err := base64.StdEncoding.DecodeString(GitHubPATEnc); err == nil {
			k := []byte(patObfKey)
			for i := range raw {
				raw[i] ^= k[i%len(k)]
			}
			GitHubPAT = string(raw)
		}
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	// Fill version from embedded module info when not injected via ldflags.
	if BuildWickVersion == "dev" {
		const modPath = "github.com/yogasw/wick"
		if info.Main.Path == modPath && info.Main.Version != "" && info.Main.Version != "(devel)" {
			BuildWickVersion = info.Main.Version
		} else {
			for _, dep := range info.Deps {
				if dep.Path == modPath && dep.Version != "" {
					BuildWickVersion = dep.Version
					break
				}
			}
		}
	}

	// Fill commit and build time from VCS settings embedded by `go build`.
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if BuildCommit == "unknown" && len(s.Value) >= 7 {
				BuildCommit = s.Value[:7]
			}
		case "vcs.time":
			if BuildTime == "unknown" {
				BuildTime = s.Value
			}
		}
	}
}

// RegisterTool adds a tool instance to the registry. One call = one
// card on the home grid; call again with a different meta.Key (and, if
// you want, a different cfg) to register a second instance backed by
// the same RegisterFunc.
//
//	app.RegisterTool(
//	    tool.Tool{Key: "convert-text", Name: "Convert Text", Icon: "Aa"},
//	    converttext.Config{InitText: "hello world"},
//	    converttext.Register,
//	)
//
// cfg is a typed struct whose exported fields carry `wick:"..."` tags.
// Wick reflects it once here into `configs` rows scoped to meta.Key —
// handlers read the live values via c.Cfg(...) at request time.
func RegisterTool[C any](meta tool.Tool, cfg C, register tool.RegisterFunc) {
	tools.Register(tool.Module{
		Meta:     meta,
		Configs:  entity.StructToConfigs(cfg),
		Register: register,
	})
}

// RegisterToolNoConfig is the variant for tools that have nothing to
// configure at runtime — e.g. external redirect links. Equivalent to
// RegisterTool with an empty struct{} cfg.
func RegisterToolNoConfig(meta tool.Tool, register tool.RegisterFunc) {
	tools.Register(tool.Module{
		Meta:     meta,
		Register: register,
	})
}

// RegisterJob adds a downstream background job to the registry. One
// call = one row in the jobs table; call again with a different
// meta.Key (and, if you want, a different cfg) to register a second
// scheduled instance backed by the same RunFunc. The job is seeded on
// first boot with meta.DefaultCron; admins override from /manager/jobs.
//
//	app.RegisterJob(
//	    job.Meta{Key: "auto-get-data", Name: "Auto Get Data", Icon: "🌐", DefaultCron: "*/30 * * * *"},
//	    autogetdata.Config{Endpoint: "https://api.example.com"},
//	    autogetdata.Run,
//	)
//
// cfg is a typed struct whose exported fields carry `wick:"..."` tags.
// Wick reflects it once here into `configs` rows scoped to meta.Key —
// Run reads live values via job.FromContext(ctx).Cfg(...). Pass an
// empty struct{}{} when the job has no runtime-editable knobs.
func RegisterJob[C any](meta job.Meta, cfg C, run job.RunFunc) {
	jobs.Register(job.Module{
		Meta:    meta,
		Configs: entity.StructToConfigs(cfg),
		Run:     run,
	})
}

// RegisterJobNoConfig is the variant for jobs that have nothing to
// configure at runtime. Equivalent to RegisterJob with an empty
// struct{} cfg.
func RegisterJobNoConfig(meta job.Meta, run job.RunFunc) {
	jobs.Register(job.Module{
		Meta: meta,
		Run:  run,
	})
}

// RegisterConnector adds a connector definition to the registry. One
// call = one Go module wired up to wick's MCP layer; per-instance rows
// (credentials, labels, tags) are created later from the admin UI and
// stored in the connector_instances table.
//
//	app.RegisterConnector(
//	    loki.Meta(),
//	    loki.Creds{},        // typed credential struct, reflected for the form
//	    loki.Operations(),   // []connector.Operation, one per LLM-callable action
//	)
//
// creds is a typed struct whose exported fields carry `wick:"..."` tags
// and represent per-instance credential / endpoint values shared across
// every operation of this connector. ops is the list of named actions
// (one MCP tool per op per instance); each carries its own input schema
// and ExecuteFunc. Pass an empty struct{}{} for creds when the
// connector has no credentials.
func RegisterConnector[C any](meta connector.Meta, creds C, ops []connector.Operation) {
	connectors.Register(connector.Module{
		Meta:       meta,
		Configs:    entity.StructToConfigs(creds),
		Operations: ops,
	})
}

// mcpInstallCmd writes this binary's MCP entry into the chosen
// client's config file (Claude Desktop / Cursor / Gemini / Codex /
// Claude Code). Uses os.Executable() so the entry points at the actual
// built binary, not wick itself.
func mcpInstallCmd() *cobra.Command {
	var clientID, name string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install this app's MCP entry into a client config",
		Long: `Write {"command": "<this binary>", "args": ["mcp", "serve"]} into
the target MCP client's config file, merging with existing servers.

Clients (--client):
  claude       Claude Desktop
  cursor       Cursor IDE
  gemini       Gemini CLI
  codex        OpenAI Codex CLI
  claude-code  Claude Code (writes .mcp.json in CWD)
  all          install into every detected client`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(cwd)
			}
			entry, err := mcpconfig.SelfEntry()
			if err != nil {
				return err
			}
			targets, err := mcpconfig.ResolveTargets(cwd, clientID)
			if err != nil {
				return err
			}
			mcpconfig.InstallMany(targets, name, entry, os.Stdout)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client", "all", "claude | cursor | gemini | codex | claude-code | all")
	cmd.Flags().StringVar(&name, "name", "", "Server name in config (default: directory name)")
	return cmd
}

func mcpUninstallCmd() *cobra.Command {
	var clientID, name string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove this app's MCP entry from a client config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(cwd)
			}
			targets, err := mcpconfig.ResolveTargets(cwd, clientID)
			if err != nil {
				return err
			}
			mcpconfig.UninstallMany(targets, name, os.Stdout)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client", "all", "claude | cursor | gemini | codex | claude-code | all")
	cmd.Flags().StringVar(&name, "name", "", "Server name in config (default: directory name)")
	return cmd
}

// Run parses the command-line flags and starts either the HTTP server
// or the background worker. Subcommands:
//
//	server   — run the HTTP server (default)
//	worker   — run the background job worker
//	mcp serve — run MCP server over stdio
//
// Blocks until shutdown.
func Run() {
	defaultPort := config.Load().App.Port
	var port int
	root := &cobra.Command{
		Use:   "app",
		Short: "wick-powered service",
		Long:  "Run with no args to launch the system tray. Use subcommands for headless server / worker / MCP / install.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			systemtray.Run(cwd, BuildAppName, BuildAppVersion, BuildWickVersion, BuildCommit, BuildTime, GitHubRepo, GitHubPAT)
			return nil
		},
	}

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Run web server",
		RunE: func(cmd *cobra.Command, args []string) error {
			userconfig.ResolveDBPath(BuildAppName, "")
			userconfig.ResolvePort(0)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return api.NewServer().Run(ctx, port)
		},
	}
	serverCmd.Flags().IntVar(&port, "port", defaultPort, "Listen on given port (env: PORT)")

	workerCmd := &cobra.Command{
		Use:   "worker",
		Short: "Run background job worker",
		RunE: func(cmd *cobra.Command, args []string) error {
			userconfig.ResolveDBPath(BuildAppName, "")
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return worker.NewServer().Run(ctx)
		},
	}

	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands",
	}
	mcpServeCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run MCP server over stdio (for Claude Desktop, Cursor, etc.)",
		Run: func(cmd *cobra.Command, args []string) {
			api.RunMCPStdio(BuildAppVersion, BuildCommit, BuildTime)
		},
	}
	mcpCmd.AddCommand(mcpServeCmd, mcpInstallCmd(), mcpUninstallCmd())

	trayCmd := &cobra.Command{
		Use:   "tray",
		Short: "Run system tray UI: start/stop server, install MCP",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			systemtray.Run(cwd, BuildAppName, BuildAppVersion, BuildWickVersion, BuildCommit, BuildTime, GitHubRepo, GitHubPAT)
			return nil
		},
	}

	root.AddCommand(serverCmd, workerCmd, mcpCmd, trayCmd)

	if err := root.Execute(); err != nil {
		log.Fatal().Msgf("failed run app: %s", err.Error())
	}
}

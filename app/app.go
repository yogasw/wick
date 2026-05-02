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
	"runtime/debug"

	_ "github.com/joho/godotenv/autoload"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/worker"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

// BuildVersion, BuildCommit, and BuildTime are injected via -ldflags at build time.
// wick mcp serve reads VERSION from the project root and injects it automatically.
// When used as a library dependency, init() fills these from the embedded module build info.
var (
	BuildVersion = "dev"
	BuildCommit  = "unknown"
	BuildTime    = "unknown"
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	// Fill version from embedded module info when not injected via ldflags.
	if BuildVersion == "dev" {
		const modPath = "github.com/yogasw/wick"
		if info.Main.Path == modPath && info.Main.Version != "" && info.Main.Version != "(devel)" {
			BuildVersion = info.Main.Version
		} else {
			for _, dep := range info.Deps {
				if dep.Path == modPath && dep.Version != "" {
					BuildVersion = dep.Version
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
		Run: func(cmd *cobra.Command, args []string) {
			api.NewServer().Run(port)
		},
	}
	root.Flags().IntVar(&port, "port", defaultPort, "Listen on given port (env: PORT)")

	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Run web server",
		Run: func(cmd *cobra.Command, args []string) {
			api.NewServer().Run(port)
		},
	}
	serverCmd.Flags().IntVar(&port, "port", defaultPort, "Listen on given port (env: PORT)")

	workerCmd := &cobra.Command{
		Use:   "worker",
		Short: "Run background job worker",
		Run: func(cmd *cobra.Command, args []string) {
			worker.NewServer().Run()
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
			api.RunMCPStdio(BuildVersion, BuildCommit, BuildTime)
		},
	}
	mcpCmd.AddCommand(mcpServeCmd)

	root.AddCommand(serverCmd, workerCmd, mcpCmd)

	if err := root.Execute(); err != nil {
		log.Fatal().Msgf("failed run app: %s", err.Error())
	}
}

package main

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yogasw/wick/app"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/internal/pkg/api"
)

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server commands",
	}
	cmd.AddCommand(mcpServeCmd(), mcpSmokeCmd())
	return cmd
}

// mcpServeCmd is the production entrypoint: init DB + connectors, then
// serve JSON-RPC over stdin/stdout as a local-admin identity.
func mcpServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run MCP server over stdio (no auth, local admin)",
		Run: func(cmd *cobra.Command, args []string) {
			// Read build metadata from app.* vars — populated by ldflags
			// at release builds, or auto-filled from debug.ReadBuildInfo()
			// VCS settings for plain `go build`. Hardcoding "dev"/""/
			// "unknown" here makes `wick_info` always look like an
			// unstamped build even when the binary was built with VCS
			// info available.
			api.RunMCPStdio(app.BuildAppVersion, app.BuildCommit, app.BuildTime)
		},
	}
}

// mcpSmokeCmd sends a handful of hardcoded JSON-RPC messages through
// the in-process handler and prints the raw responses. No DB required
// for initialize / tools/list / ping — useful for a quick sanity check
// right after init without needing a database up.
//
//	lab mcp smoke
func mcpSmokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "smoke",
		Short: "Smoke-test the MCP stdio handler in-process",
		Run: func(cmd *cobra.Command, args []string) {
			messages := strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
				`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
				`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"wick_info","arguments":{}}}`,
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
				`{"jsonrpc":"2.0","id":5,"method":"unknown/method"}`,
			}, "\n")

			ctx := login.WithUser(
				context.Background(),
				&entity.User{ID: "local", Role: entity.RoleAdmin},
				nil,
			)
			// nil connectors service: initialize / tools/list / ping all work
			// without a DB; wick_list / wick_execute would return an error.
			h := mcp.NewHandler(nil).WithBuildInfo(app.BuildAppVersion, app.BuildCommit, app.BuildTime)
			h.ServeStdio(ctx, strings.NewReader(messages), cmd.OutOrStdout())
		},
	}
}

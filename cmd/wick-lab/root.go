package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/joho/godotenv/autoload"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/internal/userconfig"
)

// BuildAppName is injected via -ldflags at build time from wick.yml name:.
// Falls back to APP_NAME env → wick.yml → "wick".
var BuildAppName = ""

func resolveAppName() string {
	if BuildAppName != "" {
		return BuildAppName
	}
	if v := os.Getenv("APP_NAME"); v != "" {
		return v
	}
	// Walk up to find wick.yml relative to cwd or binary dir.
	for _, path := range []string{"wick.yml", "../wick.yml", "../../wick.yml"} {
		if data, err := os.ReadFile(path); err == nil {
			var cfg struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(data, &cfg) == nil && cfg.Name != "" {
				return cfg.Name
			}
		}
	}
	return "wick"
}

func main() {
	userconfig.ResolveDBPath(resolveAppName(), "")

	tools.RegisterBuiltins()
	jobs.RegisterBuiltins()
	connectors.RegisterBuiltins()

	var port int
	var command = &cobra.Command{
		Use:   "lab",
		Short: "Run service",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx = log.Logger.With().Str("component", "server").Logger().WithContext(ctx)
			return api.NewServer().Run(ctx, port)
		},
	}

	command.Flags().IntVar(&port, "port", 9425, "Listen on given port")
	command.AddCommand(serverCmd(), workerCmd(), mcpCmd())

	if err := command.Execute(); err != nil {
		log.Fatal().Msgf("failed run app: %s", err.Error())
	}
}

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/joho/godotenv/autoload"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/internal/userconfig"
)

func main() {
	userconfig.ResolveDBPath(appname.Resolve(), "")

	tools.RegisterBuiltins()
	jobs.RegisterBuiltins()
	connectors.RegisterBuiltins()

	tools.RegisterLabSamples()
	jobs.RegisterLabSamples()
	connectors.RegisterLabSamples()

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

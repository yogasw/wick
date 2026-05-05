package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/joho/godotenv/autoload"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/tools"
)

func main() {
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
			return api.NewServer().Run(ctx, port)
		},
	}

	command.Flags().IntVar(&port, "port", 9425, "Listen on given port")
	command.AddCommand(serverCmd(), workerCmd(), mcpCmd())

	if err := command.Execute(); err != nil {
		log.Fatal().Msgf("failed run app: %s", err.Error())
	}
}

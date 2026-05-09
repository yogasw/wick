package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/pkg/worker"

	"github.com/spf13/cobra"
)

func workerCmd() *cobra.Command {
	var command = &cobra.Command{
		Use:   "worker",
		Short: "Run background job worker",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx = log.Logger.With().Str("component", "worker").Logger().WithContext(ctx)
			return worker.NewServer().Run(ctx)
		},
	}

	return command
}

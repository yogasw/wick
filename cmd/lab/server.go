package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"

	"github.com/spf13/cobra"
)

func serverCmd() *cobra.Command {
	var port int
	var command = &cobra.Command{
		Use:   "server",
		Short: "Run web server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return api.NewServer().Run(ctx, port)
		},
	}

	command.Flags().IntVar(&port, "port", config.Load().App.Port, "Listen on given port (env: PORT)")
	return command
}

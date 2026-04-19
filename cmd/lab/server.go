package main

import (
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"

	"github.com/spf13/cobra"
)

func serverCmd() *cobra.Command {
	var port int
	var command = &cobra.Command{
		Use:   "server",
		Short: "Run web server",
		Run: func(cmd *cobra.Command, args []string) {
			runServer(port)
		},
	}

	command.Flags().IntVar(&port, "port", config.Load().App.Port, "Listen on given port (env: PORT)")
	return command
}

func runServer(port int) {
	srv := api.NewServer()
	srv.Run(port)
}

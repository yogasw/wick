package main

import (
	"github.com/yogasw/wick/internal/pkg/worker"

	"github.com/spf13/cobra"
)

func workerCmd() *cobra.Command {
	var command = &cobra.Command{
		Use:   "worker",
		Short: "Run background job worker",
		Run: func(cmd *cobra.Command, args []string) {
			srv := worker.NewServer()
			srv.Run()
		},
	}

	return command
}

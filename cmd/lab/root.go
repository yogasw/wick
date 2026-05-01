package main

import (
	_ "github.com/joho/godotenv/autoload"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/jobs"
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
		Run: func(cmd *cobra.Command, args []string) {
			runServer(port)
		},
	}

	command.Flags().IntVar(&port, "port", 8080, "Listen on given port")
	command.AddCommand(serverCmd(), workerCmd())

	if err := command.Execute(); err != nil {
		log.Fatal().Msgf("failed run app: %s", err.Error())
	}
}

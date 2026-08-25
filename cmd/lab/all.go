package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/worker"
)

// allCmd runs the HTTP server and the cron scheduler in the same
// process, sharing one *manager.Service. Use when you can't deploy a
// separate worker pod (single-container Docker, no shared volume). For
// multi-pod deployments keep `server` and `worker` separate so the
// scheduler runs in exactly one place.
func allCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "all",
		Short: "Run web server and cron scheduler in one process (single-node)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx = log.Logger.With().Str("component", "server").Logger().WithContext(ctx)

			srv := api.NewServer()

			schedCtx := log.Logger.With().Str("component", "worker").Logger().WithContext(ctx)
			// Auto-respawn loop. RunScheduler should only return when
			// schedCtx is cancelled; any other return is treated as a
			// crash and restarted with backoff so a transient DB blip
			// (or unexpected panic recovered upstream) doesn't silently
			// disable cron until the next deploy.
			go func() {
				const (
					backoffStart = 2 * time.Second
					backoffMax   = 30 * time.Second
				)
				backoff := backoffStart
				for {
					if err := worker.RunScheduler(schedCtx, srv.JobsSvc()); err != nil {
						log.Error().Err(err).Msg("worker scheduler exited with error — respawning")
					} else if schedCtx.Err() != nil {
						return // clean shutdown
					} else {
						log.Warn().Msg("worker scheduler exited without error — respawning")
					}
					select {
					case <-schedCtx.Done():
						return
					case <-time.After(backoff):
					}
					backoff *= 2
					if backoff > backoffMax {
						backoff = backoffMax
					}
				}
			}()

			return srv.Run(ctx, port)
		},
	}

	cmd.Flags().IntVar(&port, "port", config.Load().App.Port, "Listen on given port (env: PORT)")
	return cmd
}

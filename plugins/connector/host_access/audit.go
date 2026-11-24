package main

import (
	"time"

	"github.com/rs/zerolog/log"
)

// audit records only operation names and outcomes. Never log input payloads,
// paths, command arguments, environment variables or returned file contents:
// those can contain credentials. Wick owns the connector run history.
func audit(op string, started time.Time, err error) {
	event := log.Info()
	if err != nil {
		event = log.Warn()
	}
	event.Str("connector", "host_access").
		Str("operation", op).
		Bool("ok", err == nil).
		Dur("duration", time.Since(started)).
		Msg("host access operation finished")
}

package main

import (
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/pkg/connector"
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

// audited wraps the operation once, rather than duplicating logs in every handler.
func audited(op string, run connector.ExecuteFunc) connector.ExecuteFunc {
	return func(c *connector.Ctx) (result any, err error) {
		started := time.Now()
		defer func() { audit(op, started, err) }()
		return run(c)
	}
}

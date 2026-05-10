package wickmanager

import (
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/processctl"
)

// auditEvent is the per-op record written to mcp.log. Fields are
// flat-keyed so `jq` queries on the operator's machine stay simple.
type auditEvent struct {
	Op       string
	UserID   string
	IsAdmin  bool
	Args     any
	Before   any
	After    any
	Err      error
	Duration time.Duration
}

func mcpLogger() zerolog.Logger {
	if l, ok := processctl.MCPLogger(); ok {
		return l
	}
	return log.With().Str("component", "mcp").Logger()
}

func logOp(user *entity.User, op string, args any, err error, elapsed time.Duration) {
	logEvent(auditEvent{
		Op: op, UserID: userIDOrEmpty(user), IsAdmin: user != nil && user.IsAdmin(),
		Args: args, Err: err, Duration: elapsed,
	})
}

func logOpDiff(user *entity.User, op string, args, before, after any, err error, elapsed time.Duration) {
	logEvent(auditEvent{
		Op: op, UserID: userIDOrEmpty(user), IsAdmin: user != nil && user.IsAdmin(),
		Args: args, Before: before, After: after, Err: err, Duration: elapsed,
	})
}

func logEvent(ev auditEvent) {
	result := "success"
	if ev.Err != nil {
		result = "error"
	}
	l := mcpLogger()
	e := l.Info().
		Str("op", ev.Op).
		Str("user_id", ev.UserID).
		Bool("is_admin", ev.IsAdmin).
		Interface("args", ev.Args).
		Str("result", result).
		Dur("duration", ev.Duration)
	if ev.Before != nil {
		e = e.Interface("before", ev.Before)
	}
	if ev.After != nil {
		e = e.Interface("after", ev.After)
	}
	if ev.Err != nil {
		e = e.Str("error", ev.Err.Error())
	}
	e.Msg("wickmanager op")
}

func userIDOrEmpty(u *entity.User) string {
	if u == nil {
		return ""
	}
	return u.ID
}

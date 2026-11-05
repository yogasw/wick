package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// codeRunOrigin is which door a script came through and which script it
// is, carried on ctx from the caller to RunCodeAs so the audit row can say.
type codeRunOrigin struct {
	via    entity.CodeModeRunVia
	script string
}

type codeRunOriginKey struct{}

// WithCodeRunOrigin marks ctx with the door a Code Mode run came through
// (watch, workflow, cli) and its script_path ("" = inline code), for the
// run audit. RunCodeAs without it records the run as a workflow run, the
// one caller that has no session.
func WithCodeRunOrigin(ctx context.Context, via entity.CodeModeRunVia, scriptPath string) context.Context {
	return context.WithValue(ctx, codeRunOriginKey{}, codeRunOrigin{via: via, script: scriptPath})
}

func codeRunOriginOf(ctx context.Context) codeRunOrigin {
	if o, ok := ctx.Value(codeRunOriginKey{}).(codeRunOrigin); ok {
		return o
	}
	return codeRunOrigin{via: entity.CodeModeViaWorkflow}
}

// codeRunAuditTimeout bounds the audit insert, which runs off the
// caller's path so a slow database never delays a script's result.
const codeRunAuditTimeout = 10 * time.Second

// recordCodeRun writes the audit row of one finished run. res and err are
// what codemode.Host.Run returned. It never fails the run: an insert error
// is logged and dropped.
func recordCodeRun(ctx context.Context, svc *connectors.Service, layout agentconfig.Layout, origin codeRunOrigin, lang, sessionID string, user *entity.User, started time.Time, res *codemode.Result, err error) {
	if svc == nil {
		return
	}
	row := codeRunRow(origin, lang, sessionID, started, res, err)
	if user != nil {
		row.UserID = user.ID
	}
	go func() {
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), codeRunAuditTimeout)
		defer cancel()
		row.ProjectID = sessionProjectID(layout, sessionID)
		if err := svc.RecordCodeRun(actx, row); err != nil {
			log.Warn().Err(err).Str("session", sessionID).Msg("codemode: audit row not saved")
		}
	}()
}

// codeRunRow builds the audit row from a run's outcome, without the
// fields that need the user or the session's project.
func codeRunRow(origin codeRunOrigin, lang, sessionID string, started time.Time, res *codemode.Result, err error) *entity.CodeModeRun {
	script := strings.TrimSpace(origin.script)
	if script == "" {
		script = "inline"
	}
	if lang == "" {
		lang = "ts"
	}
	row := &entity.CodeModeRun{
		ID:         uuid.NewString(),
		SessionID:  sessionID,
		Via:        origin.via,
		Script:     script,
		Lang:       strings.ToLower(lang),
		StartedAt:  started,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if err != nil {
		row.Status, row.ErrorMsg = entity.CodeModeRunNotStarted, err.Error()
		return row
	}
	if res == nil {
		row.Status = entity.CodeModeRunNotStarted
		return row
	}
	if res.RunID != "" {
		row.ID = res.RunID
	}
	row.Runner, row.QueuedMs, row.Calls = res.Runner, res.QueuedMS, len(res.Calls)
	if res.DurationMS > 0 {
		row.DurationMs = res.DurationMS
	}
	for _, c := range res.Calls {
		if !c.OK {
			row.CallErrors++
		}
	}
	row.ErrorMsg = res.Error
	row.Status = codeRunStatus(res)
	return row
}

// codeRunStatus classifies a finished run by the error text
// codemode.Host.Run wrote for it.
func codeRunStatus(res *codemode.Result) entity.CodeModeRunStatus {
	switch {
	case res.OK:
		return entity.CodeModeRunOK
	case strings.HasPrefix(res.Error, "script timeout"):
		return entity.CodeModeRunTimeout
	case strings.HasPrefix(res.Error, "the runner stopped before the script finished"):
		return entity.CodeModeRunCrashed
	default:
		return entity.CodeModeRunError
	}
}

// sessionProjectID is the project a session belongs to, "" when there is
// no session or it cannot be read.
func sessionProjectID(layout agentconfig.Layout, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return ""
	}
	return sess.Meta.ProjectID
}

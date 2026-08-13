package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/entity"
)

// Target resolution: where does a fire actually land?
//
// A schedule's delivery target is not necessarily fixed at create time. The
// three modes trade off context freshness against continuity:
//
//	existing  → the SessionID given at create. One long conversation that
//	            accumulates every fire. Fails if the session is gone.
//	new       → a generated id, unique per fire. Clean context every run;
//	            the natural shape for "every Monday, write the report".
//	template  → a rendered id, e.g. daily-report-2026-08-13. Fires that
//	            render the same id share a session (and queue behind each
//	            other); a template with no time token is the degenerate
//	            "always this one session" case.
//
// Resolution is a pure function so the whole matrix is testable without a
// DB, a pool, or a filesystem. The runner owns the side effect: for the two
// project-scoped modes it calls EnsureSession on the resolved id (idempotent
// — creates when absent, reuses when present) before sending.

// generatedIDPrefix marks sessions minted by the scheduler. Grep-able, and
// the UI can spot a scheduler-spawned session from the id alone.
const generatedIDPrefix = "sch-"

// ResolveTarget maps a claimed schedule plus its fire time to the concrete
// session id the message is delivered into. mint reports whether the runner
// must materialize that session (true for the project-scoped modes) rather
// than requiring it to already exist.
//
// m.RunCount is expected to be the post-claim value (the store increments it
// when claiming), so it is a stable per-fire counter: mode "new" needs no
// extra state to stay collision-free across fires.
func ResolveTarget(m entity.ScheduledMessage, firedAt time.Time) (sessionID string, mint bool, err error) {
	switch m.Mode() {
	case entity.ScheduledSessionExisting:
		if strings.TrimSpace(m.SessionID) == "" {
			return "", false, fmt.Errorf("schedule has no target session")
		}
		return m.SessionID, false, nil

	case entity.ScheduledSessionNew:
		if strings.TrimSpace(m.ProjectID) == "" {
			return "", false, fmt.Errorf("session_mode=new requires a project")
		}
		// Scheduled fires are numbered by run count (…-1, …-2). A MANUAL fire
		// doesn't advance that count, so numbering it the same way produced
		// "…-0" — off by one against the convention and easy to mistake for
		// the first scheduled run. Manual fires get their own suffix instead.
		suffix := strconv.Itoa(m.RunCount)
		if m.ManualFire {
			suffix = "manual-" + strconv.Itoa(m.ManualRuns)
		}
		id := generatedIDPrefix + shortScheduleID(m.ID) + "-" + suffix
		if verr := storage.ValidateSessionID(id); verr != nil {
			return "", false, verr
		}
		return id, true, nil

	case entity.ScheduledSessionTemplate:
		if strings.TrimSpace(m.ProjectID) == "" {
			return "", false, fmt.Errorf("session_mode=template requires a project")
		}
		id, rerr := RenderTemplate(m.SessionTemplate, m, firedAt)
		if rerr != nil {
			return "", false, rerr
		}
		return id, true, nil

	default:
		return "", false, fmt.Errorf("unknown session_mode %q", m.SessionMode)
	}
}

// RenderTemplate expands a session-id pattern against a fire time. Supported
// placeholders (UTC, so a schedule's ids don't shift with the server's zone):
//
//	{date}     2026-08-13
//	{datetime} 2026-08-13-0900
//	{ym}       2026-08
//	{run}      the fire number (RunCount)
//	{id}       the schedule's short id
//
// Anything else is copied literally, then the whole id is validated — so a
// pattern that would produce an unusable session id is rejected here rather
// than at fire time. Callers validate at CREATE time by rendering against
// now; see ValidateTargetSpec.
func RenderTemplate(tpl string, m entity.ScheduledMessage, firedAt time.Time) (string, error) {
	tpl = strings.TrimSpace(tpl)
	if tpl == "" {
		return "", fmt.Errorf("session_template is required for session_mode=template")
	}
	utc := firedAt.UTC()
	out := tpl
	for _, rep := range []struct{ token, value string }{
		{"{datetime}", utc.Format("2006-01-02-1504")},
		{"{date}", utc.Format("2006-01-02")},
		{"{ym}", utc.Format("2006-01")},
		{"{run}", strconv.Itoa(m.RunCount)},
		{"{id}", shortScheduleID(m.ID)},
	} {
		out = strings.ReplaceAll(out, rep.token, rep.value)
	}
	if strings.ContainsAny(out, "{}") {
		return "", fmt.Errorf("session_template %q has an unknown placeholder (supported: {date} {datetime} {ym} {run} {id})", tpl)
	}
	if err := storage.ValidateSessionID(out); err != nil {
		return "", fmt.Errorf("session_template %q renders to an invalid session id: %w", tpl, err)
	}
	return out, nil
}

// TargetSpec is the target configuration supplied at create (or edited on
// reschedule), before it becomes a row. Kept separate from the entity so the
// MCP and UI create paths share one validation.
type TargetSpec struct {
	SessionID string
	ProjectID string
	Mode      string
	Template  string
}

// NormalizeTargetSpec fills in the mode a caller left blank. The rule keeps
// the common cases to a single argument:
//
//	project_id only → "new"       (a standalone job, fresh session each run)
//	session_id only → "existing"  (nudge the conversation you are in)
//
// An explicit mode always wins. A template with no mode implies "template".
func NormalizeTargetSpec(spec TargetSpec) TargetSpec {
	spec.SessionID = strings.TrimSpace(spec.SessionID)
	spec.ProjectID = strings.TrimSpace(spec.ProjectID)
	spec.Mode = strings.TrimSpace(spec.Mode)
	spec.Template = strings.TrimSpace(spec.Template)
	if spec.Mode != "" {
		return spec
	}
	switch {
	case spec.Template != "":
		spec.Mode = entity.ScheduledSessionTemplate
	case spec.ProjectID != "" && spec.SessionID == "":
		spec.Mode = entity.ScheduledSessionNew
	default:
		spec.Mode = entity.ScheduledSessionExisting
	}
	return spec
}

// ValidateTargetSpec checks a normalized spec is coherent, and for the
// template mode proves the pattern renders to a legal session id by trying
// it against `now`. Failing loudly at create beats a recurring job that dies
// on its 40th fire at 3am.
func ValidateTargetSpec(spec TargetSpec, now time.Time) error {
	switch spec.Mode {
	case entity.ScheduledSessionExisting:
		if spec.SessionID == "" {
			return fmt.Errorf("session_id is required (the session to deliver into)")
		}
	case entity.ScheduledSessionNew:
		if spec.ProjectID == "" {
			return fmt.Errorf("project_id is required for session_mode=new")
		}
	case entity.ScheduledSessionTemplate:
		if spec.ProjectID == "" {
			return fmt.Errorf("project_id is required for session_mode=template")
		}
		probe := entity.ScheduledMessage{ID: "sm_00000000-probe", RunCount: 1}
		if _, err := RenderTemplate(spec.Template, probe, now); err != nil {
			return err
		}
	default:
		return fmt.Errorf("session_mode must be one of: %s, %s, %s",
			entity.ScheduledSessionExisting, entity.ScheduledSessionNew, entity.ScheduledSessionTemplate)
	}
	return nil
}

// shortScheduleID is the stable, id-safe fragment of a schedule id used in
// generated session ids: the first 8 chars of the uuid after the "sm_"
// prefix. Short enough to read in a sidebar, wide enough not to collide in
// practice.
func shortScheduleID(id string) string {
	s := strings.TrimPrefix(id, "sm_")
	s = strings.ReplaceAll(s, "-", "")
	if len(s) > 8 {
		s = s[:8]
	}
	if s == "" {
		return "anon"
	}
	return s
}

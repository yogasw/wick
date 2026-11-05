package entity

import "time"

// CodeModeRunVia says which door a Code Mode script came through.
type CodeModeRunVia string

const (
	// CodeModeViaMCP is a wick_run_code tool call from an agent.
	CodeModeViaMCP CodeModeRunVia = "mcp"
	// CodeModeViaCLI is `support-tools codemode run` from a shell.
	CodeModeViaCLI CodeModeRunVia = "cli"
	// CodeModeViaWatch is a code step of a schedule watch.
	CodeModeViaWatch CodeModeRunVia = "watch"
	// CodeModeViaWorkflow is a run_code node of a workflow.
	CodeModeViaWorkflow CodeModeRunVia = "workflow"
)

// CodeModeRunStatus is how one script run ended.
type CodeModeRunStatus string

const (
	CodeModeRunOK CodeModeRunStatus = "ok"
	// CodeModeRunError is the script's own failure: a throw, a bad return,
	// a refused pre-flight.
	CodeModeRunError CodeModeRunStatus = "error"
	// CodeModeRunTimeout is a script cut at its time limit.
	CodeModeRunTimeout CodeModeRunStatus = "timeout"
	// CodeModeRunCrashed is a runner that stopped before the script
	// finished (crash, OOM, restart).
	CodeModeRunCrashed CodeModeRunStatus = "crashed"
	// CodeModeRunNotStarted is a run wick could not hand to a runner at
	// all (none installed, busy past the queue limit, refused).
	CodeModeRunNotStarted CodeModeRunStatus = "not_started"
)

// CodeModeRun is the audit row of one Code Mode script run, whatever door
// it came through. The connector calls the script made are ConnectorRun
// rows with source code_mode; this row is the run around them, so a
// failure inside the script (a throw, a timeout, a runner crash) is
// visible even when no connector call failed.
//
// Only metadata is kept: no code, input or result, so the table stays
// small and holds nothing a connector response carried.
type CodeModeRun struct {
	ID        string `gorm:"type:text;primaryKey"`
	UserID    string `gorm:"type:text;index:idx_cmrun_user_started,priority:1"`
	SessionID string `gorm:"type:text;index"`
	// ProjectID is the session's project at run time, kept on the row
	// because a session can be deleted and the audit must still say where
	// the run happened.
	ProjectID string         `gorm:"type:text;index:idx_cmrun_project_started,priority:1"`
	Via       CodeModeRunVia `gorm:"type:text;not null"`
	// Script is the script_path, or "inline" for code passed in the call.
	Script     string            `gorm:"type:text"`
	Lang       string            `gorm:"type:text"`
	Runner     string            `gorm:"type:text"`
	Status     CodeModeRunStatus `gorm:"type:text;not null;index:idx_cmrun_status_started,priority:1"`
	ErrorMsg   string            `gorm:"type:text"`
	Calls      int
	CallErrors int
	DurationMs int64
	QueuedMs   int64
	StartedAt  time.Time `gorm:"not null;index;index:idx_cmrun_user_started,priority:2,sort:desc;index:idx_cmrun_project_started,priority:2,sort:desc;index:idx_cmrun_status_started,priority:2,sort:desc"`
}

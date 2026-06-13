package sessionconfigpurge

// Config is the runtime-editable config for the session-config-purge
// job. One row in the `configs` table scoped to the job's Meta.Key.
type Config struct {
	// RetentionHours is the cutoff for per-session workspaces.
	// Any sessions/<id>/workspace.json whose file mtime is older
	// than (now - RetentionHours) is deleted on each tick. Empty /
	// non-positive falls back to the in-code default — see NewRun.
	RetentionHours int `wick:"number;desc=Hours to keep per-session connector workspaces. Older workspace files are purged each run. Default 720 (30 days)."`
}

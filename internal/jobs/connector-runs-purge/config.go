package connectorrunspurge

// Config is the runtime-editable config for the connector-runs-purge
// job. One row in the `configs` table scoped to the job's Meta.Key.
//
// The seed value is set at registration time by passing
// Config{RetentionDays: N} to entity.StructToConfigs — the admin can
// then change it from the manager UI without a restart.
type Config struct {
	// RetentionDays is the cutoff for connector_runs cleanup. Rows
	// whose StartedAt is older than (now - RetentionDays) are hard
	// deleted on every tick. Empty / non-positive values fall back to
	// the in-code default of 7 — see NewRun in handler.go.
	RetentionDays int `wick:"number;desc=Days of connector_runs history to keep. Older rows are purged each run. Default 7."`
}

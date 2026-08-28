package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/yogasw/wick/internal/entity"
)

// AutoMigrate inspects the catalog per table, per column, per index —
// thousands of small sequential queries. With the database one hop away that
// is free; at ~28ms RTT it is ~100 seconds of downtime on every restart,
// paid once per process. The schema only changes on deploy, not on restart,
// so migrate() skips the pass when a fingerprint of the model definitions —
// computed locally, zero round-trips — matches the one stored in the
// database by the previous pass. The hot path is then two queries.
//
// The fingerprint vouches for the *model definitions*, not for the live
// database state: schema changes made behind wick's back are invisible to
// it. That is the same trade-off Flyway, golang-migrate and Rails
// schema_migrations all accept.

// schemaEpoch folds the hand-written DDL into the fingerprint. Hashing the
// models covers AutoMigrate's input, but not the raw statements that run
// after it in migrate() — DropStaleProfileKeyIndex, idx_storage_tree,
// idx_agent_delegations_root_handle. Those are idempotent and stay on the
// hot path, but if one is EDITED its new form must be applied everywhere,
// and self-healing ones (idx_storage_tree installs once duplicates are
// cleared) must keep re-running. Bump this when that hand-written DDL
// changes shape; model changes need no bump.
const schemaEpoch = 1

// migratedModels is the single source of truth for what Migrate manages.
// migrate() feeds it to AutoMigrate and modelFingerprint hashes it, so the
// two cannot drift apart: a model added here is both migrated and part of
// the fingerprint that decides whether migration can be skipped.
var migratedModels = []any{
	&entity.User{},
	&entity.Session{},
	&entity.ToolPermission{},
	&entity.Tag{},
	&entity.ToolTag{},
	&entity.UserTag{},
	&entity.Bookmark{},
	&entity.Config{},
	&entity.SSOProvider{},
	&entity.Job{},
	&entity.JobRun{},
	&entity.Connector{},
	&entity.ConnectorOperation{},
	&entity.ConnectorRun{},
	&entity.ConnectorAccount{},
	&entity.CustomConnector{},
	&entity.CustomConnectorMCPServer{},
	&entity.PersonalAccessToken{},
	&entity.PushSubscription{},
	&entity.UserChannelIdentity{},
	&entity.OAuthClient{},
	&entity.OAuthAuthorizationCode{},
	&entity.OAuthToken{},
	&entity.AgentChannel{},
	&entity.ProviderStorage{},
	&entity.ProviderStorageSource{},
	&entity.DataTable{},
	&entity.DataTableRow{},
	// Workflow storage migration — see
	// internal/planning/archive/workflow/svelte-migration.md. Tables added in
	// parallel with the existing file-based store; the importer in
	// internal/agents/workflow/repository (future phase) hydrates
	// the rows from disk on boot before any handler reads them.
	&entity.Workflow{},
	&entity.WorkflowVersion{},
	&entity.WorkflowTestCase{},
	&entity.Skill{},
	&entity.PluginState{},
	&entity.ConnectorState{},
	&entity.ScheduledMessage{},
	// Multi-agent sub-agent delegation — see
	// internal/planning/todo/multi-agent/design.md. Profiles are the
	// reusable role definitions; delegations are the per-call audit +
	// control records the governor and the rail UI both read.
	&entity.AgentProfile{},
	&entity.AgentDelegation{},
	&entity.AgentSquad{},
	&entity.AgentBoard{},
	&entity.AgentTask{},
	&entity.AgentMessage{},
	// Incident state: what a delegation tree has established, and the
	// quoted evidence behind it. Created lazily, so most trees never
	// write a row here.
	&entity.AgentIncident{},
	&entity.AgentEvidence{},
}

// modelFingerprint derives a stable hash of the models' shape. Purely
// local: schema.Parse reads struct tags, it never touches the database.
//
// The hash must change exactly when AutoMigrate would emit different DDL,
// so it covers everything AutoMigrate reads: column name, type, size,
// precision, nullability, uniqueness, primary key, defaults, comments —
// and the parsed indexes, which a name/type-only hash would miss (an
// `index` tag changes none of the column attributes).
//
// Returns "" when any model fails to parse. The caller treats "" as
// "unknown" and runs the full pass — a failure here degrades to the old
// always-migrate behavior, never to a silently stale schema.
func modelFingerprint(db *gorm.DB, models []any) string {
	h := sha256.New()
	fmt.Fprintf(h, "epoch=%d\n", schemaEpoch)
	cache := &sync.Map{}
	for _, m := range models {
		s, err := schema.Parse(m, cache, db.NamingStrategy)
		if err != nil {
			return ""
		}

		cols := make([]string, 0, len(s.Fields))
		for _, f := range s.Fields {
			if f.DBName == "" {
				continue // not a column (associations, ignored fields)
			}
			cols = append(cols, fmt.Sprintf(
				"col=%s type=%s size=%d prec=%d scale=%d notnull=%t unique=%t pk=%t autoinc=%t default=%q comment=%q",
				f.DBName, f.DataType, f.Size, f.Precision, f.Scale,
				f.NotNull, f.Unique, f.PrimaryKey, f.AutoIncrement,
				f.DefaultValue, f.Comment))
		}
		// Struct field order emits no DDL, so it must not change the hash.
		sort.Strings(cols)

		idxs := make([]string, 0)
		for _, idx := range s.ParseIndexes() {
			line := fmt.Sprintf("idx=%s class=%s type=%s where=%q option=%q",
				idx.Name, idx.Class, idx.Type, idx.Where, idx.Option)
			// Field order inside a composite index IS the index definition —
			// keep it, only the index list as a whole gets sorted.
			for _, f := range idx.Fields {
				line += fmt.Sprintf(" (%s sort=%s collate=%s len=%d expr=%q)",
					f.DBName, f.Sort, f.Collate, f.Length, f.Expression)
			}
			idxs = append(idxs, line)
		}
		sort.Strings(idxs)

		fmt.Fprintf(h, "table=%s\n", s.Table)
		for _, c := range cols {
			fmt.Fprintf(h, "  %s\n", c)
		}
		for _, i := range idxs {
			fmt.Fprintf(h, "  %s\n", i)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ensureStateTable creates the fingerprint store. Deliberately raw DDL, not
// AutoMigrate — AutoMigrate's catalog inspection is exactly what this table
// exists to avoid. The types are portable across Postgres and SQLite
// (SQLite resolves them by affinity), and CHECK (id) pins the table to a
// single row.
func ensureStateTable(db *gorm.DB) error {
	return db.Exec(`CREATE TABLE IF NOT EXISTS wick_schema_state (
		id          boolean PRIMARY KEY DEFAULT true CHECK (id),
		fingerprint text NOT NULL,
		applied_at  timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`).Error
}

// storedFingerprint reads the fingerprint recorded by the last completed
// pass. "" — table missing, row missing, or any error — means "unknown",
// which the caller resolves by running the full pass. Fail-safe, never
// fail-stale.
func storedFingerprint(db *gorm.DB) string {
	if err := ensureStateTable(db); err != nil {
		return ""
	}
	var fp string
	if err := db.Raw(`SELECT fingerprint FROM wick_schema_state WHERE id`).Scan(&fp).Error; err != nil {
		return ""
	}
	return fp
}

// storeFingerprint records fp as the schema now in effect. Best-effort: a
// write failure only means the next boot pays for one redundant
// AutoMigrate pass.
func storeFingerprint(db *gorm.DB, fp string) {
	l := log.With().Str("component", "migrate").Logger()
	if err := ensureStateTable(db); err != nil {
		l.Warn().Err(err).Msg("schema fingerprint not stored; next boot will re-run AutoMigrate")
		return
	}
	err := db.Exec(`INSERT INTO wick_schema_state (id, fingerprint, applied_at)
		VALUES (true, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (id) DO UPDATE
		  SET fingerprint = excluded.fingerprint, applied_at = CURRENT_TIMESTAMP`, fp).Error
	if err != nil {
		l.Warn().Err(err).Msg("schema fingerprint not stored; next boot will re-run AutoMigrate")
	}
}

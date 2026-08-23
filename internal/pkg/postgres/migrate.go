package postgres

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yogasw/wick/internal/entity"

	"github.com/rs/zerolog/log"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DropStaleProfileKeyIndex removes the pre-scoping unique index on
// agent_profiles(key).
//
// Sub-agent roles became scoped: uniqueness is now (project_id, key), so a
// project may define a role under a key the global scope already uses.
// AutoMigrate creates that composite index but leaves the old one in
// place, and the old one keeps rejecting the second row — surfacing as a
// constraint violation on save, nowhere near its cause.
//
// Idempotent, and a no-op on databases that never had the index.
func DropStaleProfileKeyIndex(db *gorm.DB) {
	l := log.With().Str("component", "migrate").Logger()
	if res := db.Exec(`DROP INDEX IF EXISTS idx_agent_profiles_key`); res.Error != nil {
		l.Warn().Err(res.Error).
			Msg("could not drop the stale agent_profiles key index; project-scoped roles may be rejected on save")
	}
}

// migrated records which databases this process has already migrated,
// keyed by DSN.
//
// Tray mode runs the server and the worker as independent toggles in one
// process (internal/systemtray), each building its own *gorm.DB via
// NewGORM. Without this guard, flipping the second toggle runs a full
// AutoMigrate pass while the first component is already serving queries
// off its own pool — and a connection that opened before the DDL keeps a
// cached statement plan describing the old table shape. Postgres then
// rejects the next query on that connection with SQLSTATE 0A000
// ("cached plan must not change result type"), intermittently, until the
// connection ages out of the pool.
//
// Keyed per database rather than a single process-wide sync.Once: the
// guard has to skip a repeat pass over the *same* database while still
// migrating a genuinely different one. A process-wide Once would leave
// the second database silently unmigrated — the tests open a fresh
// in-memory SQLite per case and would each get an empty schema.
// The in-memory case is keyed by IDENTITY of the handle, not by its address.
//
// %p was the first attempt and it is unsound: Go reuses heap addresses, so a fresh
// *gorm.DB can land where a collected one lived, find its key already marked migrated,
// and skip the DDL — leaving a caller with an empty schema and
// "no such table: configs". Measured at ~9k reused addresses per 200k allocations, which
// is why the failure moved from test to test between runs and looked flaky.
//
// A map keyed by the pointer itself both fixes the identity (the key holds the handle
// alive, so its address cannot be recycled while the entry exists) and needs no counter.
var (
	migratedMu  sync.Mutex
	migrated    = map[string]bool{}
	migratedMem = map[*gorm.DB]bool{}
)

// Migrate brings the schema up to date. Safe to call from every component
// that opens a handle to the same database — only the first call does work.
func Migrate(db *gorm.DB) {
	key := dsnKey(db)

	migratedMu.Lock()
	if key == "" {
		// No readable DSN, so identity is the handle. Keyed by the pointer VALUE rather
		// than by its formatted address: an entry in this map keeps the handle reachable,
		// so its address cannot be reused by a later allocation.
		if migratedMem[db] {
			migratedMu.Unlock()
			return
		}
		migratedMem[db] = true
		migratedMu.Unlock()
		migrate(db)
		return
	}
	if migrated[key] {
		migratedMu.Unlock()
		return
	}
	migrated[key] = true
	migratedMu.Unlock()

	migrate(db)
}

// dsnKey identifies the database behind a handle, so two handles to the
// same database share a key and distinct databases do not.
func dsnKey(db *gorm.DB) string {
	var dsn string
	// The DSN is a field on the driver's embedded Config, not a method —
	// there is no shared interface to assert against, so match the
	// concrete dialector. SQLite is deliberately absent: see below.
	if d, ok := db.Dialector.(gormpostgres.Dialector); ok && d.Config != nil {
		dsn = d.DSN
	}

	// Every ":memory:" handle is a distinct database despite sharing a DSN, so those must
	// never be deduplicated by name. Same for any dialector whose DSN we cannot read.
	//
	// Returns "" to mean "identity is the handle itself", which Migrate resolves through
	// migratedMem. This used to return a key built with %p; heap addresses are reused, so
	// a new handle could inherit a dead one's "already migrated" mark and be left with no
	// schema at all.
	if dsn == "" || strings.Contains(dsn, ":memory:") {
		return ""
	}
	return db.Dialector.Name() + "|" + dsn
}

func migrate(db *gorm.DB) {
	// Run the DDL on a connection of its own and hand it back closed, so
	// no pooled connection outlives the schema it was planned against.
	// SetMaxIdleConns(0) would only evict connections that are idle at
	// that instant; one checked out by a concurrent query would survive
	// and stay poisoned. Taking a dedicated conn sidesteps that entirely.
	//
	// Postgres only. SQLite has no plan cache to invalidate, so it gains
	// nothing — and for ":memory:" it is actively wrong: each connection
	// is its own empty database, so the schema would be created on a
	// connection that is then closed and discarded.
	//
	// Best-effort: on failure fall through to the pooled handle rather
	// than blocking boot, since the retry installed in NewGORM still
	// covers the stale-plan case.
	if _, isPG := db.Dialector.(gormpostgres.Dialector); isPG {
		if sqlDB, err := db.DB(); err == nil {
			if conn, err := sqlDB.Conn(context.Background()); err == nil {
				if scoped, err := gorm.Open(db.Dialector, &gorm.Config{
					Logger:   db.Logger,
					ConnPool: conn,
				}); err == nil {
					defer conn.Close()
					db = scoped
				} else {
					conn.Close()
				}
			}
		}
	}

	err := db.AutoMigrate(
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
	)
	if err != nil {
		log.Fatal().Msgf("failed to run migration: %s", err.Error())
	}

	// Must run AFTER AutoMigrate: the composite (project_id, key) index has
	// to exist before the single-column one is removed, so the table is
	// never briefly without a uniqueness guard on the key.
	DropStaleProfileKeyIndex(db)

	// Create adjacency-list unique index — not managed by AutoMigrate.
	// Soft-fail: a DB with duplicate (provider, instance, parent_id, name)
	// rows would reject the unique constraint; the runtime still works via
	// SELECT-then-INSERT, just without the DB-level guard. Re-runs on every
	// boot via IF NOT EXISTS, so it installs once duplicates are cleared.
	if res := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_storage_tree ON provider_storage (provider_type, instance_name, parent_id, name)`); res.Error != nil {
		log.Warn().Err(res.Error).Msg("migrate: idx_storage_tree creation failed (duplicates present?)")
	}

	// One handle per tree. Not managed by AutoMigrate because the column
	// is added to an existing table: rows written before this migration
	// carry an empty handle, and a partial index is the only portable way
	// to let those coexist with the constraint. Soft-fail for the same
	// reason as idx_storage_tree — addressing still works, just without
	// the DB-level guarantee.
	if res := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_delegations_root_handle ON agent_delegations (root_id, handle) WHERE handle <> ''`); res.Error != nil {
		log.Warn().Err(res.Error).Msg("migrate: idx_agent_delegations_root_handle creation failed (duplicate handles?)")
	}

	seedOwner(db)
}

// seedOwner promotes the oldest user to App Owner when no owner exists.
// Handles DBs created before the is_owner column was added (existing installs).
func seedOwner(db *gorm.DB) {
	var ownerCount int64
	db.Model(&entity.User{}).Where("is_owner = ?", true).Count(&ownerCount)
	if ownerCount > 0 {
		return
	}
	var oldest entity.User
	if err := db.Where("role = ?", entity.RoleAdmin).Order("created_at ASC").First(&oldest).Error; err != nil {
		return
	}
	if err := db.Model(&oldest).Update("is_owner", true).Error; err != nil {
		log.Warn().Err(err).Str("user_id", oldest.ID).Msg("migrate: seedOwner failed")
	}
}

// repairProviderStorageTree rewires every row's parent_id from its rel_path
// so a row at "/a/b/c" is parented to the row at "/a/b". Heals DBs where
// an ancestor row was deleted but descendants still reference the dead ID.
// Idempotent: returns 0 fixed when the tree is already healthy.
func RepairProviderStorageTree(db *gorm.DB) (int, error) {
	var rows []entity.ProviderStorage
	// Only the adjacency columns are needed to rewire parent_id. Skip the
	// Content bytea blob — pulling it would clone every file's bytes into
	// heap (gigabytes of transient alloc) for a tree walk that never reads it.
	if err := db.Select("id", "provider_type", "instance_name", "rel_path", "parent_id").
		Find(&rows).Error; err != nil {
		return 0, err
	}
	const sep = "\x00"
	byKey := make(map[string]uint, len(rows))
	for _, r := range rows {
		byKey[r.ProviderType+sep+r.InstanceName+sep+r.RelPath] = r.ID
	}
	fixed := 0
	for _, r := range rows {
		norm := filepath.ToSlash(r.RelPath)
		leadingSlash := strings.HasPrefix(norm, "/")
		trimmed := strings.TrimPrefix(norm, "/")
		parts := strings.Split(trimmed, "/")
		var wantParent uint
		if len(parts) > 1 {
			parentRel := strings.Join(parts[:len(parts)-1], "/")
			if leadingSlash {
				parentRel = "/" + parentRel
			}
			wantParent = byKey[r.ProviderType+sep+r.InstanceName+sep+parentRel]
			// parent rel_path not found → fall through with wantParent=0
			// so the row stays reachable via listRoots.
		}
		if r.ParentID == wantParent {
			continue
		}
		if err := db.Model(&entity.ProviderStorage{}).
			Where("id = ?", r.ID).
			Update("parent_id", wantParent).Error; err != nil {
			return fixed, err
		}
		fixed++
	}
	return fixed, nil
}

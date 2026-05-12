package connectors

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/strutil"
)

const (
	maxRunRequestLen = strutil.DefaultLimit
	maxRunResponseLen = strutil.DefaultLimit
	maxRunErrorLen   = 2_000
)

// Repo wraps the gorm handle and exposes the connector-specific CRUD
// surface used by the admin UI, the MCP dispatch layer, and the
// run-history retention job.
//
// All queries scope on context, so cancellation from the HTTP handler
// (or the cron worker) propagates cleanly into the DB driver.
type Repo struct {
	db *gorm.DB
}

// NewRepo wires a Repo around an existing gorm handle.
func NewRepo(db *gorm.DB) *Repo {
	return &Repo{db: db}
}

// ── Connector CRUD ───────────────────────────────────────────────────

// Create inserts a new Connector row. The BeforeCreate hook on the
// entity stamps an ID if the caller left it empty.
func (r *Repo) Create(ctx context.Context, c *entity.Connector) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// Get loads a Connector by ID. Returns gorm.ErrRecordNotFound when no
// row matches.
func (r *Repo) Get(ctx context.Context, id string) (*entity.Connector, error) {
	var c entity.Connector
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&c).Error
	return &c, err
}

// List returns every Connector row, newest first. Admin view.
func (r *Repo) List(ctx context.Context) ([]entity.Connector, error) {
	var out []entity.Connector
	err := r.db.WithContext(ctx).Order("created_at DESC").Find(&out).Error
	return out, err
}

// ListByKey returns every Connector that instantiates the given code
// definition (e.g. all "loki" rows).
func (r *Repo) ListByKey(ctx context.Context, key string) ([]entity.Connector, error) {
	var out []entity.Connector
	err := r.db.WithContext(ctx).Where(`"key" = ?`, key).Order("created_at DESC").Find(&out).Error
	return out, err
}

// ListAccessibleTo returns the not-disabled Connector rows the caller
// is allowed to see, mirroring the Tools tag-filter rule:
//
//   - row with no filter-type tags → visible to everyone
//   - row with ≥1 filter-type tag  → visible only when userTagIDs
//     intersects the row's filter-tags
//
// Tag association reuses the `tool_tags` table with
// `tool_path = '/connectors/{id}'` (see entity.Connector godoc).
//
// Pass an empty userTagIDs for users that carry no filter tags — they
// still see fully untagged rows. Admin callers should bypass this and
// use List instead.
func (r *Repo) ListAccessibleTo(ctx context.Context, userTagIDs []string) ([]entity.Connector, error) {
	var out []entity.Connector
	q := r.db.WithContext(ctx).
		Where("disabled = ?", false).
		Where(`
			NOT EXISTS (
				SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
				WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
			)`)
	if len(userTagIDs) > 0 {
		q = r.db.WithContext(ctx).
			Where("disabled = ?", false).
			Where(`
				NOT EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
				)
				OR EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id
					AND t.is_filter = true
					AND tt.tag_id IN ?
				)`, userTagIDs)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, err
}

// ListAccessibleForManager mirrors ListAccessibleTo but does NOT strip
// disabled rows. The admin manager surface must be able to enumerate
// disabled rows so they can be re-enabled. Tag-filter logic is unchanged.
func (r *Repo) ListAccessibleForManager(ctx context.Context, userTagIDs []string) ([]entity.Connector, error) {
	var out []entity.Connector
	q := r.db.WithContext(ctx).
		Where(`
			NOT EXISTS (
				SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
				WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
			)`)
	if len(userTagIDs) > 0 {
		q = r.db.WithContext(ctx).
			Where(`
				NOT EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
				)
				OR EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id
					AND t.is_filter = true
					AND tt.tag_id IN ?
				)`, userTagIDs)
	}
	err := q.Order("created_at DESC").Find(&out).Error
	return out, err
}

// IsAccessibleForManager mirrors IsAccessibleTo but ignores the disabled
// flag. Used by manager handlers so admins who disabled a row can still
// open its detail page to re-enable.
func (r *Repo) IsAccessibleForManager(ctx context.Context, connectorID string, userTagIDs []string) (bool, error) {
	var n int64
	q := r.db.WithContext(ctx).
		Model(&entity.Connector{}).
		Where("id = ?", connectorID).
		Where(`
			NOT EXISTS (
				SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
				WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
			)`)
	if len(userTagIDs) > 0 {
		q = r.db.WithContext(ctx).
			Model(&entity.Connector{}).
			Where("id = ?", connectorID).
			Where(`
				NOT EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
				)
				OR EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id
					AND t.is_filter = true
					AND tt.tag_id IN ?
				)`, userTagIDs)
	}
	if err := q.Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// IsAccessibleTo reports whether a single connector row is visible to
// the caller using the same rule as ListAccessibleTo. Used by
// tools/call to re-check authorization before dispatch (the tools/list
// snapshot the client cached may be stale).
func (r *Repo) IsAccessibleTo(ctx context.Context, connectorID string, userTagIDs []string) (bool, error) {
	var n int64
	q := r.db.WithContext(ctx).
		Model(&entity.Connector{}).
		Where("id = ? AND disabled = ?", connectorID, false).
		Where(`
			NOT EXISTS (
				SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
				WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
			)`)
	if len(userTagIDs) > 0 {
		q = r.db.WithContext(ctx).
			Model(&entity.Connector{}).
			Where("id = ? AND disabled = ?", connectorID, false).
			Where(`
				NOT EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id AND t.is_filter = true
				)
				OR EXISTS (
					SELECT 1 FROM tool_tags tt JOIN tags t ON t.id = tt.tag_id
					WHERE tt.tool_path = '/connectors/' || connectors.id
					AND t.is_filter = true
					AND tt.tag_id IN ?
				)`, userTagIDs)
	}
	if err := q.Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// CountByKey returns how many Connector rows exist for a code key.
// Used by Bootstrap to decide whether to auto-create the initial row.
func (r *Repo) CountByKey(ctx context.Context, key string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&entity.Connector{}).Where(`"key" = ?`, key).Count(&n).Error
	return n, err
}

// Update writes label / disabled changes to an existing row. Per-
// field config values live in the configs table now and are written
// by Service.Update via configs.Service.SetOwned, not here. Identity
// fields (ID, Key, CreatedBy, CreatedAt) are untouched.
func (r *Repo) Update(ctx context.Context, c *entity.Connector) error {
	return r.db.WithContext(ctx).Model(&entity.Connector{}).Where("id = ?", c.ID).
		Updates(map[string]any{
			"label":      c.Label,
			"disabled":   c.Disabled,
			"updated_at": time.Now(),
		}).Error
}

// SetDisabled flips the Disabled flag without touching anything else.
// Used by the admin manager toggle.
func (r *Repo) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return r.db.WithContext(ctx).Model(&entity.Connector{}).Where("id = ?", id).
		Updates(map[string]any{
			"disabled":   disabled,
			"updated_at": time.Now(),
		}).Error
}

// SetRateLimit updates the per-minute call cap for a connector instance.
// Pass 0 to remove the limit.
func (r *Repo) SetRateLimit(ctx context.Context, id string, rpm int) error {
	return r.db.WithContext(ctx).Model(&entity.Connector{}).Where("id = ?", id).
		Updates(map[string]any{
			"rate_limit_rpm": rpm,
			"updated_at":     time.Now(),
		}).Error
}

// Delete hard-deletes a connector row plus its operation toggles. Run
// history is intentionally preserved — deleting a connector should not
// retroactively erase the audit trail. The retention job purges old
// runs on its own cadence.
func (r *Repo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("connector_id = ?", id).Delete(&entity.ConnectorOperation{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&entity.Connector{}).Error
	})
}

// ── ConnectorOperation toggles ──────────────────────────────────────

// ListOperations returns the toggle rows for a connector. Missing
// rows mean "use the per-op default" (Destructive=false → on,
// Destructive=true → off); callers fold the defaults in themselves.
func (r *Repo) ListOperations(ctx context.Context, connectorID string) ([]entity.ConnectorOperation, error) {
	var out []entity.ConnectorOperation
	err := r.db.WithContext(ctx).Where("connector_id = ?", connectorID).Find(&out).Error
	return out, err
}

// SetOperation upserts the toggle for a single (connector, op) pair.
// Insert when no row exists, update when it does.
func (r *Repo) SetOperation(ctx context.Context, connectorID, opKey string, enabled bool) error {
	row := entity.ConnectorOperation{
		ConnectorID:  connectorID,
		OperationKey: opKey,
		Enabled:      enabled,
		UpdatedAt:    time.Now(),
	}
	return r.db.WithContext(ctx).Save(&row).Error
}

// SetSystemDisabled marks an operation as disabled by the health-check
// system with a human-readable reason. The manual Enabled flag is left
// untouched — effective availability becomes `Enabled AND NOT SystemDisabled`.
// Upserts a new row when none exists yet.
func (r *Repo) SetSystemDisabled(ctx context.Context, connectorID, opKey, reason string) error {
	return r.db.WithContext(ctx).
		Where(entity.ConnectorOperation{ConnectorID: connectorID, OperationKey: opKey}).
		Assign(map[string]any{
			"system_disabled":         true,
			"system_disabled_reason":  reason,
			"updated_at":              time.Now(),
		}).
		FirstOrCreate(&entity.ConnectorOperation{
			ConnectorID:          connectorID,
			OperationKey:         opKey,
			Enabled:              true,
			SystemDisabled:       true,
			SystemDisabledReason: reason,
		}).Error
}

// ClearSystemDisabled removes the health-check lock from an operation
// (e.g. after a successful re-check). Leaves Enabled untouched, so an
// op that was manually disabled stays disabled; one the system locked
// will become available for manual toggling again.
func (r *Repo) ClearSystemDisabled(ctx context.Context, connectorID, opKey string) error {
	return r.db.WithContext(ctx).Model(&entity.ConnectorOperation{}).
		Where("connector_id = ? AND operation_key = ?", connectorID, opKey).
		Updates(map[string]any{
			"system_disabled":        false,
			"system_disabled_reason": "",
			"updated_at":             time.Now(),
		}).Error
}

// SetOperationAdminOnly upserts the admin_only flag for a (connector, op)
// pair without touching the Enabled state. Inserts a new row with
// Enabled=true (safe default) when no row exists yet.
func (r *Repo) SetOperationAdminOnly(ctx context.Context, connectorID, opKey string, adminOnly bool) error {
	return r.db.WithContext(ctx).
		Where(entity.ConnectorOperation{ConnectorID: connectorID, OperationKey: opKey}).
		Assign(map[string]any{"admin_only": adminOnly, "updated_at": time.Now()}).
		FirstOrCreate(&entity.ConnectorOperation{
			ConnectorID:  connectorID,
			OperationKey: opKey,
			Enabled:      true,
		}).Error
}

// IsOperationAdminOnly returns true when the stored row has AdminOnly=true.
// Returns false (not restricted) when no row exists yet.
func (r *Repo) IsOperationAdminOnly(ctx context.Context, connectorID, opKey string) (bool, error) {
	var row entity.ConnectorOperation
	err := r.db.WithContext(ctx).
		Where("connector_id = ? AND operation_key = ?", connectorID, opKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return row.AdminOnly, err
}

// ── ConnectorRun (history) ──────────────────────────────────────────

// CreateRun inserts a row at the start of an execution. Status should
// be ConnectorRunStatusRunning; FinishRun finalizes it.
func (r *Repo) CreateRun(ctx context.Context, run *entity.ConnectorRun) error {
	run.RequestJSON = strutil.LimitText(run.RequestJSON, maxRunRequestLen)
	return r.db.WithContext(ctx).Create(run).Error
}

// FinishRun stamps terminal status, the response body, error message,
// and the timing/HTTP-status metrics. EndedAt is set to now.
func (r *Repo) FinishRun(ctx context.Context, runID string, status entity.ConnectorRunStatus, response, errMsg string, latencyMs, httpStatus int) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.ConnectorRun{}).Where("id = ?", runID).
		Updates(map[string]any{
			"status":        status,
			"response_json": strutil.LimitText(response, maxRunResponseLen),
			"error_msg":     strutil.LimitText(errMsg, maxRunErrorLen),
			"latency_ms":    latencyMs,
			"http_status":   httpStatus,
			"ended_at":      &now,
		}).Error
}

// GetRun loads a single run, used by the retry handler to replay the
// stored RequestJSON against the current Connector.Configs.
func (r *Repo) GetRun(ctx context.Context, runID string) (*entity.ConnectorRun, error) {
	var run entity.ConnectorRun
	err := r.db.WithContext(ctx).Where("id = ?", runID).First(&run).Error
	return &run, err
}

// ListRunsByConnector returns the most recent runs for one connector,
// newest first. Backed by composite index (connector_id, started_at).
func (r *Repo) ListRunsByConnector(ctx context.Context, connectorID string, limit int) ([]entity.ConnectorRun, error) {
	var out []entity.ConnectorRun
	q := r.db.WithContext(ctx).Where("connector_id = ?", connectorID).Order("started_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&out).Error
	return out, err
}

// RunFilter narrows ListRunsFiltered. Empty fields are ignored.
type RunFilter struct {
	OperationKey string
	Source       string
	Status       string
	UserID       string
}

// AuditFilter narrows cross-connector audit queries. All fields are
// optional — omit to get all runs across every connector instance.
type AuditFilter struct {
	ConnectorID  string
	OperationKey string
	Source       string
	Status       string
	UserID       string
	From         *time.Time // inclusive lower bound on StartedAt
	To           *time.Time // inclusive upper bound on StartedAt
}

// ListRunsAudit returns connector runs across all (or a filtered subset
// of) connector instances, newest first. Designed for the cross-connector
// audit log page and the /api/runs JSON endpoint. Admin-only.
func (r *Repo) ListRunsAudit(ctx context.Context, f AuditFilter, limit, offset int) ([]entity.ConnectorRun, error) {
	var out []entity.ConnectorRun
	q := r.auditFilterQuery(ctx, f).Order("started_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	return out, q.Find(&out).Error
}

// CountRunsAudit returns the total matching the same filter as
// ListRunsAudit. Used to drive pagination.
func (r *Repo) CountRunsAudit(ctx context.Context, f AuditFilter) (int64, error) {
	var n int64
	return n, r.auditFilterQuery(ctx, f).Model(&entity.ConnectorRun{}).Count(&n).Error
}

// RunSummary holds aggregated stats for a connector run query window.
type RunSummary struct {
	Total        int64   `json:"total"`
	Succeeded    int64   `json:"succeeded"`
	Errored      int64   `json:"errored"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// SummariseRuns returns aggregate stats for the given audit filter window.
func (r *Repo) SummariseRuns(ctx context.Context, f AuditFilter) (RunSummary, error) {
	type row struct {
		Status    string
		Count     int64
		AvgLatMs  float64
	}
	var rows []row
	err := r.auditFilterQuery(ctx, f).
		Model(&entity.ConnectorRun{}).
		Select("status, COUNT(*) as count, AVG(latency_ms) as avg_lat_ms").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return RunSummary{}, err
	}
	var s RunSummary
	var totalLatSum float64
	for _, rr := range rows {
		s.Total += rr.Count
		switch entity.ConnectorRunStatus(rr.Status) {
		case entity.ConnectorRunStatusSuccess:
			s.Succeeded = rr.Count
		case entity.ConnectorRunStatusError:
			s.Errored = rr.Count
		}
		totalLatSum += rr.AvgLatMs * float64(rr.Count)
	}
	if s.Total > 0 {
		s.AvgLatencyMs = totalLatSum / float64(s.Total)
	}
	return s, nil
}

func (r *Repo) auditFilterQuery(ctx context.Context, f AuditFilter) *gorm.DB {
	q := r.db.WithContext(ctx)
	if f.ConnectorID != "" {
		q = q.Where("connector_id = ?", f.ConnectorID)
	}
	if f.OperationKey != "" {
		q = q.Where("operation_key = ?", f.OperationKey)
	}
	if f.Source != "" {
		q = q.Where("source = ?", f.Source)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.UserID != "" {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.From != nil {
		q = q.Where("started_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("started_at <= ?", *f.To)
	}
	return q
}

// ListRunsFiltered returns runs for one connector filtered by op/source/
// status/user. The history page uses this to power its filter bar.
// Supports limit+offset for page-based paging.
func (r *Repo) ListRunsFiltered(ctx context.Context, connectorID string, f RunFilter, limit, offset int) ([]entity.ConnectorRun, error) {
	var out []entity.ConnectorRun
	q := r.runFilterQuery(ctx, connectorID, f).Order("started_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	err := q.Find(&out).Error
	return out, err
}

// CountRunsFiltered returns the total row count matching the same filter
// as ListRunsFiltered. Used to drive pagination controls.
func (r *Repo) CountRunsFiltered(ctx context.Context, connectorID string, f RunFilter) (int64, error) {
	var n int64
	err := r.runFilterQuery(ctx, connectorID, f).Model(&entity.ConnectorRun{}).Count(&n).Error
	return n, err
}

func (r *Repo) runFilterQuery(ctx context.Context, connectorID string, f RunFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Where("connector_id = ?", connectorID)
	if f.OperationKey != "" {
		q = q.Where("operation_key = ?", f.OperationKey)
	}
	if f.Source != "" {
		q = q.Where("source = ?", f.Source)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.UserID != "" {
		q = q.Where("user_id = ?", f.UserID)
	}
	return q
}

// PurgeRunsOlderThan deletes ConnectorRun rows whose StartedAt is
// before the cutoff. Returns how many rows were removed so the
// retention job can log progress.
//
// Backed by the standalone started_at index — a single range delete,
// no composite index needed.
func (r *Repo) PurgeRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("started_at < ?", cutoff).Delete(&entity.ConnectorRun{})
	return res.RowsAffected, res.Error
}

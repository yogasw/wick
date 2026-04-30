package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// Service is the runtime façade between code-side connector definitions
// (kept in-memory by the registry) and DB-side connector rows. The
// admin UI, the panel-test handler, and the future MCP dispatcher all
// go through it.
//
// Unlike the jobs Service, Bootstrap does NOT seed DB rows from code:
// connector instances are admin-created on demand. Bootstrap only wires
// the dispatch table so Execute can find the right ExecuteFunc when a
// row references its definition by Key.
type Service struct {
	repo       *Repo
	httpClient *http.Client

	mu      sync.RWMutex
	modules map[string]connector.Module // key -> registered module
}

// NewService wires a Service around an existing Repo and the default
// HTTP client. The HTTP client is the one Ctx.HTTP exposes to every
// ExecuteFunc — replace with a custom client at construction time when
// tests need a transport hook.
func NewService(r *Repo) *Service {
	return &Service{
		repo:       r,
		httpClient: connector.NewHTTPClient(),
		modules:    make(map[string]connector.Module),
	}
}

// NewServiceFromDB is a convenience constructor for the web server and
// worker — both already hold a *gorm.DB.
func NewServiceFromDB(db *gorm.DB) *Service {
	return NewService(NewRepo(db))
}

// Bootstrap registers code-side connector definitions for dispatch and
// ensures every registered Key has at least one row in the database.
// Call once at startup with the All() slice from the registry.
//
// For each module: if zero rows currently exist for the Key, an empty
// row is auto-created with Label = Meta.Name and Configs = "{}". This
// makes a fresh deploy ready to use — the admin opens the UI and only
// has to fill in the credentials. Existing rows (and their cred edits)
// are NEVER touched, so an admin who has already filled cred won't see
// the row reset on restart.
//
// Duplicate Keys are an error — one Key may not back two definitions.
// DB rows whose Key has no registered module are tolerated: they show
// up as "deactivated" in the admin UI, and Execute on them returns an
// error.
func (s *Service) Bootstrap(ctx context.Context, mods []connector.Module) error {
	s.mu.Lock()
	for _, m := range mods {
		if _, dup := s.modules[m.Meta.Key]; dup {
			s.mu.Unlock()
			return fmt.Errorf("bootstrap connector: duplicate key %q", m.Meta.Key)
		}
		s.modules[m.Meta.Key] = m
	}
	s.mu.Unlock()

	for _, m := range mods {
		n, err := s.repo.CountByKey(ctx, m.Meta.Key)
		if err != nil {
			return fmt.Errorf("count rows for %q: %w", m.Meta.Key, err)
		}
		if n > 0 {
			continue
		}
		row := &entity.Connector{
			Key:     m.Meta.Key,
			Label:   m.Meta.Name,
			Configs: "{}",
		}
		if err := s.repo.Create(ctx, row); err != nil {
			return fmt.Errorf("seed initial row for %q: %w", m.Meta.Key, err)
		}
	}
	return nil
}

// Modules returns the registered definitions, useful for the
// "+ New instance" picker in the admin UI.
func (s *Service) Modules() []connector.Module {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]connector.Module, 0, len(s.modules))
	for _, m := range s.modules {
		out = append(out, m)
	}
	return out
}

// Module looks up a definition by Key. The second return is false when
// no module is registered for the key (typical when a DB row outlives
// its code definition after a deploy that drops the connector).
func (s *Service) Module(key string) (connector.Module, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.modules[key]
	return m, ok
}

// ── Connector CRUD ───────────────────────────────────────────────────

// Create inserts a new Connector row for the given code-defined Key.
// configs is the credential map keyed by the Creds-struct field names
// the connector declared; it is JSON-encoded into the row.
//
// Returns the freshly stored row (with ID stamped).
func (s *Service) Create(ctx context.Context, key, label string, configs map[string]string, createdBy string) (*entity.Connector, error) {
	if _, ok := s.Module(key); !ok {
		return nil, fmt.Errorf("unknown connector key %q", key)
	}
	encoded, err := json.Marshal(configs)
	if err != nil {
		return nil, fmt.Errorf("encode configs: %w", err)
	}
	c := &entity.Connector{
		Key:       key,
		Label:     label,
		Configs:   string(encoded),
		CreatedBy: createdBy,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Get is a thin pass-through to the repo.
func (s *Service) Get(ctx context.Context, id string) (*entity.Connector, error) {
	return s.repo.Get(ctx, id)
}

// List returns every connector row newest first, regardless of tag
// filter or visibility. Used by the admin manager and the retention
// dashboard. UI-layer code is responsible for tag-filtering for
// non-admin views.
func (s *Service) List(ctx context.Context) ([]entity.Connector, error) {
	return s.repo.List(ctx)
}

// Update writes label / configs / disabled changes. Identity fields
// (Key, ParentID, CreatedBy, CreatedAt) are immutable and untouched.
func (s *Service) Update(ctx context.Context, id, label string, configs map[string]string, disabled bool) error {
	encoded, err := json.Marshal(configs)
	if err != nil {
		return fmt.Errorf("encode configs: %w", err)
	}
	return s.repo.Update(ctx, &entity.Connector{
		ID:       id,
		Label:    label,
		Configs:  string(encoded),
		Disabled: disabled,
	})
}

// SetDisabled toggles the row-level off-switch.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return s.repo.SetDisabled(ctx, id, disabled)
}

// Delete hard-deletes the connector row plus its operation toggles.
// Run history is intentionally preserved for audit.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// Duplicate copies an existing connector row with credentials reset.
// The new row carries the same Key (so it dispatches to the same code
// definition) and a "(copy)"-suffixed Label; Configs is "{}" so the
// admin must re-fill secrets. Tag inheritance is intentionally NOT
// performed — the caller is responsible for assigning the creator's
// own tags via the existing ToolTag system.
func (s *Service) Duplicate(ctx context.Context, sourceID, createdBy string) (*entity.Connector, error) {
	src, err := s.repo.Get(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	c := &entity.Connector{
		Key:       src.Key,
		Label:     src.Label + " (copy)",
		Configs:   "{}",
		CreatedBy: createdBy,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ── Operation toggles ───────────────────────────────────────────────

// SetOperationEnabled flips the per-(connector, op) toggle.
func (s *Service) SetOperationEnabled(ctx context.Context, connectorID, opKey string, enabled bool) error {
	return s.repo.SetOperation(ctx, connectorID, opKey, enabled)
}

// OperationStates returns the resolved enable state for every op the
// connector's definition declares: stored toggle when the row exists,
// otherwise the per-op default (off for Destructive, on for the rest).
//
// Map key is OperationKey. Returned map is empty when the connector's
// Key has no registered module.
func (s *Service) OperationStates(ctx context.Context, connectorID, key string) (map[string]bool, error) {
	mod, ok := s.Module(key)
	if !ok {
		return map[string]bool{}, nil
	}
	rows, err := s.repo.ListOperations(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]bool, len(rows))
	for _, r := range rows {
		stored[r.OperationKey] = r.Enabled
	}
	out := make(map[string]bool, len(mod.Operations))
	for _, op := range mod.Operations {
		if v, ok := stored[op.Key]; ok {
			out[op.Key] = v
			continue
		}
		out[op.Key] = !op.Destructive
	}
	return out, nil
}

// ── Execution ───────────────────────────────────────────────────────

// ExecuteParams bundles the ambient context for one execution. Keeping
// it as a struct keeps the call site readable when more fields are
// added (e.g. retry parent, MCP session id).
type ExecuteParams struct {
	ConnectorID string
	OperationKey string
	Input       map[string]string
	Source      entity.ConnectorRunSource
	UserID      string
	IPAddress   string
	UserAgent   string
	// ParentRunID is set when this call replays an earlier run.
	// Intended for use with Source == ConnectorRunSourceRetry.
	ParentRunID *string
}

// ExecuteResult carries the outcome of one Execute call. Returned
// alongside an error so the caller (panel-test or MCP) can render the
// run details even when the operation itself failed.
type ExecuteResult struct {
	RunID        string
	Status       entity.ConnectorRunStatus
	ResponseJSON string
	ErrorMessage string
	LatencyMs    int
}

// Execute runs one operation against one connector row, logging a
// ConnectorRun with the request, response, latency, and IP/UA.
//
// The same code path serves panel-test, MCP tools/call, and retry —
// the caller distinguishes via params.Source so the run row is tagged
// correctly. On success the returned ResponseJSON is the marshaled
// ExecuteFunc return value; on error ErrorMessage carries the message
// (the run row also stores it).
//
// Validation order:
//  1. connector row exists and is not Disabled
//  2. connector's Key has a registered module (post-Bootstrap)
//  3. requested OperationKey exists on the module
//  4. operation is currently Enabled (per OperationStates)
func (s *Service) Execute(ctx context.Context, p ExecuteParams) (*ExecuteResult, error) {
	c, err := s.repo.Get(ctx, p.ConnectorID)
	if err != nil {
		return nil, fmt.Errorf("connector not found: %w", err)
	}
	if c.Disabled {
		return nil, fmt.Errorf("connector %q is disabled", c.ID)
	}

	mod, ok := s.Module(c.Key)
	if !ok {
		return nil, fmt.Errorf("no implementation registered for connector key %q", c.Key)
	}

	var op *connector.Operation
	for i := range mod.Operations {
		if mod.Operations[i].Key == p.OperationKey {
			op = &mod.Operations[i]
			break
		}
	}
	if op == nil {
		return nil, fmt.Errorf("unknown operation %q on connector %q", p.OperationKey, c.Key)
	}

	states, err := s.OperationStates(ctx, c.ID, c.Key)
	if err != nil {
		return nil, fmt.Errorf("load op states: %w", err)
	}
	if enabled, ok := states[p.OperationKey]; ok && !enabled {
		return nil, fmt.Errorf("operation %q is disabled on this connector", p.OperationKey)
	}

	// Decode the stored credential map.
	var configs map[string]string
	if c.Configs != "" {
		if err := json.Unmarshal([]byte(c.Configs), &configs); err != nil {
			return nil, fmt.Errorf("decode configs: %w", err)
		}
	}

	reqBytes, _ := json.Marshal(p.Input)

	startedAt := time.Now()
	run := &entity.ConnectorRun{
		ConnectorID:  c.ID,
		OperationKey: op.Key,
		UserID:       p.UserID,
		Source:       p.Source,
		RequestJSON:  string(reqBytes),
		Status:       entity.ConnectorRunStatusRunning,
		IPAddress:    p.IPAddress,
		UserAgent:    p.UserAgent,
		ParentRunID:  p.ParentRunID,
		StartedAt:    startedAt,
	}
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}

	cctx := connector.NewCtx(ctx, c.ID, configs, p.Input, s.httpClient)
	value, execErr := op.Execute(cctx)
	latencyMs := int(time.Since(startedAt).Milliseconds())

	res := &ExecuteResult{
		RunID:     run.ID,
		LatencyMs: latencyMs,
	}
	if execErr != nil {
		res.Status = entity.ConnectorRunStatusError
		res.ErrorMessage = execErr.Error()
	} else {
		bytes, mErr := json.Marshal(value)
		if mErr != nil {
			res.Status = entity.ConnectorRunStatusError
			res.ErrorMessage = "marshal response: " + mErr.Error()
		} else {
			res.Status = entity.ConnectorRunStatusSuccess
			res.ResponseJSON = string(bytes)
		}
	}

	if err := s.repo.FinishRun(ctx, run.ID, res.Status, res.ResponseJSON, res.ErrorMessage, latencyMs, 0); err != nil {
		return res, fmt.Errorf("finish run: %w", err)
	}
	return res, execErr
}

// Retry replays an earlier run against the current Connector.Configs.
// The new run's ParentRunID points to the original; cred edits made
// since the original are honored.
func (s *Service) Retry(ctx context.Context, originalRunID, userID, ipAddr, userAgent string) (*ExecuteResult, error) {
	orig, err := s.repo.GetRun(ctx, originalRunID)
	if err != nil {
		return nil, fmt.Errorf("original run not found: %w", err)
	}
	var input map[string]string
	if orig.RequestJSON != "" {
		if err := json.Unmarshal([]byte(orig.RequestJSON), &input); err != nil {
			return nil, fmt.Errorf("decode original request: %w", err)
		}
	}
	parent := orig.ID
	return s.Execute(ctx, ExecuteParams{
		ConnectorID:  orig.ConnectorID,
		OperationKey: orig.OperationKey,
		Input:        input,
		Source:       entity.ConnectorRunSourceRetry,
		UserID:       userID,
		IPAddress:    ipAddr,
		UserAgent:    userAgent,
		ParentRunID:  &parent,
	})
}

// ── Retention ───────────────────────────────────────────────────────

// PurgeOldRuns deletes ConnectorRun rows older than retentionDays.
// Returns the number of rows removed. Called by the cleanup job on a
// daily cadence (set up in a later phase).
func (s *Service) PurgeOldRuns(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, fmt.Errorf("retentionDays must be positive, got %d", retentionDays)
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	return s.repo.PurgeRunsOlderThan(ctx, cutoff)
}

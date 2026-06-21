package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/metrics"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

// ErrFixedInstanceViolation is returned by Service.Create / Duplicate
// when trying to add a second instance for a connector whose Meta.Fixed
// is true. Wick auto-seeds exactly one row for a Fixed connector at
// Bootstrap; admins cannot add or duplicate beyond that.
var ErrFixedInstanceViolation = errors.New("connector is fixed: only one instance allowed")

// ownerForConnector returns the configs.Service owner string used to
// scope a connector instance's per-field config rows. Each instance
// (even multiple instances of the same Key) gets its own slot.
func ownerForConnector(connectorID string) string {
	return "connector:" + connectorID
}

// Status returns "ready" when every Required field on the connector
// has a non-empty value, "needs_setup" otherwise. Reads from the
// configs.Service cache (RWMutex, no DB hit per call).
func (s *Service) Status(c entity.Connector) string {
	if len(s.cfgs.Missing(ownerForConnector(c.ID))) == 0 {
		return "ready"
	}
	return "needs_setup"
}

// LoadConfigs returns the credential map for a connector row, keyed
// by the Creds-struct field names. Values are pulled from the configs
// table (owner = "connector:{id}").
func (s *Service) LoadConfigs(c entity.Connector) map[string]string {
	rows := s.cfgs.ListOwned(ownerForConnector(c.ID))
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out
}

// RowConfigs returns the connector module's declared config schema
// overlaid with the row's stored values. Used by the admin UI so the
// form always reflects the latest declaration even when EnsureOwned
// has not yet seeded a brand-new field. Returns nil when the row's
// Key has no registered module (e.g. after a deploy that dropped the
// connector — admins should delete the orphan row).
func (s *Service) RowConfigs(c entity.Connector) []entity.Config {
	mod, ok := s.Module(c.Key)
	if !ok {
		return nil
	}
	vals := s.LoadConfigs(c)
	out := make([]entity.Config, len(mod.Configs))
	for i, spec := range mod.Configs {
		spec.Owner = ownerForConnector(c.ID)
		spec.Value = vals[spec.Key]
		out[i] = spec
	}
	return out
}

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
	rl         *rateLimiter

	// catalogRefresh is the lazy re-sync hook wick_get fires before
	// reading a module — see SetCatalogRefresh. Set once at boot.
	catalogRefresh func(ctx context.Context, key, instanceID string)

	mu      sync.RWMutex
	modules map[string]connector.Module // key -> registered module

	// cfgs delegates per-instance configuration storage to the central
	// configs table (owner = "connector:{id}"). nil means storage falls
	// back to the legacy entity.Connector.Configs JSON column — set
	// during the dual-write migration window so reads never miss.
	cfgs *configs.Service

	// enc is the encrypted-fields cipher. nil when wick boots without
	// a configs.Service (e.g. legacy tests) or with WICK_ENC_DISABLE
	// set to true. When non-nil, Execute auto-decrypts wick_enc_ tokens
	// found in the input/credential maps before calling the connector,
	// and auto-encrypts sensitive plaintext appearing in the response.
	enc *enc.Service

	// metrics records connector run telemetry. Defaults to Noop when
	// not wired — safe to call unconditionally.
	metrics metrics.Recorder

	// tags is the central tag service used to seed Meta.DefaultTags onto
	// freshly created connector rows. nil disables tag seeding (tests
	// that don't care about the home-page grouping can leave it unset).
	tags tagSeeder
}

// tagSeeder is the slice of the tags service Bootstrap needs. Keeping
// it as an interface avoids an import cycle and makes the test double
// trivial.
type tagSeeder interface {
	EnsureToolDefaultTags(ctx context.Context, toolPath string, defaults []tool.DefaultTag) error
}

// SetTags wires the tags service used to attach Meta.DefaultTags onto
// every connector row at boot. Call before Bootstrap. nil disables tag
// seeding.
func (s *Service) SetTags(t tagSeeder) {
	s.tags = t
}

// SetEnc wires the encrypted-fields cipher in after construction. Call
// once at boot from server.go, before Execute is reachable. Passing nil
// is allowed — Execute then runs without any masking.
func (s *Service) SetEnc(e *enc.Service) {
	s.enc = e
}

// Enc exposes the encrypted-fields cipher for callers that must
// tokenize values outside an Execute round-trip (wick_session_workspace
// encrypts user-typed secrets before persisting instance config). May be
// nil — callers must treat that as "encryption unavailable".
func (s *Service) Enc() *enc.Service {
	return s.enc
}

// SetConfigs wires the central configs.Service used to store per-
// instance config rows under owner = "connector:{id}". When nil,
// reads fall back to the legacy JSON blob on entity.Connector. Call
// at boot before Bootstrap so seeded rows get their config rows
// reconciled into the configs table.
func (s *Service) SetConfigs(c *configs.Service) {
	s.cfgs = c
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
		rl:         newRateLimiter(),
		metrics:    metrics.Noop{},
	}
}

// SetMetrics wires a telemetry recorder into the service. Call once at
// boot before the server starts accepting requests. Passing nil is safe
// — the Noop recorder is used instead.
func (s *Service) SetMetrics(rec metrics.Recorder) {
	if rec == nil {
		s.metrics = metrics.Noop{}
		return
	}
	s.metrics = rec
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
		if err := s.seedModuleRows(ctx, m); err != nil {
			return err
		}
	}
	s.backfillSessionConfigDefault(ctx, mods)
	return nil
}

// sessionConfigBackfillKey marks that the one-time per-instance
// AllowSessionConfig backfill has run, so it never re-enables a row an
// admin later turns off.
const sessionConfigBackfillKey = "connector_session_config_backfilled"

// backfillSessionConfigDefault flips existing rows of session-config
// capable modules ON once. New rows already inherit the module flag at
// seed/create; this covers rows created before the per-instance toggle
// existed (which sat at the gorm default of false). Runs a single time,
// guarded by a marker — a later admin OFF then sticks.
func (s *Service) backfillSessionConfigDefault(ctx context.Context, mods []connector.Module) {
	if s.cfgs == nil || s.cfgs.Get(sessionConfigBackfillKey) == "done" {
		return
	}
	for _, m := range mods {
		if !m.AllowSessionConfig {
			continue
		}
		rows, err := s.repo.ListByKey(ctx, m.Meta.Key)
		if err != nil {
			continue
		}
		for _, row := range rows {
			if !row.AllowSessionConfig {
				_ = s.repo.SetSessionConfigAllowed(ctx, row.ID, true)
			}
		}
	}
	_ = s.cfgs.Set(ctx, sessionConfigBackfillKey, "done")
}

// seedModuleRows is the per-module half of Bootstrap: auto-create the
// single row of a Fixed module when the Key has none (Fixed hides
// "+ New row", so the row must exist up front), then reconcile every
// row of the Key with the module's declared config schema. Non-fixed
// connectors are never auto-seeded — instances are created explicitly
// via "+ New row" and deleting the last one keeps the connector empty
// across restarts. Existing values are preserved; metadata
// (description, required, secret, ...) is refreshed so renames in code
// propagate without a migration.
func (s *Service) seedModuleRows(ctx context.Context, m connector.Module) error {
	n, err := s.repo.CountByKey(ctx, m.Meta.Key)
	if err != nil {
		return fmt.Errorf("count rows for %q: %w", m.Meta.Key, err)
	}
	if n == 0 && m.Meta.Fixed {
		row := &entity.Connector{
			Key:                   m.Meta.Key,
			Label:                 m.Meta.Name,
			AllowSessionConfig:    m.AllowSessionConfig,
			EnableSSO:             m.DefaultAccess.EnableSSO,
			AllowOthersConnectSSO: m.DefaultAccess.AllowOthersConnectSSO,
			MultiAccount:          m.DefaultAccess.MultiAccount,
			AllowOthersConfigure:  m.DefaultAccess.AllowOthersConfigure,
		}
		if err := s.repo.Create(ctx, row); err != nil {
			return fmt.Errorf("seed initial row for %q: %w", m.Meta.Key, err)
		}
	}
	rows, err := s.repo.ListByKey(ctx, m.Meta.Key)
	if err != nil {
		return fmt.Errorf("list rows for %q: %w", m.Meta.Key, err)
	}
	for _, row := range rows {
		configs := m.Configs
		// OAuth connectors get a framework-managed connected_user field
		// injected automatically — no need for each connector to declare it.
		if m.OAuth != nil {
			configs = append(configs, entity.Config{
				Key:         "connected_user",
				Description: "Display name of the connected " + m.OAuth.DisplayName + " account. Set automatically after OAuth connect.",
			})
		}
		if err := s.cfgs.EnsureOwned(ctx, ownerForConnector(row.ID), configs...); err != nil {
			return fmt.Errorf("ensure configs for %q: %w", row.ID, err)
		}
		if s.tags != nil && len(m.Meta.DefaultTags) > 0 {
			path := "/connectors/" + row.ID
			if err := s.tags.EnsureToolDefaultTags(ctx, path, m.Meta.DefaultTags); err != nil {
				return fmt.Errorf("ensure tags for %q: %w", row.ID, err)
			}
		}
	}
	return nil
}

// SetCatalogRefresh installs the hook wick_get fires before reading a
// module's operations — custom MCP connectors lazily re-sync their
// live tool catalog there. Nil (the default) is a no-op; set once at
// boot before serving.
func (s *Service) SetCatalogRefresh(h func(ctx context.Context, key, instanceID string)) {
	s.catalogRefresh = h
}

// CatalogRefresh runs the lazy catalog re-sync hook, when installed.
// instanceID is the row whose account should authenticate the probe
// (oauth scheme) — servers may expose different tools per account.
func (s *Service) CatalogRefresh(ctx context.Context, key, instanceID string) {
	if s.catalogRefresh != nil {
		s.catalogRefresh(ctx, key, instanceID)
	}
}

// UpsertModule installs or replaces one module in the dispatch map at
// runtime and runs the same row seeding / config reconciliation as
// Bootstrap. This is the post-boot registration path for DB-defined
// custom connectors (save + reload) — built-in modules never change
// after boot. The map swap is atomic under the service mutex: in-
// flight Execute calls keep the module they already resolved, new
// calls see the replacement.
func (s *Service) UpsertModule(ctx context.Context, m connector.Module) error {
	s.mu.Lock()
	s.modules[m.Meta.Key] = m
	s.mu.Unlock()
	return s.seedModuleRows(ctx, m)
}

// RemoveModule drops a module definition at runtime (the reverse of
// UpsertModule), used by the plugin hot-reloader when a plugin is
// uninstalled. The connector vanishes from the LLM surface immediately;
// any DB instance rows persist and become inert (Module() returns false)
// until a module with the same key is registered again.
func (s *Service) RemoveModule(key string) {
	s.mu.Lock()
	delete(s.modules, key)
	s.mu.Unlock()
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

// Create inserts a new Connector row for the given code-defined Key
// and seeds its per-field config rows in the configs table (owner =
// "connector:{id}"). configs is the credential map keyed by the
// Creds-struct field names; values are written one row per field.
//
// Returns the freshly stored row (with ID stamped).
func (s *Service) Create(ctx context.Context, key, label string, configs map[string]string, createdBy string) (*entity.Connector, error) {
	mod, ok := s.Module(key)
	if !ok {
		return nil, fmt.Errorf("unknown connector key %q", key)
	}
	if mod.Meta.Fixed {
		n, err := s.repo.CountByKey(ctx, key)
		if err != nil {
			return nil, err
		}
		if n >= 1 {
			return nil, ErrFixedInstanceViolation
		}
	}
	c := &entity.Connector{
		Key:                   key,
		Label:                 label,
		CreatedBy:             createdBy,
		AllowSessionConfig:    mod.AllowSessionConfig,
		EnableSSO:             mod.DefaultAccess.EnableSSO,
		AllowOthersConnectSSO: mod.DefaultAccess.AllowOthersConnectSSO,
		MultiAccount:          mod.DefaultAccess.MultiAccount,
		AllowOthersConfigure:  mod.DefaultAccess.AllowOthersConfigure,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	owner := ownerForConnector(c.ID)
	if err := s.cfgs.EnsureOwned(ctx, owner, mod.Configs...); err != nil {
		return nil, fmt.Errorf("seed config rows: %w", err)
	}
	for k, v := range configs {
		if err := s.cfgs.SetOwned(ctx, owner, k, v); err != nil {
			return nil, fmt.Errorf("set %s: %w", k, err)
		}
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
func (s *Service) ListByKey(ctx context.Context, key string) ([]entity.Connector, error) {
	return s.repo.ListByKey(ctx, key)
}

func (s *Service) List(ctx context.Context) ([]entity.Connector, error) {
	return s.repo.List(ctx)
}

// ListVisibleTo returns the not-disabled connector rows the caller
// can access, applying the same tag-filter rule as Tools (see
// Repo.ListAccessibleTo). Pass isAdmin=true to bypass tag filtering
// — admins see every row whether or not they carry the row's tags.
//
// Use this from MCP tools/list and any user-facing surface that
// enumerates connectors; only the admin manager should call List.
func (s *Service) ListVisibleTo(ctx context.Context, userTagIDs []string, isAdmin bool) ([]entity.Connector, error) {
	if isAdmin {
		rows, err := s.repo.List(ctx)
		if err != nil {
			return nil, err
		}
		// Admin sees disabled rows in the manager but not in MCP/test
		// surfaces — strip them here so callers don't have to.
		filtered := rows[:0]
		for _, r := range rows {
			if !r.Disabled {
				filtered = append(filtered, r)
			}
		}
		return filtered, nil
	}
	return s.repo.ListAccessibleTo(ctx, userTagIDs)
}

// FilterBotSlot removes non-SSO instance rows from a row set when at least
// one SSO-enabled row (EnableSSO=true) exists for the same connector key in
// the visible set.
//
// Connectors without OAuthMeta are left untouched — they have no SSO concept.
// Call this after ListVisibleTo before surfacing rows to the LLM via wick_list.
func (s *Service) FilterBotSlot(rows []entity.Connector) []entity.Connector {
	// Collect which keys have ≥1 EnableSSO row in the visible set.
	ssoKeys := map[string]bool{}
	for _, row := range rows {
		mod, ok := s.Module(row.Key)
		if !ok || mod.OAuth == nil {
			continue
		}
		if row.EnableSSO {
			ssoKeys[row.Key] = true
		}
	}
	if len(ssoKeys) == 0 {
		return rows
	}
	out := rows[:0:len(rows)]
	for _, row := range rows {
		mod, ok := s.Module(row.Key)
		if !ok || mod.OAuth == nil {
			out = append(out, row)
			continue
		}
		// Hide non-SSO rows when SSO rows exist for this key.
		if ssoKeys[row.Key] && !row.EnableSSO {
			continue
		}
		out = append(out, row)
	}
	return out
}

// IsVisibleTo reports whether a single connector row is accessible to
// the caller. Used by tools/call to re-check authorization at dispatch
// time so a stale tools/list snapshot can't be replayed for access.
func (s *Service) IsVisibleTo(ctx context.Context, connectorID string, userTagIDs []string, isAdmin bool) (bool, error) {
	if isAdmin {
		c, err := s.repo.Get(ctx, connectorID)
		if err != nil {
			return false, err
		}
		return !c.Disabled, nil
	}
	return s.repo.IsAccessibleTo(ctx, connectorID, userTagIDs)
}

// ListForManager returns rows the caller can see in the admin manager.
// Unlike ListVisibleTo, disabled rows are included so users can re-
// enable or delete them. Admins see every row.
func (s *Service) ListForManager(ctx context.Context, userTagIDs []string, isAdmin bool) ([]entity.Connector, error) {
	if isAdmin {
		return s.repo.List(ctx)
	}
	return s.repo.ListAccessibleForManager(ctx, userTagIDs)
}

// IsManageableBy reports whether the caller may operate on a row from
// the manager UI. Disabled rows are still manageable — the caller may
// be re-enabling them.
func (s *Service) IsManageableBy(ctx context.Context, connectorID string, userTagIDs []string, isAdmin bool) (bool, error) {
	if isAdmin {
		_, err := s.repo.Get(ctx, connectorID)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	return s.repo.IsAccessibleForManager(ctx, connectorID, userTagIDs)
}

// Update writes label / configs / disabled changes. Identity fields
// (Key, CreatedBy, CreatedAt) are immutable and untouched.
//
// Per-field config values land in the configs table (owner =
// "connector:{id}"); only declared keys are written, unknown keys are
// silently dropped to keep stale form fields from polluting storage.
func (s *Service) Update(ctx context.Context, id, label string, configs map[string]string, disabled bool) error {
	if err := s.repo.Update(ctx, &entity.Connector{
		ID:       id,
		Label:    label,
		Disabled: disabled,
	}); err != nil {
		return err
	}
	owner := ownerForConnector(id)
	declared := make(map[string]bool, len(configs))
	for _, row := range s.cfgs.ListOwned(owner) {
		declared[row.Key] = true
	}
	for k, v := range configs {
		if !declared[k] {
			continue
		}
		if err := s.cfgs.SetOwned(ctx, owner, k, v); err != nil {
			return fmt.Errorf("set %s: %w", k, err)
		}
	}
	return nil
}

// SetDisabled toggles the row-level off-switch.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return s.repo.SetDisabled(ctx, id, disabled)
}

// SetRateLimit updates the calls-per-minute cap for a connector instance.
// Pass 0 to remove the limit.
func (s *Service) SetRateLimit(ctx context.Context, id string, rpm int) error {
	return s.repo.SetRateLimit(ctx, id, rpm)
}

// ── ConnectorAccount ─────────────────────────────────────────────────

// ListAccounts returns all connected OAuth accounts for a connector instance.
func (s *Service) ListAccounts(ctx context.Context, connectorID string) ([]entity.ConnectorAccount, error) {
	return s.repo.ListAccounts(ctx, connectorID)
}

// SaveAccount persists a connected OAuth account. Respects MultiAccount
// from the connector row: false = replace existing, true = add new.
// wickUserID is the wick platform user who initiated the OAuth flow.
// externalUserID is the provider-side user ID from GetUserIdentity.
func (s *Service) SaveAccount(ctx context.Context, connectorID, wickUserID, externalUserID, displayName, accessToken string) error {
	row, err := s.repo.Get(ctx, connectorID)
	if err != nil {
		return err
	}
	acc := &entity.ConnectorAccount{
		ConnectorID:    connectorID,
		WickUserID:     wickUserID,
		ExternalUserID: externalUserID,
		DisplayName:    displayName,
		AccessToken:    accessToken,
	}
	return s.repo.UpsertAccount(ctx, acc, row.MultiAccount)
}

// DeleteAccount removes one connected account by ID.
func (s *Service) DeleteAccount(ctx context.Context, accountID string) error {
	return s.repo.DeleteAccount(ctx, accountID)
}

// SetAccountDisabledOps updates which operations are disabled for an account.
// opKeys is the list of op keys to disable — empty slice clears all.
func (s *Service) SetAccountDisabledOps(ctx context.Context, accountID string, opKeys []string) error {
	b, err := json.Marshal(opKeys)
	if err != nil {
		return err
	}
	return s.repo.SetAccountDisabledOps(ctx, accountID, string(b))
}

// AccountDisabledOps parses the DisabledOps JSON and returns the set of
// disabled op keys for fast lookup.
func AccountDisabledOps(acc *entity.ConnectorAccount) map[string]bool {
	if acc == nil || acc.DisabledOps == "" {
		return nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(acc.DisabledOps), &keys); err != nil {
		return nil
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// GetAccount returns one ConnectorAccount by ID.
func (s *Service) GetAccount(ctx context.Context, accountID string) (*entity.ConnectorAccount, error) {
	return s.repo.GetAccountByID(ctx, accountID)
}

// ClearSystemDisabled removes the health-check lock from one operation,
// allowing admin to override a stale or incorrect health-check result.
func (s *Service) ClearSystemDisabled(ctx context.Context, connectorID, opKey string) error {
	return s.repo.ClearSystemDisabled(ctx, connectorID, opKey)
}

// SetAccessPolicy updates the access policy for a connector instance:
//   - allowConfigure: non-admin users with tag access may edit credentials
//   - allowSSO: non-admin users may connect their OAuth account
//   - enableSSO: OAuth flow is active on this instance
//   - multiAccount: each OAuth connect creates a new row (true) or replaces token (false)
func (s *Service) SetAccessPolicy(ctx context.Context, id string, allowConfigure, allowSSO, enableSSO, multiAccount bool) error {
	return s.repo.SetAccessPolicy(ctx, id, allowConfigure, allowSSO, enableSSO, multiAccount)
}

// SetSessionConfigAllowed flips the per-instance opt-in for per-session
// cloning (Config tab + wick_session_workspace). Only meaningful when the
// module declares AllowSessionConfig.
func (s *Service) SetSessionConfigAllowed(ctx context.Context, id string, allowed bool) error {
	return s.repo.SetSessionConfigAllowed(ctx, id, allowed)
}

// SessionConfigCapable reports whether a connector's MODULE opted into
// per-session config (the capability). The per-instance toggle is only
// shown / honored for capable connectors.
func (s *Service) SessionConfigCapable(key string) bool {
	mod, ok := s.Module(key)
	return ok && mod.AllowSessionConfig
}

// Delete hard-deletes the connector row plus its operation toggles
// and its per-field config rows. Run history is intentionally
// preserved for audit.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	if err := s.cfgs.DeleteOwned(ctx, ownerForConnector(id)); err != nil {
		return fmt.Errorf("delete config rows: %w", err)
	}
	return nil
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
	if mod, ok := s.Module(src.Key); ok && mod.Meta.Fixed {
		return nil, ErrFixedInstanceViolation
	}
	c := &entity.Connector{
		Key:                src.Key,
		Label:              src.Label + " (copy)",
		CreatedBy:          createdBy,
		AllowSessionConfig: src.AllowSessionConfig,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	if mod, ok := s.Module(c.Key); ok {
		if err := s.cfgs.EnsureOwned(ctx, ownerForConnector(c.ID), mod.Configs...); err != nil {
			return nil, fmt.Errorf("seed config rows: %w", err)
		}
	}
	return c, nil
}

// ── Operation toggles ───────────────────────────────────────────────

// SetOperationEnabled flips the per-(connector, op) toggle.
func (s *Service) SetOperationEnabled(ctx context.Context, connectorID, opKey string, enabled bool) error {
	return s.repo.SetOperation(ctx, connectorID, opKey, enabled)
}

// SetOperationAdminOnly sets the admin_only restriction for a (connector, op)
// pair. When true, only admin users may call the operation via MCP.
func (s *Service) SetOperationAdminOnly(ctx context.Context, connectorID, opKey string, adminOnly bool) error {
	return s.repo.SetOperationAdminOnly(ctx, connectorID, opKey, adminOnly)
}

// OperationStates returns the resolved enable state for every op the
// connector's definition declares: stored toggle when the row exists,
// otherwise the per-op default (off for Destructive, on for the rest).
//
// Map key is OperationKey. Returned map is empty when the connector's
// Key has no registered module.
func (s *Service) OperationStates(ctx context.Context, connectorID, key string) (map[string]bool, error) {
	full, err := s.OperationStatesFull(ctx, connectorID, key)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(full))
	for k, st := range full {
		// SystemDisabled is advisory — admin override via Enabled takes precedence.
		out[k] = st.Enabled
	}
	return out, nil
}

// OpState bundles the effective state of one operation on one connector
// row: the admin-controlled Enabled flag, the health-check-controlled
// SystemDisabled flag, and the reason surfaced alongside the lock when
// SystemDisabled is true. Effective availability is
// `Enabled AND NOT SystemDisabled`. AdminOnly restricts the operation to
// admin MCP callers and is toggled separately by admins.
type OpState struct {
	Enabled              bool
	SystemDisabled       bool
	SystemDisabledReason string
	AdminOnly            bool
}

// OperationStatesFull returns the full per-operation state map for a
// connector row, folding stored rows + system-disabled flag + the
// Destructive-default rule. Missing rows mean "use the default" — on
// for non-destructive, off for destructive.
func (s *Service) OperationStatesFull(ctx context.Context, connectorID, key string) (map[string]OpState, error) {
	mod, ok := s.Module(key)
	if !ok {
		return map[string]OpState{}, nil
	}
	rows, err := s.repo.ListOperations(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]entity.ConnectorOperation, len(rows))
	for _, r := range rows {
		stored[r.OperationKey] = r
	}
	modOps := mod.AllOps()
	out := make(map[string]OpState, len(modOps))
	for _, op := range modOps {
		st := OpState{Enabled: true} // destructive ops now default ON; LLM confirms before executing
		if row, ok := stored[op.Key]; ok {
			st.Enabled = row.Enabled
			st.SystemDisabled = row.SystemDisabled
			st.SystemDisabledReason = row.SystemDisabledReason
			st.AdminOnly = row.AdminOnly
		}
		out[op.Key] = st
	}
	return out, nil
}

// HealthCheckResult bundles the outcome of a health-check run for one
// connector row. Per-op transitions describe what changed in the DB so
// the UI can surface a useful summary toast ("3 ops disabled, 1 cleared").
type HealthCheckResult struct {
	Ops          []connector.OpHealth
	NewlyLocked  []string // ops that became system-disabled this run
	NewlyCleared []string // ops whose system-disabled flag was cleared this run
}

// RunHealthCheck invokes the module's HealthCheck hook and reconciles
// the per-operation system_disabled flags against the report. Ops the
// hook reports OK have their lock cleared (if previously set); ops it
// reports failing get system-disabled with the reported reason.
// Returns ErrNoHealthCheck when the module did not register a hook.
//
// The hook itself runs against a Ctx populated from the row's stored
// configs — encrypted credentials are decrypted on read, identical to
// Execute. The caller's permission to act on this row is the manager
// handler's job; this method is unauthenticated by design (it is a
// background-style operation, not user input).
func (s *Service) RunHealthCheck(ctx context.Context, connectorID string) (*HealthCheckResult, error) {
	c, err := s.repo.Get(ctx, connectorID)
	if err != nil {
		return nil, fmt.Errorf("connector not found: %w", err)
	}
	mod, ok := s.Module(c.Key)
	if !ok {
		return nil, fmt.Errorf("no implementation registered for connector key %q", c.Key)
	}
	if mod.HealthCheck == nil {
		return nil, ErrNoHealthCheck
	}

	cfg := s.LoadConfigs(*c)
	// Decrypt any wick_enc_ tokens in the credential map before handing
	// them to the hook — the hook calls upstream APIs the same way an
	// Execute would. Health-check has no per-user context (it runs from
	// the admin row page), so decrypt under the empty-user master key.
	if s.enc != nil && !s.enc.Disabled() {
		decoded, _, derr := unmaskMap(s.enc, cfg, "")
		if derr == nil {
			cfg = decoded
		}
	}

	cctx := connector.NewCtx(ctx, c.ID, cfg, nil, s.httpClient, nil, nil)
	report, err := mod.HealthCheck(cctx)
	if err != nil {
		return nil, fmt.Errorf("health check: %w", err)
	}

	prev, err := s.OperationStatesFull(ctx, c.ID, c.Key)
	if err != nil {
		return nil, fmt.Errorf("load op states: %w", err)
	}

	result := &HealthCheckResult{Ops: report}
	for _, h := range report {
		was := prev[h.Key].SystemDisabled
		if h.OK {
			if was {
				if err := s.repo.ClearSystemDisabled(ctx, c.ID, h.Key); err != nil {
					return nil, fmt.Errorf("clear system_disabled for %q: %w", h.Key, err)
				}
				result.NewlyCleared = append(result.NewlyCleared, h.Key)
			}
			continue
		}
		if err := s.repo.SetSystemDisabled(ctx, c.ID, h.Key, h.Reason); err != nil {
			return nil, fmt.Errorf("set system_disabled for %q: %w", h.Key, err)
		}
		if !was {
			result.NewlyLocked = append(result.NewlyLocked, h.Key)
		}
	}
	return result, nil
}

// ErrNoHealthCheck is returned by RunHealthCheck when the target
// connector's module did not register a HealthCheck hook. The manager
// handler treats this as a 404-ish — admins should not see the button
// on that connector at all.
var ErrNoHealthCheck = errors.New("connector does not implement HealthCheck")

// decryptInstanceConfig resolves a session-workspace instance's config
// map to plaintext: wick_cenc_ master tokens (the secret fields) via the
// system master key, plus any wick_enc_ tokens via the empty-user key.
// Used by the virtual execute/health paths, which have no DB row to read
// credentials from.
func (s *Service) decryptInstanceConfig(config map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(config))
	for k, v := range config {
		out[k] = v
	}
	if s.enc == nil || s.enc.Disabled() {
		return out, nil
	}
	for k, v := range out {
		if !enc.IsMasterToken(v) {
			continue
		}
		plain, err := s.enc.DecryptMaster(v)
		if err != nil {
			return nil, fmt.Errorf("decrypt session config %q: %w", k, err)
		}
		out[k] = plain
	}
	if decoded, _, derr := unmaskMap(s.enc, out, ""); derr == nil {
		out = decoded
	}
	return out, nil
}

// HealthCheckSessionInstance runs a base module's HealthCheck hook
// against a session-workspace instance's own config — the "test setup"
// button for an ephemeral instance. Returns ErrNoHealthCheck when the
// base module registers no hook, so the caller can fall back to running
// a real operation as the probe.
func (s *Service) HealthCheckSessionInstance(ctx context.Context, baseKey, instanceID string, config map[string]string) ([]connector.OpHealth, error) {
	mod, ok := s.Module(baseKey)
	if !ok {
		return nil, fmt.Errorf("no implementation registered for base connector key %q", baseKey)
	}
	if mod.HealthCheck == nil {
		return nil, ErrNoHealthCheck
	}
	cfg, err := s.decryptInstanceConfig(config)
	if err != nil {
		return nil, err
	}
	cctx := connector.NewCtx(ctx, instanceID, cfg, nil, s.httpClient, nil, nil)
	report, err := mod.HealthCheck(cctx)
	if err != nil {
		return nil, fmt.Errorf("health check: %w", err)
	}
	return report, nil
}

// ── Execution ───────────────────────────────────────────────────────

// ExecuteParams bundles the ambient context for one execution. Keeping
// it as a struct keeps the call site readable when more fields are
// added (e.g. retry parent, MCP session id).
type ExecuteParams struct {
	ConnectorID  string
	OperationKey string
	Input        map[string]string
	Source       entity.ConnectorRunSource
	UserID       string
	IPAddress    string
	UserAgent    string
	// IsAdmin indicates whether the caller holds admin role. When false,
	// operations marked AdminOnly in the connector_operations table are
	// blocked before execution starts.
	IsAdmin bool
	// ParentRunID is set when this call replays an earlier run.
	// Intended for use with Source == ConnectorRunSourceRetry.
	ParentRunID *string
	// Progress, when non-nil, receives incremental progress events the
	// connector emits via Ctx.ReportProgress. The MCP SSE handler wires
	// a reporter that frames each event as a notifications/progress
	// JSON-RPC message; the JSON transport leaves this nil so events
	// are dropped harmlessly.
	Progress connector.ProgressReporter
	// AccountID, when non-empty, selects a specific ConnectorAccount
	// whose access token overrides the row's user_token config. Used by
	// the test panel when multiple accounts are connected to one instance.
	AccountID string
	// SessionInstance, when non-nil, runs against a session-workspace
	// instance instead of a DB connector row: there is no row to load,
	// so the base module is cloned and the instance's own Config map is
	// used verbatim. ConnectorID is the synthetic "sw_<uuid>" id (used
	// for run logging + rate limiting only). Per-row checks (op enable/
	// disable rows, admin-only flags, OAuth accounts) are skipped — a
	// session instance has none of that backing state.
	SessionInstance *SessionInstanceTarget
}

// SessionInstanceTarget describes an ephemeral session-workspace
// connector for Execute. BaseKey is the module it clones; Config is its
// full config map (secret values stored as wick_cenc_ master tokens,
// decrypted by the shared master-token pass in Execute).
type SessionInstanceTarget struct {
	BaseKey string
	Label   string
	Config  map[string]string
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
	var c *entity.Connector
	var mod connector.Module
	virtual := p.SessionInstance != nil
	if virtual {
		// Session-workspace instance: clone the base module, no DB row.
		m, ok := s.Module(p.SessionInstance.BaseKey)
		if !ok {
			return nil, fmt.Errorf("no implementation registered for base connector key %q", p.SessionInstance.BaseKey)
		}
		mod = m
		c = &entity.Connector{ID: p.ConnectorID, Key: p.SessionInstance.BaseKey, Label: p.SessionInstance.Label}
		if err := s.rl.Allow(c.ID, c.RateLimitRPM); err != nil {
			return nil, err
		}
	} else {
		row, err := s.repo.Get(ctx, p.ConnectorID)
		if err != nil {
			return nil, fmt.Errorf("connector not found: %w", err)
		}
		c = row
		if c.Disabled {
			return nil, fmt.Errorf("connector %q is disabled", c.ID)
		}
		if err := s.rl.Allow(c.ID, c.RateLimitRPM); err != nil {
			return nil, err
		}
		m, ok := s.Module(c.Key)
		if !ok {
			return nil, fmt.Errorf("no implementation registered for connector key %q", c.Key)
		}
		mod = m
	}

	var op *connector.Operation
	ops := mod.AllOps()
	for i := range ops {
		if ops[i].Key == p.OperationKey {
			op = &ops[i]
			break
		}
	}
	if op == nil {
		return nil, fmt.Errorf("unknown operation %q on connector %q", p.OperationKey, c.Key)
	}

	// Per-row state checks (op enable/disable, admin-only) only apply to
	// real DB rows; a session-workspace instance has no such backing
	// rows — every base op is available.
	if !virtual {
		states, err := s.OperationStatesFull(ctx, c.ID, c.Key)
		if err != nil {
			return nil, fmt.Errorf("load op states: %w", err)
		}
		if st, ok := states[p.OperationKey]; ok {
			if !st.Enabled {
				return nil, fmt.Errorf("operation %q is disabled on this connector", p.OperationKey)
			}
			// SystemDisabled is a warning, not a hard block — admin can override by
			// explicitly enabling the op. If enabled=true, we let it through and log
			// the warning so the run history captures the context.
		}

		if !p.IsAdmin {
			adminOnly, err := s.repo.IsOperationAdminOnly(ctx, c.ID, p.OperationKey)
			if err != nil {
				return nil, fmt.Errorf("check op access: %w", err)
			}
			if adminOnly {
				return nil, fmt.Errorf("operation %q is restricted to admin users", p.OperationKey)
			}
		}
	}

	// Load the credential map. A session-workspace instance carries its
	// own config in the params; a real row reads the configs table (one
	// row per field, owner = "connector:{id}").
	var configs map[string]string
	if virtual {
		configs = make(map[string]string, len(p.SessionInstance.Config))
		for k, v := range p.SessionInstance.Config {
			configs[k] = v
		}
	} else {
		configs = s.LoadConfigs(*c)
	}

	// AccountID override: inject the selected account's token and check
	// per-account disabled ops. Real rows only.
	if !virtual && p.AccountID != "" {
		if acc, err := s.repo.GetAccountByID(ctx, p.AccountID); err == nil && acc.ConnectorID == c.ID {
			disabled := AccountDisabledOps(acc)
			if disabled[p.OperationKey] {
				return nil, fmt.Errorf("operation %q is disabled for account @%s", p.OperationKey, acc.DisplayName)
			}
			configs["user_token"] = acc.AccessToken
			configs["auth_mode"] = "user_token"
		}
	}

	// Session-workspace instance secrets are stored as MASTER (wick_cenc_)
	// tokens — system-only, never per-user decryptable, never shown to
	// the agent. Decrypt them to plaintext here so the connector gets
	// usable creds. The plaintext then flows into the per-call masker
	// below (via collectSensitiveValues on secret-tagged fields), so it
	// is re-masked in the response. Failure is fatal: running against an
	// undecryptable credential authenticates as nothing. (No-op for a
	// normal row whose secrets are wick_enc_ — handled by the pass below.)
	if s.enc != nil && !s.enc.Disabled() {
		for k, v := range configs {
			if !enc.IsMasterToken(v) {
				continue
			}
			plain, err := s.enc.DecryptMaster(v)
			if err != nil {
				return nil, fmt.Errorf("decrypt session config %q: %w", k, err)
			}
			configs[k] = plain
		}
	}

	// Snapshot the request BEFORE we decrypt anything — by design the
	// audit log stores wick_enc_ tokens (not plaintext) in
	// request_json, so a retry can re-decrypt under the retrier's key.
	reqBytes, _ := json.Marshal(p.Input)

	// Auto-decrypt: scan configs + input for wick_enc_ tokens and
	// replace each with its plaintext. The connector only ever sees
	// plaintext via Ctx.Cfg / Ctx.Input. A failed decrypt is fatal
	// here — running the op against a still-encrypted credential
	// would silently authenticate as nothing.
	//
	// The plaintexts produced by these decrypts are seeded into the
	// per-call masker (below) so the post-Execute auto-mask phase
	// re-tokenizes them on the way out, even when the receiving
	// field carries no `secret` tag — the LLM treats wick_enc_ as
	// opaque and may pass it into any field, so the round trip must
	// not depend on tag discipline alone.
	input := p.Input
	masker := newMaskerAdapter(s.enc, p.UserID)
	if s.enc != nil && !s.enc.Disabled() {
		var (
			err    error
			decCfg []string
			decIn  []string
		)
		configs, decCfg, err = unmaskMap(s.enc, configs, p.UserID)
		if err != nil {
			return nil, fmt.Errorf("decrypt configs: %w", err)
		}
		input, decIn, err = unmaskMap(s.enc, input, p.UserID)
		if err != nil {
			return nil, fmt.Errorf("decrypt input: %w", err)
		}
		masker.add(decCfg)
		masker.add(decIn)
		masker.add(collectSensitiveValues(mod, op, configs, input))
	}

	s.metrics.IncActive()
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

	// Pass a nil interface (not a typed-nil *maskerAdapter) when enc is
	// disabled so connector.Ctx's nil-check on c.Mask short-circuits.
	var ctxMasker connector.Masker
	if masker != nil {
		ctxMasker = masker
	}
	cctx := connector.NewCtx(ctx, c.ID, configs, input, s.httpClient, p.Progress, ctxMasker)
	value, execErr := op.Execute(cctx)
	latencyMs := int(time.Since(startedAt).Milliseconds())

	// maskOut replays every sensitive plaintext seen during this call
	// against an outgoing string — used by both the success and error
	// paths so plaintext can never reach the LLM or audit log via an
	// error message either. The masker has been fed by three sources:
	//   1. plaintexts produced by decrypting wick_enc_ tokens in
	//      configs / input — round-trip protection that does not
	//      depend on the receiving field's tag.
	//   2. plaintext values of every Configs/Input field tagged
	//      `secret` (covers credentials sent in plaintext, e.g. by
	//      the admin form).
	//   3. values the connector explicitly passed to c.Mask /
	//      c.MaskIgnoreCase — dynamic sensitive data the connector
	//      pulled from upstream.
	// Dedup happens inside snapshot() so identical values collapse
	// to a single Mask invocation.
	maskOut := func(s string) string {
		if masker == nil || s == "" {
			return s
		}
		return masker.svc.Mask(s, masker.snapshot(), p.UserID)
	}

	res := &ExecuteResult{
		RunID:     run.ID,
		LatencyMs: latencyMs,
	}
	if execErr != nil {
		res.Status = entity.ConnectorRunStatusError
		res.ErrorMessage = maskOut(execErr.Error())
	} else {
		bytes, mErr := json.Marshal(value)
		if mErr != nil {
			res.Status = entity.ConnectorRunStatusError
			res.ErrorMessage = maskOut("marshal response: " + mErr.Error())
		} else {
			res.Status = entity.ConnectorRunStatusSuccess
			res.ResponseJSON = maskOut(string(bytes))
		}
	}

	if err := s.repo.FinishRun(ctx, run.ID, res.Status, res.ResponseJSON, res.ErrorMessage, latencyMs, 0); err != nil {
		return res, fmt.Errorf("finish run: %w", err)
	}
	s.metrics.DecActive()
	s.metrics.RecordRun(c.Key, op.Key, string(res.Status), latencyMs)
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

// GetRun loads a single ConnectorRun by ID. Backs the test page's
// prefill flow when a Retry link is followed from the history view.
func (s *Service) GetRun(ctx context.Context, runID string) (*entity.ConnectorRun, error) {
	return s.repo.GetRun(ctx, runID)
}

// ListRuns returns the most recent ConnectorRun rows for a connector,
// newest first. Used by the admin detail page to render history under
// the test panel.
func (s *Service) ListRuns(ctx context.Context, connectorID string, limit int) ([]entity.ConnectorRun, error) {
	return s.repo.ListRunsByConnector(ctx, connectorID, limit)
}

// ListRunsFiltered returns runs filtered by op / source / status / user.
// Backs the history page; pass zero-value filter for "no filter".
func (s *Service) ListRunsFiltered(ctx context.Context, connectorID string, f RunFilter, limit, offset int) ([]entity.ConnectorRun, error) {
	return s.repo.ListRunsFiltered(ctx, connectorID, f, limit, offset)
}

// CountRunsFiltered returns total runs matching the filter — companion
// of ListRunsFiltered for paging.
func (s *Service) CountRunsFiltered(ctx context.Context, connectorID string, f RunFilter) (int64, error) {
	return s.repo.CountRunsFiltered(ctx, connectorID, f)
}

// ListRunsAudit returns connector runs across all instances with optional
// filters. Intended for the cross-connector admin audit log.
func (s *Service) ListRunsAudit(ctx context.Context, f AuditFilter, limit, offset int) ([]entity.ConnectorRun, error) {
	return s.repo.ListRunsAudit(ctx, f, limit, offset)
}

// CountRunsAudit returns total runs for the audit filter — pagination companion.
func (s *Service) CountRunsAudit(ctx context.Context, f AuditFilter) (int64, error) {
	return s.repo.CountRunsAudit(ctx, f)
}

// SummariseRuns returns aggregate stats (total, success, error, avg latency)
// for the given audit filter window.
func (s *Service) SummariseRuns(ctx context.Context, f AuditFilter) (RunSummary, error) {
	return s.repo.SummariseRuns(ctx, f)
}

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

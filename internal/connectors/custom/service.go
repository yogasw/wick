package custom

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"gorm.io/gorm"
)

// TagEnsurer is the slice of the tags service the custom-connector
// flow needs: idempotent create + link of the per-def access tags on a
// fresh instance row.
type TagEnsurer interface {
	EnsureToolDefaultTags(ctx context.Context, toolPath string, defaults []tool.DefaultTag) error
	CreateOwnerTag(ctx context.Context, connectorID, userID string) error
}

// Deps wires the custom-connector service into the boot sequence. Tags
// and AI are late-bound via setters because they are constructed after
// the connectors bootstrap in server.go.
type Deps struct {
	DB         *gorm.DB
	Connectors *connectors.Service
	Keys       KeyStore      // configs service: SSO seed storage + master-key secret codec
	BaseURL    func() string // wick app URL — `iss` claim for SSO JWTs
	HTTP       *http.Client  // probe/import client; per-execute calls use the Ctx client
}

// Service orchestrates custom connector definitions: replaying them
// into the connector registry at boot, the save/edit/reload lifecycle,
// MCP server registration, and the import mappers. UI handlers in
// internal/manager call into this; execution flows through the
// closures BuildModule installs.
type Service struct {
	store *Store
	conns *connectors.Service
	keys  KeyStore
	base  func() string
	http  *http.Client
	sso   SSOSigner

	tagsSvc TagEnsurer // late-bound
	// aiProviders resolves the paste parser's provider catalog per
	// call (live — new/removed provider instances show up without a
	// restart). nil or empty = AI tab hidden.
	aiProviders func() []AIProviderEntry

	logins oauthLogins // in-flight OAuth browser logins (oauth scheme)

	mu       sync.Mutex
	loadedAt map[string]time.Time // def ID → last module build
	keyToID  map[string]string    // def key → def ID
}

func New(d Deps) *Service {
	httpClient := d.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	base := d.BaseURL
	if base == nil {
		base = func() string { return "" }
	}
	return &Service{
		store:    NewStore(d.DB),
		conns:    d.Connectors,
		keys:     d.Keys,
		base:     base,
		http:     httpClient,
		sso:      NewSSOSigner(d.Keys),
		loadedAt: map[string]time.Time{},
		keyToID:  map[string]string{},
	}
}

// SetTags late-binds the tags service (constructed after connectors
// bootstrap in server.go).
func (s *Service) SetTags(t TagEnsurer) { s.tagsSvc = t }

// SetAIProviders late-binds the live provider catalog the AI paste
// tab offers. The resolver runs per page render / per parse.
func (s *Service) SetAIProviders(resolve func() []AIProviderEntry) { s.aiProviders = resolve }

// AIProviderNames lists the selectable providers for the paste page
// dropdown. Empty slice = AI tab hidden.
func (s *Service) AIProviderNames() []string {
	if s.aiProviders == nil {
		return nil
	}
	entries := s.aiProviders()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

// HasAIParser reports whether the AI paste tab should render.
func (s *Service) HasAIParser() bool { return len(s.AIProviderNames()) > 0 }

// aiParserFor picks a provider by name; "" falls back to the first.
func (s *Service) aiParserFor(name string) AIParser {
	if s.aiProviders == nil {
		return nil
	}
	entries := s.aiProviders()
	if len(entries) == 0 {
		return nil
	}
	for _, e := range entries {
		if e.Name == name {
			return e.Parser
		}
	}
	if name != "" {
		return nil // explicit pick that vanished — error beats silent fallback
	}
	return entries[0].Parser
}

// Store exposes read access for UI handlers (list/detail pages).
func (s *Service) Store() *Store { return s.store }

// SSO exposes the signer so the manager can serve
// /.well-known/wick-pubkey.pem.
func (s *Service) SSO() SSOSigner { return s.sso }

func (s *Service) mcp(h *http.Client) *mcpClient {
	if h == nil {
		h = s.http
	}
	return &mcpClient{http: h, secrets: s.keys, sso: s.sso, issuer: s.base}
}

// ── registry lifecycle ───────────────────────────────────────────────

// RegisterAllAtBoot replays every enabled definition into the global
// connector registry. MUST run after RegisterBuiltins and before
// connectorsSvc.Bootstrap so custom modules ride the same instance
// seeding, config reconciliation, and allItems/tag passes as built-ins.
func (s *Service) RegisterAllAtBoot(ctx context.Context) error {
	defs, err := s.store.ListDefs(ctx)
	if err != nil {
		return fmt.Errorf("list custom connectors: %w", err)
	}
	l := log.With().Str("component", "custom-connector").Logger()
	for i := range defs {
		def := defs[i]
		s.mu.Lock()
		s.keyToID[def.Key] = def.ID
		s.mu.Unlock()
		if s.registryHasKey(def.Key) {
			l.Warn().Str("key", def.Key).Msg("custom connector key shadows a registered module; skipping")
			continue
		}
		// Disabled defs register too — zero operations keep cards and
		// instance pages reachable while nothing is callable. MCP defs
		// also register without probing: boot must not serialize on
		// network waits, and the configs cache (where oauth instance
		// tokens live) is not warm yet — ResyncMCPAtBoot connects them
		// right after the boot sequence finishes.
		mod, err := s.buildBootModule(ctx, &def)
		if err != nil {
			l.Error().Err(err).Str("key", def.Key).Msg("build custom connector module failed; skipping")
			continue
		}
		connectors.Register(mod)
		s.stampLoaded(def.ID)
		l.Debug().Str("key", def.Key).Bool("disabled", def.Disabled).Msg("registered custom connector")
	}
	return nil
}

// buildBootModule is the boot-time module build: cURL/manual defs are
// static and assemble from their stored ops; MCP defs assemble with
// zero operations (no probe) and get their live catalog from the
// async ResyncMCPAtBoot pass.
func (s *Service) buildBootModule(ctx context.Context, def *entity.CustomConnector) (connector.Module, error) {
	if def.Source == entity.CustomConnectorSourceMCP {
		return s.assembleModule(ctx, def, nil)
	}
	return s.BuildModule(ctx, def)
}

// ResyncMCPAtBoot connects every enabled MCP def once the boot
// sequence has finished: probe tools/list, adopt the server's
// description, swap the live catalog in. Run it in a goroutine right
// after Bootstrap — by then the configs cache is warm, so oauth defs
// resolve their per-instance tokens (probing earlier, inside
// RegisterAllAtBoot, would always come up token-less and serialize
// startup on network timeouts). cURL/manual defs are static and
// skipped.
func (s *Service) ResyncMCPAtBoot(ctx context.Context) {
	defs, err := s.store.ListDefs(ctx)
	if err != nil {
		return
	}
	l := log.With().Str("component", "custom-connector").Logger()
	// Parallel per def: one unreachable server costs one probe timeout,
	// not (timeout × defs) — the boot gate waits on this pass.
	var wg sync.WaitGroup
	for i := range defs {
		def := defs[i]
		if def.Source != entity.CustomConnectorSourceMCP || def.Disabled {
			continue
		}
		wg.Add(1)
		go func(def entity.CustomConnector) {
			defer wg.Done()
			if err := s.Reload(ctx, def.ID); err != nil {
				l.Warn().Err(err).Str("key", def.Key).Msg("boot re-sync failed; serving zero ops until next refresh")
				return
			}
			l.Debug().Str("key", def.Key).Msg("boot re-sync connected")
		}(def)
	}
	wg.Wait()
}

// EnsureInstanceTags links the per-def access tags ([custom:<key>
// filter, Connector group, category]) onto every instance row. Runs
// after the tags service exists; idempotent — rows that already carry
// any tag are left untouched (admin edits survive restarts).
func (s *Service) EnsureInstanceTags(ctx context.Context) {
	if s.tagsSvc == nil {
		return
	}
	defs, err := s.store.ListDefs(ctx)
	if err != nil {
		return
	}
	for i := range defs {
		s.ensureTagsForDef(ctx, &defs[i])
	}
}

func (s *Service) ensureTagsForDef(ctx context.Context, def *entity.CustomConnector) {
	if s.tagsSvc == nil {
		return
	}
	rows, err := s.conns.ListByKey(ctx, def.Key)
	if err != nil {
		return
	}
	for _, row := range rows {
		if err := s.tagsSvc.EnsureToolDefaultTags(ctx, "/connectors/"+row.ID, s.defaultTagsFor(def)); err != nil {
			log.Warn().Err(err).Str("key", def.Key).Msg("ensure custom connector tags")
		}
	}
}

func (s *Service) registryHasKey(key string) bool {
	for _, m := range connectors.All() {
		if m.Meta.Key == key {
			return true
		}
	}
	return false
}

func (s *Service) stampLoaded(defID string) {
	s.mu.Lock()
	s.loadedAt[defID] = time.Now()
	s.mu.Unlock()
}

// CanMutate is the level-1 ownership rule: a definition may be edited,
// reloaded, disabled, or deleted by an admin or by its creator. Same
// contract jobs/tools adopt when they grow user-created definitions.
func CanMutate(def *entity.CustomConnector, user *entity.User) bool {
	if user == nil {
		return false
	}
	return user.IsAdmin() || (def.CreatedBy != "" && def.CreatedBy == user.ID)
}

// TagInstanceOwner links the owner:<userID> tag onto an instance row —
// the level-2 ownership marker. Callers decide policy (the convention
// is non-admin creators only, mirroring the built-in "+ New row").
func (s *Service) TagInstanceOwner(ctx context.Context, instanceID, userID string) {
	if s.tagsSvc == nil || instanceID == "" || userID == "" {
		return
	}
	if err := s.tagsSvc.CreateOwnerTag(ctx, instanceID, userID); err != nil {
		log.Warn().Err(err).Str("instance_id", instanceID).Msg("tag instance owner")
	}
}

// CustomTagPrefix prefixes every per-def access tag ("custom:<key>"). These are
// access-control tags, NOT categories — UI that groups by category must skip
// them (see IsCustomTag).
const CustomTagPrefix = "custom:"

// FilterTagName returns the per-def access tag name.
func FilterTagName(defKey string) string { return CustomTagPrefix + defKey }

// IsCustomTag reports whether a tag name is a per-def custom access tag rather
// than a real category tag.
func IsCustomTag(name string) bool { return strings.HasPrefix(name, CustomTagPrefix) }

// categoryCatalog maps the review form's category picker onto the
// shared default-tag catalog so custom cards group with built-ins.
var categoryCatalog = map[string]tool.DefaultTag{
	tags.Communication.Name: tags.Communication,
	tags.Development.Name:   tags.Development,
	tags.Observability.Name: tags.Observability,
	tags.API.Name:           tags.API,
	tags.AI.Name:            tags.AI,
	tags.Security.Name:      tags.Security,
}

// CategoryNames lists the picker options in stable order.
func CategoryNames() []string {
	out := make([]string, 0, len(categoryCatalog))
	for name := range categoryCatalog {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *Service) defaultTagsFor(def *entity.CustomConnector) []tool.DefaultTag {
	out := []tool.DefaultTag{tags.Connector}
	meta := ParseSourceMeta(def.SourceMeta)
	if meta.Category != "" {
		if cat, ok := categoryCatalog[meta.Category]; ok {
			out = append(out, cat)
		} else {
			out = append(out, tool.DefaultTag{
				Name: meta.Category, IsGroup: true, SortOrder: 60,
			})
		}
	}
	out = append(out, tool.DefaultTag{
		Name:        FilterTagName(def.Key),
		Description: "Access tag for custom connector '" + def.Name + "'. Assign to users at /admin/tags to grant access; remove it from the connector to open to all approved users.",
		IsFilter:    true,
		SortOrder:   2000,
	})
	return out
}

// ── definition lifecycle ─────────────────────────────────────────────

// SaveNew validates a review-form draft, persists it, registers the
// module, seeds the single instance row, and links the access tags.
// Returns the stored def and the instance row ID for the redirect to
// /manager/connectors/{key}/{id}.
func (s *Service) SaveNew(ctx context.Context, d *Draft, createdBy string) (*entity.CustomConnector, string, error) {
	if err := ValidateDraft(d); err != nil {
		return nil, "", err
	}
	if s.registryHasKey(d.Key) {
		return nil, "", fmt.Errorf("%w: %s (registered module)", ErrKeyTaken, d.Key)
	}

	def := &entity.CustomConnector{
		Key:                d.Key,
		Name:               d.Name,
		Description:        d.Description,
		Icon:               defaultIcon(d.Icon),
		Source:             entity.CustomConnectorSource(d.Source),
		SourceMeta:         mustJSON(SourceMeta{Category: d.Category, ServerID: serverIDOf(d), HealthOp: strings.TrimSpace(d.HealthOp), HealthExpect: strings.TrimSpace(d.HealthExpect)}),
		Configs:            mustJSON(d.Configs),
		Ops:                mustJSON(d.Ops),
		SingleInstance:     d.Single,
		AllowSessionConfig: d.AllowSessionConfig,
		CreatedBy:          createdBy,
	}
	if err := s.store.CreateDef(ctx, def); err != nil {
		return nil, "", err
	}

	instanceID, err := s.registerAndSeed(ctx, def)
	if err != nil {
		return nil, "", err
	}
	return def, instanceID, nil
}

// Update rewrites a definition's mutable fields. The key is immutable —
// it is baked into the registry, the instance rows, and the tag name.
// The live module keeps serving until the admin clicks Reload.
func (s *Service) Update(ctx context.Context, defID string, d *Draft) error {
	def, err := s.store.GetDef(ctx, defID)
	if err != nil {
		return err
	}
	d.Key = def.Key // immutable
	if err := ValidateDraft(d); err != nil {
		return err
	}
	meta := ParseSourceMeta(def.SourceMeta)
	if d.Category != "" {
		meta.Category = d.Category
	}
	// Health probe is fully replaceable from the form — including clearing
	// it (empty HealthOp turns the check off), so assign rather than
	// conditionally merge.
	meta.HealthOp = strings.TrimSpace(d.HealthOp)
	meta.HealthExpect = strings.TrimSpace(d.HealthExpect)
	def.Name = d.Name
	def.Description = d.Description
	def.Icon = defaultIcon(d.Icon)
	def.SourceMeta = mustJSON(meta)
	def.Configs = mustJSON(d.Configs)
	def.Ops = mustJSON(d.Ops)
	def.SingleInstance = d.Single
	def.AllowSessionConfig = d.AllowSessionConfig
	return s.store.UpdateDef(ctx, def)
}

// Reload rebuilds the module from the stored definition and atomically
// swaps it into the registry + dispatch map. In-flight calls finish on
// the old closures; new calls see the new schema. For MCP defs this is
// also the re-sync/reconnect: the rebuild re-probes tools/list and
// refreshes the Connected status.
func (s *Service) Reload(ctx context.Context, defID string) error {
	return s.ReloadFor(ctx, defID, "")
}

// ReloadFor is Reload with a preferred probe instance: the re-sync
// button on an instance page passes that instance so an oauth-scheme
// tools/list runs under its own account (servers may expose different
// tools per account).
func (s *Service) ReloadFor(ctx context.Context, defID, preferInstance string) error {
	def, err := s.store.GetDef(ctx, defID)
	if err != nil {
		return err
	}
	_, err = s.registerAndSeedFor(ctx, def, preferInstance)
	return err
}

// mcpDescriptionPlaceholder is the auto-generated MCP def description.
// Deliberately neutral: it reaches the LLM via wick_list, which has no
// business knowing the connector is custom or proxies an MCP server —
// the server's initialize instructions replace it on the first build.
// Anything not matching a placeholder is admin-written and kept.
const mcpDescriptionPlaceholder = "Tools provided by"

// isPlaceholderDescription reports whether a def description may be
// overwritten by the server's own instructions.
func isPlaceholderDescription(desc string) bool {
	return desc == "" || strings.HasPrefix(desc, mcpDescriptionPlaceholder)
}

// mcpCatalogTTL throttles the lazy wick_get re-sync — a def's catalog
// is re-probed at most once per window, no matter how many gets land.
const mcpCatalogTTL = 30 * time.Second

// RefreshIfStale re-syncs a custom MCP def's live tool catalog when
// the serving module is older than mcpCatalogTTL. Wired into wick_get
// (via connectors.SetCatalogRefresh) so the LLM reads a near-fresh
// catalog — new server-side tools appear on the next get — while
// wick_list stays snapshot-fast. A failed probe keeps the existing
// module serving (a server blip must not wipe a working catalog); the
// stamp still advances so a flapping server is probed at most once per
// TTL. No-op for built-in keys, non-MCP defs, and disabled defs.
func (s *Service) RefreshIfStale(ctx context.Context, key, preferInstance string) {
	defID, ok := s.DefIDForKey(key)
	if !ok {
		return
	}
	s.mu.Lock()
	stale := time.Since(s.loadedAt[defID]) >= mcpCatalogTTL
	if stale {
		// Claim the window before probing so concurrent gets don't
		// stampede the server.
		s.loadedAt[defID] = time.Now()
	}
	s.mu.Unlock()
	if !stale {
		return
	}
	def, err := s.store.GetDef(ctx, defID)
	if err != nil || def.Source != entity.CustomConnectorSourceMCP || def.Disabled {
		return
	}
	ops, ok := s.liveMCPOps(ctx, def, preferInstance)
	if !ok {
		return
	}
	mod, err := s.assembleModule(ctx, def, ops)
	if err != nil {
		return
	}
	connectors.Register(mod)
	if err := s.conns.UpsertModule(ctx, mod); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("catalog refresh upsert")
	}
}

// SetDefDisabled toggles a definition and swaps the module in place:
// disabled defs serve zero operations (nothing listable or callable)
// but keep their cards, instance rows, and pages; re-enabling rebuilds
// the full op set (re-probing tools/list for MCP defs).
func (s *Service) SetDefDisabled(ctx context.Context, defID string, disabled bool) error {
	def, err := s.store.GetDef(ctx, defID)
	if err != nil {
		return err
	}
	def.Disabled = disabled
	if err := s.store.UpdateDef(ctx, def); err != nil {
		return err
	}
	_, err = s.registerAndSeed(ctx, def)
	return err
}

// EnsureTagsForKey links the per-def access tags onto every instance
// row of a custom def — called by the manager right after "+ New row"
// so fresh instances are governed immediately instead of waiting for
// the next boot reconcile. No-op for built-in keys.
func (s *Service) EnsureTagsForKey(ctx context.Context, key string) {
	defID, ok := s.DefIDForKey(key)
	if !ok {
		return
	}
	def, err := s.store.GetDef(ctx, defID)
	if err != nil {
		return
	}
	s.ensureTagsForDef(ctx, def)
	rows, _ := s.conns.ListByKey(ctx, key)
	for _, row := range rows {
		s.reEncryptSecretSeeds(ctx, def, row.ID)
	}
}

// registerAndSeed is the shared register-or-replace path: build the
// module, publish it to the global registry (listeners included),
// upsert it into the dispatch service (reconciling config rows for
// instances that exist), and link tags. No instance row is auto-
// created (Meta.ManualRows) — admins add rows explicitly via "+ New
// row", so the returned instance ID is "" until the first row exists.
func (s *Service) registerAndSeed(ctx context.Context, def *entity.CustomConnector) (string, error) {
	return s.registerAndSeedFor(ctx, def, "")
}

func (s *Service) registerAndSeedFor(ctx context.Context, def *entity.CustomConnector, preferInstance string) (string, error) {
	mod, err := s.buildModuleFor(ctx, def, preferInstance)
	if err != nil {
		return "", err
	}
	connectors.Register(mod)
	if err := s.conns.UpsertModule(ctx, mod); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.keyToID[def.Key] = def.ID
	s.mu.Unlock()
	s.stampLoaded(def.ID)
	s.ensureTagsForDef(ctx, def)

	rows, err := s.conns.ListByKey(ctx, def.Key)
	if err != nil || len(rows) == 0 {
		return "", nil
	}
	for _, row := range rows {
		s.reEncryptSecretSeeds(ctx, def, row.ID)
	}
	return rows[0].ID, nil
}

// reEncryptSecretSeeds re-writes secret config seeds through SetOwned.
// The reconcile path's initial seed write runs before the row's meta
// lands in the configs cache, so shouldEncrypt sees no secret flag and
// stores the pasted credential plaintext at rest. SetOwned runs after
// the meta exists and encrypts. Guarded to values still equal to the
// schema default so admin edits and reloads are never clobbered.
func (s *Service) reEncryptSecretSeeds(ctx context.Context, def *entity.CustomConnector, instanceID string) {
	fields, err := ParseFields(def.Configs)
	if err != nil {
		return
	}
	owner := "connector:" + instanceID
	for _, f := range fields {
		if !(f.Secret || f.Widget == "secret") || f.Default == "" {
			continue
		}
		if s.keys.GetOwned(owner, f.Key) != f.Default {
			continue
		}
		if err := s.keys.SetOwned(ctx, owner, f.Key, f.Default); err != nil {
			log.Warn().Err(err).Str("key", f.Key).Msg("re-encrypt secret seed")
		}
	}
}

// Delete removes the definition and its instance rows (configs + op
// state cascade through the connectors service; run history stays for
// audit). MCP-sourced defs also drop their server row — one server is
// one connector, so an orphan registration would be dead weight. The
// custom:<key> tag row is kept by default so re-creating the same key
// restores prior user grants. The in-memory module stays registered
// until restart — calls against it fail fast because the instance row
// is gone.
func (s *Service) Delete(ctx context.Context, defID string) error {
	def, err := s.store.GetDef(ctx, defID)
	if err != nil {
		return err
	}
	rows, err := s.conns.ListByKey(ctx, def.Key)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.conns.Delete(ctx, row.ID); err != nil {
			return fmt.Errorf("delete instance %s: %w", row.ID, err)
		}
	}
	if err := s.store.DeleteDef(ctx, defID); err != nil {
		return err
	}
	if serverID := ServerIDForDef(def); serverID != "" {
		if err := s.store.DeleteServer(ctx, serverID); err != nil {
			log.Warn().Err(err).Str("server_id", serverID).Msg("delete mcp server row")
		}
	}
	s.mu.Lock()
	delete(s.loadedAt, defID)
	delete(s.keyToID, def.Key)
	s.mu.Unlock()
	return nil
}

// IsDirty reports whether the stored definition is newer than the
// module currently serving — drives the "Reload" banner.
func (s *Service) IsDirty(def *entity.CustomConnector) bool {
	s.mu.Lock()
	loaded, ok := s.loadedAt[def.ID]
	s.mu.Unlock()
	return ok && def.UpdatedAt.After(loaded)
}

// DefIDForKey resolves a connector key to a custom def ID; ok=false
// means the key belongs to a built-in (or nothing).
func (s *Service) DefIDForKey(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.keyToID[key]
	return id, ok
}

func defaultIcon(icon string) string {
	if strings.TrimSpace(icon) == "" {
		return "🔌"
	}
	return icon
}

// defaultMCPIcon falls back to the toolbox glyph for MCP defs.
func defaultMCPIcon(icon string) string {
	if strings.TrimSpace(icon) == "" {
		return "🧰"
	}
	return icon
}

func serverIDOf(d *Draft) string {
	for _, op := range d.AllOps() {
		if op.MCPSource != nil {
			return op.MCPSource.ServerID
		}
	}
	return ""
}

// ── paste parsing ────────────────────────────────────────────────────

// ParsePaste runs the requested parser over the paste box content and
// returns the review-form draft. parser is "curl" (deterministic,
// default) or "ai" (LLM extraction; requires a configured provider).
func (s *Service) ParsePaste(ctx context.Context, parser, provider, paste string) (*Draft, error) {
	if len(paste) > 8*1024 {
		return nil, fmt.Errorf("paste is larger than 8 KB — trim it down to a single endpoint")
	}
	switch parser {
	case "", "curl":
		parsed, err := ParseCurl(paste)
		if err != nil {
			return nil, err
		}
		return Extract(parsed)
	case "ai":
		ai := s.aiParserFor(provider)
		if ai == nil {
			return nil, fmt.Errorf("AI parser provider unavailable — pick another provider or use the cURL tab")
		}
		parsed, err := ai.Parse(ctx, paste)
		if err != nil {
			return nil, err
		}
		return Extract(parsed)
	default:
		return nil, fmt.Errorf("unknown parser %q", parser)
	}
}

// ── MCP servers ──────────────────────────────────────────────────────

// ServerForm is the manager form payload for creating/updating an MCP
// server registration. Secret values arrive as plaintext (or as
// wick_enc_ tokens when unchanged from a stored row) and are encrypted
// under the master key before persisting. Excluded is the opt-out tool
// list — everything the server lists becomes an operation except these
// names.
type ServerForm struct {
	Label string `json:"label"`
	Icon  string `json:"icon"`
	// Description overrides the connector description. Empty = adopt
	// the server's own initialize instructions (and keep adopting on
	// every re-sync); non-empty = admin-written, never auto-replaced.
	Description string         `json:"description"`
	URL         string         `json:"url"`
	AuthScheme  string         `json:"auth_scheme"`
	AuthSecret  string         `json:"auth_secret"`
	AuthHeaders []HeaderRow    `json:"auth_headers"`
	Headers     []HeaderRow    `json:"headers"`
	SSO         SSOExtra       `json:"sso"`
	OAuth       OAuthFormExtra `json:"oauth"`
	Excluded    []string       `json:"excluded"`
	// OAuthLoginID references a completed in-flight browser login —
	// the test/save endpoints resolve it to the session's tokens.
	OAuthLoginID string `json:"oauth_login_id"`
}

// OAuthFormExtra is the optional client override for the oauth scheme.
// Leaving ClientID empty lets wick register a client dynamically
// (RFC 7591) when the server supports it.
type OAuthFormExtra struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Scopes       string `json:"scopes"`
}

var validAuthSchemes = map[string]bool{"none": true, "bearer": true, "custom_header": true, "sso": true, "oauth": true}

func (f *ServerForm) validate() error {
	if strings.TrimSpace(f.Label) == "" {
		return fmt.Errorf("label is required")
	}
	if !strings.HasPrefix(f.URL, "http://") && !strings.HasPrefix(f.URL, "https://") {
		return fmt.Errorf("MCP URL must be http(s)")
	}
	if !validAuthSchemes[f.AuthScheme] {
		return fmt.Errorf("unknown auth scheme %q", f.AuthScheme)
	}
	if err := ValidateIcon(f.Icon); err != nil {
		return err
	}
	if f.AuthScheme == "bearer" && strings.TrimSpace(f.AuthSecret) == "" {
		return fmt.Errorf("bearer scheme needs a token")
	}
	return nil
}

// TestServer fires the initialize + tools/list probe against the form
// values (not a stored row), so admins can test before saving. caller
// supplies the SSO identity when the scheme is sso. For the oauth
// scheme the form must reference a completed browser login
// (OAuthLoginID) — without one the result asks the UI to start it.
func (s *Service) TestServer(ctx context.Context, f *ServerForm, caller *SSOClaims) ProbeResult {
	if err := f.validate(); err != nil {
		return ProbeResult{OK: false, Error: err.Error()}
	}
	srv := serverConfig{
		URL:           f.URL,
		AuthScheme:    f.AuthScheme,
		AuthSecret:    f.AuthSecret,
		AuthHeaders:   f.AuthHeaders,
		ExtraHeaders:  f.Headers,
		SSOAudience:   f.SSO.Audience,
		SSOTTLSeconds: f.SSO.TTLSeconds,
	}
	if srv.SSOAudience == "" {
		srv.SSOAudience = hostOf(f.URL)
	}
	if f.AuthScheme == "oauth" {
		login, ok := s.logins.get(f.OAuthLoginID)
		if !ok || login.Tokens == nil {
			return ProbeResult{OK: false, NeedsLogin: true, Error: "login required"}
		}
		srv.AccessToken = login.Tokens.AccessToken
	}
	return s.mcp(nil).Probe(ctx, srv, caller)
}

// SaveServer persists a tested form and keeps the server's connector in
// sync — one server row IS one connector. A fresh save auto-creates the
// definition (key = slug(label), ops always live from tools/list, no
// import step) and registers its module immediately; an edit re-syncs
// the def name and atomically reloads the module so exclusion changes
// apply at once. testedOK must reflect a successful probe in the
// current form session — the save gate also guarantees the rebuild's
// own probe will succeed. Returns the row, the connector key, and the
// seeded instance ID for the redirect.
func (s *Service) SaveServer(ctx context.Context, f *ServerForm, testedOK bool, existingID, createdBy string) (*entity.CustomConnectorMCPServer, string, string, error) {
	if err := f.validate(); err != nil {
		return nil, "", "", err
	}
	if !testedOK {
		return nil, "", "", fmt.Errorf("test the connection successfully before saving")
	}

	encryptIfNeeded := func(v string) (string, error) {
		if v == "" || strings.HasPrefix(v, "wick_enc_") || s.keys == nil {
			return v, nil
		}
		return s.keys.EncryptSecret(v)
	}
	authSecret, err := encryptIfNeeded(f.AuthSecret)
	if err != nil {
		return nil, "", "", fmt.Errorf("encrypt token: %w", err)
	}
	encHeaders := func(rows []HeaderRow) ([]HeaderRow, error) {
		out := make([]HeaderRow, 0, len(rows))
		for _, r := range rows {
			if r.Key == "" {
				continue
			}
			if r.Secret {
				v, err := encryptIfNeeded(r.Value)
				if err != nil {
					return nil, fmt.Errorf("encrypt header %s: %w", r.Key, err)
				}
				r.Value = v
			}
			out = append(out, r)
		}
		return out, nil
	}
	authHeaders, err := encHeaders(f.AuthHeaders)
	if err != nil {
		return nil, "", "", err
	}
	extraHeaders, err := encHeaders(f.Headers)
	if err != nil {
		return nil, "", "", err
	}
	excluded := f.Excluded
	if excluded == nil {
		excluded = []string{}
	}
	authExtra := mustJSON(f.SSO)
	if f.AuthScheme == "oauth" {
		extra, err := s.oauthAuthExtra(ctx, f, existingID)
		if err != nil {
			return nil, "", "", err
		}
		authExtra = extra
	}

	now := time.Now()
	row := &entity.CustomConnectorMCPServer{
		ID:            existingID,
		Label:         f.Label,
		Transport:     "http",
		URL:           f.URL,
		AuthScheme:    f.AuthScheme,
		AuthSecret:    authSecret,
		AuthHeaders:   mustJSON(authHeaders),
		AuthExtra:     authExtra,
		Headers:       mustJSON(extraHeaders),
		ExcludedTools: mustJSON(excluded),
		LastTestAt:    &now,
		LastTestOK:    true,
	}

	if existingID == "" {
		key := toFieldKey(f.Label)
		if key == "" {
			return nil, "", "", fmt.Errorf("label must contain at least one letter")
		}
		if s.registryHasKey(key) {
			return nil, "", "", fmt.Errorf("%w: %s — change the label", ErrKeyTaken, key)
		}
		if err := s.store.CreateServer(ctx, row); err != nil {
			return nil, "", "", err
		}
		def, instanceID, err := s.ensureDefForServer(ctx, row, f.Icon, f.Description, createdBy)
		if err != nil {
			_ = s.store.DeleteServer(ctx, row.ID)
			return nil, "", "", err
		}
		// The oauth register flow already holds a logged-in account —
		// land it as the first instance so save means "connector +
		// connected account" in one step.
		if login, ok := s.logins.get(f.OAuthLoginID); f.AuthScheme == "oauth" && ok && login.Tokens != nil {
			if inst, err := s.conns.Create(ctx, def.Key, def.Name, map[string]string{}, createdBy); err == nil {
				if err := s.persistInstanceTokens(ctx, inst.ID, login.Tokens, login.Account); err != nil {
					log.Warn().Err(err).Msg("attach oauth tokens to first instance")
				}
				s.ensureTagsForDef(ctx, def)
				instanceID = inst.ID
			}
		}
		return row, def.Key, instanceID, nil
	}

	existing, err := s.store.GetServer(ctx, existingID)
	if err != nil {
		return nil, "", "", err
	}
	row.CreatedAt = existing.CreatedAt
	if err := s.store.UpdateServer(ctx, row); err != nil {
		return nil, "", "", err
	}
	def := s.defForServer(ctx, row.ID)
	if def == nil {
		// Legacy row without a definition — heal by creating one.
		def2, instanceID, err := s.ensureDefForServer(ctx, row, f.Icon, f.Description, createdBy)
		if err != nil {
			return nil, "", "", err
		}
		return row, def2.Key, instanceID, nil
	}
	// Description semantics: untouched prefill round-trips identical →
	// no change; edited non-empty → admin-written (never auto-replaced);
	// cleared → reset to the placeholder so the next sync re-adopts the
	// server's own instructions.
	desc := def.Description
	if f.Description != def.Description {
		desc = f.Description
		if desc == "" {
			desc = mcpDescriptionPlaceholder + " '" + f.Label + "'."
		}
	}
	if def.Name != f.Label || (f.Icon != "" && def.Icon != f.Icon) || def.Description != desc {
		def.Name = f.Label
		def.Description = desc
		if f.Icon != "" {
			def.Icon = f.Icon
		}
		if err := s.store.UpdateDef(ctx, def); err != nil {
			return nil, "", "", err
		}
	}
	// Rebuild + swap so the new exclusions/credentials serve immediately.
	instanceID, err := s.registerAndSeed(ctx, def)
	if err != nil {
		return nil, "", "", err
	}
	return row, def.Key, instanceID, nil
}

// ensureDefForServer creates the connector definition backing a fresh
// server row and registers its module. Ops stay empty in storage — the
// module build probes tools/list live.
func (s *Service) ensureDefForServer(ctx context.Context, row *entity.CustomConnectorMCPServer, icon, desc, createdBy string) (*entity.CustomConnector, string, error) {
	if strings.TrimSpace(desc) == "" {
		// Placeholder — the first sync adopts the server's instructions.
		desc = mcpDescriptionPlaceholder + " '" + row.Label + "'."
	}
	def := &entity.CustomConnector{
		Key:         toFieldKey(row.Label),
		Name:        row.Label,
		Description: desc,
		Icon:        defaultMCPIcon(icon),
		CreatedBy:   createdBy,
		Source:      entity.CustomConnectorSourceMCP,
		SourceMeta:  mustJSON(SourceMeta{ServerID: row.ID}),
		Configs:     "[]",
		Ops:         "[]",
	}
	if err := s.store.CreateDef(ctx, def); err != nil {
		return nil, "", err
	}
	instanceID, err := s.registerAndSeed(ctx, def)
	if err != nil {
		_ = s.store.DeleteDef(ctx, def.ID)
		return nil, "", err
	}
	return def, instanceID, nil
}

// defForServer finds the definition owned by a server row (1:1 via
// SourceMeta.ServerID); nil when none exists.
func (s *Service) defForServer(ctx context.Context, serverID string) *entity.CustomConnector {
	defs, err := s.store.ListDefs(ctx)
	if err != nil {
		return nil
	}
	for i := range defs {
		if defs[i].Source == entity.CustomConnectorSourceMCP &&
			ParseSourceMeta(defs[i].SourceMeta).ServerID == serverID {
			return &defs[i]
		}
	}
	return nil
}

// DefForServer finds the definition owned by a server row — the
// manager's edit page uses it to decorate the form with the
// definition-level controls. Nil when no def references the server.
func (s *Service) DefForServer(ctx context.Context, serverID string) *entity.CustomConnector {
	return s.defForServer(ctx, serverID)
}

// ServerIDForDef resolves the MCP server row backing a definition; ""
// for curl/manual defs.
func ServerIDForDef(def *entity.CustomConnector) string {
	if def.Source != entity.CustomConnectorSourceMCP {
		return ""
	}
	return ParseSourceMeta(def.SourceMeta).ServerID
}

// ProbeStored re-tests a stored server row (detail page "Test now") and
// refreshes its tools listing + LastTest columns.
func (s *Service) ProbeStored(ctx context.Context, serverID string, caller *SSOClaims) (ProbeResult, error) {
	row, err := s.store.GetServer(ctx, serverID)
	if err != nil {
		return ProbeResult{}, err
	}
	srv, err := resolveServerConfig(row.URL, row.AuthScheme, row.AuthSecret, row.AuthHeaders, row.AuthExtra, row.Headers)
	if err != nil {
		return ProbeResult{}, err
	}
	res := s.mcp(nil).Probe(ctx, srv, caller)
	now := time.Now()
	row.LastTestAt = &now
	row.LastTestOK = res.OK
	_ = s.store.UpdateServer(ctx, row)
	return res, nil
}

var destructiveToolRe = regexp.MustCompile(`(?i)^(delete|remove|drop|destroy|disable|revoke|purge|wipe)_`)

// mapInputSchema converts an MCP JSON Schema into wick's widget grammar
// (design §5.4). Nested objects/arrays become raw-JSON textareas; the
// admin can override every mapping on the review form.
func mapInputSchema(schema map[string]any) []DefField {
	out := []DefField{}
	if schema == nil {
		return out
	}
	props, _ := schema["properties"].(map[string]any)
	required := map[string]bool{}
	if reqList, ok := schema["required"].([]any); ok {
		for _, r := range reqList {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}
	for _, name := range sortedAnyKeys(props) {
		spec, _ := props[name].(map[string]any)
		f := DefField{
			Key:      toFieldKey(name),
			Label:    name,
			Required: required[name],
		}
		if desc, ok := spec["description"].(string); ok {
			f.Desc = desc
		}
		typ, _ := spec["type"].(string)
		format, _ := spec["format"].(string)
		switch {
		case spec["enum"] != nil:
			f.Widget = "dropdown"
			if opts, ok := spec["enum"].([]any); ok {
				parts := make([]string, 0, len(opts))
				for _, o := range opts {
					parts = append(parts, fmt.Sprintf("%v", o))
				}
				f.Options = strings.Join(parts, "|")
			}
		case typ == "number" || typ == "integer":
			f.Widget = "number"
		case typ == "boolean":
			f.Widget = "checkbox"
		case typ == "object" || typ == "array":
			f.Widget = "textarea"
			if f.Desc != "" {
				f.Desc += " "
			}
			f.Desc += "(raw JSON)"
		case format == "uri":
			f.Widget = "url"
		case format == "password" || secretParamRe.MatchString(name) || secretParamRe.MatchString(f.Desc):
			f.Widget = "secret"
			f.Secret = true
		default:
			f.Widget = "text"
		}
		if def, ok := spec["default"]; ok {
			f.Default = fmt.Sprintf("%v", def)
		}
		out = append(out, f)
	}
	return out
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

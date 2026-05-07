package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/yogasw/wick/internal/admin"
	"github.com/yogasw/wick/internal/bookmark"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/connectors/wickmanager"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	encfieldstool "github.com/yogasw/wick/internal/tools/encfields"
	"github.com/yogasw/wick/internal/health"
	"github.com/yogasw/wick/internal/home"
	"github.com/yogasw/wick/internal/initcreds"
	"github.com/yogasw/wick/internal/jobrunner"
	"github.com/yogasw/wick/internal/jobs"
	connectorrunspurge "github.com/yogasw/wick/internal/jobs/connector-runs-purge"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/internal/oauth"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/sso"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/web"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func NewServer() *Server {
	cfg := config.Load()

	// Log the runtime mode so first-boot debugging is straightforward —
	// "stdout banner missing the password? logs going to the wrong file?"
	// usually traces back to whether WICK_TRAY=1 was set by the spawning
	// process. App / Server names are part of the line so per-app data
	// dir mismatches (APP_NAME unset, name typo) are obvious too.
	log.Info().
		Bool("tray", os.Getenv("WICK_TRAY") == "1").
		Str("app_name", strings.TrimSpace(os.Getenv("APP_NAME"))).
		Msg("server: runtime mode")

	db := postgres.NewGORM(cfg.Database)
	postgres.Migrate(db)

	// Built-in maintenance jobs whose RunFunc captures *gorm.DB are
	// registered here, after DB init, before validation + the jobs.All()
	// loops below. Mirrors the call in internal/pkg/worker.NewServer
	// so both processes share the same registry view.
	connectorrunspurge.Register(db)

	// ── Tool modules (discover first so their Specs feed into the
	// config bootstrap below) ──────────────────────────────────────
	modules := tools.All()
	if err := tool.ValidateModules(modules); err != nil {
		log.Fatal().Msgf("%s", err.Error())
	}
	if err := job.ValidateJobs(jobs.All()); err != nil {
		log.Fatal().Msgf("%s", err.Error())
	}

	// ── Runtime config (cached) ─────────────────────────────────
	// Bootstrap reconciles the app-level defaults with the `configs`
	// table, auto-generating session_secret on first boot. Each tool
	// module carries its pre-reflected Configs (Owner = meta.Key is
	// stamped here) so per-module rows are seeded in the same pass.
	configsSvc := configs.NewService(db)
	var extraConfigs []entity.Config
	for _, m := range modules {
		for _, row := range m.Configs {
			row.Owner = m.Meta.Key
			extraConfigs = append(extraConfigs, row)
		}
	}
	for _, jm := range jobs.All() {
		for _, row := range jm.Configs {
			row.Owner = jm.Meta.Key
			extraConfigs = append(extraConfigs, row)
		}
	}
	if err := configsSvc.Bootstrap(context.Background(), extraConfigs...); err != nil {
		log.Fatal().Msgf("configs bootstrap: %s", err.Error())
	}
	// Seed from env on first boot only — once the row exists in the DB
	// the admin UI is the only way to change it.
	if configsSvc.AppName() == configs.DefaultAppName && cfg.App.Name != "" {
		if err := configsSvc.Set(context.Background(), configs.KeyAppName, cfg.App.Name); err != nil {
			log.Warn().Msgf("seed app_name: %s", err.Error())
		}
	}
	if configsSvc.AppURL() == "" && cfg.App.URL != "" {
		if err := configsSvc.Set(context.Background(), configs.KeyAppURL, cfg.App.URL); err != nil {
			log.Warn().Msgf("seed app_url: %s", err.Error())
		}
	}

	// ── SSO providers (cached, hot-reloadable) ─────────────────
	ssoSvc := sso.NewService(db)
	if err := ssoSvc.Bootstrap(context.Background()); err != nil {
		log.Fatal().Msgf("sso bootstrap: %s", err.Error())
	}

	// ── Auth ────────────────────────────────────────────────────
	authSvc := login.NewService(db, cfg.App.AdminEmails)
	authMidd := login.NewMiddleware(authSvc, configsSvc)
	authHandler := login.NewHandler(authSvc, authMidd, ssoSvc, configsSvc)

	// ── Health Check ───────────────────────────────────────────────
	healthRepo := health.NewRepository(db)
	healthSvc := health.NewService(healthRepo)
	healthHandler := health.NewHttpHandler(healthSvc)

	// One-shot: create the default admin only when no admin user exists yet.
	// When APP_ADMIN_PASSWORD is empty, the service auto-generates a 5-word
	// passphrase and returns it so we can persist it to INITIAL_CREDENTIALS
	// for the operator to recover. Empty return = no seeding happened
	// (admins already exist) or env-supplied password (operator already
	// knows it).
	envPassword := cfg.App.AdminPassword
	if envPassword == "admin" {
		// Treat the historical default as "no explicit password" so
		// installer-style runs get a real auto-generated secret instead
		// of the well-known "admin".
		envPassword = ""
	}
	if generated := authSvc.BootstrapAdmin(context.Background(), envPassword, configsSvc.AdminPasswordChanged()); generated != "" {
		appName := strings.TrimSpace(os.Getenv("APP_NAME"))
		seedEmail := strings.SplitN(cfg.App.AdminEmails, ",", 2)[0]
		seedEmail = strings.TrimSpace(seedEmail)
		path, werr := initcreds.Write(appName, seedEmail, generated, configsSvc.AppURL())
		if werr != nil {
			log.Warn().Err(werr).Msg("write initial credentials")
		}
		// Print the credentials inline only on headless / CLI runs —
		// useful for Linux servers / docker logs / systemd journal
		// where the operator can't see a tray menu. Tray builds set
		// WICK_TRAY=1 and pipe stdout to the app log, so printing the
		// password there would leak it to disk; the tray surface (file
		// path + menu item) is enough.
		if os.Getenv("WICK_TRAY") != "1" {
			fmt.Printf("\n  ⚠ Default admin created — credentials saved to %s\n", path)
			fmt.Printf("  → Email:            %s\n", seedEmail)
			fmt.Printf("  → Default password: %s\n", generated)
		} else {
			fmt.Printf("\n  ⚠ Default admin created — credentials saved to %s\n", path)
		}
	}

	// ── Jobs (background workers) ────────────────────────────────
	jobsSvc := manager.NewServiceFromDB(db)
	jobsSvc.SetConfigReader(configsSvc)
	if err := jobsSvc.Bootstrap(context.Background(), jobs.All()); err != nil {
		log.Fatal().Msgf("jobs bootstrap: %s", err.Error())
	}

	// ── Encrypted-fields layer (encrypted-fields.md) ───────────────
	// Master key is bootstrapped by the configs service (auto-
	// generated on first boot, vault-overridable via WICK_ENC_KEY).
	// Disable globally with WICK_ENC_DISABLE=true. We initialise this
	// before the connectors service so Bootstrap can wire the cipher
	// in once and Execute is never called without it.
	encSvc, err := enc.New(configsSvc.EncryptionKey())
	if err != nil {
		log.Fatal().Msgf("enc init: %s", err.Error())
	}
	// Wire the cipher into configs so every IsSecret row is
	// encrypted at rest from this point on. Also migrates any
	// pre-existing plaintext secret rows to ciphertext on next boot.
	configsSvc.SetEncryptor(encSvc)
	// The encfields tool resolves its cipher through a package
	// singleton — built-in tools register from cmd/lab before the DB
	// or enc service exist, so a static Register signature is the
	// cost of doing business. Set once here, before any tool route
	// is mountable.
	encfieldstool.SetService(encSvc)

	// ── Connectors (LLM-facing via MCP) ──────────────────────────
	// Register the code-side definitions for dispatch and auto-seed
	// one DB row per Key on first boot. The MCP server below is the
	// runtime entry point for LLM clients.
	connectorsSvc := connectors.NewServiceFromDB(db)
	connectorsSvc.SetEnc(encSvc)
	connectorsSvc.SetConfigs(configsSvc)

	// Resolve every tool meta up front — wick stamps the mount path
	// from meta.Key so modules never have to. (Earlier here than in
	// past versions: wickmanager.Module needs the resolved tool list
	// for its tool_list / tool_get ops, so we have to compute this
	// before registering wickmanager.)
	var allItems []tool.Tool
	for _, m := range modules {
		meta := m.Meta
		meta.Path = "/tools/" + meta.Key
		allItems = append(allItems, meta)
	}

	// wickmanager is a built-in single-instance connector that exposes
	// wick's own management plane (apps, jobs, tools, connectors,
	// process lifecycle) via the same connector contract every
	// downstream connector uses. Register here, after all services
	// the handlers need are constructed but before Bootstrap so the
	// fixed instance gets seeded in the same pass as user connectors.
	connectors.Register(wickmanager.Module(wickmanager.Deps{
		Configs:    configsSvc,
		Connectors: connectorsSvc,
		Jobs:       jobsSvc,
		Login:      authSvc,
		Tools:      allItems,
		AppName:    strings.TrimSpace(os.Getenv("APP_NAME")),
	}))

	if err := connectorsSvc.Bootstrap(context.Background(), connectors.All()); err != nil {
		log.Fatal().Msgf("connectors bootstrap: %s", err.Error())
	}

	// ── Personal Access Tokens (MCP bearer auth) ─────────────────
	tokensSvc := accesstoken.NewServiceFromDB(db)
	tokensHandler := accesstoken.NewHandler(tokensSvc, configsSvc)

	// ── OAuth 2.1 server (issues bearer tokens for MCP) ──────────
	// Issuer is the live app_url; the handler refreshes it from
	// configs.Service on every request, so admin URL edits take
	// effect without a restart.
	oauthSvc := oauth.NewServiceFromDB(db, configsSvc.AppURL())
	oauthHandler := oauth.NewHandler(oauthSvc, configsSvc)

	// ── MCP server (JSON-RPC over /mcp) ──────────────────────────
	// Bearer auth in front, connector dispatch behind. PAT and
	// OAuth-issued tokens both flow through the same middleware —
	// dispatch by prefix.
	mcpHandler := mcp.NewHandler(connectorsSvc).WithAppURL(configsSvc.AppURL)
	mcpAuth := mcp.NewAuthMiddleware(
		tokensSvc,
		authSvc,
		oauthSvc,
		strings.TrimRight(configsSvc.AppURL(), "/")+"/.well-known/oauth-protected-resource",
	)

	// Tools declare routes through a write-only Router; wick collects
	// them here so duplicate "METHOD PATH" across modules fails the boot
	// with a pointed error instead of silently clobbering each other at
	// mux.Handle. Module paths are relative to /tools/{meta.Key}; the
	// router prefixes the base per meta before mounting.
	toolsMux := http.NewServeMux()
	tr := newToolRouter(configsSvc)
	for _, m := range modules {
		meta := m.Meta
		meta.Path = "/tools/" + meta.Key
		tr.withScope(meta, len(m.Configs) > 0, m.Register)
	}
	if err := tr.validate(); err != nil {
		log.Fatal().Msgf("%s", err.Error())
	}
	tr.mount(toolsMux)

	tagsSvc := tags.NewService(db)
	managerHandler := manager.NewHandler(jobsSvc, configsSvc, connectorsSvc, tagsSvc, authSvc, allItems)

	// jobrunnerHandler exposes /jobs/{key} — the operator surface with
	// a Run Now button and run history. Admin-only settings stay on
	// /manager/jobs/{key} via managerHandler above.
	jobrunnerHandler := jobrunner.NewHandler(jobsSvc, configsSvc)

	// Register jobs as items — same pattern as tools above. One module
	// registration = one row. Jobs have no self-hosted UI; the card in
	// home deep-links into the manager detail page (settings + runs).
	for _, jd := range jobs.All() {
		m := jd.Meta
		allItems = append(allItems, tool.Tool{
			Name:              m.Name,
			Description:       m.Description,
			Icon:              m.Icon,
			Path:              "/jobs/" + m.Key,
			Category:          "job",
			DefaultVisibility: entity.VisibilityPrivate,
			DefaultTags:       m.DefaultTags,
		})
	}

	// Register connectors as items. One module = one card; the card
	// links to the manager list page where users see N rows for that
	// definition (one per credential set), each with a test panel and
	// enable/disable/duplicate actions.
	for _, cm := range connectors.All() {
		m := cm.Meta
		allItems = append(allItems, tool.Tool{
			Name:              m.Name,
			Description:       m.Description,
			Icon:              m.Icon,
			Path:              "/manager/connectors/" + m.Key,
			Category:          "connector",
			DefaultVisibility: entity.VisibilityPrivate,
		})
	}

	// ── Admin ────────────────────────────────────────────────────
	adminHandler := admin.NewHandler(db, allItems, configsSvc, ssoSvc, jobsSvc, connectorsSvc, tokensSvc, oauthSvc)

	// ── Shared services ─────────────────────────────────────────
	bookmarkSvc := bookmark.NewService(db)
	bookmarkHandler := bookmark.NewHandler(bookmarkSvc)

	// Seed default tags for items that have them.
	for _, t := range allItems {
		if len(t.DefaultTags) == 0 {
			continue
		}
		if err := tagsSvc.EnsureToolDefaultTags(context.Background(), t.Path, t.DefaultTags); err != nil {
			log.Error().Msgf("seed default tags for %s: %s", t.Path, err.Error())
		}
	}
	// Backfill System tags for existing admins. New admins get the
	// sync inline via admin.Repo.SetRole; this catches admins that
	// pre-date a newly-introduced System tag.
	if err := tagsSvc.SyncSystemTagsForAllAdmins(context.Background()); err != nil {
		log.Error().Msgf("backfill system tags for admins: %s", err.Error())
	}

	// ── Home ─────────────────────────────────────────────────────
	homeHandler := home.NewHandler(allItems, authSvc, tagsSvc, bookmarkSvc)

	// ── Router ───────────────────────────────────────────────────
	r := http.NewServeMux()

	// Health check endpoint — used by load balancers and uptime monitoring.
	r.Handle("GET /health", http.HandlerFunc(healthHandler.Check))

	// Static files (embedded in binary). Directory listings are blocked.
	r.Handle("GET /public/", ui.StaticHandler("", web.PublicFiles))

	// Home module static assets (JS etc.) — served at /modules/home/js/*
	r.Handle("GET /modules/home/", ui.StaticHandler("/modules/home/", home.StaticFS))

	// Admin module static assets (tag picker etc.)
	r.Handle("GET /modules/admin/", ui.StaticHandler("/modules/admin/", admin.StaticFS))

	// MCP access page static assets (copy buttons, create-form toggle)
	r.Handle("GET /modules/accesstoken/", ui.StaticHandler("/modules/accesstoken/", accesstoken.StaticFS))

	// Auth routes: /auth/login, /auth/callback, /auth/logout, /auth/pending
	authHandler.Register(r, authMidd)

	// Admin routes: /admin, /admin/tools, /admin/configs, /admin/configs/sso, ...
	adminHandler.Register(r, authMidd)

	// Bookmark API (auth-gated inside)
	bookmarkHandler.Register(r, authMidd)

	// Personal access tokens + MCP install — /profile/tokens, /profile/mcp.
	tokensHandler.Register(r, authMidd)

	// MCP JSON-RPC endpoint. Bearer-authed (PAT or OAuth access
	// token). Mounted on the cookie-bypass mux because LLM clients
	// carry a bearer header, not a session cookie — RequireAuth would
	// 302 them into /auth/login which they can't follow.
	r.Handle("POST /mcp", mcpAuth.Wrap(mcpHandler))

	// OAuth 2.1 surface — .well-known metadata + /oauth/{register,
	// authorize, token} (public) + /profile/connections (auth-gated
	// inside, per-user grant dashboard).
	oauthHandler.Register(r, authMidd)

	// Manager (admin settings) + jobrunner (operator surface) routes.
	// The two share manager.Service so run history and banners stay in
	// sync across /manager/jobs/{key} and /jobs/{key}.
	managerHandler.Register(r, authMidd)
	jobrunnerHandler.Register(r, authMidd)

	// Tool routes — per-tool visibility enforced via RequireToolAccess.
	// Public tools are reachable without login; Private tools require
	// approval and (when set) matching tags.
	toolMetas := make([]login.ToolMeta, 0, len(allItems))
	for _, t := range allItems {
		toolMetas = append(toolMetas, login.ToolMeta{Path: t.Path, DefaultVisibility: t.DefaultVisibility})
	}
	r.Handle("/tools/", authMidd.RequireToolAccess(toolMetas)(toolsMux))

	// API — JSON endpoints
	r.Handle("GET /api/tools", http.HandlerFunc(homeHandler.APITools))

	// Home
	r.Handle("/", http.HandlerFunc(homeHandler.Index))

	return &Server{router: r, configsSvc: configsSvc, authMidd: authMidd}
}

type Server struct {
	router     *http.ServeMux
	configsSvc *configs.Service
	authMidd   *login.Middleware
}

// appNameHandler injects the configurable app name into every request
// context so templ components can read it via ui.AppNameFromContext.
func (s *Server) appNameHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := ui.WithAppName(r.Context(), s.configsSvc.AppName())
		ctx = ui.WithAppDescription(ctx, s.configsSvc.AppDescription())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// hostAllowlistHandler rejects requests whose Host header doesn't match
// the host of the configured app_url. The /health endpoint is exempt
// so external load balancers / uptime checks can probe via the raw
// listen addr (e.g. http://10.0.0.5:9425/health) without first knowing
// the public hostname. Empty app_url disables the check entirely (a
// fresh DB ships with the default localhost URL, so this is mainly a
// safety valve while the operator is bootstrapping).
func (s *Server) hostAllowlistHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		appURL := strings.TrimSpace(s.configsSvc.AppURL())
		if appURL == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := neturl.Parse(appURL)
		if err != nil || u.Host == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Compare host:port. Forwarded headers win when set so reverse
		// proxies that rewrite Host can still gate by the public name.
		got := r.Host
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			got = fh
		}
		if !hostMatches(got, u.Host) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostMatches compares request host against expected host. Both are
// normalised (lowercased, default ports stripped) so
// "localhost:9425" matches "localhost:9425" and "example.com" matches
// "example.com:80" when scheme implied port 80.
func hostMatches(got, expected string) bool {
	return strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(expected))
}

// Run starts the HTTP server. Cancel ctx to trigger a graceful
// shutdown; returns nil on clean stop or the error from
// ListenAndServe / Shutdown otherwise. CLI callers wrap with
// signal.NotifyContext; in-process callers (system tray) cancel from
// the UI.
func (s *Server) Run(ctx context.Context, port int) error {
	logger := zerolog.Ctx(ctx)
	addr := fmt.Sprintf(":%d", port)

	h := chainMiddleware(
		s.authMidd.Session(s.router),
		recoverHandler,
		loggerHandler(func(w http.ResponseWriter, r *http.Request) bool { return false }),
		s.appNameHandler,
		s.hostAllowlistHandler,
		realIPHandler,
		requestIDHandler,
	)

	httpSrv := http.Server{
		Addr:         addr,
		Handler:      h,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		// BaseContext propagates the caller's logger (tray injects
		// serverLogger here) into every request context. Without this,
		// r.Context() defaults to context.Background() and middleware's
		// log.Ctx() lookups fall back to the global logger — so HTTP
		// access logs land in app.log instead of server.log.
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		logger.Info().Msg("server is shutting down...")
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpSrv.SetKeepAlivesEnabled(false)
		shutdownErr <- httpSrv.Shutdown(sctx)
	}()

	fmt.Printf("\n  ✓ %s is running\n", s.configsSvc.AppName())
	fmt.Printf("  → URL: http://localhost:%d\n", port)
	if !s.configsSvc.AdminPasswordChanged() {
		// Tray pipes stdout to app.log, so printing plaintext password
		// here would leak it to disk. Headless / CLI gets the full
		// banner (operator might be reading from a journal, no GUI).
		if os.Getenv("WICK_TRAY") != "1" {
			appName := strings.TrimSpace(os.Getenv("APP_NAME"))
			if info, ok := initcreds.Read(appName); ok {
				fmt.Printf("  → Email:            %s\n", info.Email)
				fmt.Printf("  → Default password: %s\n", info.Password)
			}
		}
		fmt.Printf("\n  ⚠ WARNING: Change the default password at http://localhost:%d/profile/setup\n\n", port)
	} else {
		fmt.Println()
	}
	logger.Info().Msgf("server serving on port %d", port)
	err := httpSrv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	if e := <-shutdownErr; e != nil {
		return fmt.Errorf("shutdown: %w", e)
	}
	logger.Info().Msg("server stopped")
	return nil
}

// RunMCPStdio initialises only the connector layer (DB + connectors
// bootstrap) and serves the MCP JSON-RPC protocol over stdin/stdout.
// Intended for local clients that spawn wick as a child process (Claude
// Desktop, Cursor, etc.). No auth — all connectors are visible as a
// synthetic local-admin identity.
func RunMCPStdio(version, commit, buildTime string) {
	// When spawned by an MCP client (Claude Desktop, Cursor, etc.) the
	// working directory is the client's, not the project root. Chdir to
	// the project root (parent of the bin/ dir) so .env and wick.db
	// resolve correctly, then reload .env before config.Load().
	if exe, err := os.Executable(); err == nil {
		projectRoot := filepath.Dir(filepath.Dir(filepath.Clean(exe)))
		if err := os.Chdir(projectRoot); err == nil {
			_ = godotenv.Load()
		}
	}

	cfg := config.Load()

	db := postgres.NewGORM(cfg.Database)
	postgres.Migrate(db)

	// Bootstrap the configs service even in stdio mode — we don't
	// expose it over HTTP here, but the encrypted-fields layer pulls
	// the master key from it and the rest of the connector dispatch
	// path expects encrypt/decrypt to behave the same as in HTTP.
	configsSvc := configs.NewService(db)
	if err := configsSvc.Bootstrap(context.Background()); err != nil {
		log.Fatal().Msgf("configs bootstrap: %s", err.Error())
	}
	encSvc, err := enc.New(configsSvc.EncryptionKey())
	if err != nil {
		log.Fatal().Msgf("enc init: %s", err.Error())
	}
	configsSvc.SetEncryptor(encSvc)

	connSvc := connectors.NewServiceFromDB(db)
	connSvc.SetEnc(encSvc)
	connSvc.SetConfigs(configsSvc)

	// Stdio mode also needs wickmanager so the LLM can introspect
	// wick configs over the same stdio link. Jobs / tools surface
	// degrade to "no rows" because we don't run the manager service
	// in stdio — that's intentional, the LLM can still read app vars
	// and connector configs which is the common ask.
	authSvc := login.NewService(db, cfg.App.AdminEmails)
	jobsSvc := manager.NewServiceFromDB(db)
	jobsSvc.SetConfigReader(configsSvc)
	connectors.Register(wickmanager.Module(wickmanager.Deps{
		Configs:    configsSvc,
		Connectors: connSvc,
		Jobs:       jobsSvc,
		Login:      authSvc,
		AppName:    strings.TrimSpace(os.Getenv("APP_NAME")),
	}))

	if err := connSvc.Bootstrap(context.Background(), connectors.All()); err != nil {
		log.Fatal().Msgf("connectors bootstrap: %s", err.Error())
	}

	// Bind the stdio context to the oldest real admin user so wick_enc_
	// tokens minted here decrypt under that admin's session in the web
	// UI. Per-user keys are HKDF(masterKey, salt=user.ID); a synthetic
	// "local" salt would produce tokens nobody can reverse via /tools/
	// encfields. Fall back to the synthetic id only on a fresh DB with
	// no admin yet.
	localAdmin := &entity.User{ID: "local", Role: entity.RoleAdmin}
	if u, err := authSvc.FirstAdmin(context.Background()); err == nil && u != nil {
		localAdmin = u
	}
	ctx := login.WithUser(context.Background(), localAdmin, nil)

	root, _ := os.Getwd()
	mcp.NewHandler(connSvc).
		WithBuildInfo(version, commit, buildTime).
		WithWickRoot(root).
		WithAppURL(configsSvc.AppURL).
		ServeStdioOS(ctx)
}

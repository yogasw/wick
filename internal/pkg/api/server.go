package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yogasw/wick/internal/admin"
	"github.com/yogasw/wick/internal/bookmark"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/health"
	"github.com/yogasw/wick/internal/home"
	"github.com/yogasw/wick/internal/jobrunner"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/sso"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/web"

	"github.com/rs/zerolog/log"
)

func NewServer() *Server {
	cfg := config.Load()

	db := postgres.NewGORM(cfg.Database)
	postgres.Migrate(db)

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
	authSvc.BootstrapAdmin(context.Background(), cfg.App.AdminPassword)

	// ── Jobs (background workers) ────────────────────────────────
	jobsSvc := manager.NewServiceFromDB(db)
	jobsSvc.SetConfigReader(configsSvc)
	if err := jobsSvc.Bootstrap(context.Background(), jobs.All()); err != nil {
		log.Fatal().Msgf("jobs bootstrap: %s", err.Error())
	}

	// ── Connectors (LLM-facing via MCP) ──────────────────────────
	// Register the code-side definitions for dispatch and auto-seed
	// one DB row per Key on first boot. The MCP/admin surfaces wire
	// up against this Service in later phases.
	connectorsSvc := connectors.NewServiceFromDB(db)
	if err := connectorsSvc.Bootstrap(context.Background(), connectors.All()); err != nil {
		log.Fatal().Msgf("connectors bootstrap: %s", err.Error())
	}
	_ = connectorsSvc

	// Resolve every tool meta up front — wick stamps the mount path
	// from meta.Key so modules never have to.
	var allItems []tool.Tool
	for _, m := range modules {
		meta := m.Meta
		meta.Path = "/tools/" + meta.Key
		allItems = append(allItems, meta)
	}

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

	managerHandler := manager.NewHandler(jobsSvc, configsSvc, allItems)

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

	// ── Admin ────────────────────────────────────────────────────
	adminHandler := admin.NewHandler(db, allItems, configsSvc, ssoSvc, jobsSvc)

	// ── Shared services ─────────────────────────────────────────
	tagsSvc := tags.NewService(db)
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

	// Auth routes: /auth/login, /auth/callback, /auth/logout, /auth/pending
	authHandler.Register(r, authMidd)

	// Admin routes: /admin, /admin/tools, /admin/configs, /admin/configs/sso, ...
	adminHandler.Register(r, authMidd)

	// Bookmark API (auth-gated inside)
	bookmarkHandler.Register(r, authMidd)

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

func (s *Server) Run(port int) {
	addr := fmt.Sprintf(":%d", port)

	h := chainMiddleware(
		s.authMidd.Session(s.router),
		recoverHandler,
		loggerHandler(func(w http.ResponseWriter, r *http.Request) bool { return false }),
		s.appNameHandler,
		realIPHandler,
		requestIDHandler,
	)

	httpSrv := http.Server{
		Addr:         addr,
		Handler:      h,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-quit
		log.Info().Msg("server is shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpSrv.SetKeepAlivesEnabled(false)
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Fatal().Err(err).Msg("could not gracefully shutdown the server")
		}
		close(done)
	}()

	fmt.Printf("\n  ✓ %s is running\n", s.configsSvc.AppName())
	fmt.Printf("  → URL: http://localhost:%d\n", port)
	if !s.configsSvc.AdminPasswordChanged() {
		fmt.Printf("  → Email:    admin@admin.com\n")
		fmt.Printf("  → Password: admin\n")
		fmt.Printf("\n  ⚠ WARNING: Change the default password at http://localhost:%d/profile\n\n", port)
	} else {
		fmt.Println()
	}
	log.Info().Msgf("server serving on port %d", port)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err).Msgf("could not listen on %s", addr)
	}
	<-done
	log.Info().Msg("server stopped")
}

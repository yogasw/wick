package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof" // opt-in profiling endpoints, served on loopback only (see WICK_PPROF in Run)
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/admin"
	"github.com/yogasw/wick/internal/agents/agentctl"
	"github.com/yogasw/wick/internal/agents/askuser"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	channelsetup "github.com/yogasw/wick/internal/agents/channels/setup"
	slackch "github.com/yogasw/wick/internal/agents/channels/slack"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentevent "github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
	agentgate "github.com/yogasw/wick/internal/agents/gate"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	agentproject "github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/providersync"
	agentregistry "github.com/yogasw/wick/internal/agents/registry"
	agentsession "github.com/yogasw/wick/internal/agents/session"
	agentskills "github.com/yogasw/wick/internal/agents/skills"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"
	wf "github.com/yogasw/wick/internal/agents/workflow"
	wfguard "github.com/yogasw/wick/internal/agents/workflow/guard"
	wfnodes "github.com/yogasw/wick/internal/agents/workflow/nodes"
	wfsetup "github.com/yogasw/wick/internal/agents/workflow/setup"
	wfstate "github.com/yogasw/wick/internal/agents/workflow/state"
	wftrigger "github.com/yogasw/wick/internal/agents/workflow/trigger"
	"github.com/yogasw/wick/internal/agents/workflow/wftest"
	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/bookmark"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	customconn "github.com/yogasw/wick/internal/connectors/custom"
	customconnector "github.com/yogasw/wick/internal/connectors/customconnector"
	"github.com/yogasw/wick/internal/connectors/notifications"
	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/connectors/wickmanager"
	wfconn "github.com/yogasw/wick/internal/connectors/workflow"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/health"
	"github.com/yogasw/wick/internal/home"
	"github.com/yogasw/wick/internal/initcreds"
	"github.com/yogasw/wick/internal/jobrunner"
	"github.com/yogasw/wick/internal/jobs"
	connectorrunspurge "github.com/yogasw/wick/internal/jobs/connector-runs-purge"
	providerstorageretention "github.com/yogasw/wick/internal/jobs/provider-storage-retention"
	providerstoragesync "github.com/yogasw/wick/internal/jobs/provider-storage-sync"
	sessionconfigpurge "github.com/yogasw/wick/internal/jobs/session-config-purge"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/internal/metrics"
	"github.com/yogasw/wick/internal/oauth"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/pkg/pwa"
	"github.com/yogasw/wick/internal/pkg/spa"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/processctl"
	"github.com/yogasw/wick/internal/sso"
	"github.com/yogasw/wick/internal/startupscript"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/internal/tools"
	agentstool "github.com/yogasw/wick/internal/tools/agents"
	encfieldstool "github.com/yogasw/wick/internal/tools/encfields"
	providerstoragetool "github.com/yogasw/wick/internal/tools/provider-storage"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/web"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// genMCPInternalToken returns a random per-boot secret (in-memory only)
// authenticating agent spawns to the loopback MCP server.
func genMCPInternalToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// wfListerAdapter wraps a workflow service.Service to satisfy admin.WorkflowLister.
type wfListerAdapter struct {
	svc interface {
		List() ([]string, error)
		Load(id string) (wf.Workflow, error)
	}
}

func (a wfListerAdapter) List() ([]string, error) { return a.svc.List() }
func (a wfListerAdapter) LoadInfo(id string) (admin.WorkflowInfo, error) {
	w, err := a.svc.Load(id)
	if err != nil {
		return admin.WorkflowInfo{}, err
	}
	return admin.WorkflowInfo{Name: w.Name, CreatedBy: w.CreatedBy}, nil
}

func NewServer() *Server {
	cfg := config.Load()

	// Log the runtime mode so first-boot debugging is straightforward —
	// "stdout banner missing the password? logs going to the wrong file?"
	// usually traces back to whether WICK_TRAY=1 was set by the spawning
	// process. App / Server names are part of the line so per-app data
	// dir mismatches (APP_NAME unset, name typo) are obvious too.
	log.Info().
		Bool("tray", os.Getenv("WICK_TRAY") == "1").
		Str("app_name", appname.Resolve()).
		Msg("server: runtime mode")

	db := postgres.NewGORM(cfg.Database)
	postgres.Migrate(db)

	syncMgr := providersync.New(db)

	// Startup restore is deferred to after configsSvc.Bootstrap so the
	// verbose_logs config row exists when we read it (see below).

	// Built-in maintenance jobs whose RunFunc captures *gorm.DB are
	// registered here, after DB init, before validation + the jobs.All()
	// loops below. Mirrors the call in internal/pkg/worker.NewServer
	// so both processes share the same registry view.
	connectorrunspurge.Register(db)
	sessionconfigpurge.Register()
	providerstoragesync.Register(syncMgr)
	providerstorageretention.Register(syncMgr)

	// Static built-in modules every wick app gets by default — agents
	// tool plus the github / httprest connectors. cmd/lab additionally
	// registers Lab samples (convert-text, crudcrud, sample-post) from
	// its own main; downstream user apps see only Builtins here.
	// All three are idempotent on Meta.Key: a downstream main.go can
	// re-register the same key without producing duplicates.
	tools.RegisterBuiltins()
	jobs.RegisterBuiltins()

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

	connectors.RegisterProfile(configsSvc.Profile())

	var pluginMgr *connplugin.Manager
	if mgr, n, err := connplugin.Load(connplugin.DefaultDir(), 5*time.Minute); err != nil {
		log.Warn().Err(err).Msg("connector plugins: load failed")
	} else if mgr != nil {
		log.Info().Int("plugins", n).Msg("connector plugins: loaded")
		pluginMgr = mgr
	}

	// Seed connector_oauth:slack rows for the generic connector OAuth framework.
	// The manager reads/writes these at owner="connector_oauth:slack" so they
	// are namespaced per-connector and don't collide with other connectors.
	if err := configsSvc.EnsureOwned(context.Background(), "connector_oauth:slack",
		entity.Config{Key: "client_id", Description: "Slack OAuth app Client ID"},
		entity.Config{Key: "client_secret", IsSecret: true, Description: "Slack OAuth app Client Secret"},
	); err != nil {
		log.Warn().Err(err).Msg("configs: seed connector_oauth:slack rows failed")
	}
	// Seed from env on first boot only — once the row exists in the DB
	// the admin UI is the only way to change it. When APP_NAME is unset,
	// fall back to the resolved app brand (appname.Resolve(): ldflag →
	// wick.yml → exe name) so each build (wick-agent, wick-lab, …) gets a
	// distinct display name + PWA identity out of the box instead of every
	// instance sharing the generic "Wick Mini Tools" default — which made
	// two local instances collide on PWA install + cookie scope.
	if configsSvc.AppName() == configs.DefaultAppName {
		seedName := cfg.App.Name
		if seedName == "" {
			seedName = appname.Resolve()
		}
		if seedName != "" && seedName != configs.DefaultAppName {
			if err := configsSvc.Set(context.Background(), configs.KeyAppName, seedName); err != nil {
				log.Warn().Msgf("seed app_name: %s", err.Error())
			}
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
		appName := appname.Resolve()
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

	// Boot force-restore: DB is source of truth on first start after a
	// container restart (no-volume env). Skipped when the
	// provider-storage-sync job is disabled — multi-server setups run
	// only one syncing instance; the others must not restore over a
	// file system they don't own.
	//
	// Runs in a goroutine so a large source set (thousands of files)
	// doesn't block server startup. The HTTP layer comes up immediately;
	// restore progress is visible in logs (percentage ticks) and the
	// realtime watcher is armed only after restore completes so it
	// doesn't race the restore writes back into the DB.
	// bootReady gates the HTTP surface behind a "Booting…" page until the
	// async restore + registry reload have finished (see bootGateHandler).
	// Initialised false; the restore goroutine — spawned below, after the
	// agents registry exists so it can reload it — flips it true. When the
	// restore is skipped (env kill switch / disabled job) we flip it true
	// immediately, since there is nothing to wait for.
	// bootGate holds the HTTP surface behind a "Booting…" page until every
	// registered async step finishes. Starts in the "starting" phase; the
	// restore goroutine registers itself and advances the phase to
	// "restoring" before the long file copy.
	bootGate := NewBootGate("starting")

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
	if err := pwa.EnsurePushConfig(context.Background(), configsSvc); err != nil {
		log.Warn().Err(err).Msg("notification config bootstrap failed")
	}
	pushSvc := pwa.NewPushService(db, configsSvc)
	// The encfields tool resolves its cipher through a package
	// singleton — built-in tools register from cmd/lab before the DB
	// or enc service exist, so a static Register signature is the
	// cost of doing business. Set once here, before any tool route
	// is mountable.
	encfieldstool.SetService(encSvc)

	// ── Agents (AI agent sessions / pool) ────────────────────────────
	// Bootstrap reads or creates the on-disk layout (~/.wick/agents/) and
	// loads the in-memory registry. Pool is wired with the production
	// ClaudeFactory and the SSE event broadcaster.
	agentsStorageCfg := agentconfig.StorageConfig{
		BaseDir:          configsSvc.GetOwned("agents", "base_dir"),
		DefaultProjectID: configsSvc.GetOwned("agents", "default_project_id"),
	}
	agentsLayout := agentconfig.NewLayout(agentconfig.ResolveBaseDir(agentsStorageCfg))
	agentsMgr, agentsBootErr := agentregistry.Bootstrap(agentsLayout)
	if agentsBootErr != nil {
		log.Fatal().Msgf("agents bootstrap: %s", agentsBootErr.Error())
	}
	agentsBcast := agentstool.NewBroadcaster()
	// syncSessionMeta reloads one session into the in-memory registry and
	// broadcasts its current title + status over SSE so open tabs update
	// live. Used by wick_set_title (HTTP, in-process) and — relayed via
	// the agentctl refresh_session op — by stdio MCP processes that
	// mutated session meta on disk. Without the broadcast the sidebar
	// would only catch up on the next page load.
	syncSessionMeta := func(sessionID string) {
		if err := agentsMgr.RefreshSession(sessionID); err != nil {
			return
		}
		if sess, ok := agentsMgr.Registry().Session(sessionID); ok {
			agentsBcast.PublishSessionMeta(sessionID, sess.Meta.Label, sess.Meta.TitleCustom, string(sess.Meta.Status))
		}
	}
	agentsSpawnLogger := provider.NewSpawnLogger(agentsLayout.BaseDir)
	// Trim any backlog of spawn logs from before pruning existed so the
	// dir is bounded immediately, not only after the next spawn.
	_ = agentsSpawnLogger.Prune(provider.MaxSpawnLogs)

	// Boot restore. Deferred to here (rather than the jobs-bootstrap block
	// above) so the goroutine can capture agentsMgr and reload the registry
	// from disk once files land — otherwise the registry, loaded synchronously
	// at agentregistry.Bootstrap above, scans before restore has written the
	// session/project folders and the sidebar comes up empty. We always log
	// WHY a restore did or didn't run so a "files not restored" report can be
	// diagnosed from logs alone (env kill switch, missing job row, disabled job).
	{
		syncJob, jobErr := jobsSvc.GetJob(context.Background(), providerstoragesync.Key)
		switch {
		case cfg.App.ProviderSyncDisable:
			log.Info().Bool("env_disable", true).
				Msg("providersync: boot restore skipped — WICK_PROVIDERSYNC_DISABLE=true on this instance")
			bootGate.MarkReady("restore skipped: env disable")
		case jobErr != nil:
			log.Warn().Err(jobErr).Str("job", providerstoragesync.Key).
				Msg("providersync: boot restore skipped — could not read sync job row")
			bootGate.MarkReady("restore skipped: job row read error")
		case !syncJob.Enabled:
			log.Info().Str("job", providerstoragesync.Key).Bool("job_enabled", false).
				Msg("providersync: boot restore skipped — sync job is disabled (enable it in Jobs UI to restore on boot)")
			bootGate.MarkReady("restore skipped: job disabled")
		default:
			verboseRestore := configsSvc.GetOwned("provider-storage", "verbose_logs") == "true"
			watcherOn := configsSvc.GetOwned(providerstoragesync.Key, providerstoragesync.CfgWatcherStatus) == "true"
			debounce, _ := strconv.Atoi(configsSvc.GetOwned(providerstoragesync.Key, providerstoragesync.CfgWatcherDebounceMs))
			bootGate.Register("provider-restore")
			go func() {
				// Done on every exit path — a restore failure must not strand
				// the server behind the gate forever.
				defer bootGate.Done("provider-restore")

				// Advance the gate label to the long-running phase.
				bootGate.SetPhase("restoring")

				log.Info().Bool("job_enabled", true).Bool("watcher", watcherOn).
					Msg("providersync: starting boot restore in background — gate page is up, restore continues")

				// Re-parent orphan rows: rewires parent_id from rel_path so that
				// listChildren works even when an ancestor row was previously deleted
				// (drive-letter row rotation, etc.). Idempotent.
				if n, err := postgres.RepairProviderStorageTree(db); err != nil {
					log.Warn().Err(err).Msg("providersync: repair provider_storage tree failed")
				} else if n > 0 {
					log.Info().Int("rows", n).Msg("providersync: repaired orphan provider_storage parent_id")
				}

				if err := syncMgr.RestoreAllForce(context.Background(), verboseRestore); err != nil {
					log.Warn().Err(err).Msg("providersync: startup restore failed")
				}

				// Restore wrote session/project files straight to disk, bypassing
				// the registry mutators — rescan so the sidebar reflects them.
				if err := agentsMgr.Registry().Reload(); err != nil {
					log.Warn().Err(err).Msg("providersync: registry reload after boot restore failed")
				} else {
					log.Info().Msg("providersync: boot restore complete, registry reloaded")
				}

				if watcherOn {
					if err := syncMgr.EnsureWatcher(context.Background(), debounce); err != nil {
						log.Warn().Err(err).Msg("providersync: watcher start failed")
					}
				}
			}()
		}
	}

	// One-shot migration: the deprecated agents.bypass_permissions checkbox
	// folded into the new GateConfig.PermissionMode dropdown. When the
	// legacy row exists, map its boolean into the new key and drop the
	// old one so the UI only shows the current control.
	if legacy := configsSvc.GetOwned("agents", "bypass_permissions"); legacy != "" {
		mode := "on"
		if legacy == "true" {
			mode = "bypass"
		}
		if cur := configsSvc.GetOwned("agents", "permission_mode"); cur == "" {
			if err := configsSvc.SetOwned(context.Background(), "agents", "permission_mode", mode); err != nil {
				log.Warn().Err(err).Msg("agents: migrate bypass_permissions → permission_mode")
			}
		}
		if err := configsSvc.DeleteOwnedKey(context.Background(), "agents", "bypass_permissions"); err != nil {
			log.Warn().Err(err).Msg("agents: drop legacy bypass_permissions row")
		}
	}

	// Resolve the gate binary up front: sibling-of-executable first, embedded
	// extract as backup, PATH lookup as last resort. Failure is non-fatal —
	// gate stays disabled and pool falls back to whitelist-only mode.
	gateConfigEnabled := true
	if v := configsSvc.GetOwned("agents", "gate_enabled"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			gateConfigEnabled = b
		}
	}
	resolvedGateBin, gateSource, gateBinErr := agentgate.ResolveGateBinaryWithSource(filepath.Join(agentsLayout.BaseDir, "_gate-bin"))
	gateStatus := agentstool.GateStatus{
		Enabled: gateBinErr == nil && gateConfigEnabled,
		Binary:  resolvedGateBin,
		Source:  gateSource,
	}
	switch {
	case !gateConfigEnabled:
		gateStatus.Reason = "disabled via agents.gate_enabled"
		log.Info().Msg("agents gate disabled via config")
	case gateBinErr != nil:
		gateStatus.Reason = gateBinErr.Error()
		log.Warn().Msgf("agents gate disabled: %s", gateBinErr.Error())
	}
	var agentsPool *agentpool.Pool
	channelReg := agentchannels.NewRegistry()
	// Per-boot secret: agent spawns use it to reach the live MCP server
	// over loopback instead of cold-starting `wick mcp serve` per run.
	mcpInternalToken := genMCPInternalToken()
	agentsFactory := &agentpool.ClaudeFactory{
		Layout:      agentsLayout,
		RecordRaw:   false,
		MCPToken:    mcpInternalToken,
		SpawnLogger: agentsSpawnLogger,
		OnEvent: func(sid, name string, ev agentevent.AgentEvent) {
			agentsBcast.Publish(sid, name, ev)
			channelReg.DispatchAgentEvent(sid, ev)
		},
		OnExit: func(sid, name string, reason provider.ExitReason) {
			// OnExit fires from the agent reader goroutine — no HTTP ctx
			// in scope here, so request_id is not available. Pool's
			// onAgentExit (called via HandleExit) will use the spawn-time
			// logger it stashed in runEntry, which DOES carry request_id.
			log.Debug().
				Str("component", "lifecycle").
				Str("session", sid).
				Str("agent", name).
				Int("reason", int(reason)).
				Msg("OnExit: publishing synthetic Done + handing to pool")
			agentsPool.HandleExit(sid, name, reason)
			doneEv := agentevent.AgentEvent{Type: agentevent.Done}
			agentsBcast.Publish(sid, name, doneEv)
			channelReg.DispatchAgentEvent(sid, doneEv)
		},
	}
	maxConc := 2
	// n >= 0 so an explicit 0 (unlimited) is honoured; only a parse error
	// or absent key falls back to the default of 2.
	if n, err := strconv.Atoi(configsSvc.GetOwned("agents", "max_concurrent")); err == nil && n >= 0 {
		maxConc = n
	}
	idleSec := 120
	if n, err := strconv.Atoi(configsSvc.GetOwned("agents", "idle_timeout_sec")); err == nil && n > 0 {
		idleSec = n
	}
	killAfterIdleSec := 0
	if n, err := strconv.Atoi(configsSvc.GetOwned("agents", "kill_after_idle_sec")); err == nil && n >= 0 {
		killAfterIdleSec = n
	}

	agentsFactory.PermissionModeLoader = func() string {
		// Gate master switch off → every sub-policy snaps to its
		// unguarded default; for permission that means "bypass".
		if configsSvc.GetOwned("agents", "gate_enabled") != "true" {
			return "bypass"
		}
		mode := configsSvc.GetOwned("agents", "permission_mode")
		if mode == "" {
			mode = "on"
		}
		return mode
	}
	agentsFactory.SystemPromptLoader = func() string {
		return configsSvc.GetOwned("agents", "system_prompt")
	}
	agentsFactory.TraceInlineKBLoader = func() int {
		v, _ := strconv.Atoi(configsSvc.GetOwned("agents", "trace_event_inline_kb"))
		return v
	}
	agentsFactory.TraceEventMaxKBLoader = func() int {
		v, _ := strconv.Atoi(configsSvc.GetOwned("agents", "trace_event_max_kb"))
		return v
	}

	// syncSharedSpec rewrites the shared spec.json on every spawn so
	// allowed_cmds edits take effect without a server restart.
	// AutoApproved entries are preserved from disk.
	syncSharedSpec := func() error {
		rules := parseGateRules(configsSvc.GetOwned("agents", "allowed_cmds"))
		spec, _ := agentgate.LoadSpec(appname.Resolve())
		spec.Rules = rules
		return agentgate.WriteSharedSpec(appname.Resolve(), spec)
	}
	// gateSocketOK is set to true only after approvalMgr.Start() succeeds.
	// GateLoader checks this so a failed socket bind doesn't let spawns
	// write hooks that would later fail-open on every tool call.
	var gateSocketOK bool
	agentsFactory.GateLoader = func() *agentpool.GateConfig {
		if configsSvc.GetOwned("agents", "gate_enabled") != "true" {
			return nil
		}
		if resolvedGateBin == "" {
			return nil
		}
		if !gateSocketOK {
			return nil
		}
		_ = syncSharedSpec()
		log.Debug().Int("rules", 0).Msg("agents: gate active for spawn")
		return &agentpool.GateConfig{
			GateBinary:   resolvedGateBin,
			AppName:      appname.Resolve(),
			DefaultScope: agentsLayout.ProjectsDir(),
		}
	}
	preemptIdle := configsSvc.GetOwned("agents", "preempt_idle") != "false"
	agentsPool = agentpool.New(agentpool.PoolConfig{
		MaxConcurrent:    maxConc,
		IdleTimeout:      time.Duration(idleSec) * time.Second,
		KillAfterIdle:    time.Duration(killAfterIdleSec) * time.Second,
		PreemptIdle:      preemptIdle,
		Layout:           agentsLayout,
		Factory:          agentsFactory,
		DefaultProjectID: agentsStorageCfg.DefaultProjectID,
		OnSessionCreated: func(s agentsession.Session) {
			agentsMgr.Register(s)
		},
		OnAgentAdded: func(sessionID string) {
			_ = agentsMgr.RefreshSession(sessionID)
		},
		OnSessionMeta: syncSessionMeta,
		OnLifecycle: func(ev agentpool.LifecycleEvent) {
			log.Ctx(ev.Ctx).Debug().
				Str("component", "lifecycle").
				Str("session", ev.SessionID).
				Str("agent", ev.AgentName).
				Str("lifecycle", ev.Lifecycle).
				Int("pid", ev.PID).
				Msg("lifecycle: broadcasting to SSE")
			prov := ev.ProviderType
			if ev.ProviderName != "" && ev.ProviderName != ev.ProviderType {
				prov = ev.ProviderType + "/" + ev.ProviderName
			}
			agentsBcast.PublishLifecycle(ev.Ctx, ev.SessionID, ev.AgentName, ev.Lifecycle, prov, ev.PID)
			// Broadcast updated pool stats to Providers page global
			// subscribers. MUST run async: this hook can fire while the
			// pool already holds p.mu (e.g. onAgentExit → MarkKilled →
			// lifecycle hook), and broadcastPoolStats calls
			// pool.ActiveSnapshot() which re-locks p.mu — a synchronous
			// call would self-deadlock and freeze every pool-touching
			// request (the "agents page hangs" bug).
			go broadcastPoolStats(agentsBcast, agentsPool)
			// Fan out a push to opt-in subscribers when the agent
			// goes idle (the "your turn is back" moment). Other
			// transitions are skipped — see dispatchLifecyclePush.
			dispatchLifecyclePush(ev.Ctx, pushSvc, agentsMgr, agentsLayout, ev)
		},
	})
	// Wire the hook writer so Manager injects .claude/settings.local.json
	// into every workspace on create or switch. The loader re-reads gate config
	// on each call so UI toggles take effect immediately without a restart.
	agentsMgr.HookWriter = agentgate.HookWriter{}
	agentsMgr.GateBinLoader = func() string {
		if configsSvc.GetOwned("agents", "gate_enabled") != "true" {
			return ""
		}
		return resolvedGateBin
	}

	agentstool.SetManager(agentsMgr)
	ui.SetNavParamsFn(func(ctx context.Context, u *entity.User) ui.NavParams {
		return ui.NavParams{
			CanSeeAgents: authSvc.CanAccessTool(ctx, u, "/tools/agents", entity.VisibilityPrivate),
		}
	})
	agentstool.SetPool(agentsPool)
	agentstool.SetBroadcaster(agentsBcast)
	agentstool.SetLayout(agentsLayout)
	agentstool.SetSpawnLogger(agentsSpawnLogger)
	agentstool.SetConfigs(configsSvc)
	agentstool.SetAuth(authSvc)
	go agentstool.AutoInstallMCP()
	agentstool.SetDB(db)
	agentstool.SetChannelRegistry(channelReg)
	agentstool.SetSyncManager(syncMgr)

	// ask_user Manager: blocks the calling agent over MCP until the
	// user clicks an option / types an answer in the web UI. SSE
	// fan-out goes through the broadcaster (one event per request +
	// one on resolve) so every open tab updates without polling.
	askUsersMgr := askuser.NewManager(askuser.Options{
		OnRequest: func(req askuser.AskRequest) {
			payload, _ := json.Marshal(req)
			agentsBcast.PublishAskUser(req.SessionID, req.AgentName, payload)
		},
		OnResolved: func(sessionID, requestID string) {
			agentsBcast.PublishAskUserResolved(sessionID, requestID)
		},
	})
	agentstool.SetAskUsers(askUsersMgr)
	// Bind the askuser unix socket so sibling processes (stdio MCP —
	// Claude Desktop/Cursor/Claude Code running `wick mcp serve`) can
	// route ask_user / wick_session_workspace asks into this process's
	// web UI. Same trust model as gate.sock: 0700 parent dir, no HTTP
	// auth. Best-effort — stdio asks degrade to an error without it.
	if askSock, err := askuser.ServeSocket(askuser.SocketPath(appname.Resolve()), askUsersMgr); err != nil {
		log.Warn().Err(err).Msg("agents: askuser socket bind failed — stdio MCP ask_user unavailable")
	} else {
		log.Info().Str("socket", askSock.SocketPath()).Msg("agents: askuser socket ready")
	}

	// Workflow stack — bundles every workflow subpkg into one Manager
	// and bootstraps the router with every workflow folder found on disk.
	wfMgr := wfsetup.New(agentsLayout)
	// Wire live registries — workflow nodes need real providers +
	// connectors to dispatch through, not empty registries. Skipping
	// these means `type: classify`/`type: connector` nodes fail at
	// runtime with "not registered".
	wfsetup.RegisterLiveConnectors(wfMgr.Connectors)
	// Channels are owned by the base channelReg — workflow registry just
	// wraps it and filters to channels that opt into the workflow surface
	// (Slack today; Telegram/REST nyusul). No duplicate registration.
	wfsetup.RegisterLiveChannels(wfMgr.Channels, channelReg)
	// Workflow guard mode — admin-configurable via agents settings.
	// off (default) skips Guard.Review entirely; warn surfaces
	// violations without blocking; block rejects Publish/Run.
	if mode := configsSvc.GetOwned("agents", "workflow_guard_mode"); mode != "" {
		wfMgr.WithGuardConfig(wfguard.Config{Mode: mode})
	}
	// Global concurrent-run cap across all workflows. 0 = unlimited.
	if n, err := strconv.Atoi(configsSvc.GetOwned("agents", "workflow_max_parallel_global")); err == nil && n >= 0 {
		wfMgr.Router.SetGlobalConcurrency(n)
	}
	// connectorsSvc is constructed further down (line ~517) — the
	// creds adapter is wired after that block so RowCreds can resolve
	// rows from the live connectors service. Search for
	// "SetRowCreds" below.
	if provs, perr := wfsetup.NewCLIProviders(); perr == nil {
		for _, p := range provs {
			wfMgr.WithProvider(p)
		}
	} else {
		log.Warn().Err(perr).Msg("workflow: provider adapter init failed")
	}
	// Bridge engine run events → SSE broadcaster so the editor can paint
	// per-node progress without polling state.json. The broadcaster
	// session key is "wf:<id>"; client opens
	// /stream?session=wf:<id>.
	// Optionally mirror events to Loki when workflow_loki_url is set.
	sseHook := agentstool.WorkflowEventHook(agentsBcast)
	lokiURL := configsSvc.GetOwned("agents", "workflow_loki_url")
	lokiLabels := configsSvc.GetOwned("agents", "workflow_loki_labels")
	lokiPusher := wfstate.NewLokiPusher(lokiURL, lokiLabels)
	wfMgr.Engine.SetEventHook(func(id, runID string, ev wf.RunEvent) {
		sseHook(id, runID, ev)
		if lokiPusher != nil {
			lokiPusher.Push(id, runID, ev)
		}
	})
	// Wire the shared agent pool + an adapter that translates
	// tools/agents.Broadcaster events into the slim AgentEvent the
	// workflow executor consumes. Pool-routed agent nodes go through
	// the FIFO queue + session reuse machinery — see
	// internal/planning/archive/workflow/pool.md.
	wfMgr.WithAgentRuntime(agentsPool, func(sessionID string) (<-chan wfnodes.AgentEvent, func()) {
		raw, unsub := agentsBcast.Subscribe(sessionID)
		out := make(chan wfnodes.AgentEvent, 256)
		go func() {
			defer close(out)
			for ev := range raw {
				out <- wfnodes.AgentEvent{Type: ev.Type, Data: ev.Data}
			}
		}()
		return out, unsub
	})
	// Promote in-memory data tables to the Postgres backend so rows
	// survive restart and multi-instance deploys see consistent state.
	// Must run before Start so executors register against the Pg
	// service, not MockService.
	wfMgr.WithDataTablesDB(db)
	// Swap the file-based workflow service for the DB-primary one. After
	// this call, workflow body + draft + history + tests live in SQL;
	// only state.json + env.json + runs/<id>/ stay on disk.
	wfMgr.WithDB(db)
	if err := wfMgr.Start(context.Background()); err != nil {
		log.Warn().Err(err).Msg("workflow bootstrap failed; workflows tab will be empty")
	}
	agentstool.SetWorkflowManager(wfMgr)
	agentstool.SetWorkflowEncService(encSvc)
	// Wire master-key decryptor into the engine so wick_enc_ workflow
	// secrets are decrypted at run time without a user context.
	wfMgr.Engine.Decryptor = encDecryptorFunc(encSvc.DecryptMaster)
	agentstool.SetDataTables(wfMgr.DataTables)
	providerstoragetool.SetSyncManager(syncMgr)
	// Restore writes session/project files straight to disk, bypassing the
	// registry's in-memory cache. Wire the reload hook so a restore refreshes
	// the sidebar immediately instead of waiting for the next boot.
	providerstoragetool.SetReloadRegistryHook(agentsMgr.Registry().Reload)
	provider.AppName = appname.Resolve()
	// Wire the auto-rescan toggle: provider package consults this
	// before triggering background stale-version re-probes. Defaults
	// true when configs row is empty.
	provider.SetAutoRescanLookup(func() bool {
		v := configsSvc.GetOwned("agents", "auto_rescan")
		return v != "false"
	})
	// Prime the persistent status cache once in the background so the
	// first load of /tools/agents/providers renders from cache instead
	// of waiting on three cold `--version` spawns. Subsequent boots
	// hit the cache directly until 24h staleness or a manual rescan.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = provider.RescanAll(ctx)
	}()

	// ── Gate: ApprovalManager (shared socket + initial spec.json) ────────
	// RouteByCWD maps the gate binary's working directory to the wick
	// session that owns that workspace so SSE events land in the right tab.
	approvalMgr, amErr := gate.NewApprovalManager(gate.ApprovalManagerOptions{
		AppName: appname.Resolve(),
		// Route by active pool sessions only — multiple sessions can share the
		// same workspace name, so iterating the registry (which includes idle
		// sessions) is non-deterministic and picks the wrong session. The active
		// pool is precise: only the running agent owns that CWD right now.
		RouteByCWD: func(cwd string) (string, bool) {
			cleanCWD := filepath.Clean(cwd)
			for _, entry := range agentsPool.ActiveSnapshot() {
				if entry.CWD == "" {
					continue
				}
				wsPath := filepath.Clean(entry.CWD)
				if cleanCWD == wsPath || strings.HasPrefix(cleanCWD, wsPath+string(filepath.Separator)) {
					return entry.SessionID, true
				}
			}
			return "", false
		},
		OnRequest: func(sessionID string, r gate.ApprovalRequest) {
			agentsBcast.PublishApprovalRequest(sessionID, r)
			channelReg.DispatchApprovalRequest(sessionID, r)
		},
		OnResolved: func(sessionID, requestID, decision string) {
			agentsBcast.PublishApprovalResolved(sessionID, requestID, decision)
			channelReg.DispatchApprovalResolved(sessionID, requestID, decision)
		},
	})
	if amErr != nil {
		log.Warn().Err(amErr).Msg("agents: gate ApprovalManager init failed — interactive approval disabled")
		gateStatus.Enabled = false
		gateStatus.Reason = amErr.Error()
	} else if _, err := approvalMgr.Start(); err != nil {
		log.Warn().Err(err).Msg("agents: gate socket bind failed — interactive approval disabled")
		gateStatus.Enabled = false
		gateStatus.Reason = "listener start: " + err.Error()
	} else {
		gateSocketOK = true
		// Write initial spec.json so the gate binary finds the whitelist
		// rules on the very first spawn before any agent has started.
		initialRules := parseGateRules(configsSvc.GetOwned("agents", "allowed_cmds"))
		if wsErr := gate.WriteSharedSpec(appname.Resolve(), gate.Spec{
			Rules:        initialRules,
			DefaultScope: agentsLayout.ProjectsDir(),
		}); wsErr != nil {
			log.Warn().Err(wsErr).Msg("agents: write initial spec.json failed")
		}
		agentstool.SetApprovals(approvalMgr)
		log.Info().Str("socket", approvalMgr.SocketPath()).Msg("agents: gate socket ready")
	}
	agentstool.SetGateStatus(gateStatus)

	// ── Channel registry: shared deps (session checker, approve fn) ──
	// Per-channel SendFunc is wired below with its own workspace lookup so
	// each channel can have its own configured workspace.
	channelReg.WithSessionChecker(agentsPool)
	if approvalMgr != nil {
		channelReg.WithApproveFn(func(channelName, sessionID, requestID, decision, matchKey string) error {
			ok, err := approvalMgr.Resolve(sessionID, requestID, decision, channelName, matchKey)
			if !ok && err == nil {
				return fmt.Errorf("approval request already resolved or timed out")
			}
			return err
		})
	}

	// sendFnFor returns a pool-dispatch closure that re-reads the project
	// binding from agent_channels on every send, so UI changes take effect
	// without a restart. One closure per channel type so projects can differ.
	sendFnFor := func(channelType string) agentchannels.SendFunc {
		raw := agentchannels.SendFunc(func(ctx context.Context, sessionID, agentName, source, role, text string) error {
			pid := ""
			// Per-request override (e.g. REST body `project`) wins, when it
			// names a real project; otherwise fall back to channel config.
			if ov := agentchannels.ProjectOverride(ctx); ov != "" && agentproject.Exists(agentsLayout, ov) {
				pid = ov
			}
			if pid == "" {
				if m, err := agentchannels.GetChannelConfigMap(db, channelType); err == nil {
					pid = m["project_id"]
				}
			}
			if pid == "" {
				if ids, err := agentproject.List(agentsLayout); err == nil && len(ids) == 1 {
					pid = ids[0]
				}
			}
			return agentsPool.SendWithProject(ctx, sessionID, agentName, source, role, text, pid)
		})
		return agentchannels.WrapSendFunc(raw, agentsLayout, agentsPool, func(sessionID, agentName, source, text string) {
			if err := agentsMgr.RefreshSession(sessionID); err != nil {
				log.Warn().Err(err).Str("session", sessionID).Msg("agents: refresh session after provider switch failed")
			}
			agentsBcast.PublishRaw(sessionID, agentName, "text_delta", text)
			agentsBcast.PublishRaw(sessionID, agentName, "done", "")
			channelReg.DispatchAgentEvent(sessionID, agentevent.AgentEvent{Type: agentevent.TextDelta, Text: text})
			channelReg.DispatchAgentEvent(sessionID, agentevent.AgentEvent{Type: agentevent.Done})
		})
	}

	// PAT service is needed by the REST channel (per-request Bearer auth).
	// Instantiated here so channelsetup.All can pass it in; the handler
	// further below reuses the same instance.
	tokensSvc := accesstoken.NewServiceFromDB(db)

	// One call wires every built-in channel: setup.All handles EnsureChannel,
	// config load, NewChannel, setters, and registry.Add per transport.
	// Adding a new channel = subpackage + composer in channels/setup; this
	// line never changes.
	channelStore := agentchannels.NewDBStore(db)
	channelStore.Configs = configsSvc
	channelsetup.All(channelReg, channelStore, sendFnFor, tokensSvc)

	// Wire each channel's workflow integration surface — registers
	// per-event + per-action descriptors and attaches the inbound
	// event sink that fires router.Dispatch. Per-channel calls so
	// telegram/rest can opt in independently as they grow workflow
	// surfaces.
	wfsetup.RegisterSlackIntegration(wfMgr.Integration, channelReg, wfMgr.Router, wfMgr.MCP.Pickers)

	// ── Connectors (LLM-facing via MCP) ──────────────────────────
	// Register the code-side definitions for dispatch and auto-seed
	// one DB row per Key on first boot. The MCP server below is the
	// runtime entry point for LLM clients.
	connectorsSvc := connectors.NewServiceFromDB(db)
	connectorsSvc.SetEnc(encSvc)
	connectorsSvc.SetConfigs(configsSvc)
	metricsRec := metrics.NewSimpleRecorder()
	connectorsSvc.SetMetrics(metricsRec)
	// Wire the connectors service into the agents tool so the session
	// Config tab can read connector field schemas + AllowSessionConfig.
	agentstool.SetConnectors(connectorsSvc)

	// Wire the agent factory's connector-catalog loader now that the
	// connectors service exists. The loader runs at every agent spawn,
	// listing only connector definitions whose Meta.Key has at least
	// one instance with status="ready" — so the model never sees a
	// connector the operator hasn't finished configuring. A 2s ctx
	// budget keeps a slow DB from stalling spawn.
	agentsFactory.ConnectorCatalogLoader = func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rows, err := connectorsSvc.List(ctx)
		if err != nil {
			return ""
		}
		ready := make(map[string]bool, len(rows))
		for _, row := range rows {
			if connectorsSvc.Status(row) == "ready" {
				ready[row.Key] = true
			}
		}
		return systemprompt.ConnectorCatalog(ready)
	}

	// Workflow connector executor needs row credentials resolved
	// from the connectors service — wire after the service is built.
	wfMgr.Connectors.SetRowCreds(wfsetup.ConnectorsCredsAdapter(connectorsSvc))
	// Run identity-gated connector ops as the workflow owner — headless
	// runs have no cookie session to stamp login.GetUser. See connector
	// node executor.
	wfMgr.Connectors.SetUserResolver(wfsetup.UserResolverAdapter(authSvc))

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

	// workflow is a built-in single-instance connector that exposes the
	// workflow engine's full Tier 1/2/3 MCP surface. Lets any AI client
	// (Claude Desktop, ChatGPT, Gemini) create/edit/test/run workflows
	// over MCP without native file access. Late-bound here because it
	// needs wfMgr.MCP — the resolved Ops bundle built during workflow
	// bootstrap above.
	if wfMgr != nil && wfMgr.MCP != nil {
		wfRunner := wftest.New(wfMgr.Engine, wfMgr.Service, wfMgr.Layout)
		connectors.Register(wfconn.ModuleWithRunner(wfMgr.MCP, wfRunner))
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
		AppName:    appname.Resolve(),
	}))
	connectors.Register(notifications.Module(notifications.Deps{
		DB:   db,
		Push: pushSvc,
	}))

	// Custom connectors: replay admin-built definitions from the DB
	// into the registry. MUST run before Bootstrap so custom modules
	// ride the same instance seeding, allItems, and tag passes as
	// built-ins. Tags are late-bound below (tagsSvc doesn't exist yet).
	customConnSvc := customconn.New(customconn.Deps{
		DB:         db,
		Connectors: connectorsSvc,
		Keys:       configsSvc,
		BaseURL:    configsSvc.AppURL,
	})
	// Paste-page AI tab: the provider list resolves live per render /
	// per parse, so adding or disabling a provider instance shows up
	// without a restart. Only structured-output-capable providers
	// qualify (NewProviderAIParser returns nil otherwise).
	customConnSvc.SetAIProviders(func() []customconn.AIProviderEntry {
		provs, perr := wfsetup.NewCLIProviders()
		if perr != nil {
			return nil
		}
		out := []customconn.AIProviderEntry{}
		for _, p := range provs {
			if ai := customconn.NewProviderAIParser(p); ai != nil {
				out = append(out, customconn.AIProviderEntry{Name: p.Name(), Parser: ai})
			}
		}
		return out
	})
	if err := customConnSvc.RegisterAllAtBoot(context.Background()); err != nil {
		log.Error().Err(err).Msg("custom connectors: boot registration failed")
	}
	// custom-connector is the management connector for custom defs:
	// the UI's create/update/re-sync/disable/delete lifecycle exposed
	// as admin-only MCP operations, so an LLM can build a connector
	// without the dashboard.
	connectors.Register(customconnector.Module(customconnector.Deps{
		Custom:     customConnSvc,
		Connectors: connectorsSvc,
	}))
	// wick_get lazily re-syncs custom MCP tool catalogs (throttled).
	connectorsSvc.SetCatalogRefresh(customConnSvc.RefreshIfStale)

	if err := connectorsSvc.Bootstrap(context.Background(), connectors.All()); err != nil {
		log.Fatal().Msgf("connectors bootstrap: %s", err.Error())
	}

	// Wire Slack user-token lookup via the connectors service.
	// SetTokenRefreshFn + RefreshTokenMap seeds the initial userID→token cache
	// by calling auth.test once per Slack connector row configured in
	// user_token mode. A background ticker keeps the cache fresh every 5
	// minutes so new connector rows are picked up without a restart.
	//
	// Note: OAuth flow (start/callback) has moved to the generic connector
	// manager at /manager/connectors/{key}/oauth/*. The Slack channel only
	// needs token-refresh wiring for the send-proxy feature.
	for _, ch := range channelReg.Channels() {
		if slackCh, ok := ch.(*slackch.Channel); ok {
			// Wire the refresh function so RefreshTokenMap can rebuild the map
			// from connector rows without a server restart.
			slackCh.SetTokenRefreshFn(func(ctx context.Context) map[string]string {
				return buildSlackUserTokenMap(ctx, connectorsSvc)
			})

			// Seed the initial cache synchronously (same behaviour as before,
			// but now stored in userTokenCache instead of a closed-over map).
			slackCh.RefreshTokenMap(context.Background())

			// ConnectorTokenFn is called on every cache miss. Returning false
			// signals resolveUserToken to trigger a background RefreshTokenMap
			// so subsequent requests will find the token in cache.
			slackCh.SetConnectorTokenFn(func(_ context.Context, _ string) (string, bool) {
				return "", false
			})

			// WickUserIDFn resolves a Slack user ID to a wick platform user ID
			// by scanning ConnectorAccount rows where ExternalUserID matches the
			// inbound Slack user ID and returning the stored WickUserID.
			slackCh.SetWickUserIDFn(func(ctx context.Context, slackUserID string) (string, bool) {
				rows, err := connectorsSvc.ListByKey(ctx, "slack")
				if err != nil {
					return "", false
				}
				for _, row := range rows {
					accs, err := connectorsSvc.ListAccounts(ctx, row.ID)
					if err != nil {
						continue
					}
					for _, acc := range accs {
						if acc.ExternalUserID == slackUserID && acc.WickUserID != "" {
							return acc.WickUserID, true
						}
					}
				}
				return "", false
			})

			// OwnerFn stamps the resolved wick user ID on the session.
			if agentsPool != nil {
				slackCh.SetOwnerFn(func(ctx context.Context, sessionID, userID string) {
					agentsPool.EnsureSessionOwner(ctx, sessionID, userID)
				})
			}

			// Background ticker: refresh every 5 minutes.
			go func(ch *slackch.Channel) {
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for range ticker.C {
					ch.RefreshTokenMap(context.Background())
				}
			}(slackCh)
		}
	}

	// ── Personal Access Tokens (MCP bearer auth) ─────────────────
	// tokensSvc instantiated earlier so the REST channel can reuse it.
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
	mcpHandler := mcp.NewHandler(connectorsSvc).
		WithAppURL(configsSvc.AppURL).
		WithDB(db).
		WithAskUser(askUsersMgr).
		WithAskUserPolicy(func(sessionID string) (bool, string) {
			// Resolved per session origin — independent of the master
			// command gate. ui/external → global agents.ask_user_mode;
			// slack/telegram/rest → that channel's own ask_user_mode.
			return askUserPolicy(db, configsSvc, agentsLayout, sessionID)
		}).
		WithPool(agentsPool, agentsLayout).
		WithRefreshSession(func(id string) error {
			syncSessionMeta(id)
			return nil
		})
	mcpAuth := mcp.NewAuthMiddleware(
		tokensSvc,
		authSvc,
		oauthSvc,
		strings.TrimRight(configsSvc.AppURL(), "/")+"/.well-known/oauth-protected-resource",
	).WithInternalToken(mcpInternalToken)

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
	// Late-bind tags into the custom-connector service and link the
	// per-def access tags ([custom:<key> filter, Connector, category])
	// onto existing instance rows. Idempotent — rows that already carry
	// tags keep their admin edits.
	customConnSvc.SetTags(tagsSvc)
	agentstool.SetTagsService(tagsSvc)
	agentstool.SetSkillStore(agentskills.NewStore(db))
	customConnSvc.EnsureInstanceTags(context.Background())
	// Connect MCP custom connectors before the gate lifts: boot
	// registered them without probing, this pass pulls each server's
	// live catalog now that configs (incl. oauth instance tokens) are
	// loaded — so the app opens with every MCP connector already
	// connected instead of racing the first wick_list.
	bootGate.Register("mcp-connectors")
	go func() {
		defer bootGate.Done("mcp-connectors")
		bootGate.SetPhase("connecting-mcp")
		customConnSvc.ResyncMCPAtBoot(context.Background())
	}()
	managerHandler := manager.NewHandler(jobsSvc, configsSvc, connectorsSvc, tagsSvc, authSvc, allItems)
	managerHandler.SetCustomConnectors(customConnSvc)
	if pluginMgr != nil {
		managerHandler.SetPluginResolver(pluginMgr)
	}

	// Build the hidden-key set from the "agents" module's seed. Any config
	// field tagged with `wick:"hidden"` is managed from a dedicated UI page
	// (Channels, Providers) and must not appear on the generic Settings page.
	agentsHidden := make(map[string]bool)
	for _, m := range modules {
		if m.Meta.Key == "agents" {
			for _, row := range m.Configs {
				if row.Hidden {
					agentsHidden[row.Key] = true
				}
			}
			break
		}
	}
	managerHandler.RegisterConfigDecorator("agents", func(rows []pkgentity.Config) []pkgentity.Config {
		projectIDs, _ := agentproject.List(agentsLayout)
		// Build "label::path" options for the allowed_cmds scope column and
		// "label::id" options for the default_project_id dropdown.
		var scopeOpts string
		var projectOpts string
		if len(projectIDs) > 0 {
			var scopeParts, projParts []string
			for _, id := range projectIDs {
				p, lerr := agentproject.Load(agentsLayout, id)
				if lerr != nil {
					continue
				}
				label := p.Meta.Name
				if label == "" {
					label = id
				}
				projParts = append(projParts, label+"::"+id)
				if path, err := agentproject.ResolvePath(agentsLayout, id); err == nil && path != "" {
					scopeParts = append(scopeParts, label+"::"+path)
				}
			}
			if len(scopeParts) > 0 {
				scopeOpts = strings.Join(scopeParts, "|")
			}
			if len(projParts) > 0 {
				projectOpts = strings.Join(projParts, "|")
			}
		}
		// Return only rows not managed by a dedicated UI page, with ColOptions injected.
		out := rows[:0]
		for _, r := range rows {
			if agentsHidden[r.Key] {
				continue
			}
			if r.Key == "allowed_cmds" && scopeOpts != "" {
				r.ColOptions = map[string]string{"scope": scopeOpts}
			}
			if r.Key == "default_project_id" {
				r.Options = projectOpts
			}
			out = append(out, r)
		}
		return out
	})

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

	// Register connectors as items. One module = one card path under
	// /manager/connectors/{key} where users see N rows for that
	// definition (one per credential set), each with a test panel and
	// enable/disable/duplicate actions. These entries stay in allItems
	// for the admin tag UI, access-control seeding, and wickmanager's
	// tool_list — but the home grid does NOT render them individually;
	// it shows a single "Connectors" launcher instead (see homeItems
	// below). The connector set is expected to grow large and every row
	// needs config before use, so per-connector home tiles only add noise.
	for _, cm := range connectors.All() {
		m := cm.Meta
		allItems = append(allItems, tool.Tool{
			Name:              m.Name,
			Description:       m.Description,
			Icon:              m.Icon,
			Path:              "/manager/connectors/" + m.Key,
			Category:          "connector",
			DefaultVisibility: entity.VisibilityPrivate,
			DefaultTags:       m.DefaultTags,
		})
	}

	// ── Admin ────────────────────────────────────────────────────
	skillsStore := agentskills.NewStore(db)
	var wfLister admin.WorkflowLister
	if wfMgr != nil {
		wfLister = wfListerAdapter{svc: wfMgr.Service}
	}
	adminHandler := admin.NewHandler(db, allItems, configsSvc, ssoSvc, jobsSvc, connectorsSvc, tokensSvc, oauthSvc, authSvc, agentsMgr.Registry(), wfLister, skillsStore)

	// ── Shared services ─────────────────────────────────────────
	bookmarkSvc := bookmark.NewService(db)
	bookmarkHandler := bookmark.NewHandler(bookmarkSvc)
	pushHandler := pwa.NewPushHandler(pushSvc, authSvc)

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
	// Home shows connectors as one launcher tile under the AI group
	// instead of one tile per definition. Derive a home-only item list:
	// drop the per-connector entries, append a single "Connectors" card
	// that deep-links to the /manager/connectors index page.
	homeItems := make([]tool.Tool, 0, len(allItems)+1)
	for _, t := range allItems {
		if t.Category == "connector" {
			continue
		}
		homeItems = append(homeItems, t)
	}
	connectorsTile := tool.Tool{
		Name:              "Connectors",
		Description:       "Browse and manage LLM-callable connectors that wrap external APIs.",
		Icon:              "🔌",
		Path:              "/manager/connectors",
		Category:          "connector",
		DefaultVisibility: entity.VisibilityPrivate,
		DefaultTags:       []tool.DefaultTag{tags.AI},
	}
	homeItems = append(homeItems, connectorsTile)
	// Seed the AI group tag on the launcher path so it lands in the AI
	// card next to Agents (the generic loop above only walks allItems).
	if err := tagsSvc.EnsureToolDefaultTags(context.Background(), connectorsTile.Path, connectorsTile.DefaultTags); err != nil {
		log.Error().Msgf("seed connectors launcher tag: %s", err.Error())
	}
	homeHandler := home.NewHandler(homeItems, authSvc, tagsSvc, bookmarkSvc)

	// ── Router ───────────────────────────────────────────────────
	r := http.NewServeMux()

	// Global SPA dev-reload SSE endpoint. No-op when WICK_DEV_REPO_ROOT is
	// unset — all Loader.New() calls auto-register their dist/ dirs.
	spa.RegisterGlobalHandler(r)

	// Health check endpoint — used by load balancers and uptime monitoring.
	r.Handle("GET /health", http.HandlerFunc(healthHandler.Check))
	// /boot-status reports readiness for the boot gate page. The gate
	// middleware answers this with {"ready":false} while booting; once
	// bootReady flips, requests fall through to here and get true.
	r.HandleFunc("GET /boot-status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ready":true}`)
	})

	// PWA manifest is dynamic — bakes the configured app name into the
	// installable identity so downstream "MyApp" doesn't install as
	// "wick". Registered before the static catch-all below.
	r.Handle("GET /public/manifest.json", http.HandlerFunc(pwa.ManifestHandler))

	// Service worker served from the root so its scope covers the whole
	// app — required for the PWA install prompt to appear.
	r.Handle("GET /sw.js", http.HandlerFunc(pwa.ServiceWorkerHandler))

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

	// Notification API (auth-gated inside)
	pushHandler.Register(r, authMidd)

	// Personal access tokens + MCP install — /profile/tokens, /profile/mcp.
	tokensHandler.Register(r, authMidd)

	// MCP JSON-RPC endpoint. Bearer-authed (PAT or OAuth access
	// token). Mounted on the cookie-bypass mux because LLM clients
	// carry a bearer header, not a session cookie — RequireAuth would
	// 302 them into /auth/login which they can't follow.
	//
	// Full Streamable HTTP transport: POST (JSON-RPC), GET (server→client
	// SSE channel the client needs to finish its handshake), DELETE
	// (session teardown). One wrapped handler dispatches by method.
	wrappedMCP := mcpAuth.Wrap(mcpHandler)
	r.Handle("POST /mcp", wrappedMCP)
	r.Handle("GET /mcp", wrappedMCP)
	r.Handle("DELETE /mcp", wrappedMCP)

	// Channel webhooks — public, no session auth (each channel enforces
	// integrity inside its handler, e.g. Slack HMAC). Mounted from
	// whichever channels implement HTTPHandlerProvider.
	for path, h := range channelReg.HTTPHandlers() {
		r.Handle(path, h)
	}

	// Workflow webhook triggers.
	// /webhook/  — dispatches against the published workflow copy.
	// /webhook-test/ — dispatches against the draft copy so operators
	// can test changes without publishing. Both accept optional HMAC
	// via X-Wick-Sig; path format: /{wf_id}/{slug}.
	if wfMgr != nil && wfMgr.Router != nil {
		r.Handle("/webhook/", wftrigger.NewWebhookHandler(wfMgr.Router))
		r.Handle("/webhook-test/", wftrigger.NewDraftWebhookHandler(wfMgr.Router, wfMgr.Service))
	}

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

	// Prometheus-compatible metrics scrape endpoint. Admin-only — bearer
	// token or session required so the endpoint is not public by default.
	r.Handle("GET /metrics", authMidd.RequireAdmin(metricsRec.Handler()))

	// Home
	r.Handle("/", http.HandlerFunc(homeHandler.RootRedirect))
	r.Handle("/launcher", http.HandlerFunc(homeHandler.Launcher))

	return &Server{router: r, configsSvc: configsSvc, authMidd: authMidd, agentsPool: agentsPool, agentsLayout: agentsLayout, syncSessionMeta: syncSessionMeta, channelReg: channelReg, db: db, gateBin: resolvedGateBin, jobsSvc: jobsSvc, wfMgr: wfMgr, bootGate: bootGate, pluginMgr: pluginMgr}
}

type Server struct {
	router       *http.ServeMux
	configsSvc   *configs.Service
	authMidd     *login.Middleware
	agentsPool   *agentpool.Pool
	agentsLayout agentconfig.Layout
	pluginMgr    *connplugin.Manager
	// syncSessionMeta reloads one session into the in-memory registry
	// and broadcasts its meta over SSE. Built in NewServer (where the
	// registry + broadcaster are in scope) and reused by Run to wire
	// the agentctl refresh_session handler. nil before agents boot.
	syncSessionMeta func(sessionID string)
	channelReg      *agentchannels.Registry
	db              *gorm.DB
	gateBin         string // resolved gate binary path; used for hook cleanup on shutdown
	jobsSvc         *manager.Service
	wfMgr           *wfsetup.Manager
	// bootGate tracks the async boot steps that must finish before the HTTP
	// surface opens. Until it lifts, bootGateHandler serves a lightweight
	// "Booting…" page (HTTP 503, auto refreshing) for every non-exempt
	// request, so users see progress instead of an empty sidebar or a
	// 502/503. /health stays exempt so load-balancer / k8s probes succeed
	// during the restore window. The gate lifts only once EVERY registered
	// step is Done — add a step by Register/Done, not by flipping a flag.
	bootGate *BootGate
}

// JobsSvc returns the manager.Service the API server owns. Exposed so
// the single-node `lab all` entrypoint can hand it to worker.RunScheduler
// — both the HTTP handlers and the cron loop then share one Service,
// avoiding the double-fire race two independent Services would have.
func (s *Server) JobsSvc() *manager.Service { return s.jobsSvc }

// startChannels starts every configured channel and launches the
// registry's hot-reload watcher. Replaces the per-channel watchSlack /
// watchTelegram methods — Reload semantics now live inside each
// channel's ConfigSource.
func (s *Server) startChannels(ctx context.Context) {
	s.channelReg.StartAll(ctx)
	go s.channelReg.WatchConfigs(ctx, 30*time.Second)
	if s.wfMgr != nil {
		go wfsetup.WatchWorkflows(ctx, s.wfMgr.Layout.WorkflowsDir(), s.wfMgr.Service, s.wfMgr.Router, s.wfMgr.Cron, s.wfMgr.ScheduleAt)
	}
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

// buildSlackUserTokenMap scans Slack connector rows configured in user_token
// mode, calls auth.test once per token to resolve the Slack user ID, and
// returns a map[slackUserID]xoxpToken. Called once at startup so live
// requests never pay an auth.test round-trip.
func buildSlackUserTokenMap(ctx context.Context, svc *connectors.Service) map[string]string {
	out := map[string]string{}
	rows, err := svc.ListByKey(ctx, "slack")
	if err != nil {
		log.Warn().Err(err).Msg("buildSlackUserTokenMap: ListByKey failed")
		return out
	}
	for _, row := range rows {
		cfgs := svc.LoadConfigs(row)
		if strings.TrimSpace(cfgs["auth_mode"]) != "user_token" {
			continue
		}
		token := strings.TrimSpace(cfgs["user_token"])
		if token == "" {
			continue
		}
		userID, err := slackch.AuthTestWithToken(ctx, token)
		if err != nil {
			log.Warn().Err(err).Str("connector_id", row.ID).
				Msg("buildSlackUserTokenMap: auth.test failed for row")
			continue
		}
		out[userID] = token
		log.Info().Str("user_id", userID).Str("connector_id", row.ID).
			Msg("slack: user token registered")
	}
	return out
}

// hostAllowlistHandler rejects requests whose Host header doesn't match
// the host of app_url or any entry in allowed_origins. The /health
// endpoint is exempt so external load balancers / uptime checks can
// probe via the raw listen addr (e.g. http://10.0.0.5:9425/health)
// without first knowing the public hostname. Empty app_url AND empty
// allowed_origins disables the check entirely (a fresh DB ships with
// the default localhost URL, so this is mainly a safety valve while
// the operator is bootstrapping).
func (s *Server) hostAllowlistHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		// The internal agent MCP connects to /mcp over loopback
		// (127.0.0.1:<port>); exempt that so shared-MCP works even when
		// app_url is a remote/tunnel domain. Scoped to /mcp + loopback Host
		// only — other paths/hosts still enforce the allowlist. Safe:
		// DNS-rebinding attacks send the attacker's own domain as Host,
		// never a loopback address, and /mcp stays Bearer-authed.
		if mcpLoopbackExempt(r.URL.Path, r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		allowed := collectAllowedHosts(s.configsSvc.AppURL(), s.configsSvc.AllowedOrigins())
		if len(allowed) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		// Compare host:port. Forwarded headers win when set so reverse
		// proxies that rewrite Host can still gate by the public name.
		got := r.Host
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			got = fh
		}
		for _, exp := range allowed {
			if hostMatches(got, exp) {
				next.ServeHTTP(w, r)
				return
			}
		}
		log.Warn().Str("request_host", got).Strs("allowed_hosts", allowed).Msg("hostAllowlist: forbidden — host mismatch")
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

// mcpLoopbackExempt reports whether the request is the internal agent MCP
// path reached over loopback — /mcp from 127.0.0.1 / ::1 / localhost. Such
// requests bypass the host allowlist so shared-MCP works regardless of
// app_url. Scoped to /mcp only.
func mcpLoopbackExempt(path, host string) bool {
	return path == "/mcp" && isLoopbackHost(host)
}

// isLoopbackHost reports whether host (a Host header, with or without a
// port) refers to the loopback interface.
func isLoopbackHost(hostport string) bool {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// collectAllowedHosts extracts the host:port of app_url plus each entry
// in allowed_origins. Empty / unparseable URLs are skipped. Returns
// nil when nothing was extractable so the caller can detect "no
// allowlist configured" and pass through.
func collectAllowedHosts(appURL string, origins []string) []string {
	out := make([]string, 0, 1+len(origins))
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		// Accept either a full URL or a bare host:port; neturl.Parse
		// returns Host == "" for the latter, so fall back to the raw
		// string in that case.
		if u, err := neturl.Parse(raw); err == nil && u.Host != "" {
			out = append(out, u.Host)
			return
		}
		out = append(out, raw)
	}
	add(appURL)
	for _, o := range origins {
		add(o)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
// parseMemoryLimit parses a GOMEMLIMIT-style size string ("1200MiB",
// "2GiB", "500MB", or raw bytes) into a byte count for debug.SetMemoryLimit.
func parseMemoryLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "GiB"):
		mult, s = 1<<30, strings.TrimSuffix(s, "GiB")
	case strings.HasSuffix(s, "MiB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MiB")
	case strings.HasSuffix(s, "KiB"):
		mult, s = 1<<10, strings.TrimSuffix(s, "KiB")
	case strings.HasSuffix(s, "GB"):
		mult, s = 1_000_000_000, strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		mult, s = 1_000_000, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		mult, s = 1_000, strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(mult)), nil
}

func (s *Server) Run(ctx context.Context, port int) error {
	// Tray injects serverLogger (file sink) via processctl before calling Run.
	// Lab/CLI pass a plain context — inject global logger with component=server
	// so log.Ctx(r.Context()) in middleware is not a disabled logger.
	if zerolog.Ctx(ctx).GetLevel() == zerolog.Disabled {
		ctx = log.With().Str("component", "server").Logger().WithContext(ctx)
	}
	logger := zerolog.Ctx(ctx)
	// Opt-in profiling on loopback only. Set WICK_PPROF=1 to expose
	// /debug/pprof on 127.0.0.1:6060 (heap, goroutine, profile) for
	// diagnosing memory/CPU. Never bound to the public listener.
	if os.Getenv("WICK_PPROF") != "" {
		go func() {
			logger.Info().Msg("pprof: serving on 127.0.0.1:6060")
			if err := http.ListenAndServe("127.0.0.1:6060", nil); err != nil {
				logger.Warn().Err(err).Msg("pprof: listener closed")
			}
		}()
	}
	// Soft memory limit (GOMEMLIMIT) so GC stays aggressive and returns
	// memory to the OS on constrained hosts — helps boot restore not pin
	// RSS at its high-water mark. Opt-in via WICK_MEMORY_LIMIT
	// ("1200MiB", "2GiB", or raw bytes); the standard GOMEMLIMIT env still
	// works independently. Off by default — no cap is imposed unless set.
	if v := strings.TrimSpace(os.Getenv("WICK_MEMORY_LIMIT")); v != "" {
		if n, err := parseMemoryLimit(v); err == nil && n > 0 {
			debug.SetMemoryLimit(n)
			logger.Info().Str("limit", v).Int64("bytes", n).Msg("memory: soft limit applied (WICK_MEMORY_LIMIT)")
		} else {
			logger.Warn().Str("value", v).Msg("memory: invalid WICK_MEMORY_LIMIT, ignored")
		}
	}
	// WICK_HOST pins the listen interface — empty (default) binds all
	// interfaces so Docker and remote-VPS deploys keep working as-is.
	// Set WICK_HOST=127.0.0.1 (or use --localhost on `server` / `start`)
	// to make wick unreachable from the LAN — required on Termux phones
	// where unrooted Android has no firewall to keep port :9425 private.
	host := os.Getenv("WICK_HOST")
	addr := fmt.Sprintf("%s:%d", host, port)

	// Expose the server port to agent subprocesses via WICK_PORT so they
	// can reach wick's local proxy endpoints (e.g. /integrations/slack/send)
	// without needing any credentials injected into their environment.
	os.Setenv("WICK_PORT", fmt.Sprintf("%d", port)) //nolint:errcheck

	// Start agent control socket — lets stdio MCP processes send
	// switch_provider / kill commands to this daemon's pool.
	if s.agentsPool != nil {
		agentctlSrv := agentctl.NewServer(s.agentsPool, s.agentsLayout)
		if s.syncSessionMeta != nil {
			agentctlSrv.WithRefresh(s.syncSessionMeta)
		}
		go func() {
			if err := agentctlSrv.Listen(ctx); err != nil {
				log.Warn().Err(err).Msg("agentctl: socket closed")
			}
		}()
	}

	// Start channel listeners and watch for config changes.
	s.startChannels(ctx)

	// Admin-defined startup script (e.g. ngrok / cloudflared tunnel).
	// Lifetime is tied to the server ctx — tray stop or process exit
	// kills the subprocess via signal. Edits to the script row only
	// take effect on next server boot. Runs detached from the request
	// hot path so a slow tunnel command never blocks HTTP serve.
	if s.configsSvc.Get(configs.KeyStartupScriptEnabled) == "true" {
		script := s.configsSvc.Get(configs.KeyStartupScript)
		go func() {
			if err := startupscript.Run(ctx, appname.Resolve(), script); err != nil {
				logger.Warn().Err(err).Msg("startup script")
			}
		}()
	}

	h := chainMiddleware(
		s.authMidd.Session(s.router),
		recoverHandler,
		loggerHandler(func(w http.ResponseWriter, r *http.Request) bool { return false }),
		s.appNameHandler,
		s.hostAllowlistHandler,
		realIPHandler,
		requestIDHandler,
		// Outermost: short-circuit to the "Booting…" page until the async
		// boot restore + registry reload finish. Sits above auth/host checks
		// so the gate shows even before a session exists, but inside
		// requestID/realIP so the gate's own requests are still logged.
		s.bootGateHandler,
	)

	httpSrv := http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
		// ReadTimeout and WriteTimeout unset — SSE connections stay open
		// indefinitely and must not be cut by server-side timeouts.
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
		if s.agentsPool != nil {
			s.agentsPool.Stop()
		}
		if s.pluginMgr != nil {
			s.pluginMgr.KillAll()
		}
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpSrv.SetKeepAlivesEnabled(false)
		shutdownErr <- httpSrv.Shutdown(sctx)
	}()

	appURL := strings.TrimRight(s.configsSvc.AppURL(), "/")
	if appURL == "" {
		appURL = fmt.Sprintf("http://localhost:%d", port)
	}
	fmt.Printf("\n  ✓ %s is running\n", s.configsSvc.AppName())
	fmt.Printf("  → Listening on: %s\n", addr)
	fmt.Printf("  → App URL:      %s\n", appURL)
	if !s.configsSvc.AdminPasswordChanged() {
		// Tray pipes stdout to app.log, so printing plaintext password
		// here would leak it to disk. Headless / CLI gets the full
		// banner (operator might be reading from a journal, no GUI).
		if os.Getenv("WICK_TRAY") != "1" {
			appName := appname.Resolve()
			if info, ok := initcreds.Read(appName); ok {
				fmt.Printf("  → Email:            %s\n", info.Email)
				fmt.Printf("  → Default password: %s\n", info.Password)
			}
		}
		fmt.Printf("\n  ⚠ WARNING: Change the default password at %s/profile/setup\n\n", appURL)
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

// Stdio MCP entry points (BuildMCPHandler, RunMCPStdio, resolveWickGateBin)
// live in server_mcp.go. fileExists below is shared by both files.

// dispatchLifecyclePush fans an agent lifecycle transition out to the
// session's opt-in push subscribers (meta.Subscribers, populated via
// POST /tools/agents/sessions/{id}/subscribe).
//
// Only the "idle" transition surfaces a push — that's the "your turn
// is back" moment users actually care about. "working" was dropped
// because it fires at the START of a turn (noise — you just sent the
// message), "spawning" / "killed" are operational events that don't
// need a page.
//
// Body uses the last assistant message snippet so the notification
// previews the result, not just a generic "agent finished". Falls
// back to the session label when the snippet is unavailable.
//
// Sessions are shared (anyone with a wick login can open them) so
// targeting via Subscribers means a user only gets paged about the
// sessions they explicitly watched. SendToUser handles the fan-out
// across each user's devices on the receiving side.
//
// Best-effort: errors are logged but never block the SSE broadcast.
// Service worker on each receiver decides whether to surface as OS
// notification or postMessage an in-app toast — see web/public/js/sw.js.
func broadcastPoolStats(b *agentstool.Broadcaster, pool *agentpool.Pool) {
	if b == nil || pool == nil {
		return
	}
	entries := pool.ActiveSnapshot()
	procs := make([]agentstool.LiveProcessEntry, 0, len(entries))
	for _, e := range entries {
		prov := e.ProviderType
		if e.ProviderName != "" && e.ProviderName != e.ProviderType {
			prov = e.ProviderType + "/" + e.ProviderName
		}
		procs = append(procs, agentstool.LiveProcessEntry{
			SessionID: e.SessionID,
			AgentName: e.AgentName,
			Provider:  prov,
			PID:       e.PID,
			Queued:    e.Queued,
			Alive:     e.Respawns || e.PID == 0 || processctl.ProcessAlive(e.PID),
			Lifecycle: e.Lifecycle,
			Substate:  e.Substate,
		})
	}
	b.PublishPoolStats(pool.Active(), pool.MaxConcurrent(), pool.QueueLen(), procs)
}

func dispatchLifecyclePush(ctx context.Context, pushSvc *pwa.PushService, mgr *agentregistry.Manager, layout agentconfig.Layout, ev agentpool.LifecycleEvent) {
	if pushSvc == nil || mgr == nil {
		return
	}
	if ev.Lifecycle != "idle" {
		return
	}
	sess, ok := mgr.Registry().Session(ev.SessionID)
	if !ok {
		return
	}
	if len(sess.Meta.Subscribers) == 0 {
		return
	}
	title := "Agent is idle — your turn"
	body := lastAssistantPreview(layout, ev.SessionID, 140)
	if body == "" {
		// No assistant text available (early failure, killed mid-turn,
		// etc.) — fall back to the session label so the user at least
		// recognises which session pinged.
		if label := strings.TrimSpace(sess.Meta.Label); label != "" {
			body = label
		} else {
			body = ev.AgentName
		}
	}
	url := "/tools/agents/sessions/" + ev.SessionID
	subscribers := append([]string(nil), sess.Meta.Subscribers...)
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, userID := range subscribers {
			if _, err := pushSvc.SendToUser(bgCtx, userID, title, body, url); err != nil {
				log.Warn().
					Err(err).
					Str("session", ev.SessionID).
					Str("user", userID).
					Str("lifecycle", ev.Lifecycle).
					Msg("push: lifecycle dispatch had send errors")
			}
		}
	}()
}

// lastAssistantPreview returns the trimmed first `max` runes of the
// most recent assistant turn in the session's conversation.jsonl.
// Empty string when nothing is readable (missing file, no assistant
// turns yet, decode failure). Best-effort: errors are silently turned
// into "" so callers can fall back to a label.
//
// Walks the file forward — for typical short sessions this is cheap;
// long sessions might want a reverse scan but jsonl is line-oriented
// so a forward scan is simplest and reliable.
func lastAssistantPreview(layout agentconfig.Layout, sessionID string, max int) string {
	if max <= 0 {
		max = 140
	}
	var last string
	storage.ReadJSONL(layout.SessionConversation(sessionID), func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil {
			return true
		}
		if t.Role == "assistant" && strings.TrimSpace(t.Text) != "" {
			last = t.Text
		}
		return true
	})
	last = strings.TrimSpace(last)
	if last == "" {
		return ""
	}
	// Collapse newlines so the notification doesn't render a multi-
	// line wall (most OS notif systems truncate vertically anyway).
	last = strings.Join(strings.Fields(last), " ")
	r := []rune(last)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return last
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// parseGateRules decodes the kvlist JSON (pattern|scope columns) stored in
// AllowedCmds into a slice of gate.CommandRule.
func parseGateRules(raw string) []gate.CommandRule {
	if raw == "" {
		return nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil
	}
	rules := make([]gate.CommandRule, 0, len(rows))
	for _, r := range rows {
		pattern := strings.TrimSpace(r["pattern"])
		if pattern == "" {
			continue
		}
		rules = append(rules, gate.CommandRule{
			Pattern: pattern,
			Scope:   strings.TrimSpace(r["scope"]),
		})
	}
	return rules
}

// encDecryptorFunc adapts a func(string)(string,error) to the
// engine.SecretDecryptor interface so enc.Service.DecryptMaster can
// be wired into the engine without a new concrete type in enc/.
type encDecryptorFunc func(string) (string, error)

func (f encDecryptorFunc) Decrypt(token string) (string, error) { return f(token) }

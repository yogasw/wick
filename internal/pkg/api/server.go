package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/admin"
	"github.com/yogasw/wick/internal/appname"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	channelsetup "github.com/yogasw/wick/internal/agents/channels/setup"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentevent "github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
	agentgate "github.com/yogasw/wick/internal/agents/gate"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/providersync"
	agentregistry "github.com/yogasw/wick/internal/agents/registry"
	agentsession "github.com/yogasw/wick/internal/agents/session"
	agentworkspace "github.com/yogasw/wick/internal/agents/workspace"
	"github.com/yogasw/wick/internal/bookmark"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/connectors/wickmanager"
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
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/internal/metrics"
	"github.com/yogasw/wick/internal/oauth"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/sso"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/internal/tools"
	wfguard "github.com/yogasw/wick/internal/agents/workflow/guard"
	wfnodes "github.com/yogasw/wick/internal/agents/workflow/nodes"
	wftrigger "github.com/yogasw/wick/internal/agents/workflow/trigger"
	wfsetup "github.com/yogasw/wick/internal/agents/workflow/setup"
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

	// Restore all provider files from DB to filesystem on startup.
	if err := syncMgr.RestoreAll(context.Background()); err != nil {
		log.Warn().Err(err).Msg("providersync: startup restore failed")
	}

	// Built-in maintenance jobs whose RunFunc captures *gorm.DB are
	// registered here, after DB init, before validation + the jobs.All()
	// loops below. Mirrors the call in internal/pkg/worker.NewServer
	// so both processes share the same registry view.
	connectorrunspurge.Register(db)
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
	connectors.RegisterBuiltins()

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

	// ── Agents (AI agent sessions / pool) ────────────────────────────
	// Bootstrap reads or creates the on-disk layout (~/.wick/agents/) and
	// loads the in-memory registry. Pool is wired with the production
	// ClaudeFactory and the SSE event broadcaster.
	agentsWorkspaceCfg := agentconfig.WorkspaceConfig{
		BaseDir:          configsSvc.GetOwned("agents", "base_dir"),
		DefaultWorkspace: configsSvc.GetOwned("agents", "default_workspace"),
	}
	agentsLayout := agentconfig.NewLayout(agentconfig.ResolveBaseDir(agentsWorkspaceCfg))
	agentsMgr, agentsBootErr := agentregistry.Bootstrap(agentsLayout)
	if agentsBootErr != nil {
		log.Fatal().Msgf("agents bootstrap: %s", agentsBootErr.Error())
	}
	agentsBcast := agentstool.NewBroadcaster()
	agentsSpawnLogger := provider.NewSpawnLogger(agentsLayout.BaseDir)

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
	agentsFactory := &agentpool.ClaudeFactory{
		Layout:      agentsLayout,
		RecordRaw:   false,
		SpawnLogger: agentsSpawnLogger,
		OnEvent: func(sid, name string, ev agentevent.AgentEvent) {
			agentsBcast.Publish(sid, name, ev)
			channelReg.DispatchAgentEvent(sid, ev)
		},
		OnExit: func(sid, name string, reason provider.ExitReason) {
			agentsPool.HandleExit(sid, name, reason)
			doneEv := agentevent.AgentEvent{Type: agentevent.Done}
			agentsBcast.Publish(sid, name, doneEv)
			channelReg.DispatchAgentEvent(sid, doneEv)
		},
	}
	maxConc := 2
	if n, err := strconv.Atoi(configsSvc.GetOwned("agents", "max_concurrent")); err == nil && n > 0 {
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

	agentsFactory.BypassPermissionsLoader = func() bool {
		return configsSvc.GetOwned("agents", "bypass_permissions") == "true"
	}
	agentsFactory.SystemPromptLoader = func() string {
		return configsSvc.GetOwned("agents", "system_prompt")
	}

	// syncSharedSpec rewrites the shared spec.json on every spawn so
	// allowed_cmds edits take effect without a server restart.
	// AutoApproved entries are preserved from disk.
	syncSharedSpec := func() error {
		rules := parseGateRules(configsSvc.GetOwned("agents", "allowed_cmds"))
		spec, _ := agentgate.LoadSpec(agentgate.AppName())
		spec.Rules = rules
		return agentgate.WriteSharedSpec(agentgate.AppName(), spec)
	}
	agentsFactory.GateLoader = func() *agentpool.GateConfig {
		if configsSvc.GetOwned("agents", "gate_enabled") != "true" {
			return nil
		}
		if resolvedGateBin == "" {
			return nil
		}
		_ = syncSharedSpec()
		log.Debug().Int("rules", 0).Msg("agents: gate active for spawn")
		return &agentpool.GateConfig{
			GateBinary:   resolvedGateBin,
			AppName:      agentgate.AppName(),
			DefaultScope: agentsLayout.WorkspaceManagedPath("default"),
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
		DefaultWorkspace: agentsWorkspaceCfg.DefaultWorkspace,
		OnSessionCreated: func(s agentsession.Session) {
			agentsMgr.Register(s)
		},
		OnLifecycle: func(ev agentpool.LifecycleEvent) {
			agentsBcast.PublishLifecycle(ev.SessionID, ev.AgentName, ev.Lifecycle, ev.PID)
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
	agentstool.SetPool(agentsPool)
	agentstool.SetBroadcaster(agentsBcast)
	agentstool.SetLayout(agentsLayout)
	agentstool.SetSpawnLogger(agentsSpawnLogger)
	agentstool.SetConfigs(configsSvc)
	agentstool.SetDB(db)
	agentstool.SetChannelRegistry(channelReg)
	agentstool.SetSyncManager(syncMgr)

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
	// session key is "wf:<slug>"; client opens
	// /stream?session=wf:<slug>.
	wfMgr.Engine.SetEventHook(agentstool.WorkflowEventHook(agentsBcast))
	// Wire the shared agent pool + an adapter that translates
	// tools/agents.Broadcaster events into the slim AgentEvent the
	// workflow executor consumes. Pool-routed agent nodes go through
	// the FIFO queue + session reuse machinery — see
	// internal/docs/workflow/pool.md.
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
	if err := wfMgr.Start(context.Background()); err != nil {
		log.Warn().Err(err).Msg("workflow bootstrap failed; workflows tab will be empty")
	}
	agentstool.SetWorkflowManager(wfMgr)
	providerstoragetool.SetSyncManager(syncMgr)
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
		AppName: agentgate.AppName(),
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
		// Write initial spec.json so the gate binary finds the whitelist
		// rules on the very first spawn before any agent has started.
		initialRules := parseGateRules(configsSvc.GetOwned("agents", "allowed_cmds"))
		if wsErr := gate.WriteSharedSpec(agentgate.AppName(), gate.Spec{
			Rules:        initialRules,
			DefaultScope: agentsLayout.WorkspaceManagedPath("default"),
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

	// sendFnFor returns a pool-dispatch closure that re-reads the workspace
	// from agent_channels on every send, so UI changes take effect without
	// a restart. One closure per channel type so workspaces can differ.
	sendFnFor := func(channelType string) agentchannels.SendFunc {
		return func(ctx context.Context, sessionID, agentName, source, role, text string) error {
			ws := ""
			if m, err := agentchannels.GetChannelConfigMap(db, channelType); err == nil {
				ws = m["workspace"]
			}
			if ws == "" {
				if wsNames, err := agentworkspace.List(agentsLayout); err == nil && len(wsNames) == 1 {
					ws = wsNames[0]
				}
			}
			return agentsPool.SendWithWorkspace(ctx, sessionID, agentName, source, role, text, ws)
		}
	}

	// PAT service is needed by the REST channel (per-request Bearer auth).
	// Instantiated here so channelsetup.All can pass it in; the handler
	// further below reuses the same instance.
	tokensSvc := accesstoken.NewServiceFromDB(db)

	// One call wires every built-in channel: setup.All handles EnsureChannel,
	// config load, NewChannel, setters, and registry.Add per transport.
	// Adding a new channel = subpackage + composer in channels/setup; this
	// line never changes.
	channelsetup.All(channelReg, agentchannels.NewDBStore(db), sendFnFor, tokensSvc)

	// Wire each channel's workflow integration surface — registers
	// per-event + per-action descriptors and attaches the inbound
	// event sink that fires router.Dispatch. Per-channel calls so
	// telegram/rest can opt in independently as they grow workflow
	// surfaces.
	wfsetup.RegisterSlackIntegration(wfMgr.Integration, channelReg, wfMgr.Router)

	// ── Connectors (LLM-facing via MCP) ──────────────────────────
	// Register the code-side definitions for dispatch and auto-seed
	// one DB row per Key on first boot. The MCP server below is the
	// runtime entry point for LLM clients.
	connectorsSvc := connectors.NewServiceFromDB(db)
	connectorsSvc.SetEnc(encSvc)
	connectorsSvc.SetConfigs(configsSvc)
	metricsRec := metrics.NewSimpleRecorder()
	connectorsSvc.SetMetrics(metricsRec)

	// Workflow connector executor needs row credentials resolved
	// from the connectors service — wire after the service is built.
	wfMgr.Connectors.SetRowCreds(wfsetup.ConnectorsCredsAdapter(connectorsSvc))

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
		AppName:    appname.Resolve(),
	}))

	if err := connectorsSvc.Bootstrap(context.Background(), connectors.All()); err != nil {
		log.Fatal().Msgf("connectors bootstrap: %s", err.Error())
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
		wsNames, _ := agentworkspace.List(agentsLayout)
		// Build "label::path" options for the allowed_cmds scope column.
		var scopeOpts string
		if len(wsNames) > 0 {
			var parts []string
			for _, name := range wsNames {
				path, err := agentworkspace.ResolvePath(agentsLayout, name)
				if err == nil && path != "" {
					parts = append(parts, name+"::"+path)
				}
			}
			if len(parts) > 0 {
				scopeOpts = strings.Join(parts, "|")
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

	// Register connectors as items. One module = one card; the card
	// links to the manager list page where users see N rows for that
	// definition (one per credential set), each with a test panel and
	// enable/disable/duplicate actions. DefaultTags propagate so the
	// generic seed loop below attaches them to the card's path, which
	// is what the home page renders.
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

	// Channel webhooks — public, no session auth (each channel enforces
	// integrity inside its handler, e.g. Slack HMAC). Mounted from
	// whichever channels implement HTTPHandlerProvider.
	for path, h := range channelReg.HTTPHandlers() {
		r.Handle(path, h)
	}

	// Workflow webhook triggers — public path /hooks/<...>. The handler
	// inspects path + body, dispatches matching workflow triggers via
	// the router. Each trigger spec can carry HMAC `secret_ref` for
	// integrity check; otherwise plain POST.
	if wfMgr != nil && wfMgr.Router != nil {
		r.Handle("/hooks/", wftrigger.NewWebhookHandler(wfMgr.Router))
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
	r.Handle("/", http.HandlerFunc(homeHandler.Index))

	return &Server{router: r, configsSvc: configsSvc, authMidd: authMidd, agentsPool: agentsPool, channelReg: channelReg, db: db, gateBin: resolvedGateBin, jobsSvc: jobsSvc, wfMgr: wfMgr}
}

type Server struct {
	router     *http.ServeMux
	configsSvc *configs.Service
	authMidd   *login.Middleware
	agentsPool *agentpool.Pool
	channelReg *agentchannels.Registry
	db         *gorm.DB
	gateBin    string // resolved gate binary path; used for hook cleanup on shutdown
	jobsSvc    *manager.Service
	wfMgr      *wfsetup.Manager
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
		go wfsetup.WatchWorkflows(ctx, s.wfMgr.Layout.WorkflowsDir(), s.wfMgr.Service, s.wfMgr.Router, s.wfMgr.Cron)
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
			log.Warn().Str("request_host", got).Str("app_url_host", u.Host).Msg("hostAllowlist: forbidden — host mismatch")
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
	// Tray injects serverLogger (file sink) via processctl before calling Run.
	// Lab/CLI pass a plain context — inject global logger with component=server
	// so log.Ctx(r.Context()) in middleware is not a disabled logger.
	if zerolog.Ctx(ctx).GetLevel() == zerolog.Disabled {
		ctx = log.With().Str("component", "server").Logger().WithContext(ctx)
	}
	logger := zerolog.Ctx(ctx)
	addr := fmt.Sprintf(":%d", port)

	// Start channel listeners and watch for config changes.
	s.startChannels(ctx)

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
	fmt.Printf("  → Listening on: :%d\n", port)
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
		AppName:    appname.Resolve(),
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

// resolveWickGateBin finds the wick-gate binary: next to this executable,
// then ./bin/ relative to cwd (where wick setup puts it), then PATH.
func resolveWickGateBin() string {
	names := []string{"wick-gate", "wick-gate.exe"}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range names {
			if candidate := filepath.Join(dir, name); fileExists(candidate) {
				return candidate
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		for _, name := range names {
			if candidate := filepath.Join(cwd, "bin", name); fileExists(candidate) {
				return candidate
			}
		}
	}
	if p, err := exec.LookPath("wick-gate"); err == nil {
		return p
	}
	return ""
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

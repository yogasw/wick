package api

import (
	"context"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"

	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	slackwf "github.com/yogasw/wick/internal/agents/channels/slack/workflow"
	"github.com/yogasw/wick/internal/agents/agentctl"
	"github.com/yogasw/wick/internal/agents/askuser"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	wfsetup "github.com/yogasw/wick/internal/agents/workflow/setup"
	"github.com/yogasw/wick/internal/agents/workflow/wftest"
	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/connectors/notifications"
	"github.com/yogasw/wick/internal/connectors/wickmanager"
	wfconn "github.com/yogasw/wick/internal/connectors/workflow"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/pkg/pwa"
	"github.com/yogasw/wick/internal/safeexec"
	"github.com/yogasw/wick/internal/userconfig"

	"github.com/rs/zerolog/log"
)

// BuildMCPHandler initialises the connector layer (DB + connectors
// bootstrap) and returns a ready-to-serve MCP handler + admin context.
// Callers must either call ServeStdioOS (for the production stdio path)
// or ServeStdio with a custom reader/writer (for exec/test paths).
func BuildMCPHandler(version, commit, buildTime string) (*mcp.Handler, context.Context) {
	// When spawned by an MCP client (Claude Desktop, Cursor, etc.) the
	// working directory is the client's, not the project root. Chdir to
	// the project root (parent of the bin/ dir) so .env and wick.db
	// resolve correctly, then reload .env before config.Load().
	if exe, err := os.Executable(); err == nil {
		projectRoot := filepath.Dir(filepath.Dir(filepath.Clean(exe)))
		if err := os.Chdir(projectRoot); err == nil {
			_ = godotenv.Load()
			name := appname.ResolveAfterChdir()
			userconfig.ResolveDBPath(name, "")
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
	if err := pwa.EnsurePushConfig(context.Background(), configsSvc); err != nil {
		log.Warn().Err(err).Msg("notification config bootstrap failed")
	}
	pushSvc := pwa.NewPushService(db, configsSvc)

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
	connectors.Register(notifications.Module(notifications.Deps{
		DB:   db,
		Push: pushSvc,
	}))

	// Workflow connector — bootstrap minimal workflow manager so MCP
	// clients (Claude Desktop, Cursor) can introspect / create / edit /
	// test workflows. Engine has no live channel / provider wiring here
	// so type:channel + type:agent nodes will fail at run time; everything
	// else (validate/simulate/test/file ops/canvas mutations) works.
	stdioAgentsCfg := agentconfig.StorageConfig{
		BaseDir:          configsSvc.GetOwned("agents", "base_dir"),
		DefaultProjectID: configsSvc.GetOwned("agents", "default_project_id"),
	}
	stdioWfLayout := agentconfig.NewLayout(agentconfig.ResolveBaseDir(stdioAgentsCfg))
	stdioWfMgr := wfsetup.New(stdioWfLayout)
	// Subscribe the workflow connector registry to the global registry —
	// every module already registered flows in immediately, and every
	// future Register call (incl. wfconn below) flows in automatically.
	// Ordering of the wfconn / RegisterBuiltins calls below no longer
	// matters; see internal/agents/workflow/setup/connectors.go.
	wfsetup.RegisterLiveConnectors(stdioWfMgr.Connectors)
	stdioWfMgr.Connectors.SetRowCreds(wfsetup.ConnectorsCredsAdapter(connSvc))
	// Register Slack workflow descriptors into the integration registry
	// so workflow_integration / workflow_channels can surface full
	// MatchSchema + InputSchema metadata. Execute closures bind to a
	// stub Slack channel (no live API) — they error at runtime if a node
	// actually fires, but AI discovery + workflow_validate work fully.
	stdioStubSlack := agentslack.New(agentconfig.SlackChannelConfig{})
	slackwf.RegisterAll(stdioWfMgr.Integration, stdioStubSlack)
	// stdio path: register pickers too, even though the stub channel
	// has no live API — calls will surface the configuration error
	// rather than silently returning empty lists.
	slackwf.RegisterPickers(stdioWfMgr.MCP.Pickers, stdioStubSlack)
	stdioWfMgr.WithDataTablesDB(db)
	// DB-primary workflow store also needs wiring in stdio mode so
	// workflow_versions / workflow_diff_versions / workflow_restore_version
	// + the DBService backing the body don't 503 in MCP clients.
	stdioWfMgr.WithDB(db)
	if err := stdioWfMgr.Start(context.Background()); err != nil {
		log.Warn().Err(err).Msg("stdio: workflow bootstrap failed; workflow_* ops unavailable")
	} else {
		stdioRunner := wftest.New(stdioWfMgr.Engine, stdioWfMgr.Service, stdioWfMgr.Layout)
		connectors.Register(wfconn.ModuleWithRunner(stdioWfMgr.MCP, stdioRunner))
	}

	connectors.RegisterBuiltins()
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
	h := mcp.NewHandler(connSvc).
		WithBuildInfo(version, commit, buildTime).
		WithWickRoot(root).
		WithAppURL(configsSvc.AppURL).
		WithDB(db).
		// Session-scoped tools (wick_session_info / wick_set_title /
		// wick_session_workspace) read the same on-disk layout the server
		// writes — stdio shares it via agents.base_dir config.
		WithLayout(stdioWfLayout).
		// Stdio writes session meta straight to disk, but the running
		// daemon owns the in-memory registry + the live UI. Relay a
		// refresh signal over the agentctl socket so the daemon reloads
		// the session and broadcasts its new meta — otherwise the
		// sidebar/title stays stale until the next page load. Best-effort:
		// no daemon running just means the on-disk write stands alone.
		WithRefreshSession(func(id string) error {
			return agentctl.SignalRefresh(id)
		}).
		// ask_user from stdio can't render UI in this process; forward
		// to the running server over the askuser unix socket (gate.sock
		// pattern). Dial happens per ask, so server restarts are fine;
		// no server running → the tool returns a clear error.
		WithAskUser(&askuser.SocketAsker{Path: askuser.SocketPath(appname.Resolve())}).
		WithAskUserPolicy(func(sessionID string) (bool, string) {
			// Same per-origin resolution as the HTTP server (shared
			// configs + agent_channels tables); uses the stdio workflow
			// layout to load the session.
			return askUserPolicy(db, configsSvc, stdioWfLayout, sessionID)
		})
	return h, ctx
}

// RunMCPStdio initialises the connector layer and serves MCP JSON-RPC
// over stdin/stdout. Intended for local clients (Claude Desktop, Cursor).
func RunMCPStdio(version, commit, buildTime string) {
	h, ctx := BuildMCPHandler(version, commit, buildTime)
	h.ServeStdioOS(ctx)
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
	if p, err := safeexec.LookPath("wick-gate"); err == nil {
		return p
	}
	return ""
}

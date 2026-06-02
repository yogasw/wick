// Package agents backs /tools/agents — the Agents UI Manager. It lets
// users manage AI agent sessions, workspaces, and presets from the
// browser and streams real-time agent output via Server-Sent Events.
package agents

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/askuser"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/providersync"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	wfrepo "github.com/yogasw/wick/internal/agents/workflow/repository"
	"github.com/yogasw/wick/internal/agents/workspace"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// Package-level singletons wired at boot from server.go via SetX funcs.
// Handlers return 503 when these are nil rather than panicking.
var (
	globalMgr        *registry.Manager
	globalPool       *pool.Pool
	globalBcast      *Broadcaster
	globalLayout     agentconfig.Layout
	globalSpawnLog   *provider.SpawnLogger
	globalApprovals  *gate.ApprovalManager
	globalAskUsers   *askuser.Manager
	globalGateStatus GateStatus
	globalConfigs    *configs.Service
	globalDB         *gorm.DB
	globalChannels   *agentchannels.Registry
	globalSyncMgr    *providersync.Manager
)

// GateStatus is the boot-time snapshot of the command gate. Populated
// once during server.go startup and read by the Providers page so
// operators can tell at a glance whether the gate sidecar is wired up.
//
// Enabled=false means ResolveGateBinary returned an error — every
// command will hit fail-safe block at the matcher / no-socket path,
// except whitelist matches. Reason carries the error message so
// the UI can show actionable guidance (run `wick build`).
type GateStatus struct {
	Enabled bool
	Binary  string // absolute path
	Source  string // gate.Source* constant
	Reason  string // populated when Enabled=false
}

// SetManager wires in the agents registry manager.
func SetManager(m *registry.Manager) { globalMgr = m }

// SetPool wires in the agent subprocess pool.
func SetPool(p *pool.Pool) { globalPool = p }

// SetBroadcaster wires in the SSE event broadcaster.
func SetBroadcaster(b *Broadcaster) { globalBcast = b }

// SetLayout wires in the on-disk layout used for direct file reads.
func SetLayout(l agentconfig.Layout) { globalLayout = l }

// SetSpawnLogger wires in the per-spawn jsonl writer/reader. The
// Providers page reads from it via List + Read; the pool factory
// already writes through it.
func SetSpawnLogger(s *provider.SpawnLogger) { globalSpawnLog = s }

// SetApprovals wires in the gate ApprovalManager. nil = gate
// disabled (handler endpoints fall back to 503).
func SetApprovals(m *gate.ApprovalManager) { globalApprovals = m }

// SetAskUsers wires in the ask_user Manager. nil = ask_user MCP
// tool returns errors and the answer endpoint 503s.
func SetAskUsers(m *askuser.Manager) { globalAskUsers = m }

// SetGateStatus records the boot-time gate-resolution result. Read
// by the Providers page. Call exactly once during server boot.
func SetGateStatus(s GateStatus) { globalGateStatus = s }

// SetConfigs wires the shared configs service so the Providers page
// can toggle agents.gate_enabled inline. Without this, the toggle
// endpoint 503s.
func SetConfigs(c *configs.Service) { globalConfigs = c }

// SetDB wires the shared GORM DB so channel handlers can read/write
// agent_channels rows. Without this, channel config endpoints 503.
func SetDB(db *gorm.DB) {
	globalDB = db
	// Best-effort one-shot importer: hydrate the workflows + workflow_versions
	// tables from the file-based store on first boot after the DB lands.
	// Idempotent — re-runs are no-ops. Soft-fail so a DB error doesn't
	// take down the server; the file-based UI keeps working regardless.
	if db != nil && globalWorkflowMgr != nil {
		repo := workflowRepoFor(db)
		if _, err := repo.ImportFromFiles(globalWorkflowMgr.Service); err != nil {
			log.Warn().Err(err).Msg("workflow importer (file → DB) failed; file-store stays primary")
		}
	}
}

func workflowRepoFor(db *gorm.DB) *wfrepo.Repo { return wfrepo.New(db) }

// SetChannelRegistry wires the live channel registry so picker fields
// can issue lookup queries against each channel's upstream (Slack API,
// etc.). Without this, /channels/{slug}/lookup returns 503.
func SetChannelRegistry(r *agentchannels.Registry) { globalChannels = r }

// SetSyncManager wires the provider storage sync manager.
func SetSyncManager(m *providersync.Manager) { globalSyncMgr = m }

// GetGateStatus is the read side. Returns a zero value when boot
// hasn't reached SetGateStatus yet.
func GetGateStatus() GateStatus { return globalGateStatus }

// AskUsers returns the wired Manager so the boot path can hand it
// to the MCP handler. Reading this is racy if SetAskUsers is
// called concurrently with reads, but in practice it's set once
// during boot before serving begins.
func AskUsers() *askuser.Manager { return globalAskUsers }

// Register mounts all Agents routes under /tools/agents.
func Register(r tool.Router) {
	r.Static("/static/", StaticFS)

	// Svelte SPA shell + assets for the workflow editor.
	registerSPA(r)
	registerSPAWorkflows(r)
	registerSPAWorkflowHistory(r)
	registerSPAPanels(r)
	registerSPAPalette(r)

	r.GET("/", newSessionCompose)
	r.POST("/", startNewSession)
	r.GET("/overview", overviewPage)

	r.GET("/sessions", sessionsPage)
	r.POST("/sessions", createSession)
	r.POST("/sessions/quick", createSessionQuick)
	r.GET("/sessions/{id}", sessionDetail)
	r.POST("/sessions/{id}/send", sendMessage)
	r.POST("/sessions/{id}/provider", switchProvider)
	r.POST("/sessions/{id}/workspace", switchWorkspace)
	r.POST("/sessions/{id}/kill", killAgent)
	r.POST("/sessions/{id}/dequeue", dequeueAgent)
	r.DELETE("/sessions/{id}", deleteSession)

	r.GET("/sessions/{id}/uploads/{name}", sessionUploadServe)

	r.GET("/sessions/{id}/files", sessionContextList)
	r.GET("/sessions/{id}/files/read", sessionContextRead)
	r.GET("/sessions/{id}/files/download", sessionContextDownload)
	r.POST("/sessions/{id}/files/save", sessionContextSave)
	r.POST("/sessions/{id}/files/create", sessionContextCreate)
	r.DELETE("/sessions/{id}/files", sessionContextDelete)

	// Gate approval (Stage 5). Modal in the UI POSTs the user's
	// decision here; revoke removes a previously-approved match key.
	r.POST("/sessions/{id}/approve", approveCommand)
	r.GET("/sessions/{id}/approvals", approvalsSnapshot)
	r.DELETE("/sessions/{id}/approve/{matchKey}", revokeApproval)

	// ask_user (Stage 6). MCP tool blocks; the card in the UI
	// POSTs the answer here; rehydrate runs on page load.
	r.POST("/sessions/{id}/answer", answerAsk)
	r.GET("/sessions/{id}/asks", asksSnapshot)

	r.GET("/workspaces", workspacesPage)
	r.GET("/workspaces/options", workspaceOptionsJSON)
	r.POST("/workspaces", createWorkspace)
	r.DELETE("/workspaces/{name}", deleteWorkspace)

	r.GET("/presets", presetsPage)
	r.GET("/presets/{name}", presetDetail)
	r.POST("/presets", createPreset)
	r.POST("/presets/{name}", updatePreset)
	r.DELETE("/presets/{name}", deletePreset)

	r.GET("/providers", providersPage)
	r.POST("/providers", saveProviderInstance)
	r.DELETE("/providers/{type}/{name}", deleteProviderInstance)
	r.GET("/providers/spawns/{file}", providerSpawnDetail)
	r.POST("/providers/gate/toggle", toggleGate)
	r.POST("/providers/gate/modes", saveGateModes)
	r.POST("/providers/rescan", rescanAllProviders)
	r.POST("/providers/rescan/{type}/{name}", rescanOneProvider)
	r.POST("/providers/probe-gate/{type}/{name}", probeProviderGate)
	r.POST("/providers/{type}/{name}/hooks/{event}/check", checkProviderHook)
	r.POST("/providers/{type}/{name}/hooks/{event}/enable", enableProviderHook)
	r.POST("/providers/{type}/{name}/hooks/{event}/disable", disableProviderHook)
	r.POST("/providers/auto-rescan/toggle", toggleAutoRescan)

	r.POST("/providers/mcp/{clientID}/install", mcpInstallClient)
	r.POST("/providers/mcp/{clientID}/uninstall", mcpUninstallClient)

	r.GET("/skills", skillsPage)
	r.POST("/skills/sync", skillsSync)
	r.POST("/skills/upload", skillsUpload)
	r.GET("/skills/{name}", skillDetail)
	r.GET("/skills/{name}/download", skillDownload)
	r.POST("/skills/{name}/delete", skillDelete)
	r.POST("/skills/{name}/delete-from/{dirLabel}", skillDeleteFromDir)
	r.GET("/skills/{folder}/files/{file}", skillFolderFileDetail)
	r.GET("/skills/{folder}/files/{file}/download", skillFolderFileDownload)
	r.POST("/skills/{folder}/files/{file}/delete", skillFolderFileDelete)
	// provider-scoped views — {path...} matches arbitrary depth
	r.GET("/skills/{provider}/{path...}", skillProviderPath)
	r.POST("/skills-sync/{provider}/{path...}", skillProviderSync)
	r.POST("/skills/{name}/sync", skillEntrySync)

	r.POST("/providers/storage/sync/{type}/{name}", syncProviderStorage)
	r.GET("/providers/storage", storagePage)
	r.POST("/providers/storage/restore", storageRestoreSelected)
	r.POST("/providers/storage/upload", storageUpload)
	r.GET("/providers/storage/{id}/preview", storagePreview)
	r.POST("/providers/storage/{id}/retention", storageSetRetention)
	r.DELETE("/providers/storage/{id}", storageDelete)

	r.GET("/channels", channelsPage)
	r.GET("/channels/slack", slackChannelPage)
	r.POST("/channels/slack/{key}", makeChannelSaveHandler("slack"))
	r.GET("/channels/telegram", telegramChannelPage)
	r.POST("/channels/telegram/{key}", makeChannelSaveHandler("telegram"))
	r.GET("/channels/rest", restChannelPage)
	r.POST("/channels/rest/{key}", makeChannelSaveHandler("rest"))
	r.GET("/channels/{slug}/lookup", channelLookupHandler)
	r.POST("/channels/test/{slug}", channelHealthHandler)

	r.GET("/settings", settingsPage)

	// Workflows tab — Svelte v2 editor. `/workflows` lists; the editor
	// page mounts the Svelte SPA via the templ shell.
	r.GET("/workflows", workflowsPage)
	r.POST("/workflows", createWorkflow)
	r.POST("/workflows/import", importWorkflow)
	r.GET("/workflows/edit/{id}/download", downloadWorkflowYAML)
	r.GET("/workflows/edit/{id}", workflowEditor)
	r.POST("/workflows/edit/{id}/rename", renameWorkflow)
	r.GET("/workflows/edit/{id}/runs/{runID}/state", workflowRunStateAPI)
	r.POST("/workflows/edit/{id}/delete", deleteWorkflow)
	r.GET("/workflows/api/registry", workflowRegistryAPI)
	r.GET("/workflows/api/lookup", workflowLookupAPI)

	r.GET("/stream", streamSSE)
	r.GET("/stream/snapshot", streamSnapshot)

	// Data Tables tab — n8n-style standalone shared key/value store.
	// Schema + rows live in-memory (Postgres backend deferred); shared
	// with the workflow engine so datatable_* nodes see the same data.
	r.GET("/data-tables", dataTablesPage)
	r.GET("/api/data-tables", listDataTablesJSON)
	r.GET("/api/data-tables/{slug}/columns", listDataTableColumnsJSON)
	r.POST("/data-tables", createDataTable)
	r.POST("/data-tables/import-csv", importDataTableCSV)
	r.GET("/data-tables/{slug}", dataTableDetail)
	r.POST("/data-tables/{slug}/delete", dropDataTable)
	r.POST("/data-tables/{slug}/rows", insertDataTableRow)
	r.POST("/data-tables/{slug}/rows/bulk-delete", bulkDeleteDataTableRows)
	r.POST("/data-tables/{slug}/rows/{pk}/delete", deleteDataTableRow)
	r.POST("/data-tables/{slug}/columns", addDataTableColumn)
	r.POST("/data-tables/{slug}/columns/{col}/rename", renameDataTableColumn)
	r.POST("/data-tables/{slug}/columns/{col}/delete", dropDataTableColumn)
	r.GET("/data-tables/{slug}/export.csv", exportDataTableCSV)
}

func settingsPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if u := login.GetUser(c.Context()); u == nil || !u.IsAdmin() {
		c.Error(http.StatusForbidden, "admins only")
		return
	}
	rows := globalConfigs.ListOwned("agents")
	// Split system_prompt_append out — rendered in its own panel with a
	// Reset button. The rest fall through to the generic ConfigsTable.
	// Skip Hidden rows — those belong to dedicated pages (Channels,
	// Providers, Workspaces) and must not leak onto generic Settings.
	current := ""
	rest := rows[:0:len(rows)]
	for _, r := range rows {
		if r.Hidden {
			continue
		}
		if r.Key == "system_prompt" {
			current = r.Value
			continue
		}
		rest = append(rest, r)
	}
	c.HTML(view.SettingsPage(view.SettingsVM{
		Layout:              sidebarVM(c, "settings", ""),
		Base:                c.Base(),
		Rows:                rest,
		SystemPromptCurrent: current,
		SystemPromptDefault: agentconfig.DefaultSystemPrompt(),
	}))
}

// sidebarVM builds AgentsLayoutVM for the sidebar session list.
// activeSessionID is set on session detail pages to highlight the current row.
func sidebarVM(c *tool.Ctx, activePage, activeSessionID string) view.AgentsLayoutVM {
	const sidebarCap = 15
	allIDs := globalMgr.Registry().SessionIDs()
	ids := allIDs
	if len(ids) > sidebarCap {
		ids = ids[:sidebarCap]
	}
	lc := make(map[string]view.SessionLifecycleVM)
	for _, e := range globalPool.ActiveSnapshot() {
		entry := view.SessionLifecycleVM{Lifecycle: e.Lifecycle, PID: e.PID}
		if !e.LastActive.IsZero() {
			entry.LastActiveMs = e.LastActive.UnixMilli()
		}
		lc[e.SessionID] = entry
	}
	// Read labels concurrently — buffered channel = no goroutine leak.
	type result struct{ id, label string }
	ch := make(chan result, len(ids)) // buffered: goroutines never block
	for _, id := range ids {
		id := id
		go func() { ch <- result{id, loadFirstUserMessage(globalLayout, id, 40)} }()
	}
	labels := make(map[string]string, len(ids))
	for range ids {
		r := <-ch
		labels[r.id] = r.label
	}
	close(ch)
	return view.AgentsLayoutVM{
		Base:             c.Base(),
		ActivePage:       activePage,
		SidebarIDs:       ids,
		SidebarSessions:  globalMgr.Registry().Sessions(),
		SidebarLifecycle: lc,
		SidebarLabels:    labels,
		ActiveSessionID:  activeSessionID,
		IdleTimeoutMs:    globalPool.IdleTimeout().Milliseconds(),
	}
}

// createSessionQuick creates a session with defaults (no form) and redirects.
func createSessionQuick(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	prov := "claude"
	// Use first healthy provider if available
	if ps := providerChoicesCached(c.Context()); len(ps) > 0 {
		prov = ps[0].Type
	}
	id := uuid.New().String()
	_, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:     id,
		Origin: session.OriginUI,
		Preset: "default",
	})
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	if err := globalMgr.AddAgent(id, "main", prov); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/sessions/"+id, http.StatusSeeOther)
}

// ── guards ────────────────────────────────────────────────────────────

func notReady(c *tool.Ctx) bool {
	if globalMgr == nil || globalPool == nil {
		c.Error(http.StatusServiceUnavailable, "agents not initialised — check server boot logs")
		return true
	}
	return false
}

// newSessionCompose renders the compose page: provider/preset/workspace
// pickers + a textarea for the first message. No session is persisted
// here — that only happens in startNewSession when the form posts back
// with a non-empty message.
func newSessionCompose(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	renderCompose(c, "", "")
}

// startNewSession is the compose form's POST target. It creates the
// session, attaches the agent with the chosen provider, and queues the
// first message in one go — matching ChatGPT/Claude's "session exists
// only after first send" UX.
func startNewSession(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	// Compose form may be multipart (when files are attached) or
	// urlencoded. ParseMultipartForm short-circuits to ParseForm for
	// urlencoded bodies so calling it once is safe either way.
	if strings.HasPrefix(c.R.Header.Get("Content-Type"), "multipart/") {
		if err := c.R.ParseMultipartForm(maxMultipartTotal); err != nil {
			renderCompose(c, "", "parse form: "+err.Error())
			return
		}
	}
	text := strings.TrimSpace(c.Form("message"))
	hasFiles := c.R.MultipartForm != nil && len(c.R.MultipartForm.File["files"]) > 0
	if text == "" && !hasFiles {
		renderCompose(c, "", "Type a message or attach a file to start the session.")
		return
	}
	prov := c.Form("provider")
	if prov == "" {
		prov = "claude"
		if ps := providerChoicesCached(c.Context()); len(ps) > 0 {
			prov = ps[0].Type
		}
	}
	ws := c.Form("workspace")
	presetName := c.Form("preset")
	if presetName == "" {
		presetName = "default"
		if ws != "" {
			if wsData, werr := workspace.Load(globalLayout, ws); werr == nil && wsData.Meta.DefaultPreset != "" {
				presetName = wsData.Meta.DefaultPreset
			}
		}
	}
	id := uuid.New().String()
	if _, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        id,
		Workspace: ws,
		Origin:    session.OriginUI,
		Preset:    presetName,
	}); err != nil {
		log.Ctx(c.Context()).Error().Msgf("compose create session: %s", err.Error())
		renderCompose(c, text, err.Error())
		return
	}
	if err := globalMgr.AddAgent(id, "main", prov); err != nil {
		log.Ctx(c.Context()).Error().Msgf("compose add agent: %s", err.Error())
		renderCompose(c, text, err.Error())
		return
	}
	atts, err := saveUploadsFromMultipart(c, id, c.Base())
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("compose save uploads: %s", err.Error())
		renderCompose(c, text, err.Error())
		return
	}
	// Detach from HTTP ctx (see sendMessage note) — keep request_id for logs.
	bgCtx := log.Ctx(c.Context()).WithContext(context.Background())
	if err := globalPool.SendWithAttachments(bgCtx, id, "main", "ui", "user", text, "", atts); err != nil {
		log.Ctx(c.Context()).Error().Msgf("compose send: %s", err.Error())
		renderCompose(c, text, err.Error())
		return
	}
	c.Redirect(c.Base()+"/sessions/"+id, http.StatusSeeOther)
}

// renderCompose is the shared body of newSessionCompose / startNewSession's
// validation branches. message and errMsg are round-tripped so a failed
// submit keeps the user's text and surfaces the reason inline.
func renderCompose(c *tool.Ctx, message, errMsg string) {
	providers := providerChoicesCached(c.Context())
	defaultProv := ""
	if len(providers) > 0 {
		defaultProv = providers[0].Type
	}
	layout := sidebarVM(c, "new", "")
	layout.FullBleed = true
	c.HTML(view.NewSessionCompose(view.NewSessionComposeVM{
		Layout:          layout,
		Base:            c.Base(),
		Providers:       providers,
		Presets:         globalMgr.Registry().PresetNames(),
		Workspaces:      globalMgr.Registry().WorkspaceNames(),
		DefaultProvider: defaultProv,
		Message:         message,
		Error:           errMsg,
	}))
}

// ── Overview ──────────────────────────────────────────────────────────

func overviewPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	// Active = sessions whose subprocess is still alive in the pool
	// (any lifecycle except killed). Cap at 5; rest live in /sessions.
	active := globalPool.ActiveSnapshot()
	const activeCap = 5
	lc := make(map[string]view.SessionLifecycleVM, len(active))
	activeIDs := make([]string, 0, len(active))
	for _, e := range active {
		entry := view.SessionLifecycleVM{
			Lifecycle: e.Lifecycle,
			PID:       e.PID,
		}
		if !e.LastActive.IsZero() {
			entry.LastActiveMs = e.LastActive.UnixMilli()
		}
		lc[e.SessionID] = entry
		if len(activeIDs) < activeCap {
			activeIDs = append(activeIDs, e.SessionID)
		}
	}
	queue := globalPool.QueueSnapshot()
	now := time.Now()
	queued := make([]view.QueuedEntryVM, len(queue))
	for i, q := range queue {
		queued[i] = view.QueuedEntryVM{
			SessionID: q.SessionID,
			AgentName: q.AgentName,
			WaitingMs: now.Sub(q.Enqueued).Milliseconds(),
		}
	}
	c.HTML(view.Overview(view.OverviewVM{
		Layout:        sidebarVM(c, "overview", ""),
		Base:          c.Base(),
		Active:        globalPool.Active(),
		QueueLen:      globalPool.QueueLen(),
		PoolMax:       globalPool.MaxConcurrent(),
		SessionIDs:    activeIDs,
		Sessions:      globalMgr.Registry().Sessions(),
		Lifecycle:     lc,
		IdleTimeoutMs: globalPool.IdleTimeout().Milliseconds(),
		Queued:        queued,
	}))
}

// ── Sessions ──────────────────────────────────────────────────────────

func sessionsPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	const perPage = 50
	ids := globalMgr.Registry().SessionIDs()
	start := (page - 1) * perPage
	if start > len(ids) {
		start = len(ids)
	}
	end := start + perPage
	if end > len(ids) {
		end = len(ids)
	}
	lc := make(map[string]view.SessionLifecycleVM)
	for _, e := range globalPool.ActiveSnapshot() {
		entry := view.SessionLifecycleVM{
			Lifecycle: e.Lifecycle,
			PID:       e.PID,
		}
		if !e.LastActive.IsZero() {
			entry.LastActiveMs = e.LastActive.UnixMilli()
		}
		lc[e.SessionID] = entry
	}
	pageIDs := ids[start:end]
	pageLabels := make(map[string]string, len(pageIDs))
	for _, id := range pageIDs {
		pageLabels[id] = loadFirstUserMessage(globalLayout, id, 60)
	}
	c.HTML(view.SessionsList(view.SessionsListVM{
		Layout:        sidebarVM(c, "sessions", ""),
		Base:          c.Base(),
		IDs:           pageIDs,
		Sessions:      globalMgr.Registry().Sessions(),
		Labels:        pageLabels,
		Workspaces:    globalMgr.Registry().Workspaces(),
		WorkspaceList: globalMgr.Registry().WorkspaceNames(),
		PresetList:    globalMgr.Registry().PresetNames(),
		Providers:     providerChoicesCached(c.Context()),
		Lifecycle:     lc,
		IdleTimeoutMs: globalPool.IdleTimeout().Milliseconds(),
		Page:          page,
		HasNext:       end < len(ids),
	}))
}

func createSession(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ws := c.Form("workspace")
	prov := c.Form("provider")
	if prov == "" {
		prov = "claude"
	}
	id := uuid.New().String()
	presetName := "default"
	if ws != "" {
		if wsData, werr := workspace.Load(globalLayout, ws); werr == nil && wsData.Meta.DefaultPreset != "" {
			presetName = wsData.Meta.DefaultPreset
		}
	}
	_, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        id,
		Workspace: ws,
		Origin:    session.OriginUI,
		Preset:    presetName,
	})
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("create session: %s", err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	if err := globalMgr.AddAgent(id, "main", prov); err != nil {
		log.Ctx(c.Context()).Error().Msgf("add agent: %s", err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/sessions/"+id, http.StatusSeeOther)
}

func sessionDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.NotFound()
		return
	}
	tab := c.Query("tab")
	if tab == "" {
		tab = "conversation"
	}
	var turns []view.TurnVM
	var cmdLines []string
	switch tab {
	case "conversation":
		raw, err := loadConversation(globalLayout, id)
		if err != nil {
			log.Ctx(c.Context()).Error().Msgf("load conversation %s: %s", id, err.Error())
		}
		for _, t := range raw {
			vm := view.TurnVM{
				Role:      t.Role,
				Agent:     t.Agent,
				Provider:  t.Provider,
				Text:      t.Text,
				Truncated: t.Truncated,
				Time:      t.Timestamp,
			}
			for _, e := range t.Events {
				vm.Events = append(vm.Events, view.TurnEventVM{
					Type:      e.Type,
					ToolName:  e.ToolName,
					ToolInput: e.ToolInput,
					ToolUseID: e.ToolUseID,
					IsError:   e.IsError,
					Text:      e.Text,
				})
			}
			for _, a := range t.Attachments {
				// IsImage must match the server's inline-serve whitelist
				// (imageMIMEAllowed) — SVG / generic image/* types are
				// served as attachments, so rendering them via <img> would
				// just produce a broken icon.
				vm.Attachments = append(vm.Attachments, view.AttachmentVM{
					Name:    a.Name,
					URL:     a.URL,
					MIME:    a.MIME,
					Size:    a.Size,
					IsImage: imageMIMEAllowed[a.MIME],
				})
			}
			turns = append(turns, vm)
		}
	case "commands":
		lines, err := loadCommands(globalLayout, id)
		if err != nil {
			log.Ctx(c.Context()).Error().Msgf("load commands %s: %s", id, err.Error())
		}
		cmdLines = lines
	}
	gs := GetGateStatus()
	activeProv := ""
	if len(sess.Agents) > 0 {
		activeProv = sess.Agents[0].Provider
	}
	vm := view.SessionDetailVM{
		Layout:          sidebarVM(c, "sessions", id),
		Base:            c.Base(),
		Session:         sess,
		Tab:             tab,
		Turns:           turns,
		CmdLines:        cmdLines,
		IdleTimeoutMs:   globalPool.IdleTimeout().Milliseconds(),
		Providers:       providerChoicesCached(c.Context()),
		ActiveProvider:  activeProv,
		WorkspaceList:   globalMgr.Registry().WorkspaceNames(),
		ActiveWorkspace: sess.Meta.Workspace,
		Gate: view.GateStatusVM{
			Enabled: gs.Enabled,
			Binary:  gs.Binary,
			Source:  gs.Source,
			Reason:  gs.Reason,
		},
	}
	for _, e := range globalPool.ActiveSnapshot() {
		if e.SessionID != id {
			continue
		}
		vm.Lifecycle = e.Lifecycle
		vm.PID = e.PID
		if !e.LastActive.IsZero() {
			vm.LastActiveMs = e.LastActive.UnixMilli()
		}
		break
	}
	c.HTML(view.SessionDetail(vm))
}

type switchProviderReq struct {
	Provider string `json:"provider"`
}

// switchProvider creates a new session with the same workspace but a
// different provider. Provider sessions cannot be resumed across CLI
// implementations (ResumeID is provider-specific), so a fresh session
// is always the right call. Returns the new session URL for redirect.
func switchProvider(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req switchProviderReq
	if err := c.BindJSON(&req); err != nil || req.Provider == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider required"})
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	newID := uuid.New().String()
	_, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        newID,
		Workspace: sess.Meta.Workspace,
		Origin:    session.OriginUI,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := globalMgr.AddAgent(newID, "main", req.Provider); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"status":   "switched",
		"provider": req.Provider,
		"redirect": c.Base() + "/sessions/" + newID,
	})
}

type switchWorkspaceReq struct {
	Workspace string `json:"workspace"`
}

// switchWorkspace updates the session's workspace in-place and kills
// any running subprocess so it respawns with the new folder on the
// next message.
func switchWorkspace(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req switchWorkspaceReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "workspace required"})
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if err := globalMgr.SwitchWorkspace(c.Context(), id, req.Workspace); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Kill running subprocess so next Send respawns in the new workspace.
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	if agentName != "" {
		_ = globalPool.Kill(id, agentName)
	}
	c.JSON(http.StatusOK, map[string]string{"status": "switched", "workspace": req.Workspace})
}

type sendReq struct {
	Text string `json:"text"`
}

func sendMessage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req sendReq
	var atts []agentstore.Attachment

	// Accept multipart/form-data when files are attached; fall back to
	// the JSON shape so text-only sends from older clients still work.
	if strings.HasPrefix(c.R.Header.Get("Content-Type"), "multipart/") {
		if err := c.R.ParseMultipartForm(maxMultipartTotal); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "parse form: " + err.Error()})
			return
		}
		req.Text = strings.TrimSpace(c.Form("text"))
		saved, err := saveUploadsFromMultipart(c, id, c.Base())
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		atts = saved
	} else {
		if err := c.BindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		req.Text = strings.TrimSpace(req.Text)
	}
	if req.Text == "" && len(atts) == 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "text or file required"})
		return
	}

	// #<provider> prefix → switch provider before sending.
	if r := agentchannels.ParseProviderTag(req.Text); r.HasTag {
		sess, ok := globalMgr.Registry().Session(id)
		if !ok {
			c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		agentName := sess.Meta.ActiveAgent
		if agentName == "" && len(sess.Agents) > 0 {
			agentName = sess.Agents[0].Name
		}
		if agentName == "" {
			c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "no agent in session"})
			return
		}
		bcast := globalBcast
		if err := provider.Switch(globalLayout, globalPool, id, agentName, r.Tag, provider.SwitchOptions{
			Source:   "ui",
			UserText: req.Text,
			Notify: func(tag string, steps []string) {
				if bcast != nil {
					bcast.PublishRaw(id, agentName, "user_message", req.Text)
					bcast.PublishSystemTurn(id, agentName, "Provider switched → "+tag, steps)
				}
			},
			Reply: func(text string) {
				if bcast != nil {
					bcast.PublishRaw(id, agentName, "text_delta", text)
					bcast.PublishRaw(id, agentName, "done", "")
				}
			},
		}); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := globalMgr.RefreshSession(id); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": "refresh session: " + err.Error()})
			return
		}
		if r.Rest == "" {
			c.JSON(http.StatusOK, map[string]string{"status": "switched", "provider": r.Tag})
			return
		}
		req.Text = r.Rest
	}

	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	if agentName == "" {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "no agent in session"})
		return
	}
	// Detach from HTTP ctx — pool.spawn calls exec.CommandContext, so
	// inheriting c.Context() would SIGKILL claude.exe the moment the
	// response returns. Copy request_id over so logs still correlate.
	bgCtx := log.Ctx(c.Context()).WithContext(context.Background())
	if err := globalPool.SendWithAttachments(bgCtx, id, agentName, "ui", "user", req.Text, "", atts); err != nil {
		log.Ctx(c.Context()).Error().Msgf("pool send %s: %s", id, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "queued"})
}

func dequeueAgent(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	removed := globalPool.Dequeue(id, agentName)
	_ = session.SaveMeta(globalLayout, id, session.Meta{
		Workspace:   sess.Meta.Workspace,
		Origin:      sess.Meta.Origin,
		ChannelID:   sess.Meta.ChannelID,
		ActiveAgent: sess.Meta.ActiveAgent,
		Status:      session.StatusIdle,
		CreatedAt:   sess.Meta.CreatedAt,
		LastActive:  time.Now().UTC(),
	})
	c.JSON(http.StatusOK, map[string]any{"status": "dequeued", "removed": removed})
}

func killAgent(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	if err := globalPool.Kill(id, agentName); err != nil {
		log.Ctx(c.Context()).Error().Msgf("kill agent %s/%s: %s", id, agentName, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "killed"})
}

func deleteSession(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if err := globalMgr.DeleteSession(c.Context(), id); err != nil {
		log.Ctx(c.Context()).Error().Msgf("delete session %s: %s", id, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// ── Workspaces ────────────────────────────────────────────────────────

func workspacesPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	c.HTML(view.WorkspacesPage(view.WorkspacesVM{
		Layout:        sidebarVM(c, "workspaces", ""),
		Base:          c.Base(),
		WorkspaceList: globalMgr.Registry().WorkspaceNames(),
		Workspaces:    globalMgr.Registry().Workspaces(),
		PresetList:    globalMgr.Registry().PresetNames(),
	}))
}

// workspaceOptionsJSON returns [{name, path}] for every workspace —
// consumed by the allowed_cmds scope dropdown in the settings UI.
func workspaceOptionsJSON(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	type option struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	wss := globalMgr.Registry().Workspaces()
	opts := make([]option, 0, len(wss))
	for _, ws := range wss {
		path := ws.Meta.CustomPath
		if path == "" {
			path = globalLayout.WorkspaceManagedPath(ws.Name)
		}
		opts = append(opts, option{Name: ws.Name, Path: path})
	}
	c.JSON(http.StatusOK, opts)
}

func createWorkspace(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := strings.TrimSpace(c.Form("name"))
	if name == "" {
		c.Error(http.StatusBadRequest, "workspace name required")
		return
	}
	opt := workspace.CreateOptions{
		Name:            name,
		CustomPath:      strings.TrimSpace(c.Form("custom_path")),
		DefaultPreset:   c.Form("preset"),
		DefaultProvider: c.Form("provider"),
		Description:     c.Form("description"),
	}
	if opt.DefaultPreset == "" {
		opt.DefaultPreset = "default"
	}
	if opt.DefaultProvider == "" {
		opt.DefaultProvider = "claude"
	}
	if _, err := globalMgr.CreateWorkspace(c.Context(), opt); err != nil {
		log.Ctx(c.Context()).Error().Msgf("create workspace %s: %s", name, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/workspaces", http.StatusSeeOther)
}

func deleteWorkspace(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := c.PathValue("name")
	if name == workspace.DefaultName {
		c.JSON(http.StatusForbidden, map[string]string{"error": "workspace \"default\" is built-in and cannot be deleted"})
		return
	}
	if err := globalMgr.DeleteWorkspace(c.Context(), name); err != nil {
		log.Ctx(c.Context()).Error().Msgf("delete workspace %s: %s", name, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// ── Presets ───────────────────────────────────────────────────────────

func presetsPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	c.HTML(view.PresetsPage(view.PresetsVM{
		Layout: sidebarVM(c, "presets", ""),
		Base:   c.Base(),
		Names:  globalMgr.Registry().PresetNames(),
	}))
}

func presetDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := c.PathValue("name")
	p, err := preset.Load(globalLayout, name)
	if err != nil {
		c.NotFound()
		return
	}
	c.HTML(view.PresetEditor(view.PresetDetailVM{
		Layout: sidebarVM(c, "presets", ""),
		Base:   c.Base(),
		Name:   p.Name,
		Body:   p.Body,
	}))
}

type presetReq struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

func createPreset(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := strings.TrimSpace(c.Form("name"))
	body := c.Form("body")
	if name == "" {
		c.Error(http.StatusBadRequest, "preset name required")
		return
	}
	if err := globalMgr.CreatePreset(name, body); err != nil {
		log.Ctx(c.Context()).Error().Msgf("create preset %s: %s", name, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/presets/"+name, http.StatusSeeOther)
}

func updatePreset(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := c.PathValue("name")
	body := c.Form("body")
	if err := globalMgr.UpdatePreset(name, body); err != nil {
		log.Ctx(c.Context()).Error().Msgf("update preset %s: %s", name, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "saved"})
}

func deletePreset(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := c.PathValue("name")
	if name == preset.DefaultName {
		c.JSON(http.StatusForbidden, map[string]string{"error": "preset \"default\" is built-in and cannot be deleted"})
		return
	}
	if err := globalMgr.DeletePreset(name); err != nil {
		log.Ctx(c.Context()).Error().Msgf("delete preset %s: %s", name, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// ── SSE ───────────────────────────────────────────────────────────────

func streamSSE(c *tool.Ctx) {
	if globalBcast == nil {
		c.Error(http.StatusServiceUnavailable, "broadcaster not ready")
		return
	}
	sessionID := c.Query("session")
	w := c.W
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	// Clear the server's default 60 s write timeout so the SSE connection
	// stays alive indefinitely until the client disconnects.
	_ = rc.SetWriteDeadline(time.Time{})
	ch, unsub := globalBcast.Subscribe(sessionID)
	defer unsub()

	ctx := c.R.Context()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	flush := func() { _ = rc.Flush() }

	// Immediately confirm the connection is alive so the browser doesn't
	// wait up to 15 s for the first keepalive tick.
	fmt.Fprintf(w, ": connected\n\n")
	flush()

	// Snapshot: emit the current lifecycle + substate for every active
	// agent in this session so the UI recovers state immediately on
	// refresh without waiting for the next real event.
	if sessionID != "" && globalPool != nil {
		for _, ev := range snapshotEvents(sessionID) {
			fmt.Fprintf(w, "event: agent\ndata: %s\n\n", ev.JSON())
		}
		flush()
	}

	for {
		select {
		case ev, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "event: agent\ndata: %s\n\n", ev.JSON())
			flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flush()
		case <-ctx.Done():
			return
		}
	}
}

// snapshotEvents builds the lifecycle + in-flight event list for sessionID
// from the current pool state. Used by both streamSSE (SSE replay on fresh
// connect) and streamSnapshot (JSON endpoint for SharedWorker reuse path).
//
// Two sources, in order of preference:
//  1. Live pool entry — turn is currently in flight, replay from RAM.
//  2. On-disk inflight.jsonl — turn was mid-stream when the process died
//     (server crash, agent killed before Done). Read the file and emit
//     the same event sequence so the FE shows what was already streamed
//     plus a stale "killed" lifecycle pill the operator can act on.
func snapshotEvents(sessionID string) []Event {
	var out []Event
	matched := false
	for _, e := range globalPool.ActiveSnapshot() {
		if e.SessionID != sessionID {
			continue
		}
		matched = true
		// At = actual LastActive (when lifecycle last transitioned) so
		// the FE can paint the right amount of idle-countdown burn on
		// refresh instead of restarting from zero.
		var lastActiveMs int64
		if !e.LastActive.IsZero() {
			lastActiveMs = e.LastActive.UnixMilli()
		}
		out = append(out, Event{
			SessionID: e.SessionID,
			AgentName: e.AgentName,
			Type:      "lifecycle",
			Lifecycle: e.Lifecycle,
			Data:      e.Substate,
			PID:       e.PID,
			At:        lastActiveMs,
		})
		// Replay the partial assistant text accumulated since the last
		// flushed turn — needed for mid-stream refresh so the bubble
		// repaints instead of going blank until Done arrives. The FE
		// treats text_delta as append; sending the full partial here
		// works because the FE clears any pending turn before replay.
		if e.PartialText != "" {
			out = append(out, Event{
				SessionID: e.SessionID,
				AgentName: e.AgentName,
				Type:      "text_delta",
				Data:      e.PartialText,
			})
		}
		for _, te := range e.InFlightEvents {
			ev := Event{
				SessionID: e.SessionID,
				AgentName: e.AgentName,
			}
			switch te.Type {
			case "thinking":
				ev.Type = "thinking"
				ev.Data = te.Text
				ev.At = te.At.UnixMilli()
			case "tool_use":
				ev.Type = "tool_use"
				ev.ToolName = te.ToolName
				ev.ToolInput = te.ToolInput
				ev.ToolUseID = te.ToolUseID
				ev.At = te.At.UnixMilli()
				ev.EndAt = te.EndAt.UnixMilli()
			case "tool_result":
				ev.Type = "tool_result"
				ev.ToolUseID = te.ToolUseID
				ev.IsError = te.IsError
				ev.Data = te.Text
				ev.At = te.At.UnixMilli()
			default:
				continue
			}
			out = append(out, ev)
		}
	}
	if matched {
		return out
	}
	// Pool has no live entry — fall back to inflight.jsonl on disk for
	// the case where a turn was mid-stream when the process died. Emit
	// the cached deltas + trace cards so the operator at least sees how
	// far the agent got before the kill.
	if globalLayout == (agentconfig.Layout{}) {
		return out
	}
	entries, err := agentstore.LoadInflight(globalLayout, sessionID)
	if err != nil || len(entries) == 0 {
		return out
	}
	for _, e := range entries {
		ev := Event{
			SessionID: sessionID,
		}
		switch e.Type {
		case "text_delta":
			ev.Type = "text_delta"
			ev.Data = e.Text
		case "thinking":
			ev.Type = "thinking"
			ev.Data = e.Text
			if !e.At.IsZero() {
				ev.At = e.At.UnixMilli()
			}
		case "tool_use":
			ev.Type = "tool_use"
			ev.ToolName = e.ToolName
			ev.ToolInput = e.ToolInput
			ev.ToolUseID = e.ToolUseID
			if !e.At.IsZero() {
				ev.At = e.At.UnixMilli()
			}
		case "tool_result":
			ev.Type = "tool_result"
			ev.ToolUseID = e.ToolUseID
			ev.IsError = e.IsError
			ev.Data = e.Text
			if !e.At.IsZero() {
				ev.At = e.At.UnixMilli()
			}
		default:
			continue
		}
		out = append(out, ev)
	}
	return out
}

// streamSnapshot returns the current lifecycle + in-flight events for a
// session as JSON. Called by the SharedWorker whenever a new page subscribes
// (even when the EventSource is already open) so the UI can replay trace
// cards without waiting for the next real event.
func streamSnapshot(c *tool.Ctx) {
	if globalPool == nil {
		c.JSON(http.StatusOK, []Event{})
		return
	}
	sessionID := c.Query("session")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "session required"})
		return
	}
	out := snapshotEvents(sessionID)
	if out == nil {
		out = []Event{}
	}
	c.JSON(http.StatusOK, out)
}

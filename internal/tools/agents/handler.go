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
	"github.com/yogasw/wick/internal/agents/workspace"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/tools/agents/view"
	wfnodes "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/all"
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
func SetDB(db *gorm.DB) { globalDB = db }

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
	r.Static("/static/nodes/", wfnodes.StaticFS)

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
	r.POST("/providers/rescan", rescanAllProviders)
	r.POST("/providers/rescan/{type}/{name}", rescanOneProvider)
	r.POST("/providers/probe-gate/{type}/{name}", probeProviderGate)
	r.POST("/providers/{type}/{name}/hooks/{event}/check", checkProviderHook)
	r.POST("/providers/{type}/{name}/hooks/{event}/enable", enableProviderHook)
	r.POST("/providers/{type}/{name}/hooks/{event}/disable", disableProviderHook)
	r.POST("/providers/auto-rescan/toggle", toggleAutoRescan)

	r.POST("/providers/mcp/{clientID}/install", mcpInstallClient)
	r.POST("/providers/mcp/{clientID}/uninstall", mcpUninstallClient)

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

	// Workflows tab — visual DAG editor (mockup §3).
	r.GET("/workflows", workflowsPage)
	r.POST("/workflows", createWorkflow)
	r.POST("/workflows/import", importWorkflow)
	r.GET("/workflows/edit/{id}/download", downloadWorkflowYAML)
	// ID-bound routes live under /edit/ so Go 1.22's mux doesn't
	// flag a conflict with /static/{path}. The ID is the folder name
	// (UUID for canvas-created workflows) — stable across name renames.
	r.GET("/workflows/edit/{id}", workflowEditor)
	r.POST("/workflows/edit/{id}/save", saveWorkflow)
	r.POST("/workflows/edit/{id}/rename", renameWorkflow)
	r.POST("/workflows/edit/{id}/publish", publishWorkflow)
	r.POST("/workflows/edit/{id}/discard", discardWorkflowDraft)
	r.POST("/workflows/edit/{id}/toggle", toggleWorkflow)
	r.POST("/workflows/edit/{id}/run", runWorkflowNow)
	r.POST("/workflows/edit/{id}/exec-node", execNodeStep)
	r.GET("/workflows/edit/{id}/runs/{runID}/state", workflowRunStateAPI)
	r.POST("/workflows/edit/{id}/runs/{runID}/copy-to-editor", copyRunToEditor)
	r.POST("/workflows/edit/{id}/delete", deleteWorkflow)
	r.GET("/workflows/edit/{id}/runs/{runID}", workflowRunDetail)
	r.GET("/workflows/edit/{id}/executions", executionsPanel)
	r.GET("/workflows/edit/{id}/executions/{runID}", executionDetail)
	r.GET("/workflows/api/registry", workflowRegistryAPI)
	r.GET("/workflows/api/lookup", workflowLookupAPI)
	r.POST("/workflows/edit/{id}/test", runWorkflowTests)
	r.GET("/workflows/edit/{id}/test-cases", listTestCases)
	r.POST("/workflows/edit/{id}/test-cases", saveTestCase)
	r.POST("/workflows/edit/{id}/test-cases/{name}/run", runOneTestCase)
	r.DELETE("/workflows/edit/{id}/test-cases/{name}", deleteTestCase)

	r.GET("/stream", streamSSE)
}

func settingsPage(c *tool.Ctx) {
	if notReady(c) {
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
	text := strings.TrimSpace(c.Form("message"))
	if text == "" {
		renderCompose(c, "", "Type a message to start the session.")
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
	if err := globalPool.Send(context.Background(), id, "main", "ui", "user", text); err != nil {
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
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "text required"})
		return
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
	if err := globalPool.Send(context.Background(), id, agentName, "ui", "user", req.Text); err != nil {
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

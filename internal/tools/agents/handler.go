// Package agents backs /tools/agents — the Agents UI Manager. It lets
// users manage AI agent sessions, workspaces, and presets from the
// browser and streams real-time agent output via Server-Sent Events.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	"github.com/yogasw/wick/internal/processctl"
	"github.com/yogasw/wick/internal/agents/providersync"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/skills"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/internal/pkg/ui"
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
	globalAuth       *login.Service
	globalDB         *gorm.DB
	globalChannels   *agentchannels.Registry
	globalSyncMgr    *providersync.Manager
	globalSkillStore *skills.Store
	globalTagsSvc    *tags.Service
	globalConnectors *connectors.Service
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
func SetManager(m *registry.Manager) {
	globalMgr = m
	ui.SetAgentsAvailable(func() bool { return globalMgr != nil })
}

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

// SetAuth wires the login service so per-user preferences (pinned
// project) can be read/written from the agents tool.
func SetAuth(a *login.Service) { globalAuth = a }

// actorID returns the logged-in user's id for authorship stamping, or
// "" when no user is in context (internal / MCP callers).
func actorID(c *tool.Ctx) string {
	if u := login.GetUser(c.Context()); u != nil {
		return u.ID
	}
	return ""
}

// SetDB wires the shared GORM DB so channel handlers can read/write
// agent_channels rows. Without this, channel config endpoints 503.
//
// Workflow data is DB-primary. Folders left on disk from before the
// DB migration are not imported automatically — use the SPA or MCP
// to recreate them, then remove the old folders.
func SetDB(db *gorm.DB) {
	globalDB = db
}

// SetChannelRegistry wires the live channel registry so picker fields
// can issue lookup queries against each channel's upstream (Slack API,
// etc.). Without this, /channels/{slug}/lookup returns 503.
func SetChannelRegistry(r *agentchannels.Registry) { globalChannels = r }

// SetSyncManager wires the provider storage sync manager.
func SetSyncManager(m *providersync.Manager) { globalSyncMgr = m }

// SetSkillStore wires the skills ownership store so delete/upload/sync
// handlers can enforce owner-or-admin access control.
func SetSkillStore(s *skills.Store) { globalSkillStore = s }

// SetTagsService wires the tags service for skill ownership checks.
func SetTagsService(svc *tags.Service) { globalTagsSvc = svc }

// SetConnectors wires the connectors service so the session Config tab
// can read a connector's field schema (to render the form) and resolve
// which connectors opted into per-session overrides.
func SetConnectors(c *connectors.Service) { globalConnectors = c }

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
	r.POST("/sessions/{id}/project", moveSessionToProject)
	r.POST("/sessions/{id}/kill", killAgent)
	r.POST("/sessions/{id}/dequeue", dequeueAgent)
	r.GET("/sessions/{id}/subscription", sessionSubscriptionStatus)
	r.POST("/sessions/{id}/subscribe", sessionSubscribe)
	r.POST("/sessions/{id}/unsubscribe", sessionUnsubscribe)
	r.DELETE("/sessions/{id}", deleteSession)

	r.GET("/sessions/{id}/uploads/{name}", sessionUploadServe)
	r.GET("/sessions/{id}/turns/{turn_id}", sessionTurnTrace)
	r.GET("/sessions/{id}/turns/{turn_id}/events/{event_id}", sessionTurnEvent)

	r.GET("/sessions/{id}/files", sessionContextList)
	r.GET("/sessions/{id}/processes", sessionProcesses)
	r.GET("/sessions/{id}/files/read", sessionContextRead)
	r.GET("/sessions/{id}/files/download", sessionContextDownload)
	r.POST("/sessions/{id}/files/save", sessionContextSave)
	r.POST("/sessions/{id}/files/create", sessionContextCreate)
	r.DELETE("/sessions/{id}/files", sessionContextDelete)

	// JSON API — conversation SPA endpoints.
	r.GET("/api/sessions", apiSessionList)
	r.GET("/api/sessions/{id}/conversation", apiSessionConversation)
	r.GET("/api/sessions/{id}/meta", apiSessionMeta)

	// Git source control (session cwd, multi-repo).
	registerSCM(r)

	// Gate approval (Stage 5). Modal in the UI POSTs the user's
	// decision here; revoke removes a previously-approved match key.
	r.POST("/sessions/{id}/approve", approveCommand)
	r.GET("/sessions/{id}/approvals", approvalsSnapshot)
	r.DELETE("/sessions/{id}/approve/{matchKey}", revokeApproval)

	// ask_user (Stage 6). MCP tool blocks; the card in the UI
	// POSTs the answer here; rehydrate runs on page load.
	r.POST("/sessions/{id}/answer", answerAsk)
	r.GET("/sessions/{id}/asks", asksSnapshot)
	// Session workspace (the Config tab) — user-initiated, ephemeral
	// connector instances scoped to this session, no agent involvement.
	r.GET("/sessions/{id}/workspace", sessionWorkspaceListUI)
	r.POST("/sessions/{id}/workspace", sessionWorkspaceAddUI)
	r.GET("/sessions/{id}/workspace/{cid}", sessionWorkspaceInstanceUI)
	r.POST("/sessions/{id}/workspace/{cid}", sessionWorkspaceSetUI)
	r.POST("/sessions/{id}/workspace/{cid}/duplicate", sessionWorkspaceDuplicateUI)
	r.POST("/sessions/{id}/workspace/{cid}/rename", sessionWorkspaceRenameUI)
	r.POST("/sessions/{id}/workspace/{cid}/test", sessionWorkspaceTestUI)
	r.DELETE("/sessions/{id}/workspace/{cid}", sessionWorkspaceRemoveUI)

	// No standalone /projects list page — the sidebar Projects section is
	// the canonical project nav. "+ New" → /projects/new (create page),
	// project rows → /sessions?project=<id> (scoped landing), and
	// /projects/{id} is the per-project settings page.
	r.GET("/projects", projectsRedirect) // legacy entry → all chats
	r.GET("/projects/options", projectOptionsJSON)
	r.GET("/projects/{id}", projectSettingsPage)
	r.POST("/projects", createProject)
	r.POST("/projects/{id}", updateProject)
	r.POST("/projects/{id}/pin", toggleProjectPin)
	r.DELETE("/projects/{id}", deleteProject)

	r.GET("/presets", presetsPage)
	r.GET("/presets/{name}", presetDetail)
	r.POST("/presets", createPreset)
	r.POST("/presets/{name}", updatePreset)
	r.DELETE("/presets/{name}", deletePreset)

	r.GET("/providers", providersPage)
	r.GET("/providers/detail/{type}/{name}", providerDetailPage)
	r.POST("/providers/detail/{type}/{name}/save", saveProviderDetail)
	r.POST("/providers/detail/{type}/{name}/{key}", saveProviderConfigKey)
	r.GET("/providers/{type}/{name}", providerDetailPage)
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
	r.GET("/channels/{slug}/status", channelStatusHandler)

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

func requireAdmin(c *tool.Ctx) bool {
	if u := login.GetUser(c.Context()); u == nil || !u.IsAdmin() {
		c.Error(http.StatusForbidden, "admins only")
		return false
	}
	return true
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
	return sidebarVMScoped(c, activePage, activeSessionID, "")
}

// sidebarVMScoped builds the sidebar VM, optionally scoped to a project.
// When scopedProjectID is set, the Recent list is filtered to that
// project's sessions and the scoped breadcrumb renders.
func sidebarVMScoped(c *tool.Ctx, activePage, activeSessionID, scopedProjectID string) view.AgentsLayoutVM {
	const sidebarCap = 15
	allSessions := globalMgr.Registry().Sessions()
	// Per-project session counts across ALL sessions (sidebar pills).
	counts := make(map[string]int, len(allSessions))
	for _, s := range allSessions {
		if s.Meta.ProjectID != "" {
			counts[s.Meta.ProjectID]++
		}
	}
	allIDs := globalMgr.Registry().SessionIDs()
	// Scoped sidebar: keep only sessions bound to the active project.
	if scopedProjectID != "" {
		filtered := allIDs[:0:0]
		for _, id := range allIDs {
			if s, ok := allSessions[id]; ok && s.Meta.ProjectID == scopedProjectID {
				filtered = append(filtered, id)
			}
		}
		allIDs = filtered
	}
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
	allProjects := globalMgr.Registry().Projects()
	allProjectIDs := globalMgr.Registry().ProjectIDs()
	if u := login.GetUser(c.Context()); u != nil && !u.IsAdmin() {
		filtered := allProjectIDs[:0:0]
		for _, pid := range allProjectIDs {
			p, ok := allProjects[pid]
			if !ok {
				continue
			}
			if globalTagsSvc != nil {
				owns, _ := globalTagsSvc.UserOwnsResource(c.Context(), u.ID, pid)
				if owns {
					filtered = append(filtered, pid)
				}
			} else if p.Meta.OwnerUserID == "" || p.Meta.OwnerUserID == u.ID {
				filtered = append(filtered, pid)
			}
		}
		allProjectIDs = filtered
		filteredMap := make(map[string]project.Project, len(filtered))
		for _, pid := range filtered {
			filteredMap[pid] = allProjects[pid]
		}
		allProjects = filteredMap
	}
	return view.AgentsLayoutVM{
		Base:             c.Base(),
		ActivePage:       activePage,
		SidebarIDs:       ids,
		SidebarSessions:  globalMgr.Registry().Sessions(),
		SidebarLifecycle: lc,
		SidebarLabels:    labels,
		ActiveSessionID:  activeSessionID,
		IdleTimeoutMs:    globalPool.IdleTimeout().Milliseconds(),
		Projects:         allProjects,
		ProjectList:      allProjectIDs,
		ProjectCounts:    counts,
		ScopedProjectID:  scopedProjectID,
		PinnedProjectID:  pinnedProjectID(c),
	}
}

// projectChoices builds the picker rows for the compose form / move menu
// from the registry projects, ordered by display name. Non-admin users only
// see their own personal project.
func projectChoices(c *tool.Ctx) []view.ProjectChoiceVM {
	ids := globalMgr.Registry().ProjectIDs()
	projects := globalMgr.Registry().Projects()
	u := login.GetUser(c.Context())
	out := make([]view.ProjectChoiceVM, 0, len(ids))
	for _, id := range ids {
		p, ok := projects[id]
		if !ok {
			continue
		}
		if u != nil && !u.IsAdmin() {
			if globalTagsSvc != nil {
				owns, _ := globalTagsSvc.UserOwnsResource(c.Context(), u.ID, id)
				if !owns {
					continue
				}
			} else if p.Meta.OwnerUserID != "" && p.Meta.OwnerUserID != u.ID {
				continue
			}
		}
		out = append(out, view.ProjectChoiceVM{
			ID:              id,
			Name:            p.Meta.Name,
			Icon:            p.Meta.Icon,
			DefaultPreset:   p.Meta.Defaults.Preset,
			DefaultProvider: p.Meta.Defaults.Provider,
		})
	}
	return out
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
		UserID: actorID(c),
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

// ownsSession reports whether the caller may access sess. App owners see
// all sessions; regular users may only access sessions they own or legacy
// sessions that have no recorded owner (UserID=="").
func ownsSession(c *tool.Ctx, sess session.Session) bool {
	u := login.GetUser(c.Context())
	if u == nil {
		return true
	}
	if u.CanSeeAllSessions() {
		return true
	}
	return sess.Meta.UserID == "" || sess.Meta.UserID == u.ID
}

// ensurePersonalProjectForUser auto-creates a personal project for a non-admin
// user on their first agents access, then pins it so pinnedProjectID() returns
// it on subsequent visits. Best-effort: errors are logged and do not block access.
func ensurePersonalProjectForUser(c *tool.Ctx) {
	if globalMgr == nil || globalAuth == nil {
		return
	}
	u := login.GetUser(c.Context())
	if u == nil || u.IsAdmin() {
		return
	}
	pid, err := project.FindPersonalProject(globalLayout, u.ID)
	if err != nil {
		log.Ctx(c.Context()).Warn().Err(err).Str("user", u.ID).Msg("findPersonalProject failed")
		return
	}
	if pid == "" {
		opt := project.PersonalProjectOptions(uuid.New().String(), u.ID, u.Name)
		p, cerr := globalMgr.CreateProject(c.Context(), opt)
		if cerr != nil {
			log.Ctx(c.Context()).Warn().Err(cerr).Str("user", u.ID).Msg("createPersonalProject failed")
			return
		}
		pid = p.Meta.ID
		if globalTagsSvc != nil {
			_ = globalTagsSvc.CreateResourceOwnerTag(c.Context(), pid, u.ID)
		}
	}
	if u.Metadata.PinnedAgentProjectID == pid {
		return
	}
	if err := globalAuth.SetPinnedAgentProject(c.Context(), u.ID, pid); err != nil {
		log.Ctx(c.Context()).Warn().Err(err).Str("user", u.ID).Msg("pin personal project failed")
	}
}

// newSessionCompose renders the compose page: provider/preset/workspace
// pickers + a textarea for the first message. No session is persisted
// here — that only happens in startNewSession when the form posts back
// with a non-empty message.
func newSessionCompose(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ensurePersonalProjectForUser(c)
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
	projectID := c.Form("project_id")
	presetName := c.Form("preset")
	if presetName == "" {
		presetName = "default"
		if projectID != "" {
			if p, perr := project.Load(globalLayout, projectID); perr == nil && p.Meta.Defaults.Preset != "" {
				presetName = p.Meta.Defaults.Preset
			}
		}
	}
	id := uuid.New().String()
	if _, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        id,
		ProjectID: projectID,
		Origin:    session.OriginUI,
		Preset:    presetName,
		UserID:    actorID(c),
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
	// Pre-subscribe: the new-session composer carries a bell with a
	// hidden "subscribe" input that flips to "1" when toggled on. If
	// set, opt the calling user in to lifecycle pushes for this brand-
	// new session right after creation. Best-effort — registry failures
	// here would be confusing to surface inline because the session is
	// already live; log and continue.
	if c.Form("subscribe") == "1" {
		if u := login.GetUser(c.Context()); u != nil {
			if _, err := globalMgr.SubscribeUser(id, u.ID); err != nil {
				log.Ctx(c.Context()).Warn().Err(err).Str("session", id).Msg("compose pre-subscribe failed")
			}
		}
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
	scoped := c.Query("project")
	if scoped != "" {
		if _, ok := globalMgr.Registry().Project(scoped); !ok {
			scoped = ""
		}
	}
	// No explicit ?project= → land on the user's pinned project (their
	// personal default). Opening the agents tool drops straight into it.
	// The compose picker still lets them pick "— no project —" per-session.
	if scoped == "" {
		scoped = pinnedProjectID(c)
	}
	// Scope the sidebar too so the compose page keeps the breadcrumb +
	// filtered Recent when opened from a scoped project (mockup ②).
	layout := sidebarVMScoped(c, "new", "", scoped)
	layout.FullBleed = true
	// When not explicitly scoped, fall back to the operator's configured
	// default project (Settings → default_project_id) as a soft default.
	configuredDefault := ""
	if globalConfigs != nil {
		configuredDefault = globalConfigs.GetOwned("agents", "default_project_id")
	}
	// effective = the project whose defaults prefill provider/preset.
	effective := scoped
	if effective == "" {
		effective = configuredDefault
	}
	defaultPreset := ""
	if effective != "" {
		if p, ok := globalMgr.Registry().Project(effective); ok {
			if p.Meta.Defaults.Provider != "" {
				defaultProv = p.Meta.Defaults.Provider
			}
			defaultPreset = p.Meta.Defaults.Preset
		} else if effective == configuredDefault {
			// Stale configured default (project deleted) — ignore.
			configuredDefault = ""
		}
	}
	c.HTML(view.NewSessionCompose(view.NewSessionComposeVM{
		Layout:           layout,
		Base:             c.Base(),
		Providers:        providers,
		Presets:          globalMgr.Registry().PresetNames(),
		Projects:         projectChoices(c),
		DefaultProvider:  defaultProv,
		DefaultPreset:    defaultPreset,
		ScopedProjectID:  scoped,
		DefaultProjectID: configuredDefault,
		Message:          message,
		Error:            errMsg,
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
	projects := globalMgr.Registry().Projects()
	queued := make([]view.QueuedEntryVM, len(queue))
	for i, q := range queue {
		projName := ""
		if s, ok := globalMgr.Registry().Session(q.SessionID); ok && s.Meta.ProjectID != "" {
			if p, ok := projects[s.Meta.ProjectID]; ok {
				projName = p.Meta.Name
			}
		}
		queued[i] = view.QueuedEntryVM{
			SessionID: q.SessionID,
			AgentName: q.AgentName,
			WaitingMs: now.Sub(q.Enqueued).Milliseconds(),
			Label:     loadFirstUserMessage(globalLayout, q.SessionID, 60),
			Project:   projName,
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
		Projects:      globalMgr.Registry().Projects(),
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
	scoped := c.Query("project")
	// Validate the scope — an unknown project id falls back to all chats.
	if scoped != "" {
		if _, ok := globalMgr.Registry().Project(scoped); !ok {
			scoped = ""
		}
	}
	ids := globalMgr.Registry().SessionIDs()
	caller := login.GetUser(c.Context())
	allSessions := globalMgr.Registry().Sessions()
	if scoped != "" {
		filtered := ids[:0:0]
		for _, id := range ids {
			if s, ok := allSessions[id]; ok && s.Meta.ProjectID == scoped {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	if caller != nil && !caller.CanSeeAllSessions() {
		filtered := ids[:0:0]
		for _, id := range ids {
			if s, ok := allSessions[id]; ok && (s.Meta.UserID == "" || s.Meta.UserID == caller.ID) {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	// Render all rows; pagination (10/page) + search run client-side so
	// paging never reloads the page (keeps the compose box + search text).
	// Guard against pathological counts with a generous cap.
	const maxRender = 1000
	if len(ids) > maxRender {
		ids = ids[:maxRender]
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
	pageLabels := make(map[string]string, len(ids))
	for _, id := range ids {
		pageLabels[id] = loadFirstUserMessage(globalLayout, id, 60)
	}
	providers := providerChoicesCached(c.Context())
	// Build the scoped project landing composer (Claude-style: compose
	// box on top of the chats list, defaults inherited from the project).
	var composer view.ComposerVM
	if scoped != "" {
		defProv := ""
		if len(providers) > 0 {
			defProv = providers[0].Type
		}
		defPreset := ""
		if p, ok := globalMgr.Registry().Project(scoped); ok {
			if p.Meta.Defaults.Provider != "" {
				defProv = p.Meta.Defaults.Provider
			}
			defPreset = p.Meta.Defaults.Preset
		}
		composer = view.ComposerVM{
			Base:            c.Base(),
			Providers:       providers,
			Presets:         globalMgr.Registry().PresetNames(),
			DefaultProvider: defProv,
			DefaultPreset:   defPreset,
			ScopedProjectID: scoped,
			Scoped:          true,
			// Project picker hidden — already in the project (binding sent
			// as a hidden field). Matches Claude's project landing.
			ShowProjectPicker: false,
		}
	}
	c.HTML(view.SessionsList(view.SessionsListVM{
		Layout:          sidebarVMScoped(c, "sessions", "", scoped),
		Base:            c.Base(),
		IDs:             ids,
		Sessions:        allSessions,
		Labels:          pageLabels,
		Projects:        globalMgr.Registry().Projects(),
		ProjectList:     globalMgr.Registry().ProjectIDs(),
		PresetList:      globalMgr.Registry().PresetNames(),
		Providers:       providers,
		Lifecycle:       lc,
		IdleTimeoutMs:   globalPool.IdleTimeout().Milliseconds(),
		ScopedProjectID: scoped,
		Composer:        composer,
	}))
}

func createSession(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	projectID := c.Form("project_id")
	prov := c.Form("provider")
	if prov == "" {
		prov = "claude"
	}
	id := uuid.New().String()
	presetName := "default"
	if projectID != "" {
		if p, perr := project.Load(globalLayout, projectID); perr == nil && p.Meta.Defaults.Preset != "" {
			presetName = p.Meta.Defaults.Preset
		}
	}
	_, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        id,
		ProjectID: projectID,
		Origin:    session.OriginUI,
		Preset:    presetName,
		UserID:    actorID(c),
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
	if !ok || !ownsSession(c, sess) {
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
				TurnID:      t.TurnID,
				Role:        t.Role,
				Agent:       t.Agent,
				Provider:    t.Provider,
				Text:        t.Text,
				Truncated:   t.Truncated,
				Interrupted: t.Interrupted,
				HasTrace:    t.HasTrace,
				Time:        t.Timestamp,
			}
			// Legacy: old turns that inlined events are still rendered inline.
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
		Layout:          sidebarVMScoped(c, "sessions", id, sess.Meta.ProjectID),
		Base:            c.Base(),
		Session:         sess,
		Tab:             tab,
		Turns:           turns,
		CmdLines:        cmdLines,
		IdleTimeoutMs:   globalPool.IdleTimeout().Milliseconds(),
		Providers:       providerChoicesCached(c.Context()),
		ActiveProvider:  activeProv,
		Projects:        globalMgr.Registry().Projects(),
		ProjectList:     globalMgr.Registry().ProjectIDs(),
		ActiveProjectID: sess.Meta.ProjectID,
		SCMAssetURL:     spaAssetURL("scm"),
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
	// Chat is a full-height app surface — skip the layout's px-6 py-6
	// gutter so the composer sits flush at the viewport bottom (no
	// leftover padding to fight the keyboard) and the overlay header
	// aligns to the true top instead of being shifted by a -mt-6 hack.
	vm.Layout.FullBleed = true
	c.HTML(view.SessionDetail(vm))
}

type switchProviderReq struct {
	Provider string `json:"provider"`
}

// switchProvider creates a new session with the same project but a
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
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	u := login.GetUser(c.Context())
	callerID := ""
	if u != nil {
		callerID = u.ID
	}
	newID := uuid.New().String()
	_, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        newID,
		ProjectID: sess.Meta.ProjectID,
		Origin:    session.OriginUI,
		UserID:    callerID,
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

type moveSessionReq struct {
	ProjectID string `json:"project_id"`
}

// moveSessionToProject moves the session to a different project in-place
// and kills any running subprocess so it respawns in the new folder on
// the next message. Empty project_id unscopes the session.
func moveSessionToProject(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req moveSessionReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "project_id required"})
		return
	}
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if err := globalMgr.MoveSession(c.Context(), id, req.ProjectID); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Kill running subprocess so next Send respawns in the new folder.
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	if agentName != "" {
		_ = globalPool.Kill(id, agentName)
	}
	c.JSON(http.StatusOK, map[string]string{"status": "moved", "project_id": req.ProjectID})
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
		if !ok || !ownsSession(c, sess) {
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
	if !ok || !ownsSession(c, sess) {
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
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	// Remove every queued entry for this session (any agent) + clear its
	// buffered input so it won't execute. Preserve the rest of meta.
	removed := globalPool.DequeueSession(id)
	meta := sess.Meta
	meta.Status = session.StatusIdle
	meta.PendingInput = nil
	meta.LastActive = time.Now().UTC()
	_ = session.SaveMeta(globalLayout, id, meta)
	globalMgr.Register(sessionWithMeta(sess, meta))
	c.JSON(http.StatusOK, map[string]any{"status": "dequeued", "removed": removed})
}

// sessionWithMeta returns a copy of sess with replaced meta, for
// refreshing the registry cache after a meta-only mutation.
func sessionWithMeta(sess session.Session, meta session.Meta) session.Session {
	sess.Meta = meta
	return sess
}

// sessionSubscriptionStatus reports whether the calling user is on the
// session's lifecycle-push subscriber list. Used by the bell UI to
// decide its on/off rendering — anyone can open any session, so the
// bell state has to come from the server, not the browser.
func sessionSubscriptionStatus(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"subscribed": sess.Meta.IsSubscribed(u.ID),
	})
}

// sessionSubscribe adds the calling user to the session's lifecycle-
// push subscriber list. Idempotent: subscribing twice is a no-op.
// Sessions are shared (anyone can open them) but pushes target only
// the IDs in meta.Subscribers, so a user "watching" a session
// explicitly opts in via this endpoint.
func sessionSubscribe(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	id := c.PathValue("id")
	if sess, ok := globalMgr.Registry().Session(id); !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if _, err := globalMgr.SubscribeUser(id, u.ID); err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"subscribed": true})
}

// sessionUnsubscribe drops the calling user from the subscriber list.
// Idempotent: unsubscribing when not subscribed is a no-op.
func sessionUnsubscribe(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	id := c.PathValue("id")
	if sess, ok := globalMgr.Registry().Session(id); !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if _, err := globalMgr.UnsubscribeUser(id, u.ID); err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"subscribed": false})
}

// sessionProcesses returns active pool entries for the given session as JSON.
func sessionProcesses(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if sess, ok := globalMgr.Registry().Session(id); !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	type procEntry struct {
		SessionID string `json:"session_id"`
		AgentName string `json:"agent_name"`
		Provider  string `json:"provider"` // "type/name" e.g. codex/codex
		PID       int    `json:"pid"`
		Queued    int    `json:"queued"` // messages waiting after current turn
		Lifecycle string `json:"lifecycle"`
		Substate  string `json:"substate"`
		Alive     bool   `json:"alive"` // OS-verified: PID exists AND is our process
	}
	var out []procEntry
	if globalPool != nil {
		// Opening the panel is a natural reconcile trigger: drop any
		// active entry whose subprocess is actually dead (crashed /
		// externally killed without the reader seeing EOF). Releases the
		// slot + drains the queue so a zombie idle entry can't wedge it.
		globalPool.ReconcileDead()
		// Per-session view: this slide-over belongs to one session, so list
		// only that session's spawns. The global picture lives on the admin
		// Providers page; here the operator wants just their own session.
		for _, e := range globalPool.ActiveSnapshot() {
			if e.SessionID != id {
				continue
			}
			// PID alive AND identity matches (recycled PID owned by another
			// process reads as not-alive). 0 = test fake / pre-PID spawn.
			//
			// Respawn-mode agents (codex) have NO live process between turns
			// by design — the one-shot turn process exits and the agent idles
			// until the next message. A dead PID there is healthy, not a
			// zombie, so don't flag it dead; the lifecycle/substate
			// (idle/working) already conveys the real status.
			alive := e.Respawns || e.PID == 0 || processctl.ProcessAlive(e.PID)
			prov := e.ProviderType
			if e.ProviderName != "" && e.ProviderName != e.ProviderType {
				prov = e.ProviderType + "/" + e.ProviderName
			}
			out = append(out, procEntry{
				SessionID: e.SessionID,
				AgentName: e.AgentName,
				Provider:  prov,
				PID:       e.PID,
				Queued:    e.Queued,
				Lifecycle: e.Lifecycle,
				Substate:  e.Substate,
				Alive:     alive,
			})
		}
		// Same session filter, applied to the FIFO queue: a request still
		// waiting for a slot has no PID yet, so it never appears in the
		// active snapshot. Surface it as a "queued" row (pid 0 → "—") so the
		// operator sees their request is accepted but not yet running.
		for _, q := range globalPool.QueueSnapshot() {
			if q.SessionID != id {
				continue
			}
			out = append(out, procEntry{
				SessionID: q.SessionID,
				AgentName: q.AgentName,
				PID:       0,
				Lifecycle: "queued",
				// Pending, not a dead zombie: there's no process yet, so the
				// "alive" probe doesn't apply. Mark alive so the UI renders
				// the "queued" status instead of flipping it to "dead".
				Alive: true,
			})
		}
	}
	if out == nil {
		out = []procEntry{}
	}
	c.JSON(http.StatusOK, out)
}

func killAgent(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
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
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if err := globalMgr.DeleteSession(c.Context(), id); err != nil {
		log.Ctx(c.Context()).Error().Msgf("delete session %s: %s", id, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// sessionTurnEvent serves the payload for one large trace event.
// File lives at thinking/<turn_id>/<event_id>.json.
func sessionTurnEvent(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if sess, ok := globalMgr.Registry().Session(id); !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no payload"})
		return
	}
	turnID := c.PathValue("turn_id")
	eventID := c.PathValue("event_id")
	data, err := os.ReadFile(globalLayout.SessionThinkingEvent(id, turnID, eventID))
	if errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no payload"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.W.Header().Set("Content-Type", "application/json")
	_, _ = c.W.Write(data)
}

// sessionTurnTrace serves the trace payload for one assistant turn.
// The file lives at thinking/<turn_id>.json inside the session dir.
// Returns 404 when the turn has no trace (user turns, old turns).
func sessionTurnTrace(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if sess, ok := globalMgr.Registry().Session(id); !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no trace"})
		return
	}
	turnID := c.PathValue("turn_id")
	tracePath := globalLayout.SessionThinking(id, turnID)
	data, err := os.ReadFile(tracePath)
	if errors.Is(err, os.ErrNotExist) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no trace"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.W.Header().Set("Content-Type", "application/json")
	_, _ = c.W.Write(data)
}

// ── Projects ──────────────────────────────────────────────────────────

// projectsRedirect keeps the legacy /projects URL alive — projects are
// managed from the sidebar now, so it just lands on All chats.
func projectsRedirect(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	c.Redirect(c.Base()+"/sessions", http.StatusSeeOther)
}

// pinnedProjectID returns the current user's pinned project id, or "" if
// none / unset / the user isn't logged in. Validates the project still
// exists so a deleted-but-still-pinned project doesn't break the landing.
func pinnedProjectID(c *tool.Ctx) string {
	u := login.GetUser(c.Context())
	if u == nil {
		return ""
	}
	pid := u.Metadata.PinnedAgentProjectID
	if pid == "" {
		return ""
	}
	if _, ok := globalMgr.Registry().Project(pid); !ok {
		return ""
	}
	return pid
}

// toggleProjectPin sets the current user's pinned project to {id}, or
// clears it when {id} is already pinned. One pin per user; it becomes
// their personal default project (auto-scoped on open). Stored in
// UserMetadata.
func toggleProjectPin(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalAuth == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "auth service not wired"})
		return
	}
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "not logged in"})
		return
	}
	id := c.PathValue("id")
	if _, ok := globalMgr.Registry().Project(id); !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	pinned := id
	if u.Metadata.PinnedAgentProjectID == id {
		pinned = "" // toggle off
	}
	if err := globalAuth.SetPinnedAgentProject(c.Context(), u.ID, pinned); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "ok", "pinned": pinned != "", "project_id": pinned})
}

// projectSettingsPage renders the full project settings page (mockup ④)
// for an existing project, or the create form when id == "new".
func projectSettingsPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	vm := view.ProjectSettingsVM{
		Layout:     sidebarVM(c, "projects", ""),
		Base:       c.Base(),
		PresetList: globalMgr.Registry().PresetNames(),
		Managed:    true,
	}
	if id == "new" {
		vm.IsNew = true
		vm.Icon = "📁"
		vm.DefaultPreset = "default"
		vm.DefaultProvider = "claude"
		vm.Action = c.Base() + "/projects"
		c.HTML(view.ProjectSettingsPage(vm))
		return
	}
	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.NotFound()
		return
	}
	vm.ID = id
	vm.Name = p.Meta.Name
	vm.Icon = p.Meta.Icon
	vm.Description = p.Meta.Description
	vm.CustomPath = p.Meta.CustomPath
	vm.Managed = p.Meta.CustomPath == ""
	vm.IsDefault = p.Meta.Name == project.DefaultName
	vm.DefaultPreset = p.Meta.Defaults.Preset
	vm.DefaultProvider = p.Meta.Defaults.Provider
	vm.SystemAddon = p.Meta.Defaults.SystemAddon
	vm.CreatedAt = p.Meta.CreatedAt.Format("2006-01-02")
	vm.Action = c.Base() + "/projects/" + id
	// Session count + pinned labels.
	for sid, s := range globalMgr.Registry().Sessions() {
		if s.Meta.ProjectID == id {
			vm.ChatCount++
		}
		_ = sid
	}
	for _, pinID := range p.Meta.PinnedSessions {
		label := loadFirstUserMessage(globalLayout, pinID, 50)
		if label == "" {
			label = pinID
		}
		vm.Pinned = append(vm.Pinned, view.PinnedSessionVM{ID: pinID, Label: label})
	}
	if b, err := json.MarshalIndent(p.Meta, "", "  "); err == nil {
		vm.MetaJSON = string(b)
	}
	c.HTML(view.ProjectSettingsPage(vm))
}

// projectOptionsJSON returns [{id, name, path}] for every project —
// consumed by the allowed_cmds scope dropdown in the settings UI.
func projectOptionsJSON(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	type option struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	projects := globalMgr.Registry().Projects()
	opts := make([]option, 0, len(projects))
	for id, p := range projects {
		path := p.Meta.CustomPath
		if path == "" {
			path = globalLayout.ProjectManagedPath(id)
		}
		opts = append(opts, option{ID: id, Name: p.Meta.Name, Path: path})
	}
	c.JSON(http.StatusOK, opts)
}

// createProject materializes a new project from the settings form.
func createProject(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	name := strings.TrimSpace(c.Form("name"))
	if name == "" {
		c.Error(http.StatusBadRequest, "project name required")
		return
	}
	// Folder mode radio: "managed" forces an empty custom path regardless
	// of any stale value in the path input.
	customPath := strings.TrimSpace(c.Form("custom_path"))
	if c.Form("folder_mode") == "managed" {
		customPath = ""
	}
	opt := project.CreateOptions{
		ID:          uuid.New().String(),
		Name:        name,
		Icon:        strings.TrimSpace(c.Form("icon")),
		Description: c.Form("description"),
		CustomPath:  customPath,
		OwnerUserID: actorID(c),
		Defaults: project.Defaults{
			Preset:      c.Form("preset"),
			Provider:    c.Form("provider"),
			SystemAddon: c.Form("system_addon"),
		},
	}
	if _, err := globalMgr.CreateProject(c.Context(), opt); err != nil {
		log.Ctx(c.Context()).Error().Msgf("create project %s: %s", name, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	if globalTagsSvc != nil {
		_ = globalTagsSvc.CreateResourceOwnerTag(c.Context(), opt.ID, actorID(c))
	}
	// Land on the new project's page (sidebar-driven nav — no list page).
	c.Redirect(c.Base()+"/sessions?project="+opt.ID, http.StatusSeeOther)
}

// updateProject patches an existing project's editable fields
// (name / icon / description / defaults / custom_path / pinned).
func updateProject(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.Error(http.StatusNotFound, "project not found")
		return
	}
	meta := p.Meta
	// Unpin path: a lightweight POST carrying only `unpin=<sid>` removes
	// one pinned session without touching other fields.
	if sid := c.Form("unpin"); sid != "" {
		kept := meta.PinnedSessions[:0:0]
		for _, pin := range meta.PinnedSessions {
			if pin != sid {
				kept = append(kept, pin)
			}
		}
		meta.PinnedSessions = kept
		if _, err := globalMgr.UpdateProject(c.Context(), id, meta); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, map[string]string{"status": "unpinned"})
		return
	}
	if v := strings.TrimSpace(c.Form("name")); v != "" {
		meta.Name = v
	}
	if v := c.Form("icon"); v != "" {
		meta.Icon = strings.TrimSpace(v)
	}
	meta.Description = c.Form("description")
	if v := c.Form("preset"); v != "" {
		meta.Defaults.Preset = v
	}
	meta.Defaults.Provider = c.Form("provider")
	meta.Defaults.SystemAddon = c.Form("system_addon")
	// Folder mode radio: "managed" forces empty custom path; "custom"
	// keeps the path input. Managed files/ left in place on switch (§4.2).
	customPath := strings.TrimSpace(c.Form("custom_path"))
	if c.Form("folder_mode") == "managed" {
		customPath = ""
	}
	meta.CustomPath = customPath
	if _, err := globalMgr.UpdateProject(c.Context(), id, meta); err != nil {
		log.Ctx(c.Context()).Error().Msgf("update project %s: %s", id, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/sessions?project="+id, http.StatusSeeOther)
}

func deleteProject(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	p, ok := globalMgr.Registry().Project(id)
	if ok && p.Meta.Name == project.DefaultName {
		c.JSON(http.StatusForbidden, map[string]string{"error": "the default project cannot be deleted"})
		return
	}
	if err := globalMgr.DeleteProject(c.Context(), id); err != nil {
		log.Ctx(c.Context()).Error().Msgf("delete project %s: %s", id, err.Error())
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

// poolStatsEvent builds a pool_stats SSE Event JSON string for the
// current pool state. Returns "" when no pool is available.
func poolStatsEvent() string {
	if globalPool == nil {
		return ""
	}
	entries := globalPool.ActiveSnapshot()
	procs := make([]LiveProcessEntry, 0, len(entries))
	for _, e := range entries {
		prov := e.ProviderType
		if e.ProviderName != "" && e.ProviderName != e.ProviderType {
			prov = e.ProviderType + "/" + e.ProviderName
		}
		procs = append(procs, LiveProcessEntry{
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
	ev := Event{
		Type: "pool_stats",
	}
	payload, _ := json.Marshal(PoolStatsPayload{
		Active:        globalPool.Active(),
		Max:           globalPool.MaxConcurrent(),
		QueueLen:      globalPool.QueueLen(),
		LiveProcesses: procs,
	})
	ev.Data = string(payload)
	return ev.JSON()
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

	// Start a git fs-watcher for session-scoped subscribers so the SCM
	// rail badge gets live git_status events. Ref-counted: stops when the
	// last subscriber for this session disconnects.
	if sessionID != "" && globalMgr != nil {
		if sess, found := globalMgr.Registry().Session(sessionID); found {
			if cwd, cerr := resolveSessionCwd(sess); cerr == nil {
				globalGitWatch.acquire(sessionID, cwd)
				defer globalGitWatch.release(sessionID)
			}
		}
	}

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
	// Global subscriber: emit current pool_stats immediately on connect
	// so the Process panel shows live data without waiting for next lifecycle event.
	if sessionID == "" && globalPool != nil {
		if ps := poolStatsEvent(); ps != "" {
			fmt.Fprintf(w, "event: agent\ndata: %s\n\n", ps)
			flush()
		}
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

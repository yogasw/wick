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
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/providersync"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/skills"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/processctl"
	"github.com/yogasw/wick/internal/tags"
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

// Register mounts all Agents routes under /tools/agents.
func Register(r tool.Router) {
	// Svelte SPA shell + assets for the workflow editor.
	registerSPA(r)
	registerSPAWorkflows(r)
	registerSPAWorkflowHistory(r)
	registerSPAPanels(r)
	registerSPAPalette(r)

	// Access gates for every per-resource subtree. Registered once here so
	// each {id} route — and any added later — is checked before its handler
	// runs, instead of repeating ownsSession/allowProject in every handler.
	r.Use("/sessions/{id}", sessionAccessMW)
	r.Use("/api/sessions/{id}", sessionAccessMW)
	r.Use("/projects/{id}", projectAccessMW)
	r.Use("/api/projects/{id}", projectAccessMW)

	r.GET("/", newSessionCompose)
	r.POST("/", startNewSession)
	r.GET("/overview", overviewPage)
	r.GET("/connectors", connectorsPage)

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
	r.GET("/sessions/{id}/files/raw", sessionContextRaw)
	r.POST("/sessions/{id}/files/save", sessionContextSave)
	r.POST("/sessions/{id}/files/create", sessionContextCreate)
	r.DELETE("/sessions/{id}/files", sessionContextDelete)

	// JSON API — overview SPA endpoint.
	r.GET("/api/overview", apiOverview)

	// JSON API — conversation SPA endpoints.
	r.GET("/api/sessions", apiSessionList)
	r.GET("/api/sessions/{id}/conversation", apiSessionConversation)
	r.GET("/api/sessions/{id}/meta", apiSessionMeta)

	// JSON API — skills SPA endpoints (mirrors templ skills handlers).
	r.GET("/api/skills", apiSkillsList)
	r.GET("/api/skills/{name}", apiSkillDetail)
	r.GET("/api/skills/{folder}/files/{file...}", apiSkillFolderFileDetail)
	r.GET("/api/skills/{provider}/{path...}", apiSkillProviderPath)

	// JSON API — presets SPA endpoints.
	r.GET("/api/presets", apiPresetList)
	r.GET("/api/presets/{name}", apiPresetDetail)

	// JSON API — project-settings SPA endpoints.
	r.GET("/api/projects/{id}", apiProjectDetail)
	r.POST("/api/projects/{id}", apiProjectUpdate)

	// JSON API — providers SPA endpoints (mirrors templ providers handlers).
	r.GET("/api/providers", apiProvidersList)
	r.GET("/api/providers/storage", apiProvidersStorage)
	r.GET("/api/providers/{type}/{name}", apiProviderDetail)

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
	r.GET("/providers/options", providerOptionsJSON)
	r.GET("/presets/options", presetOptionsJSON)
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
		SystemPromptDefault: systemprompt.DefaultSystemPrompt(),
	}))
}

// sidebarVM builds AgentsLayoutVM for the sidebar session list.
// activeSessionID is set on session detail pages to highlight the current row.
func sidebarVM(c *tool.Ctx, activePage, activeSessionID string) view.AgentsLayoutVM {
	return sidebarVMScoped(c, activePage, activeSessionID, "")
}

// projectAccess captures a caller's session/project visibility for one
// request. seeAll is true for admins/owners (no filtering). Otherwise only
// resources whose ID is in `projects` are visible. userID is the caller's
// own ID, used to keep unscoped sessions visible to their creator.
type projectAccess struct {
	seeAll   bool
	userID   string
	projects map[string]struct{}
}

// allowProject reports whether the caller may see the project with projectID.
func (a projectAccess) allowProject(projectID string) bool {
	if a.seeAll {
		return true
	}
	_, ok := a.projects[projectID]
	return ok
}

// allowSession reports whether the caller may see a session with the given
// project + owner. Project-bound sessions follow project access. Unscoped
// sessions (no ProjectID) aren't covered by any project tag, so a non-admin
// may only see their OWN unscoped sessions — ownerless ones (UserID == "")
// are admin-only, never shown to other users.
func (a projectAccess) allowSession(projectID, userID string) bool {
	if a.seeAll {
		return true
	}
	if projectID == "" {
		// Unscoped session: visible only to its own creator. Ownerless
		// unscoped sessions (UserID == "") are hidden from everyone while
		// AdminSeeAll is off — no one is scoped to "see all".
		return userID != "" && userID == a.userID
	}
	_, ok := a.projects[projectID]
	return ok
}

// adminSeeAll reports whether the AdminSeeAll knob is on. When true, admins
// regain the legacy unrestricted view of every project and session. Default
// (and on missing config) is false: admins are scoped like regular users.
func adminSeeAll() bool {
	if globalConfigs == nil {
		return false
	}
	return globalConfigs.GetOwned("agents", "admin_see_all") == "true"
}

// callerProjectAccess resolves the caller's project visibility once per
// request with a single bulk query, instead of an N+1 UserOwnsResource per
// project/session. Unauthenticated and admin/owner callers see everything.
func callerProjectAccess(c *tool.Ctx) projectAccess {
	u := login.GetUser(c.Context())
	// No user in context = internal / MCP caller: unrestricted.
	if u == nil {
		return projectAccess{seeAll: true}
	}
	// Admins see everything only when AdminSeeAll is on (legacy behaviour).
	// With it off (default) an admin is scoped like a regular user, falling
	// through to the tag-based path below — keeps the admin's own session
	// list and conversation history clean and private.
	if u.IsAdmin() && adminSeeAll() {
		return projectAccess{seeAll: true}
	}
	set := make(map[string]struct{})
	if globalTagsSvc != nil {
		var err error
		set, err = globalTagsSvc.AccessibleResourceIDs(c.Context(), u.ID)
		if err != nil {
			log.Ctx(c.Context()).Warn().Err(err).Msg("resolve accessible projects")
			set = map[string]struct{}{}
		}
	}
	// Union tag grants with project ownership. Some legacy projects can predate
	// owner tags (or lose their tag rows), but their metadata still records the
	// creator — a scoped admin/user must keep access to projects they own even
	// when AdminSeeAll is off.
	//
	// Ownerless projects (OwnerUserID == "") are NOT a public escape hatch: they
	// are admin-only by default. A non-admin reaches one only via an explicit tag
	// grant (already unioned in from AccessibleResourceIDs above), never just
	// because it lacks an owner. Without this guard every authenticated user
	// could see (and open) every ownerless project + its sessions in Recent.
	isAdmin := u.IsAdmin()
	for pid, p := range globalMgr.Registry().Projects() {
		if p.Meta.OwnerUserID == u.ID || (p.Meta.OwnerUserID == "" && isAdmin) {
			set[pid] = struct{}{}
		}
	}
	return projectAccess{userID: u.ID, projects: set}
}

// sidebarVMScoped builds the sidebar VM, optionally scoped to a project.
// When scopedProjectID is set, the Recent list is filtered to that
// project's sessions and the scoped breadcrumb renders.
func sidebarVMScoped(c *tool.Ctx, activePage, activeSessionID, scopedProjectID string) view.AgentsLayoutVM {
	const sidebarCap = 10
	access := callerProjectAccess(c)
	allSessions := globalMgr.Registry().Sessions()
	// Per-project session counts across ALL sessions (sidebar pills).
	counts := make(map[string]int, len(allSessions))
	for _, s := range allSessions {
		if s.Meta.ProjectID != "" {
			counts[s.Meta.ProjectID]++
		}
	}
	allIDs := globalMgr.Registry().SessionIDs()
	// Keep only sessions the caller may see (project access). When scoped to a
	// project, also drop sessions outside it. Sorted-desc order is preserved.
	{
		filtered := allIDs[:0:0]
		for _, id := range allIDs {
			s, ok := allSessions[id]
			if !ok {
				continue
			}
			if scopedProjectID != "" && s.Meta.ProjectID != scopedProjectID {
				continue
			}
			if !access.allowSession(s.Meta.ProjectID, s.Meta.UserID) {
				continue
			}
			filtered = append(filtered, id)
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
	if !access.seeAll {
		filtered := allProjectIDs[:0:0]
		for _, pid := range allProjectIDs {
			if _, ok := allProjects[pid]; ok && access.allowProject(pid) {
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
		ShellAssetURL:    spaAssetURL("shell"),
	}
}

// projectChoices builds the picker rows for the compose form / move menu
// from the registry projects, ordered by display name. Non-admin users only
// see their own personal project.
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

// idleTimeoutMs returns the configured agents idle timeout in milliseconds.
// Falls back to 120 000 ms when globalConfigs is unavailable or the key is unset.
func idleTimeoutMs() int {
	if globalConfigs != nil {
		if n, err := strconv.Atoi(globalConfigs.GetOwned("agents", "idle_timeout_sec")); err == nil && n > 0 {
			return n * 1000
		}
	}
	return 120 * 1000
}

// ownsSession reports whether the caller may access sess. App owners (and
// admins while AdminSeeAll is on) see all sessions; everyone else may only
// access sessions they own OR sessions belonging to a project they can reach
// (via project tag grants / project ownership). This mirrors the sidebar's
// allowSession path so a session shown in the list is also openable. Ownerless
// UNSCOPED sessions (ProjectID=="" && UserID=="") are reachable only by the
// owner / see-all caller — they are not a public escape hatch.
func ownsSession(c *tool.Ctx, sess session.Session) bool {
	u := login.GetUser(c.Context())
	if u == nil {
		return true
	}
	if u.CanSeeAllSessions() {
		return true
	}
	if u.IsAdmin() && adminSeeAll() {
		return true
	}
	if sess.Meta.UserID != "" && sess.Meta.UserID == u.ID {
		return true
	}
	// Project-scoped sessions are reachable by anyone with access to the
	// project (tag grant, ownership, or ownerless/system project) — same rule
	// the sidebar uses, keeping list visibility and detail access consistent.
	return callerProjectAccess(c).allowSession(sess.Meta.ProjectID, sess.Meta.UserID)
}

// sessionAccessMW gates every route under /sessions/{id} and /api/sessions/{id}:
// the caller must be allowed to access the session named by the {id} path value
// (ownsSession — owner, project grant, or admin/see-all). Registered once via
// r.Use, so any current OR future subroute (conversation, files, approvals,
// asks, workspace, …) is covered without each handler repeating the check.
// 404 on no access — don't confirm a session exists to a caller who can't see
// it. notReady is left to the handlers (the mux only mounts these when up).
func sessionAccessMW(next tool.HandlerFunc) tool.HandlerFunc {
	return func(c *tool.Ctx) {
		if globalMgr == nil {
			next(c)
			return
		}
		sess, ok := globalMgr.Registry().Session(c.PathValue("id"))
		if !ok || !ownsSession(c, sess) {
			c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		next(c)
	}
}

// projectAccessMW gates every route under /projects/{id} and /api/projects/{id}
// by project access (allowProject — owner, tag grant, or ownerless-if-admin).
// "/projects/{id}/pin", settings, update, delete all inherit it; siblings
// without an {id} segment (/projects, /projects/options) are not covered. The
// special "new" id (create form / draft) is allowed through — there's no
// project to gate yet. 404 on no access so existence isn't leaked.
func projectAccessMW(next tool.HandlerFunc) tool.HandlerFunc {
	return func(c *tool.Ctx) {
		id := c.PathValue("id")
		if id == "new" || globalMgr == nil {
			next(c)
			return
		}
		if _, ok := globalMgr.Registry().Project(id); !ok || !callerProjectAccess(c).allowProject(id) {
			c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		next(c)
	}
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

// newSessionCompose renders the Svelte compose SPA shell. No session is
// persisted here — that only happens in startNewSession when the SPA POSTs
// back with a non-empty message.
func newSessionCompose(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ensurePersonalProjectForUser(c)
	access := callerProjectAccess(c)
	scoped := c.Query("project")
	if scoped != "" {
		if _, ok := globalMgr.Registry().Project(scoped); !ok || !access.allowProject(scoped) {
			scoped = ""
		}
	}
	// No explicit project in the URL: fall back to the user's pinned project
	// (their personal default) and redirect so the URL carries ?project=. The
	// new-session SPA reads ?project= to prefill the composer — without the
	// redirect it has no way to know the active project.
	if scoped == "" && c.Query("project") == "" {
		if pinned := pinnedProjectID(c); pinned != "" && access.allowProject(pinned) {
			c.Redirect(c.Base()+"/?project="+pinned, http.StatusSeeOther)
			return
		}
	}
	layout := sidebarVMScoped(c, "new", "", scoped)
	layout.FullBleed = true
	c.HTML(view.NewSessionSPA(view.NewSessionSPAVM{
		Layout:   layout,
		Base:     c.Base(),
		AssetURL: spaAssetURL("new-session"),
	}))
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

// renderCompose re-renders the new-session SPA shell after a failed
// startNewSession submit. The SPA owns compose state client-side, so
// message/errMsg are accepted for call-site compatibility only.
func renderCompose(c *tool.Ctx, _, _ string) {
	scoped := c.Query("project")
	if scoped != "" {
		if _, ok := globalMgr.Registry().Project(scoped); !ok {
			scoped = ""
		}
	}
	if scoped == "" {
		scoped = pinnedProjectID(c)
	}
	layout := sidebarVMScoped(c, "new", "", scoped)
	layout.FullBleed = true
	c.HTML(view.NewSessionSPA(view.NewSessionSPAVM{
		Layout:   layout,
		Base:     c.Base(),
		AssetURL: spaAssetURL("new-session"),
	}))
}

// ── Overview ──────────────────────────────────────────────────────────

func overviewPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	c.HTML(view.OverviewSPA(view.OverviewSPAVM{
		Layout:   sidebarVM(c, "overview", ""),
		Base:     c.Base(),
		AssetURL: spaAssetURL("overview"),
	}))
}

// connectorsPage hosts the manager SPA inside the Agents shell. It renders
// the SAME Vite bundle the /manager pages serve (asset URL + client-route
// base from manager.SPAMount), just wrapped in the Agents sidebar layout
// instead of the manager navbar. The SPA's API calls and internal links
// keep their /manager base, so no connector code moves — this is purely a
// host-shell swap. Auth is the standard agents gate; /manager/* routes run
// their own per-row checks.
func connectorsPage(c *tool.Ctx) {
	layout := sidebarVM(c, "connectors", "")
	layout.FullBleed = true
	assetURL, base := manager.SPAMount()
	c.HTML(view.ConnectorsPage(view.ConnectorsSPAVM{
		Layout:   layout,
		Base:     base,
		AssetURL: assetURL,
		// Deep is the client-route path forwarded by manager's connectors
		// redirect (?deep=/connectors/<key>/<id>...) so a reload of a manager
		// connectors URL reopens the same view here instead of the index.
		Deep: c.Query("deep"),
	}))
}

// ── Sessions ──────────────────────────────────────────────────────────

func sessionsPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	scoped := c.Query("project")
	if scoped != "" {
		if _, ok := globalMgr.Registry().Project(scoped); !ok {
			scoped = ""
		}
	}
	c.HTML(view.Conversation(view.ConversationVM{
		Layout:        sidebarVMScoped(c, "sessions", "", scoped),
		Base:          c.Base(),
		AssetURL:      spaAssetURL("conversation"),
		ScmAsset:      spaAssetURL("scm"),
		IdleTimeoutMs: idleTimeoutMs(),
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
	c.HTML(view.Conversation(view.ConversationVM{
		Layout:         sidebarVMScoped(c, "sessions", id, sess.Meta.ProjectID),
		Base:           c.Base(),
		AssetURL:       spaAssetURL("conversation"),
		ScmAsset:       spaAssetURL("scm"),
		InitialSession: id,
		IdleTimeoutMs:  idleTimeoutMs(),
	}))
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
	// Fallback: when no active/queued process exists (session idle),
	// surface agents from agents.json so the UI can still show
	// provider/agent name in the composer toolbar.
	if len(out) == 0 {
		if sess, ok := globalMgr.Registry().Session(id); ok {
			for _, a := range sess.Agents {
				prov := a.Provider
				out = append(out, procEntry{
					SessionID: id,
					AgentName: a.Name,
					Provider:  prov,
					PID:       0,
					Lifecycle: string(a.Status),
					Alive:     false,
				})
			}
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

// projectSettingsPage renders the project-settings SPA shell for an existing
// project or the create form when id == "new". Data is served via the JSON
// API endpoint GET /api/projects/{id}.
func projectSettingsPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if id != "new" {
		if _, ok := globalMgr.Registry().Project(id); !ok {
			c.NotFound()
			return
		}
	}
	c.HTML(view.ProjectSettingsSPA(view.ProjectSettingsSPAVM{
		Layout:    sidebarVM(c, "projects", ""),
		Base:      c.Base(),
		ProjectID: id,
		AssetURL:  spaAssetURL("project-settings"),
	}))
}

// projectOptionsJSON returns [{id, name, path}] for every project —
// consumed by the allowed_cmds scope dropdown in the settings UI.
func projectOptionsJSON(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	type option struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Path    string `json:"path"`
		Managed bool   `json:"managed"`
		Pinned  bool   `json:"pinned"`
	}
	access := callerProjectAccess(c)
	pinned := pinnedProjectID(c)
	projects := globalMgr.Registry().Projects()
	opts := make([]option, 0, len(projects))
	for id, p := range projects {
		if !access.allowProject(id) {
			continue
		}
		managed := p.Meta.CustomPath == ""
		path := p.Meta.CustomPath
		if path == "" {
			path = globalLayout.ProjectManagedPath(id)
		}
		opts = append(opts, option{ID: id, Name: p.Meta.Name, Path: path, Managed: managed, Pinned: id == pinned})
	}
	c.JSON(http.StatusOK, opts)
}

// providerOptionsJSON returns [{type, name, version}] for every healthy
// provider — consumed by the composer's provider selector in the SPA.
func providerOptionsJSON(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	type option struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	ps := providerChoicesCached(c.Context())
	opts := make([]option, 0, len(ps))
	for _, p := range ps {
		opts = append(opts, option{Type: p.Type, Name: p.Name, Version: p.Version})
	}
	c.JSON(http.StatusOK, opts)
}

// presetOptionsJSON returns [{name}] for every configured preset —
// consumed by the new-session compose SPA's preset selector.
func presetOptionsJSON(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	type option struct {
		Name string `json:"name"`
	}
	names := globalMgr.Registry().PresetNames()
	opts := make([]option, 0, len(names))
	for _, n := range names {
		opts = append(opts, option{Name: n})
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
	// Access is enforced by projectAccessMW (r.Use "/projects/{id}"); here we
	// only need the project row for its current meta.
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
	// Access is enforced by projectAccessMW (r.Use "/projects/{id}").
	id := c.PathValue("id")
	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.Error(http.StatusNotFound, "project not found")
		return
	}
	if p.Meta.Name == project.DefaultName {
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
	c.HTML(view.PresetsSPA(view.PresetsSPAVM{
		Layout:   sidebarVM(c, "presets", ""),
		Base:     c.Base(),
		AssetURL: spaAssetURL("presets"),
	}))
}

func presetDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	c.HTML(view.PresetsSPA(view.PresetsSPAVM{
		Layout:   sidebarVM(c, "presets", ""),
		Base:     c.Base(),
		AssetURL: spaAssetURL("presets"),
	}))
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
	// Access guard. A session-scoped stream replays that session's lifecycle +
	// partial assistant text and subscribes to its live turn events, so the
	// caller must be allowed to open the session. A global stream (no session)
	// carries pool_stats listing every active session across all users — that's
	// the admin Providers view, so restrict it to see-all callers.
	if sessionID != "" {
		sess, ok := globalMgr.Registry().Session(sessionID)
		if !ok || !ownsSession(c, sess) {
			c.Error(http.StatusNotFound, "session not found")
			return
		}
	} else if u := login.GetUser(c.Context()); u != nil && !u.IsAdmin() {
		// Global stream = admin Providers view (same gate as providersPage).
		// u == nil is an internal/MCP caller and stays unrestricted.
		c.Error(http.StatusForbidden, "admins only")
		return
	}
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

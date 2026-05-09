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

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// Package-level singletons wired at boot from server.go via SetX funcs.
// Handlers return 503 when these are nil rather than panicking.
var (
	globalMgr    *registry.Manager
	globalPool   *pool.Pool
	globalBcast  *Broadcaster
	globalLayout agentconfig.Layout
)

// SetManager wires in the agents registry manager.
func SetManager(m *registry.Manager) { globalMgr = m }

// SetPool wires in the agent subprocess pool.
func SetPool(p *pool.Pool) { globalPool = p }

// SetBroadcaster wires in the SSE event broadcaster.
func SetBroadcaster(b *Broadcaster) { globalBcast = b }

// SetLayout wires in the on-disk layout used for direct file reads.
func SetLayout(l agentconfig.Layout) { globalLayout = l }

// Register mounts all Agents routes under /tools/agents.
func Register(r tool.Router) {
	r.Static("/static/", StaticFS)

	r.GET("/", overviewPage)

	r.GET("/sessions", sessionsPage)
	r.POST("/sessions", createSession)
	r.GET("/sessions/{id}", sessionDetail)
	r.POST("/sessions/{id}/send", sendMessage)
	r.POST("/sessions/{id}/kill", killAgent)
	r.DELETE("/sessions/{id}", deleteSession)

	r.GET("/workspaces", workspacesPage)
	r.POST("/workspaces", createWorkspace)
	r.DELETE("/workspaces/{name}", deleteWorkspace)

	r.GET("/presets", presetsPage)
	r.GET("/presets/{name}", presetDetail)
	r.POST("/presets", createPreset)
	r.POST("/presets/{name}", updatePreset)
	r.DELETE("/presets/{name}", deletePreset)

	r.GET("/stream", streamSSE)
}

// ── guards ────────────────────────────────────────────────────────────

func notReady(c *tool.Ctx) bool {
	if globalMgr == nil || globalPool == nil {
		c.Error(http.StatusServiceUnavailable, "agents not initialised — check server boot logs")
		return true
	}
	return false
}

// ── Overview ──────────────────────────────────────────────────────────

func overviewPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ids := globalMgr.Registry().SessionIDs()
	recent := ids
	if len(recent) > 10 {
		recent = recent[:10]
	}
	c.HTML(view.Overview(view.OverviewVM{
		Base:       c.Base(),
		Active:     globalPool.Active(),
		QueueLen:   globalPool.QueueLen(),
		SessionIDs: recent,
		Sessions:   globalMgr.Registry().Sessions(),
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
	c.HTML(view.SessionsList(view.SessionsListVM{
		Base:          c.Base(),
		IDs:           ids[start:end],
		Sessions:      globalMgr.Registry().Sessions(),
		Workspaces:    globalMgr.Registry().Workspaces(),
		WorkspaceList: globalMgr.Registry().WorkspaceNames(),
		PresetList:    globalMgr.Registry().PresetNames(),
		Page:          page,
		HasNext:       end < len(ids),
	}))
}

func createSession(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ws := c.Form("workspace")
	backend := c.Form("backend")
	if backend == "" {
		backend = "claude"
	}
	id := uuid.New().String()
	_, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        id,
		Workspace: ws,
		Origin:    session.OriginUI,
	})
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("create session: %s", err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	if err := globalMgr.AddAgent(id, "main", backend); err != nil {
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
			turns = append(turns, view.TurnVM{
				Role:      t.Role,
				Agent:     t.Agent,
				Text:      t.Text,
				Truncated: t.Truncated,
				Time:      t.Timestamp,
			})
		}
	case "commands":
		lines, err := loadCommands(globalLayout, id)
		if err != nil {
			log.Ctx(c.Context()).Error().Msgf("load commands %s: %s", id, err.Error())
		}
		cmdLines = lines
	}
	c.HTML(view.SessionDetail(view.SessionDetailVM{
		Base:     c.Base(),
		Session:  sess,
		Tab:      tab,
		Turns:    turns,
		CmdLines: cmdLines,
	}))
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
		Base:          c.Base(),
		WorkspaceList: globalMgr.Registry().WorkspaceNames(),
		Workspaces:    globalMgr.Registry().Workspaces(),
		PresetList:    globalMgr.Registry().PresetNames(),
	}))
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
		Name:           name,
		CustomPath:     strings.TrimSpace(c.Form("custom_path")),
		DefaultPreset:  c.Form("preset"),
		DefaultBackend: c.Form("backend"),
		Description:    c.Form("description"),
	}
	if opt.DefaultPreset == "" {
		opt.DefaultPreset = "default"
	}
	if opt.DefaultBackend == "" {
		opt.DefaultBackend = "claude"
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
		Base:  c.Base(),
		Names: globalMgr.Registry().PresetNames(),
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
		Base: c.Base(),
		Name: p.Name,
		Body: p.Body,
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

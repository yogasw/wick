package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/pkg/tool"
)

// readBodyAll captures the request body once so we can sniff its shape
// (yaml-envelope vs. raw workflow JSON) without consuming the reader.
func readBodyAll(c *tool.Ctx) ([]byte, error) {
	if c.R.Body == nil {
		return nil, errors.New("empty body")
	}
	defer c.R.Body.Close()
	return io.ReadAll(c.R.Body)
}

// normaliseWorkflowBody returns YAML bytes ready for parse.Parse. The
// FE may send either {"yaml": "..."} or the full workflow as JSON
// (a JSON object that lacks a `yaml` field). Strip whitespace first
// because an empty buffer is treated as "no body".
func normaliseWorkflowBody(id string, raw []byte) ([]byte, error) {
	trim := strings.TrimSpace(string(raw))
	if trim == "" {
		return nil, errors.New("body is required")
	}
	// yaml-envelope shape.
	var env struct {
		YAML string `json:"yaml"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && strings.TrimSpace(env.YAML) != "" {
		return []byte(env.YAML), nil
	}
	// Raw workflow JSON — marshal back to YAML so the parser sees one
	// shape. yaml.v3 round-trips a Workflow cleanly because every YAML
	// tag on the struct doubles as a JSON-compatible key.
	var w wf.Workflow
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, errors.New("body must be either {\"yaml\":...} or a workflow JSON object: " + err.Error())
	}
	if w.ID == "" {
		w.ID = id
	}
	out, err := yaml.Marshal(w)
	if err != nil {
		return nil, errors.New("marshal workflow → yaml: " + err.Error())
	}
	return out, nil
}

// JSON-only API wrappers consumed by the Svelte SPA under
// /tools/agents-v2/. Mounted at /api/workflows/* to keep the surface
// separate from the legacy templ + HTMX routes. Both stay live during
// the migration (see internal/docs/workflow/svelte-migration.md).

// registerSPAWorkflows wires the JSON workflow endpoints. Call from
// handler.Register after the legacy routes — the dual-mount works
// because /api/workflows/* paths don't overlap any existing pattern.
func registerSPAWorkflows(r tool.Router) {
	r.GET("/api/workflows/list", spaWorkflowList)
	r.GET("/api/workflows/get/{id}", spaWorkflowGet)
	r.POST("/api/workflows/save/{id}", spaWorkflowSave)
	r.POST("/api/workflows/publish/{id}", spaWorkflowPublish)
	r.POST("/api/workflows/discard/{id}", spaWorkflowDiscard)
	r.POST("/api/workflows/toggle/{id}", spaWorkflowToggle)
	r.POST("/api/workflows/run/{id}", spaWorkflowRunNow)
	r.GET("/api/workflows/runs/{id}", spaWorkflowRuns)
}

type spaWorkflowSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	HasDraft  bool   `json:"has_draft"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func spaWorkflowList(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	summaries, err := globalWorkflowMgr.MCP.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]spaWorkflowSummary, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, spaWorkflowSummary{
			ID:       s.ID,
			Name:     s.Name,
			Enabled:  s.Enabled,
			HasDraft: globalWorkflowMgr.Service.HasDraft(s.ID),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"workflows": out})
}

func spaWorkflowGet(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	w, err := globalWorkflowMgr.Service.Load(id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	hasDraft := globalWorkflowMgr.Service.HasDraft(id)
	resp := map[string]any{
		"workflow":  w,
		"has_draft": hasDraft,
	}
	if hasDraft {
		if draft, err := globalWorkflowMgr.Service.LoadDraft(id); err == nil {
			resp["draft"] = draft
		}
	}
	c.JSON(http.StatusOK, resp)
}

func spaWorkflowSave(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	// Accept either {yaml: "..."} (canvas YAML round-trip) or a full
	// Workflow JSON object posted directly by the Svelte editor. The
	// JSON path marshals back to YAML so the parse + validate pipeline
	// stays a single code path.
	raw, err := readBodyAll(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}
	yamlBytes, err := normaliseWorkflowBody(id, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w, err := parse.Parse(id, yamlBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "parse: " + err.Error()})
		return
	}
	if err := globalWorkflowMgr.Service.SaveDraft(id, w); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Bundle validation alongside the save outcome so the SPA can
	// refresh its toolbar chip + Validation tab in a single
	// round-trip — same contract the v1 templ /save endpoint used.
	report := parse.Validate(w)
	c.JSON(http.StatusOK, map[string]any{
		"ok":         true,
		"validation": validationPayload(report),
	})
}

func spaWorkflowPublish(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	if _, err := globalWorkflowMgr.Service.Publish(id); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func spaWorkflowDiscard(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	if err := globalWorkflowMgr.Service.DiscardDraft(id); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func spaWorkflowToggle(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.Enabled = body.Enabled
	if err := globalWorkflowMgr.Service.SaveDraft(id, w); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func spaWorkflowRunNow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	evt := wf.Event{
		Type: string(wf.TriggerManual),
		At:   time.Now().UTC(),
		Payload: map[string]any{"source": "spa"},
	}
	if err := globalWorkflowMgr.MCP.RunNowWith(c.Context(), id, &w, evt); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func spaWorkflowRuns(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	page := 1
	if v := strings.TrimSpace(c.Query("page")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	runs, hasMore, err := globalWorkflowMgr.MCP.GetRunSummaries(id, page, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"runs":     runs,
		"page":     page,
		"has_more": hasMore,
	})
}

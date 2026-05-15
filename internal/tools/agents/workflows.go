package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/setup"
	wfview "github.com/yogasw/wick/internal/tools/agents/view/workflow"
	"github.com/yogasw/wick/pkg/tool"
)

// globalWorkflowMgr is the wired workflow stack. Server.go calls
// SetWorkflowManager once at boot; nil = every workflows handler 503s.
var globalWorkflowMgr *setup.Manager

// SetWorkflowManager wires in the workflow Manager constructed by
// server.go.
func SetWorkflowManager(m *setup.Manager) { globalWorkflowMgr = m }

func notReadyWorkflow(c *tool.Ctx) bool {
	if globalWorkflowMgr == nil {
		c.Error(http.StatusServiceUnavailable, "workflows not initialised — check server boot logs")
		return true
	}
	return false
}

// ── List + Create ───────────────────────────────────────────────────

func workflowsPage(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	summaries, err := globalWorkflowMgr.MCP.List()
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.HTML(wfview.List(wfview.ListVM{
		Layout:    sidebarVM(c, "workflows", ""),
		Base:      c.Base(),
		Workflows: summaries,
	}))
}

func createWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := strings.TrimSpace(c.Form("slug"))
	template := strings.TrimSpace(c.Form("template"))
	if template == "" {
		template = "empty"
	}
	w, err := globalWorkflowMgr.MCP.Create(mcp.CreateInput{Slug: slug, Template: template})
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("create workflow %s: %s", slug, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	// Register the fresh workflow with the router so Run Now / triggers
	// work without a manual restart. Bootstrap only registers existing
	// folders at startup — first-time Create needs an explicit reload.
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, w.Slug)
	c.Redirect(c.Base()+"/workflows/edit/"+w.Slug, http.StatusSeeOther)
}

// ── Editor + CRUD ──────────────────────────────────────────────────

func workflowEditor(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	// Editor always opens the draft if one exists, otherwise the
	// published workflow — so in-progress edits survive page refresh.
	w, err := globalWorkflowMgr.Service.LoadDraft(slug)
	if err != nil {
		c.NotFound()
		return
	}
	hasDraft := globalWorkflowMgr.Service.HasDraft(slug)
	yamlBytes, _ := parse.Marshal(w)
	graphJSON, err := workflowToDrawflowJSON(w)
	if err != nil {
		log.Ctx(c.Context()).Warn().Msgf("graph json serialize: %s", err.Error())
		graphJSON = "{}"
	}
	report := globalWorkflowMgr.Guard.Review(c.Context(), w)
	runs, _ := globalWorkflowMgr.MCP.GetRuns(slug, 20)
	approved := false
	if st, err := globalWorkflowMgr.Service.LoadState(slug); err == nil {
		approved = st.Approved
	}

	// Parse validation report on every render so the canvas can paint
	// per-node error badges on initial load — without this badges only
	// showed up after the first auto-save round-trip and disappeared on
	// refresh.
	validation := parse.Validate(w)
	validationJSON, _ := json.Marshal(validationPayload(validation))

	c.HTML(wfview.Editor(wfview.EditorVM{
		Layout:         sidebarVM(c, "workflows", ""),
		Base:           c.Base(),
		Slug:           slug,
		Workflow:       w,
		HasDraft:       hasDraft,
		YAML:           string(yamlBytes),
		GraphJSON:      graphJSON,
		ValidationJSON: string(validationJSON),
		Approved:       approved,
		GuardReport:    &report,
		NodeTypes:      globalWorkflowMgr.MCP.NodeTypes(),
		Runs:           runs,
	}))
}

func saveWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	body := c.Form("body")
	w, err := drawflowJSONToWorkflow(slug, body)
	if err != nil {
		saveResponse(c, http.StatusBadRequest, "invalid graph payload: "+err.Error(), nil)
		return
	}
	// Carry forward triggers + entry from disk — the canvas only edits
	// nodes + edges, so refreshing the trigger list each save would
	// drop user-configured cron/channel/webhook bindings. Use draft
	// state so iterative trigger edits land in the same draft.
	if prev, err := globalWorkflowMgr.Service.LoadDraft(slug); err == nil {
		w.Triggers = prev.Triggers
		w.Enabled = prev.Enabled
		w.Name = prev.Name
		w.Description = prev.Description
		w.Env = prev.Env
		w.Datasets = prev.Datasets
		w.OnError = prev.OnError
		if w.Graph.Entry == "" {
			w.Graph.Entry = prev.Graph.Entry
		}
	}
	// Validation is non-blocking on save — the canvas is allowed to
	// be in a half-built state. We still run Validate so the response
	// carries the violations; the UI hangs error badges on the
	// offending nodes. Publish + RunNow gate on the same Validate
	// result and refuse to proceed when it fails.
	report := parse.Validate(w) // *parse.Result
	// Save always writes to draft. Published workflow.yaml stays
	// untouched until the user explicitly Publishes — that means the
	// router keeps firing the previous version while edits are in
	// flight.
	if err := globalWorkflowMgr.Service.SaveDraft(slug, w); err != nil {
		log.Ctx(c.Context()).Error().Msgf("save workflow draft %s: %s", slug, err.Error())
		saveResponse(c, http.StatusInternalServerError, err.Error(), nil)
		return
	}
	saveResponse(c, http.StatusOK, "", report)
}

// saveResponse returns JSON when the client wants it (XHR auto-save) or
// redirects for plain-form posts. Keeps the no-JS fallback alive while
// the canvas auto-save path uses fetch(). When a validation report is
// supplied, the JSON body carries per-node errors so the canvas can
// hang badges on the offending nodes without blocking the save.
func saveResponse(c *tool.Ctx, status int, errMsg string, report *parse.Result) {
	if strings.Contains(c.R.Header.Get("Accept"), "application/json") {
		body := map[string]any{"ok": status < 400 && errMsg == ""}
		if errMsg != "" {
			body["error"] = errMsg
		}
		if report != nil {
			body["validation"] = validationPayload(report)
		}
		c.JSON(status, body)
		return
	}
	if status >= 400 {
		c.Error(status, errMsg)
		return
	}
	c.Redirect(c.Base()+"/workflows/edit/"+c.PathValue("slug"), http.StatusSeeOther)
}

// validationPayload reshapes a parse.Result into a per-node lookup the
// canvas JS can index by node id. The flat errors list is preserved
// for any global (non-node-scoped) violations.
func validationPayload(r *parse.Result) map[string]any {
	byNode := map[string][]string{}
	global := []string{}
	for _, e := range r.Errors {
		if id := nodeIDFromPath(e.Path); id != "" {
			byNode[id] = append(byNode[id], e.Message)
		} else {
			global = append(global, e.Path+": "+e.Message)
		}
	}
	return map[string]any{
		"ok":          r.Ok(),
		"errors":      r.Errors,
		"warnings":    r.Warnings,
		"by_node":     byNode,
		"global":      global,
	}
}

// nodeIDFromPath extracts a node id from validation error paths like
// `graph.nodes[<id>].field` or `graph.nodes[<idx>]` (when the validator
// uses numeric index rather than the id). Returns empty for paths that
// don't scope to a node.
func nodeIDFromPath(p string) string {
	const prefix = "graph.nodes["
	i := strings.Index(p, prefix)
	if i < 0 {
		return ""
	}
	rest := p[i+len(prefix):]
	end := strings.Index(rest, "]")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// publishWorkflow promotes draft → workflow.yaml + HotReload so router
// picks up the new version.
func publishWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	if !globalWorkflowMgr.Service.HasDraft(slug) {
		c.Redirect(c.Base()+"/workflows/edit/"+slug, http.StatusSeeOther)
		return
	}
	// Validate the draft BEFORE we promote it. If we promoted first
	// then rejected on validation, the previous published version is
	// already overwritten with broken yaml.
	draft, err := globalWorkflowMgr.Service.LoadDraft(slug)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	if r := parse.Validate(draft); !r.Ok() {
		c.Error(http.StatusBadRequest, "cannot publish — fix validation errors:\n"+r.Error())
		return
	}
	if _, err := globalWorkflowMgr.Service.Publish(slug); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, slug)
	c.Redirect(c.Base()+"/workflows/edit/"+slug, http.StatusSeeOther)
}

// discardWorkflowDraft drops workflow.draft.yaml — editor reverts to
// the published copy on next open.
func discardWorkflowDraft(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	if err := globalWorkflowMgr.Service.DiscardDraft(slug); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/workflows/edit/"+slug, http.StatusSeeOther)
}

func toggleWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	w, err := globalWorkflowMgr.MCP.Get(slug)
	if err != nil {
		c.NotFound()
		return
	}
	if _, err := globalWorkflowMgr.MCP.Toggle(slug, !w.Enabled); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, slug)
	c.Redirect(c.Base()+"/workflows/edit/"+slug, http.StatusSeeOther)
}

func runWorkflowNow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	// Run Now is a live test of the canvas — fire the draft if one
	// exists, otherwise the published workflow. Bypasses Enabled
	// so the admin can verify drafts before publishing.
	w, err := globalWorkflowMgr.Service.LoadDraft(slug)
	if err != nil {
		c.NotFound()
		return
	}
	// Validation gates Run Now: a half-built draft would crash the
	// engine partway through. Save itself stays non-blocking so the
	// canvas can be in an unfinished state — only the actions that
	// would actually invoke the graph (Run Now, Publish) require Ok.
	if r := parse.Validate(w); !r.Ok() {
		c.Error(http.StatusBadRequest, "cannot run — fix validation errors:\n"+r.Error())
		return
	}
	// Defensive HotReload — covers the case where boot saw an empty
	// workflows/ dir and never registered this slug.
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, slug)
	report := globalWorkflowMgr.Guard.Review(c.Context(), w)
	if err := globalWorkflowMgr.Guard.Apply(report, nil); err != nil {
		c.Error(http.StatusForbidden, err.Error())
		return
	}
	if err := globalWorkflowMgr.MCP.RunNow(c.Context(), slug, wf.Event{Type: string(wf.TriggerManual)}); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/workflows/edit/"+slug, http.StatusSeeOther)
}

// workflowRegistryAPI returns JSON catalog the editor uses to hydrate
// pickers (channels, channel ops, connectors, providers). No free
// text — every dropdown sources from this endpoint.
func workflowRegistryAPI(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	channels := []map[string]any{}
	for _, info := range globalWorkflowMgr.MCP.ChannelsList() {
		ops := []map[string]any{}
		for _, a := range info.Actions {
			ops = append(ops, map[string]any{
				"id":          a.ID,
				"description": a.Description,
				"destructive": a.Destructive,
			})
		}
		channels = append(channels, map[string]any{
			"name":             info.Name,
			"supports_session": info.SupportsSession,
			"ops":              ops,
		})
	}
	connectors := []map[string]any{}
	for _, info := range globalWorkflowMgr.MCP.ConnectorsList() {
		ops := []map[string]any{}
		for _, op := range info.Operations {
			inputs := make([]map[string]any, 0, len(op.Input))
			for _, in := range op.Input {
				inputs = append(inputs, map[string]any{
					"key":         in.Key,
					"description": in.Description,
					"required":    in.Required,
				})
			}
			ops = append(ops, map[string]any{
				"id":          op.Key,
				"name":        op.Name,
				"description": op.Description,
				"destructive": op.Destructive,
				"input":       inputs,
			})
		}
		connectors = append(connectors, map[string]any{
			"module": info.Module,
			"name":   info.Name,
			"ops":    ops,
		})
	}
	providers := []map[string]any{}
	for _, info := range globalWorkflowMgr.MCP.ProvidersList() {
		providers = append(providers, map[string]any{
			"name":       info.Name,
			"is_default": info.IsDefault,
		})
	}
	c.JSON(http.StatusOK, map[string]any{
		"channels":   channels,
		"connectors": connectors,
		"providers":  providers,
	})
}

func deleteWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	if err := globalWorkflowMgr.MCP.Delete(slug); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/workflows", http.StatusSeeOther)
}

// ── Run detail ────────────────────────────────────────────────────

func workflowRunDetail(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	runID := c.PathValue("runID")
	st, err := globalWorkflowMgr.StateStore.Load(slug, runID)
	if err != nil {
		c.NotFound()
		return
	}
	events, _ := globalWorkflowMgr.StateStore.ListEvents(slug, runID)
	c.HTML(wfview.Run(wfview.RunVM{
		Layout: sidebarVM(c, "workflows", ""),
		Base:   c.Base(),
		Slug:   slug,
		RunID:  runID,
		State:  st,
		Events: events,
	}))
}

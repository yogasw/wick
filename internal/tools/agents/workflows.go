package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/setup"
	wfview "github.com/yogasw/wick/internal/tools/agents/view/workflow"
	"github.com/yogasw/wick/pkg/tool"
)

// WorkflowSSESession returns the broadcaster session key used for
// workflow run events. The editor JS subscribes to /stream?session=<key>
// to receive per-node progress without polling.
func WorkflowSSESession(slug string) string { return "wf:" + slug }

// WorkflowEventHook builds an engine.OnEvent callback that fans
// workflow run events out to the SSE broadcaster. Marshals the
// RunEvent as JSON in the SSE payload Data field; Type prefixes
// "wf_" so the editor JS can dispatch on it without colliding with
// agent stream events.
func WorkflowEventHook(b *Broadcaster) func(slug, runID string, ev wf.RunEvent) {
	if b == nil {
		return nil
	}
	return func(slug, runID string, ev wf.RunEvent) {
		payload := map[string]any{
			"slug":   slug,
			"run_id": runID,
			"event":  ev.Event,
			"node":   ev.Node,
			"case":   ev.Case,
			"data":   ev.Data,
			"ts":     time.Now().UTC().Format(time.RFC3339Nano),
		}
		body, _ := json.Marshal(payload)
		b.fanout(WorkflowSSESession(slug), Event{
			SessionID: WorkflowSSESession(slug),
			Type:      "wf_" + ev.Event,
			Data:      string(body),
		})
	}
}

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

	// The editor owns the full viewport (toolbar + canvas + bottom
	// panel) and paints its own borders, so opt out of the layout's
	// default px-6 py-6 padding wrapper — otherwise the canvas
	// inherits the gutter and the toolbar sits inside a card instead
	// of butting against the sidebar.
	layoutVM := sidebarVM(c, "workflows", "")
	layoutVM.FullBleed = true
	c.HTML(wfview.Editor(wfview.EditorVM{
		Layout:         layoutVM,
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
	// Read enabled from whichever copy the editor is showing (draft
	// if present, otherwise published) so the toggle button reflects
	// what the user clicked.
	w, err := globalWorkflowMgr.Service.LoadDraft(slug)
	if err != nil {
		c.NotFound()
		return
	}
	next := !w.Enabled
	// Toggle should always succeed regardless of validation state —
	// it just flips a boolean flag, it doesn't run the graph. Going
	// through MCP.Toggle would route via Canvas.mutate which runs
	// parse.Validate and refuses to save half-built drafts. Skip
	// that path and write the flag directly to whichever file is
	// the active source.
	if globalWorkflowMgr.Service.HasDraft(slug) {
		w.Enabled = next
		if err := globalWorkflowMgr.Service.SaveDraft(slug, w); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		if err := globalWorkflowMgr.Service.Toggle(slug, next); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
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
		runResponse(c, http.StatusBadRequest, "", "cannot run — fix validation errors:\n"+r.Error())
		return
	}
	// Defensive HotReload — covers the case where boot saw an empty
	// workflows/ dir and never registered this slug.
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, slug)
	report := globalWorkflowMgr.Guard.Review(c.Context(), w)
	if err := globalWorkflowMgr.Guard.Apply(report, nil); err != nil {
		runResponse(c, http.StatusForbidden, "", err.Error())
		return
	}
	// Pre-assign the run ID so the response can return it before
	// the engine wakes up. The engine reads `run_id` from the event
	// payload (falls back to its own IDGen when absent), so the
	// browser can subscribe to the SSE stream in time to catch the
	// very first node_started event.
	runID := uuid.NewString()
	evt := wf.Event{
		Type:    string(wf.TriggerManual),
		At:      time.Now().UTC(),
		Payload: map[string]any{"run_id": runID, "source": "ui"},
	}
	if err := globalWorkflowMgr.MCP.RunNow(c.Context(), slug, evt); err != nil {
		runResponse(c, http.StatusInternalServerError, "", err.Error())
		return
	}
	runResponse(c, http.StatusAccepted, runID, "")
}

// runResponse returns JSON when the client wants async behavior
// (fetch from the Run Now button) or redirects for plain form posts
// so the no-JS fallback keeps working. status 202 + run_id is the
// success shape; non-empty errMsg yields {error}.
func runResponse(c *tool.Ctx, status int, runID, errMsg string) {
	if strings.Contains(c.R.Header.Get("Accept"), "application/json") {
		body := map[string]any{"ok": status < 400 && errMsg == ""}
		if runID != "" {
			body["run_id"] = runID
		}
		if errMsg != "" {
			body["error"] = errMsg
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

// execNodeStep runs a single node in isolation — n8n's "Execute step"
// pattern. Body is `{node, input}` where node is the Drawflow node JSON
// (so the user can iterate the inspector without saving the whole
// graph) and input is the parent's output (or user-supplied mock).
// Output streams back synchronously; nothing persists to runs/.
func execNodeStep(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	var body struct {
		Node  map[string]any `json:"node"`
		Input map[string]any `json:"input"`
	}
	if err := json.NewDecoder(c.R.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	if body.Node == nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "node is required"})
		return
	}
	// Re-use the drawflow → workflow codec to materialise a Node value
	// from the single node JSON. We wrap it in a minimal Workflow so
	// the codec's existing parsing path stays exact (one node, no edges).
	w, err := singleNodeWorkflow(slug, body.Node)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if len(w.Graph.Nodes) == 0 {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "node decode produced no nodes"})
		return
	}
	n := w.Graph.Nodes[0]
	exec, ok := globalWorkflowMgr.Engine.Executors[n.Type]
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "no executor for node type " + string(n.Type)})
		return
	}
	envVals, _ := globalWorkflowMgr.Service.LoadEnvValues(slug)
	// Seed the run context with parent input under both `input` and the
	// expected parent node ID so templates like `{{.Node.parentID.field}}`
	// resolve. Caller can pass the input via a `_parent` key to control
	// which alias the template engine sees.
	parentID := ""
	if p, ok := body.Node["_parent_id"].(string); ok {
		parentID = p
	}
	outputs := map[string]any{}
	nodeOutputs := map[string]wf.NodeOutput{}
	if parentID != "" {
		outputs[parentID] = body.Input
		nodeOutputs[parentID] = wf.NodeOutput{Result: body.Input}
	}
	outputs["input"] = body.Input
	rc := &wf.RunContext{
		Workflow:    w,
		Event:       wf.Event{Type: string(wf.TriggerManual), At: time.Now().UTC(), Payload: body.Input},
		Outputs:     outputs,
		EnvValues:   envVals,
		RunID:       "step-" + uuid.NewString(),
		NodeOutputs: nodeOutputs,
	}
	startedAt := time.Now()
	out, runErr := exec.Execute(c.Context(), n, rc)
	latency := time.Since(startedAt).Milliseconds()
	resp := map[string]any{
		"ok":         runErr == nil,
		"latency_ms": latency,
		"output":     nodeOutputToJSON(out),
	}
	if runErr != nil {
		resp["error"] = runErr.Error()
	}
	c.JSON(http.StatusOK, resp)
}

// nodeOutputToJSON flattens a NodeOutput into the same map shape
// recordSuccess writes to RunContext.Outputs so the bottom-panel
// "Output" tab renders consistently with full-run output.
func nodeOutputToJSON(o wf.NodeOutput) map[string]any {
	m := map[string]any{}
	if o.Verdict != "" {
		m["verdict"] = o.Verdict
	}
	if o.Confidence != 0 {
		m["confidence"] = o.Confidence
	}
	if o.Reasoning != "" {
		m["reasoning"] = o.Reasoning
	}
	if o.Result != nil {
		m["result"] = o.Result
	}
	for k, v := range o.Fields {
		m[k] = v
	}
	return m
}

// singleNodeWorkflow takes one drawflow node JSON and round-trips it
// through the codec to build a one-node Workflow value. Wrapping a
// "Drawflow doc with just this node" reuses the existing converter
// (drawflowJSONToWorkflow) so node spec → Node fidelity stays in
// lockstep with full-graph save.
func singleNodeWorkflow(slug string, node map[string]any) (wf.Workflow, error) {
	df := map[string]any{
		"drawflow": map[string]any{
			"Home": map[string]any{
				"data": map[string]any{
					"1": node,
				},
			},
		},
	}
	raw, err := json.Marshal(df)
	if err != nil {
		return wf.Workflow{}, err
	}
	return drawflowJSONToWorkflow(slug, string(raw))
}

// workflowRunStateAPI returns the latest persisted state.json for a
// run. Editor JS uses this when the user re-opens a run that was
// triggered before page load — SSE events catch live runs, this
// catches up cold-start.
func workflowRunStateAPI(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	slug := c.PathValue("slug")
	runID := c.PathValue("runID")
	st, err := globalWorkflowMgr.StateStore.Load(slug, runID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	events, _ := globalWorkflowMgr.StateStore.ListEvents(slug, runID)
	c.JSON(http.StatusOK, map[string]any{
		"state":  st,
		"events": events,
	})
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

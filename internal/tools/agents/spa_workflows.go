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
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
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
	r.POST("/api/workflows/exec-node/{id}", spaExecNode)
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
	// Approval / governance snapshot — fed to the toolbar's
	// "approved vN" badge. Soft-fail when state hasn't been written
	// yet (fresh workflow before any approve action).
	if st, err := globalWorkflowMgr.Service.LoadState(id); err == nil {
		resp["state"] = st
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
	// trigger_id is REQUIRED — the editor pins one before firing so the
	// engine routes to the right entry_node. Refusing the call when
	// it's missing means a future caller can't accidentally trip the
	// "first-matching-trigger wins" path and run a different branch
	// than what's visible in the UI.
	var body struct {
		TriggerID string `json:"trigger_id"`
	}
	if err := json.NewDecoder(c.R.Body).Decode(&body); err != nil && err != io.EOF {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	body.TriggerID = strings.TrimSpace(body.TriggerID)
	if body.TriggerID == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "trigger_id is required — pick a trigger to fire"})
		return
	}
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	// Resolve the trigger so we can stamp the right Type on the
	// synthesized event (cron / channel / manual / …). pickEntry will
	// still route via trigger_id; using the actual type keeps run logs
	// + SSE events honest about which trigger fired.
	var picked *wf.Trigger
	for i := range w.Triggers {
		if w.Triggers[i].ID == body.TriggerID {
			picked = &w.Triggers[i]
			break
		}
	}
	if picked == nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "trigger_id not found in workflow"})
		return
	}
	evt := wf.Event{
		Type: string(picked.Type),
		At:   time.Now().UTC(),
		Payload: map[string]any{
			"source":     "spa",
			"trigger_id": body.TriggerID,
		},
	}
	if err := globalWorkflowMgr.MCP.RunNowWith(c.Context(), id, &w, evt); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// runsFilter narrows the runs list before pagination. Empty fields =
// no-op for that dimension. Applied in-memory after IndexList reads
// the shardedlog page; that's good enough for typical run volumes
// (<10k per workflow) and keeps the index file format unchanged.
type runsFilter struct {
	Status string
	From   time.Time
	To     time.Time
	Q      string // case-insensitive substring of the run id
	Kind   string // "manual" | "automation" | "test" — coarse provenance bucket
}

// runKind derives the provenance bucket from index fields. Source is
// authoritative (set by the API surface that fired the run); trigger
// type is the fallback when older runs have an empty source.
func runKind(r mcp.RunSummary) string {
	switch r.Source {
	case "spa":
		return "manual"
	case "test", "wftest":
		return "test"
	}
	// Manual is sometimes fired by MCP / external API without setting
	// source; fall through to trigger type to bucket those.
	if r.TriggerType == "manual" {
		return "manual"
	}
	if r.TriggerType == "cron" || r.TriggerType == "webhook" || r.TriggerType == "channel" || r.TriggerType == "schedule_at" || r.TriggerType == "error" {
		return "automation"
	}
	return "automation"
}

func (f runsFilter) keep(r mcp.RunSummary) bool {
	if f.Status != "" && !strings.EqualFold(r.Status, f.Status) {
		return false
	}
	if !f.From.IsZero() && r.StartedAt.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && r.StartedAt.After(f.To) {
		return false
	}
	if f.Q != "" && !strings.Contains(strings.ToLower(r.ID), f.Q) {
		return false
	}
	if f.Kind != "" && runKind(r) != f.Kind {
		return false
	}
	return true
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
	pageSize := 50
	if v := strings.TrimSpace(c.Query("page_size")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			pageSize = n
		}
	}

	// Parse filters. Date inputs accept yyyy-mm-dd or full RFC3339;
	// `to` gets bumped to end-of-day so a same-day "from=to" still
	// includes runs that fired through 23:59.
	f := runsFilter{
		Status: strings.TrimSpace(c.Query("status")),
		Q:      strings.ToLower(strings.TrimSpace(c.Query("q"))),
		Kind:   strings.ToLower(strings.TrimSpace(c.Query("kind"))),
	}
	if v := strings.TrimSpace(c.Query("from")); v != "" {
		if t, err := parseDateInput(v, false); err == nil {
			f.From = t
		}
	}
	if v := strings.TrimSpace(c.Query("to")); v != "" {
		if t, err := parseDateInput(v, true); err == nil {
			f.To = t
		}
	}

	// No filters → fall back to the cheap paginated path. Saves a
	// full-scan read when the FE is just polling for new runs.
	noFilter := f.Status == "" && f.Q == "" && f.Kind == "" && f.From.IsZero() && f.To.IsZero()
	if noFilter {
		runs, hasMore, err := globalWorkflowMgr.MCP.GetRunSummaries(id, page, pageSize)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, map[string]any{
			"runs":     runs,
			"page":     page,
			"has_more": hasMore,
			"total":    -1, // unknown without a full scan
		})
		return
	}

	// Filtered path: read up to N pages, accumulate matches, then
	// paginate the matched set. N bounded so a runaway query can't
	// scan an infinite history.
	const maxScanPages = 40 // 40 * pageSize ≤ 8000 entries — plenty for typical workloads
	matched := make([]mcp.RunSummary, 0, pageSize*2)
	for p := 1; p <= maxScanPages; p++ {
		rows, hasMore, err := globalWorkflowMgr.MCP.GetRunSummaries(id, p, pageSize)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, r := range rows {
			if f.keep(r) {
				matched = append(matched, r)
			}
		}
		if !hasMore {
			break
		}
	}

	from := (page - 1) * pageSize
	if from > len(matched) {
		from = len(matched)
	}
	to := from + pageSize
	if to > len(matched) {
		to = len(matched)
	}
	c.JSON(http.StatusOK, map[string]any{
		"runs":     matched[from:to],
		"page":     page,
		"has_more": to < len(matched),
		"total":    len(matched),
	})
}

// parseDateInput accepts "yyyy-mm-dd" (FE date input) or full RFC3339.
// endOfDay=true bumps a yyyy-mm-dd value to 23:59:59.999 so a
// same-day from/to range still includes everything fired that day.
func parseDateInput(v string, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return t.UTC(), nil
}

// spaExecNode runs one node in isolation — n8n's "Execute step"
// pattern, JSON-only twin of the legacy execNodeStep handler. Accepts
// a raw wf.Node JSON object (not a drawflow node blob) so the v2
// inspector doesn't need to round-trip through the legacy codec.
//
// Body shape: { node: <wf.Node JSON>, input?: <map>, event?: <map>,
// parent_id?: <string> }. Response mirrors execNodeStep so the
// Output pane of the modal can render either result interchangeably.
func spaExecNode(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	var body struct {
		Node     wf.Node        `json:"node"`
		Input    map[string]any `json:"input"`
		Event    map[string]any `json:"event"`
		ParentID string         `json:"parent_id"`
		// Snapshot of upstream node outputs captured by the FE from
		// prior workflow_run SSE events (or earlier step runs). Keyed
		// by node id; values are the same flat output maps the
		// executor returns. Populates rc.NodeOutputs so template refs
		// like {{.Node.<upstream_label>.row}} resolve even though only
		// this one node is being executed in isolation.
		NodeOutputs map[string]map[string]any `json:"node_outputs"`
	}
	if err := json.NewDecoder(c.R.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	if body.Node.Type == "" {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "node.type is required"})
		return
	}
	exec, ok := globalWorkflowMgr.Engine.Executors[body.Node.Type]
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "no executor for node type " + string(body.Node.Type)})
		return
	}
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		// Soft-fail — operator may be iterating before any save lands.
		w = wf.Workflow{ID: id}
	}
	envVals, _ := globalWorkflowMgr.Service.LoadEnvValues(id)
	outputs := map[string]any{}
	nodeOutputs := map[string]wf.NodeOutput{}
	// Hydrate every upstream output the FE knows about. Each entry
	// becomes a NodeOutput.Fields blob — BuildRenderCtx already
	// flattens Fields into the per-node template map plus aliases
	// the same payload under the node's label. Special-cases verdict
	// + result so classify/branch downstream refs stay intact.
	for nodeID, out := range body.NodeOutputs {
		if out == nil {
			continue
		}
		no := wf.NodeOutput{Fields: out}
		if v, ok := out["verdict"].(string); ok {
			no.Verdict = v
		}
		if v, ok := out["result"]; ok {
			no.Result = v
		}
		nodeOutputs[nodeID] = no
		outputs[nodeID] = out
	}
	if body.ParentID != "" {
		outputs[body.ParentID] = body.Input
	}
	outputs["input"] = body.Input
	rc := &wf.RunContext{
		Workflow:    w,
		Event:       eventFromExecBody(body.Event, body.Input),
		Outputs:     outputs,
		EnvValues:   envVals,
		RunID:       "step-" + time.Now().UTC().Format("20060102T150405.000000000"),
		NodeOutputs: nodeOutputs,
	}
	startedAt := time.Now()
	out, runErr := exec.Execute(c.Context(), body.Node, rc)
	resp := map[string]any{
		"ok":         runErr == nil,
		"latency_ms": time.Since(startedAt).Milliseconds(),
		"output":     nodeOutputToJSON(out),
	}
	if runErr != nil {
		resp["error"] = runErr.Error()
	}
	c.JSON(http.StatusOK, resp)
}

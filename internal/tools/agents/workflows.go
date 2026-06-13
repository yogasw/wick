package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/setup"
	wftest "github.com/yogasw/wick/internal/agents/workflow/wftest"
	"github.com/yogasw/wick/internal/entity"
	wfview "github.com/yogasw/wick/internal/tools/agents/view/workflow"
	"github.com/yogasw/wick/pkg/tool"
)

// TestCaseItem pairs an on-disk test case with its last-run result for
// the spa test panel handlers.
type TestCaseItem struct {
	Name   string
	Case   wftest.Case
	Result *wftest.Result
}

// renderArgFormHTML serializes the wfview.ArgForm templ into the HTML
// fragment the workflow editor injects when the user picks a connector
// module + op.
func renderArgFormHTML(ctx context.Context, rows []entity.Config) string {
	if len(rows) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := wfview.ArgForm(rows).Render(ctx, &buf); err != nil {
		return ""
	}
	return buf.String()
}

// WorkflowSSESession returns the broadcaster session key used for
// workflow run events.
func WorkflowSSESession(id string) string { return "wf:" + id }

// WorkflowEventHook builds an engine.OnEvent callback that fans
// workflow run events out to the SSE broadcaster.
func WorkflowEventHook(b *Broadcaster) func(id, runID string, ev wf.RunEvent) {
	if b == nil {
		return nil
	}
	return func(id, runID string, ev wf.RunEvent) {
		ts := ev.TS
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		payload := map[string]any{
			"id":     id,
			"run_id": runID,
			"event":  ev.Event,
			"node":   ev.Node,
			"case":   ev.Case,
			"data":   ev.Data,
			"ts":     ts.UTC().Format(time.RFC3339Nano),
		}
		body, _ := json.Marshal(payload)
		b.fanout(WorkflowSSESession(id), Event{
			SessionID: WorkflowSSESession(id),
			Type:      "wf_" + ev.Event,
			Data:      string(body),
		})
	}
}

// globalWorkflowMgr is the wired workflow stack. server.go calls
// SetWorkflowManager once at boot.
var globalWorkflowMgr *setup.Manager

// SetWorkflowManager wires in the workflow Manager constructed by
// server.go. After the JSON migration, workflow body is DB-primary —
// no file→DB importer runs here.
func SetWorkflowManager(m *setup.Manager) {
	globalWorkflowMgr = m
}

func notReadyWorkflow(c *tool.Ctx) bool {
	if globalWorkflowMgr == nil {
		c.Error(http.StatusServiceUnavailable, "workflows not initialised — check server boot logs")
		return true
	}
	return false
}

// ── List + Create ───────────────────────────────────────────────────

func workflowsPage(c *tool.Ctx) {
	c.HTML(wfview.SvelteList(wfview.SvelteListVM{
		Layout:   sidebarVM(c, "workflows", ""),
		Base:     c.Base(),
		AssetURL: spaAssetURL("workflow"),
	}))
}

func createWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	name := strings.TrimSpace(c.Form("name"))
	id := strings.TrimSpace(c.Form("slug"))
	template := strings.TrimSpace(c.Form("template"))
	if template == "" {
		template = "empty"
	}
	w, err := globalWorkflowMgr.MCP.Create(mcp.CreateInput{ID: id, Name: name, Template: template})
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("create workflow name=%q id=%q: %s", name, id, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, w.ID)
	c.Redirect(c.Base()+"/workflows/edit/"+w.ID, http.StatusSeeOther)
}

// importWorkflow handles POST /workflows/import — receives a workflow
// JSON file upload, parses + validates it, creates the workflow
// folder, and redirects to the editor.
func importWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	f, header, err := c.R.FormFile("file")
	if err != nil {
		c.Error(http.StatusBadRequest, "file is required: "+err.Error())
		return
	}
	defer f.Close()

	if header.Size > 512*1024 {
		c.Error(http.StatusBadRequest, "file too large (max 512 KB)")
		return
	}

	data := make([]byte, header.Size)
	if _, err := f.Read(data); err != nil {
		c.Error(http.StatusBadRequest, "read file: "+err.Error())
		return
	}

	w, err := parse.Parse(uuid.NewString(), data)
	if err != nil {
		c.Error(http.StatusBadRequest, "invalid workflow JSON: "+err.Error())
		return
	}

	if r := parse.Validate(w); !r.Ok() {
		c.Error(http.StatusBadRequest, "validation failed: "+r.Error())
		return
	}

	w.ID = ""
	w.CreatedAt = time.Time{}

	created, err := globalWorkflowMgr.MCP.Create(mcp.CreateInput{Name: w.Name})
	if err != nil {
		c.Error(http.StatusInternalServerError, "create workflow: "+err.Error())
		return
	}

	w.ID = created.ID
	if err := globalWorkflowMgr.Service.SaveDraft(created.ID, w); err != nil {
		c.Error(http.StatusInternalServerError, "save imported workflow: "+err.Error())
		return
	}

	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, created.ID)
	c.Redirect(c.Base()+"/workflows/edit/"+created.ID, http.StatusSeeOther)
}

// downloadWorkflowYAML serves the published workflow body as a JSON
// file download. Name kept (handler is referenced by route table) so
// callers don't need to update; the served file is JSON now.
func downloadWorkflowYAML(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	w, err := globalWorkflowMgr.Service.Load(id)
	if err != nil {
		c.NotFound()
		return
	}
	data, err := parse.Marshal(w)
	if err != nil {
		c.Error(http.StatusInternalServerError, "marshal body: "+err.Error())
		return
	}
	filename := id + ".workflow.json"
	if w.Name != "" {
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, w.Name)
		filename = safe + ".workflow.json"
	}
	c.W.Header().Set("Content-Type", "application/json")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.W.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	_, _ = c.W.Write(data)
}

// ── Editor ──────────────────────────────────────────────────────────

func workflowEditor(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	if _, err := globalWorkflowMgr.Service.LoadDraft(id); err != nil {
		c.NotFound()
		return
	}
	layoutVM := sidebarVM(c, "workflows", "")
	layoutVM.FullBleed = true
	propsJSON, _ := json.Marshal(map[string]string{"workflowID": id})
	c.HTML(wfview.SvelteEditor(wfview.SvelteEditorVM{
		Layout:    layoutVM,
		Base:      c.Base(),
		ID:        id,
		AssetURL:  spaAssetURL("workflow"),
		PropsJSON: string(propsJSON),
	}))
}

// validationPayload reshapes a parse.Result into a per-node lookup the
// canvas JS can index by node id.
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
		"ok":       r.Ok(),
		"errors":   r.Errors,
		"warnings": r.Warnings,
		"by_node":  byNode,
		"global":   global,
	}
}

// nodeIDFromPath extracts a node id from validation error paths like
// `graph.nodes[<id>].field`.
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

// renameWorkflow updates the display Name without touching the folder
// or URL. Synced across both published + draft when both exist.
func renameWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	name := strings.TrimSpace(c.Form("name"))
	if name == "" {
		c.Error(http.StatusBadRequest, "name is required")
		return
	}
	svc := globalWorkflowMgr.Service
	if pub, err := svc.Load(id); err == nil {
		pub.Name = name
		if err := svc.Update(id, pub); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		c.NotFound()
		return
	}
	if svc.HasDraft(id) {
		draft, err := svc.LoadDraft(id)
		if err == nil {
			draft.Name = name
			if err := svc.SaveDraft(id, draft); err != nil {
				c.Error(http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	if strings.Contains(c.R.Header.Get("Accept"), "application/json") {
		c.JSON(http.StatusOK, map[string]any{"ok": true, "name": name})
		return
	}
	c.Redirect(c.Base()+"/workflows/edit/"+id, http.StatusSeeOther)
}

// triggerLabel turns a TriggerType slug ("schedule_at") into a
// human-readable palette label ("Schedule At").
func triggerLabel(t string) string {
	if t == "" {
		return ""
	}
	out := make([]rune, 0, len(t))
	upperNext := true
	for _, r := range t {
		if r == '_' {
			out = append(out, ' ')
			upperNext = true
			continue
		}
		if upperNext {
			if r >= 'a' && r <= 'z' {
				r = r - 'a' + 'A'
			}
			upperNext = false
		}
		out = append(out, r)
	}
	return string(out)
}

// workflowRegistryAPI returns JSON catalog the editor uses to hydrate
// pickers (channels, channel ops, connectors, providers).
func workflowRegistryAPI(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	channels := []map[string]any{}
	for _, info := range globalWorkflowMgr.MCP.ChannelsList() {
		actionDescs := map[string]integration.ActionDescriptor{}
		for _, ad := range globalWorkflowMgr.Integration.ActionsByChannel(info.Name) {
			actionDescs[ad.Action] = ad
		}
		ops := []map[string]any{}
		for _, a := range info.Actions {
			row := map[string]any{
				"id":          a.ID,
				"description": a.Description,
				"destructive": a.Destructive,
			}
			if ad, ok := actionDescs[a.ID]; ok && ad.InputType != nil {
				schema := entity.StructToConfigs(ad.InputType)
				row["args_html"] = renderArgFormHTML(c.Context(), schema)
				row["args_schema"] = schema
			}
			ops = append(ops, row)
		}
		events := []map[string]any{}
		for _, ev := range globalWorkflowMgr.Integration.EventsByChannel(info.Name) {
			events = append(events, map[string]any{
				"id":           ev.Event,
				"name":         ev.Name,
				"description":  ev.Description,
				"match_html":   renderArgFormHTML(c.Context(), ev.MatchSchema),
				"match_schema": ev.MatchSchema,
			})
		}
		channels = append(channels, map[string]any{
			"name":             info.Name,
			"supports_session": info.SupportsSession,
			"ops":              ops,
			"events":           events,
		})
	}
	connectors := []map[string]any{}
	for _, info := range globalWorkflowMgr.MCP.ConnectorsList() {
		mod, modOK := globalWorkflowMgr.Connectors.Module(info.Module)
		ops := []map[string]any{}
		for i, op := range info.Operations {
			inputs := make([]map[string]any, 0, len(op.Input))
			for _, in := range op.Input {
				inputs = append(inputs, map[string]any{
					"key":         in.Key,
					"description": in.Description,
					"required":    in.Required,
				})
			}
			row := map[string]any{
				"id":          op.Key,
				"name":        op.Name,
				"description": op.Description,
				"destructive": op.Destructive,
				"input":       inputs,
			}
			if modOK && i < len(mod.Operations) {
				row["args_html"] = renderArgFormHTML(c.Context(), mod.Operations[i].Input)
				row["args_schema"] = mod.Operations[i].Input
			}
			ops = append(ops, row)
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
	nodeTypes := globalWorkflowMgr.MCP.NodeTypes()
	triggerTypes := []map[string]any{}
	if globalWorkflowMgr.Engine != nil && globalWorkflowMgr.Engine.Triggers != nil {
		for _, d := range globalWorkflowMgr.Engine.Triggers.List() {
			triggerTypes = append(triggerTypes, map[string]any{
				"type":        string(d.Type),
				"label":       triggerLabel(string(d.Type)),
				"description": d.Description,
				"schema":      d.Schema,
				"example":     d.Example,
			})
		}
	}
	// Expose the server's public base URL so the webhook trigger
	// inspector can build a full, clickable URL for the user.
	// Falls back to empty string — the UI shows relative paths in that case.
	hooksBaseURL := ""
	if globalConfigs != nil {
		base := strings.TrimRight(globalConfigs.AppURL(), "/")
		if base != "" {
			hooksBaseURL = base + "/hooks"
		}
	}
	c.JSON(http.StatusOK, map[string]any{
		"channels":       channels,
		"connectors":     connectors,
		"providers":      providers,
		"node_types":     nodeTypes,
		"trigger_types":  triggerTypes,
		"hooks_base_url": hooksBaseURL,
	})
}

// workflowLookupAPI serves the trigger inspector + connector args
// picker widgets. URL: GET /workflows/api/lookup?module=…&source=…&q=…
func workflowLookupAPI(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	module := c.Query("module")
	source := c.Query("source")
	query := c.Query("q")
	if module == "" || source == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "module and source required"})
		return
	}
	if globalChannels == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "channel registry not ready"})
		return
	}
	ch := globalChannels.ChannelByName(module)
	if ch == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "module not registered: " + module})
		return
	}
	lp, ok := ch.(agentchannels.LookupProvider)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": "module does not support lookup"})
		return
	}
	items, err := lp.Lookup(source, query)
	if err != nil {
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if items == nil {
		items = []agentchannels.LookupItem{}
	}
	c.JSON(http.StatusOK, items)
}

func deleteWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	if err := globalWorkflowMgr.MCP.Delete(id); err != nil {
		if c.R.Header.Get("Accept") == "application/json" {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		} else {
			c.Error(http.StatusInternalServerError, err.Error())
		}
		return
	}
	if globalTagsSvc != nil {
		_ = globalTagsSvc.DeleteResourceOwnerTag(c.Context(), id)
	}
	if c.R.Header.Get("Accept") == "application/json" {
		c.JSON(http.StatusOK, map[string]any{"ok": true})
		return
	}
	c.Redirect(c.Base()+"/workflows", http.StatusSeeOther)
}

// eventFromExecBody builds RunContext.Event for an Execute step.
func eventFromExecBody(evt map[string]any, input map[string]any) wf.Event {
	if len(evt) == 0 {
		return wf.Event{Type: string(wf.TriggerManual), At: time.Now().UTC(), Payload: input}
	}
	out := wf.Event{}
	if v, ok := evt["type"].(string); ok {
		out.Type = v
	}
	if out.Type == "" {
		out.Type = string(wf.TriggerManual)
	}
	if v, ok := evt["subtype"].(string); ok {
		out.Subtype = v
	}
	if v, ok := evt["channel"].(string); ok {
		out.Channel = v
	}
	if v, ok := evt["payload"].(map[string]any); ok {
		out.Payload = v
	} else if input != nil {
		out.Payload = input
	}
	out.At = time.Now().UTC()
	return out
}

// nodeOutputToJSON flattens a NodeOutput into the same map shape
// recordSuccess writes to RunContext.Outputs.
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

// workflowRunStateAPI returns the latest persisted state.json for a run.
func workflowRunStateAPI(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	runID := c.PathValue("runID")
	st, err := globalWorkflowMgr.StateStore.Load(id, runID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	limit := 200
	if v := strings.TrimSpace(c.Query("events_limit")); v != "" {
		if v == "all" {
			limit = 0
		} else if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	events, total, _ := globalWorkflowMgr.StateStore.ListEventsTail(id, runID, limit)
	c.JSON(http.StatusOK, map[string]any{
		"state":            st,
		"events":           events,
		"events_total":     total,
		"events_truncated": total > len(events),
	})
}

// loadTestCaseItems reads every test case for the workflow.
func loadTestCaseItems(id string) ([]TestCaseItem, error) {
	names, err := globalWorkflowMgr.MCP.ListTests(id)
	if err != nil {
		return nil, err
	}
	var items []TestCaseItem
	for _, name := range names {
		data, err := globalWorkflowMgr.MCP.GetTest(id, name)
		if err != nil {
			continue
		}
		var tc wftest.Case
		if err := json.Unmarshal(data, &tc); err != nil {
			continue
		}
		if tc.Name == "" {
			tc.Name = name
		}
		items = append(items, TestCaseItem{Name: name, Case: tc})
	}
	return items, nil
}

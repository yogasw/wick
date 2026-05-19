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
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/setup"
	"github.com/yogasw/wick/internal/agents/workflow/wftest"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/config"
	wfview "github.com/yogasw/wick/internal/tools/agents/view/workflow"
	"github.com/yogasw/wick/pkg/tool"
)

// renderArgFormHTML serializes the wfview.ArgForm templ into the HTML
// fragment the workflow editor injects when the user picks a
// connector module + op. Kept here next to the only caller — separate
// templ render into a buffer is cheap, ~50µs per op, and avoids
// pre-computing a giant map at boot time.
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
// workflow run events. The editor JS subscribes to /stream?session=<key>
// to receive per-node progress without polling.
func WorkflowSSESession(id string) string { return "wf:" + id }

// triggerHasEntry reports whether the named trigger type has a
// destination node wired up so the engine can actually start.
//
// Canvas-driven workflows (any Trigger has an ID set, meaning the
// codec produced this list) use strict per-trigger rules:
//   - found a matching trigger with EntryNode set → true
//   - everything else → false
//
// Legacy YAML (no Trigger.ID anywhere) falls back to the global
// `graph.entry` so hand-edited files still run. Once the canvas
// rewrites the workflow, that legacy path drops away naturally.
func triggerHasEntry(w wf.Workflow, t wf.TriggerType) bool {
	canvasDriven := false
	for _, tr := range w.Triggers {
		if tr.ID != "" {
			canvasDriven = true
			break
		}
	}
	for _, tr := range w.Triggers {
		if tr.Type != t {
			continue
		}
		if tr.EntryNode != "" {
			return true
		}
	}
	if canvasDriven {
		return false
	}
	return w.Graph.Entry != ""
}

// pickTriggerByID resolves the user-chosen trigger to fire. When
// `id` is non-empty it must match a Trigger.ID exactly — required
// for canvas-driven workflows where multiple triggers (manual,
// slack, cron, …) coexist and the engine has no way to guess.
//
// As a last-ditch fallback for legacy YAML that has exactly one
// trigger and no Trigger.ID set, an empty id returns that trigger
// so hand-edited single-trigger workflows still fire from the UI.
func pickTriggerByID(w wf.Workflow, id string) (*wf.Trigger, error) {
	if id != "" {
		for i := range w.Triggers {
			if w.Triggers[i].ID == id {
				return &w.Triggers[i], nil
			}
		}
		return nil, fmt.Errorf("trigger %q not found on canvas", id)
	}
	if len(w.Triggers) == 0 {
		return nil, fmt.Errorf("no trigger on canvas — drag a trigger from the palette first")
	}
	if len(w.Triggers) > 1 {
		return nil, fmt.Errorf("multiple triggers on canvas — pick one from the Execute workflow menu")
	}
	return &w.Triggers[0], nil
}

// mergeTriggers folds prev's per-trigger metadata (channel name,
// schedule, webhook path, …) into the canvas-derived list. The
// canvas owns: ID, Type, EntryNode, position. prev owns: all
// other config fields the inspector lets the user edit.
//
// Match key is Trigger.ID; new triggers added on the canvas keep
// their codec-defaulted shape (just Type + EntryNode), prev
// triggers without a canvas counterpart are dropped — the canvas
// is the source of truth for which triggers exist.
func mergeTriggers(canvas, prev []wf.Trigger) []wf.Trigger {
	byID := make(map[string]wf.Trigger, len(prev))
	for _, p := range prev {
		if p.ID != "" {
			byID[p.ID] = p
		}
	}
	out := make([]wf.Trigger, 0, len(canvas))
	for _, c := range canvas {
		if p, ok := byID[c.ID]; ok {
			// Canvas wins for every editor-driven field
			// (ChannelName, Event, Match, Schedule, Path, …). Prev
			// only carries metadata the canvas doesn't model so the
			// operator's hand-edited YAML or earlier config isn't
			// wiped: whitelist, dedup TTL, reply-source flag,
			// require-role, webhook secret, schedule_at metadata,
			// error severity/source filters.
			c.Whitelist = p.Whitelist
			c.DedupTTLSec = p.DedupTTLSec
			c.ReplySource = p.ReplySource
			c.RequireRole = p.RequireRole
			c.SecretRef = p.SecretRef
			c.ParseBody = p.ParseBody
			c.BodyToVar = p.BodyToVar
			if c.At.IsZero() {
				c.At = p.At
			}
			c.DeleteAfter = p.DeleteAfter
			if c.SourceWorkflow == "" {
				c.SourceWorkflow = p.SourceWorkflow
			}
			if len(c.Severity) == 0 {
				c.Severity = p.Severity
			}
			if len(c.NodeTypes) == 0 {
				c.NodeTypes = p.NodeTypes
			}
		}
		out = append(out, c)
	}
	return out
}

// WorkflowEventHook builds an engine.OnEvent callback that fans
// workflow run events out to the SSE broadcaster. Marshals the
// RunEvent as JSON in the SSE payload Data field; Type prefixes
// "wf_" so the editor JS can dispatch on it without colliding with
// agent stream events.
func WorkflowEventHook(b *Broadcaster) func(id, runID string, ev wf.RunEvent) {
	if b == nil {
		return nil
	}
	return func(id, runID string, ev wf.RunEvent) {
		// Mirror state.events.jsonl's ts so the FE can dedup state
		// backfill against live SSE by `ts|event|node|case`.
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
	// UI passes display name only — folder/URL id is the UUID
	// MCP.Create generates so later renames can't break run history
	// or shared edit links. Legacy `slug` field stays accepted (MCP /
	// CLI / hand-edited fetches) but the form no longer offers it.
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
	// Register the fresh workflow with the router so Run Now / triggers
	// work without a manual restart. Bootstrap only registers existing
	// folders at startup — first-time Create needs an explicit reload.
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, w.ID)
	c.Redirect(c.Base()+"/workflows/edit/"+w.ID, http.StatusSeeOther)
}

// importWorkflow handles POST /workflows/import — receives a YAML file
// upload, parses + validates it, creates the workflow folder, and
// redirects to the editor. The YAML must be a valid workflow.yaml;
// the workflow ID (folder name) is always a fresh UUID to avoid
// collisions with existing workflows.
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

	// Parse with a throwaway UUID — Create will assign the real one.
	// Must be a valid id ([a-z0-9-]) so parse.ValidateID passes.
	w, err := parse.Parse(uuid.NewString(), data)
	if err != nil {
		c.Error(http.StatusBadRequest, "invalid workflow YAML: "+err.Error())
		return
	}

	// Validate graph structure before writing to disk.
	if r := parse.Validate(w); !r.Ok() {
		c.Error(http.StatusBadRequest, "validation failed: "+r.Error())
		return
	}

	// Always assign a new UUID so imports never collide.
	w.ID = ""
	w.CreatedAt = time.Time{}

	created, err := globalWorkflowMgr.MCP.Create(mcp.CreateInput{Name: w.Name})
	if err != nil {
		c.Error(http.StatusInternalServerError, "create workflow: "+err.Error())
		return
	}

	// Overwrite the scaffolded workflow.yaml with the imported content.
	w.ID = created.ID
	if err := globalWorkflowMgr.Service.SaveDraft(created.ID, w); err != nil {
		c.Error(http.StatusInternalServerError, "save imported workflow: "+err.Error())
		return
	}

	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, created.ID)
	c.Redirect(c.Base()+"/workflows/edit/"+created.ID, http.StatusSeeOther)
}

// downloadWorkflowYAML serves the published workflow.yaml as a file download.
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
		c.Error(http.StatusInternalServerError, "marshal YAML: "+err.Error())
		return
	}
	filename := id + ".workflow.yaml"
	if w.Name != "" {
		// Use the display name for a friendlier filename.
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, w.Name)
		filename = safe + ".workflow.yaml"
	}
	c.W.Header().Set("Content-Type", "application/x-yaml")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.W.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	_, _ = c.W.Write(data)
}

// ── Editor + CRUD ──────────────────────────────────────────────────

func workflowEditor(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	// Editor always opens the draft if one exists, otherwise the
	// published workflow — so in-progress edits survive page refresh.
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.NotFound()
		return
	}
	hasDraft := globalWorkflowMgr.Service.HasDraft(id)
	yamlBytes, _ := parse.Marshal(w)
	graphJSON, err := workflowToDrawflowJSON(w)
	if err != nil {
		log.Ctx(c.Context()).Warn().Msgf("graph json serialize: %s", err.Error())
		graphJSON = "{}"
	}
	report := globalWorkflowMgr.Guard.Review(c.Context(), w)
	// Runs panel pagination — `?runs_page=N` (1-based). 100 per page
	// matches the index shard cap so each page request reads exactly
	// one shard file.
	page := 1
	if v := strings.TrimSpace(c.Query("runs_page")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	runs, hasMore, _ := globalWorkflowMgr.MCP.GetRunSummaries(id, page, 100)
	approved := false
	if st, err := globalWorkflowMgr.Service.LoadState(id); err == nil {
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
		ID:             id,
		Workflow:       w,
		HasDraft:       hasDraft,
		YAML:           string(yamlBytes),
		GraphJSON:      graphJSON,
		ValidationJSON: string(validationJSON),
		Approved:       approved,
		GuardReport:    &report,
		NodeTypes:      globalWorkflowMgr.MCP.NodeTypes(),
		Palette:        wfview.BuildPalette(globalWorkflowMgr.MCP.ChannelsList(), globalWorkflowMgr.MCP.ConnectorsList()),
		Runs:           runs,
		RunsPage:       page,
		RunsHasMore:    hasMore,
	}))
}

func saveWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	body := c.Form("body")
	w, err := drawflowJSONToWorkflow(id, body)
	if err != nil {
		saveResponse(c, http.StatusBadRequest, "invalid graph payload: "+err.Error(), nil)
		return
	}
	// Carry forward metadata from disk that the canvas doesn't model
	// (name, env, on_error, …). For triggers, the canvas now drives
	// the shape: each canvas trigger node maps to one Trigger entry.
	// Merge per-trigger config (channel name, schedule, webhook
	// path, …) from prev by Trigger.ID so the canvas can re-wire
	// EntryNode without losing the inspector-typed settings.
	//
	// Codec's empty graph.entry is now intentional ("trigger not
	// wired anywhere"); don't paper over it with the prev value.
	if prev, err := globalWorkflowMgr.Service.LoadDraft(id); err == nil {
		w.Triggers = mergeTriggers(w.Triggers, prev.Triggers)
		w.Enabled = prev.Enabled
		w.Name = prev.Name
		w.Description = prev.Description
		w.Env = prev.Env
		w.Datasets = prev.Datasets
		w.OnError = prev.OnError
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
	if err := globalWorkflowMgr.Service.SaveDraft(id, w); err != nil {
		log.Ctx(c.Context()).Error().Msgf("save workflow draft %s: %s", id, err.Error())
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
	c.Redirect(c.Base()+"/workflows/edit/"+c.PathValue("id"), http.StatusSeeOther)
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
	id := c.PathValue("id")
	if !globalWorkflowMgr.Service.HasDraft(id) {
		c.Redirect(c.Base()+"/workflows/edit/"+id, http.StatusSeeOther)
		return
	}
	// Validate the draft BEFORE we promote it. If we promoted first
	// then rejected on validation, the previous published version is
	// already overwritten with broken yaml.
	draft, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	if r := parse.Validate(draft); !r.Ok() {
		c.Error(http.StatusBadRequest, "cannot publish — fix validation errors:\n"+r.Error())
		return
	}
	guardReport := globalWorkflowMgr.Guard.Review(c.Context(), draft)
	if err := globalWorkflowMgr.Guard.Apply(guardReport, nil); err != nil {
		c.Error(http.StatusForbidden, err.Error())
		return
	}
	if _, err := globalWorkflowMgr.Service.Publish(id); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, id)
	c.Redirect(c.Base()+"/workflows/edit/"+id, http.StatusSeeOther)
}

// discardWorkflowDraft drops workflow.draft.yaml — editor reverts to
// the published copy on next open.
func discardWorkflowDraft(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	if err := globalWorkflowMgr.Service.DiscardDraft(id); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/workflows/edit/"+id, http.StatusSeeOther)
}

// renameWorkflow updates the display Name without touching the folder
// or URL. Name is non-structural metadata so we sync it across BOTH
// workflow.yaml AND workflow.draft.yaml when both exist — otherwise
// the list page (which reads published) drifts behind the editor
// (which reads draft) after a rename.
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
		if err := svc.Update(id, pub, nil); err != nil {
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

func toggleWorkflow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	// Read enabled from whichever copy the editor is showing (draft
	// if present, otherwise published) so the toggle button reflects
	// what the user clicked.
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
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
	if globalWorkflowMgr.Service.HasDraft(id) {
		w.Enabled = next
		if err := globalWorkflowMgr.Service.SaveDraft(id, w); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		if err := globalWorkflowMgr.Service.Toggle(id, next); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
	}
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, id)
	c.Redirect(c.Base()+"/workflows/edit/"+id, http.StatusSeeOther)
}

func runWorkflowNow(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	// Run Now is a live test of the canvas — fire the draft if one
	// exists, otherwise the published workflow. Bypasses Enabled
	// so the admin can verify drafts before publishing.
	w, err := globalWorkflowMgr.Service.LoadDraft(id)
	if err != nil {
		c.NotFound()
		return
	}
	// The UI's Execute workflow picker passes which trigger to
	// fire. Required for canvas-driven workflows (multi-trigger
	// support) — without it we'd have to guess, which broke when
	// the user wanted Slack-only runs but we defaulted to manual.
	triggerID := strings.TrimSpace(c.Form("trigger_id"))
	chosen, err := pickTriggerByID(w, triggerID)
	if err != nil {
		runResponse(c, http.StatusBadRequest, "", err.Error())
		return
	}
	if chosen.EntryNode == "" {
		runResponse(c, http.StatusBadRequest, "", "trigger \""+chosen.ID+"\" is not wired to any node. Drag a line from the trigger to the node you want to start at.")
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
	// workflows/ dir and never registered this id.
	_ = setup.HotReload(context.Background(), globalWorkflowMgr.Service, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, id)
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
	//
	// Format matches engine.NewRunID (`<utc-timestamp>-<uuid>`) so
	// the runs/ directory listing sorts chronologically without an
	// extra state.json read per row in the Runs panel.
	runID := engine.NewRunID()
	// Carry request_id across the queue boundary. The HTTP request
	// ctx dies as soon as we return, but the worker goroutine that
	// drains the queue lives forever — without this hop, engine
	// logs lose the request_id from the HTTP middleware.
	reqID, _ := c.Context().Value(config.RequestIDKey).(string)
	evt := wf.Event{
		Type: string(chosen.Type),
		At:   time.Now().UTC(),
		Payload: map[string]any{
			"run_id":     runID,
			"source":     "ui",
			"request_id": reqID,
			"trigger_id": chosen.ID,
		},
	}
	// Bookend log: the engine will fire its own "workflow event"
	// lines with the same wf_run_id, so an operator searching by
	// either request_id (HTTP middleware) or wf_run_id (engine)
	// gets the complete trace from click → enqueue → run finish.
	log.Ctx(c.Context()).Info().
		Str("component", "wf").
		Str("wf_id", id).
		Str("wf_run_id", runID).
		Str("wf_event", "run_enqueue").
		Msg("workflow run enqueued from UI")
	// Pass the draft as an explicit override so the run uses the
	// canvas-fresh wiring (e.g. trigger → http after the user just
	// rewired). Without this, the engine walks Router's registered
	// PUBLISHED workflow.yaml and the rewire is invisible until the
	// user clicks Publish — exactly the bug repro: YAML draft says
	// `entry: http`, click Run Now, agent runs because router still
	// holds the old published copy.
	if err := globalWorkflowMgr.MCP.RunNowWith(c.Context(), id, &w, evt); err != nil {
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
	c.Redirect(c.Base()+"/workflows/edit/"+c.PathValue("id"), http.StatusSeeOther)
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
		// Index ActionDescriptors by action id so we can attach args_html.
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
				row["args_html"] = renderArgFormHTML(c.Context(), entity.StructToConfigs(ad.InputType))
			}
			ops = append(ops, row)
		}
		// Per-event match form — each EventDescriptor declares
		// MatchSchema, the API renders it to HTML via ArgForm so the
		// trigger inspector can innerHTML-inject without rebuilding
		// the widget layer in JS.
		events := []map[string]any{}
		for _, ev := range globalWorkflowMgr.Integration.EventsByChannel(info.Name) {
			events = append(events, map[string]any{
				"id":          ev.Event,
				"name":        ev.Name,
				"description": ev.Description,
				"match_html":  renderArgFormHTML(c.Context(), ev.MatchSchema),
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
		// Resolve back to the raw connector.Module so we can hand the
		// full []entity.Config rows to the ArgForm renderer — the
		// stripped OpInput shape (key/description/required only) drops
		// the widget metadata (Type, Options, IsSecret, …) the form
		// needs to pick the right widget.
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
	c.JSON(http.StatusOK, map[string]any{
		"channels":   channels,
		"connectors": connectors,
		"providers":  providers,
	})
}

// workflowLookupAPI serves the trigger inspector + connector args
// picker widgets. Mirrors /channels/{slug}/lookup but keyed by the
// channel/connector "module" name rather than the admin slug so the
// workflow editor's URL pattern stays under /workflows/api/. Source
// (e.g. "slack.channels") is the same lookup key the channel's
// LookupProvider already understands.
//
// URL: GET /workflows/api/lookup?module=slack&source=slack.channels&q=...
//
// Returns JSON array of {id, name}.
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
	id := c.PathValue("id")
	var body struct {
		Node  map[string]any `json:"node"`
		Input map[string]any `json:"input"`
		// Event is an optional full envelope override — when present
		// (the editor sends it after a Replay so the step sees the
		// same Event the original run had), it replaces the manual
		// fallback below so templates like `{{.Event.Channel}}` or
		// `{{.Event.Subtype}}` resolve to the historical values
		// instead of "manual" / "".
		Event map[string]any `json:"event"`
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
	w, err := singleNodeWorkflow(id, body.Node)
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
	envVals, _ := globalWorkflowMgr.Service.LoadEnvValues(id)
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
		Event:       eventFromExecBody(body.Event, body.Input),
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

// eventFromExecBody builds the RunContext.Event for an Execute step
// invocation. When the editor sent an `event` blob (Replay → Execute
// pattern — same Event the historical run saw), unpack it into a
// typed wf.Event so templates referencing `.Event.Channel`,
// `.Event.Subtype`, etc. resolve. Otherwise fall back to a synthetic
// manual event whose Payload mirrors `body.Input`, preserving the
// pre-existing UX where a bare Execute step uses the mock map as
// `.Event.Payload`.
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
func singleNodeWorkflow(id string, node map[string]any) (wf.Workflow, error) {
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
	return drawflowJSONToWorkflow(id, string(raw))
}

// workflowRunStateAPI returns the latest persisted state.json for a
// run. Editor JS uses this when the user re-opens a run that was
// triggered before page load — SSE events catch live runs, this
// catches up cold-start.
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
	events, _ := globalWorkflowMgr.StateStore.ListEvents(id, runID)
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
	id := c.PathValue("id")
	runID := c.PathValue("runID")
	st, err := globalWorkflowMgr.StateStore.Load(id, runID)
	if err != nil {
		c.NotFound()
		return
	}
	events, _ := globalWorkflowMgr.StateStore.ListEvents(id, runID)
	c.HTML(wfview.Run(wfview.RunVM{
		Layout: sidebarVM(c, "workflows", ""),
		Base:   c.Base(),
		ID:     id,
		RunID:  runID,
		State:  st,
		Events: events,
	}))
}

func runWorkflowTests(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	runner := wftest.New(
		globalWorkflowMgr.Engine,
		globalWorkflowMgr.Service,
		globalWorkflowMgr.Layout,
	)
	results, cov, err := runner.RunAllWithCoverage(context.Background(), id)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	// Render as a raw HTML fragment — not c.HTML() which wraps in the
	// full page shell. The JS fetch injects this directly into #wf-test-results.
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = wfview.TestResultsPanel(wfview.TestResultsVM{
		ID:       id,
		Results:  results,
		Coverage: cov,
	}).Render(c.R.Context(), c.W)
}

// ── Test Case Manager ───────────────────────────────────────────────

// loadTestCaseItems reads __tests__/*.json for an id and returns items.
func loadTestCaseItems(id string) ([]wfview.TestCaseItem, error) {
	files, err := globalWorkflowMgr.MCP.ListFiles(id)
	if err != nil {
		return nil, err
	}
	var items []wfview.TestCaseItem
	for _, f := range files {
		if !strings.HasPrefix(f, "__tests__/") || !strings.HasSuffix(f, ".json") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(f, "__tests__/"), ".json")
		data, err := globalWorkflowMgr.MCP.ReadFile(id, f)
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
		items = append(items, wfview.TestCaseItem{Name: name, Case: tc})
	}
	return items, nil
}

// listTestCases returns the test manager panel HTML fragment.
func listTestCases(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	items, err := loadTestCaseItems(id)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = wfview.TestManager(wfview.TestManagerVM{
		ID:    id,
		Base:  c.Base(),
		Items: items,
	}).Render(c.R.Context(), c.W)
}

// saveTestCase creates or updates a test case fixture file.
func saveTestCase(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	var body struct {
		Name       string           `json:"name"`
		Input      wftest.Input     `json:"input"`
		Assertions []wftest.Assertion `json:"assertions"`
	}
	if err := json.NewDecoder(c.R.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		c.JSON(http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	// Sanitise name: allow alphanumeric, dash, underscore only.
	for _, ch := range body.Name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			c.JSON(http.StatusBadRequest, map[string]any{"error": "name must be slug-safe (a-z, 0-9, dash, underscore)"})
			return
		}
	}
	tc := wftest.Case{
		Name:       body.Name,
		Input:      body.Input,
		Assertions: body.Assertions,
	}
	data, err := json.MarshalIndent(tc, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	path := "__tests__/" + body.Name + ".json"
	if err := globalWorkflowMgr.MCP.WriteFile(id, path, data); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "name": body.Name})
}

// runOneTestCase runs a single named test case and returns the row HTML fragment.
func runOneTestCase(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	name := c.PathValue("name")
	path := "__tests__/" + name + ".json"
	data, err := globalWorkflowMgr.MCP.ReadFile(id, path)
	if err != nil {
		c.Error(http.StatusNotFound, "test case not found")
		return
	}
	var tc wftest.Case
	if err := json.Unmarshal(data, &tc); err != nil {
		c.Error(http.StatusBadRequest, "invalid test case JSON")
		return
	}
	w, err := globalWorkflowMgr.Service.Load(id)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	runner := wftest.New(globalWorkflowMgr.Engine, globalWorkflowMgr.Service, globalWorkflowMgr.Layout)
	result := runner.RunOne(context.Background(), w, tc)
	item := wfview.TestCaseItem{Name: name, Case: tc, Result: &result}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = wfview.TestCaseRow(wfview.TestCaseRowVM{
		ID:   id,
		Base: c.Base(),
		Item: item,
	}).Render(c.R.Context(), c.W)
}

// deleteTestCase removes a test case fixture file.
func deleteTestCase(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	name := c.PathValue("name")
	path := "__tests__/" + name + ".json"
	if err := globalWorkflowMgr.MCP.DeleteFile(id, path); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// ── Executions panel ────────────────────────────────────────────────

// executionsPanel returns the full Executions panel as an HTML fragment.
// Called by the JS tab switcher on first click and on manual refresh.
func executionsPanel(c *tool.Ctx) {
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
	runs, hasMore, _ := globalWorkflowMgr.MCP.GetRunSummaries(id, page, 50)
	vm := wfview.ExecutionsVM{
		Base:        c.Base(),
		ID:          id,
		Runs:        runs,
		RunsHasMore: hasMore,
	}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = wfview.ExecutionsPanel(vm).Render(c.R.Context(), c.W)
}

// executionDetail returns a single run's detail fragment.
// Injected into the right pane when user clicks a run row.
func executionDetail(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	runID := c.PathValue("runID")
	st, err := globalWorkflowMgr.StateStore.Load(id, runID)
	if err != nil {
		c.NotFound()
		return
	}
	events, _ := globalWorkflowMgr.StateStore.ListEvents(id, runID)
	detail := wfview.ExecutionDetailVM{
		RunID:  runID,
		State:  st,
		Events: events,
	}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = wfview.ExecutionDetail(c.Base(), id, detail).Render(c.R.Context(), c.W)
}

// ── Copy run to editor ──────────────────────────────────────────────

// copyRunToEditor restores the current published workflow as a new
// draft and tags it with the source run ID. The run's node outputs
// are written to runs/<runID>/mocks.json so Execute Step can pre-fill
// inputs from that run's actual data.
func copyRunToEditor(c *tool.Ctx) {
	if notReadyWorkflow(c) {
		return
	}
	id := c.PathValue("id")
	runID := c.PathValue("runID")

	st, err := globalWorkflowMgr.StateStore.Load(id, runID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]any{"error": "run not found"})
		return
	}

	svc := globalWorkflowMgr.Service
	w, err := svc.Load(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": "load workflow: " + err.Error()})
		return
	}

	hadDraft := svc.HasDraft(id)

	if err := svc.SaveDraft(id, w); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]any{"error": "save draft: " + err.Error()})
		return
	}

	if len(st.Outputs) > 0 {
		if mockData, merr := json.Marshal(st.Outputs); merr == nil {
			_ = svc.WriteFile(id, "runs/"+runID+"/mocks.json", mockData)
		}
	}

	_ = setup.HotReload(context.Background(), svc, globalWorkflowMgr.Router, globalWorkflowMgr.Cron, globalWorkflowMgr.ScheduleAt, id)

	c.JSON(http.StatusOK, map[string]any{
		"ok":       true,
		"hadDraft": hadDraft,
		"runID":    runID,
		"id":       id,
	})
}

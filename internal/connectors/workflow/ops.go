package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/wftest"
	"github.com/yogasw/wick/pkg/connector"
)

// ── Tier 1: introspection ──────────────────────────────────────────────

func (h *handlers) workspace(c *connector.Ctx) (any, error) {
	return h.ops.Workspace(), nil
}

func (h *handlers) nodeTypes(c *connector.Ctx) (any, error) {
	return h.ops.NodeTypes(), nil
}

func (h *handlers) triggerTypes(c *connector.Ctx) (any, error) {
	return h.ops.TriggerTypes(), nil
}

func (h *handlers) channels(c *connector.Ctx) (any, error) {
	return h.ops.ChannelsList(), nil
}

func (h *handlers) integration(c *connector.Ctx) (any, error) {
	rawActions := h.ops.IntegrationActions()
	actions := make([]map[string]any, len(rawActions))
	for i, a := range rawActions {
		actions[i] = map[string]any{
			"channel":       a.Channel,
			"action":        a.Action,
			"name":          a.Name,
			"description":   a.Description,
			"destructive":   a.Destructive,
			"input_schema":  integration.StructSchema(a.InputType),
			"output_schema": integration.StructSchema(a.OutputType),
		}
	}

	rawEvents := h.ops.IntegrationEvents()
	events := make([]map[string]any, len(rawEvents))
	for i, e := range rawEvents {
		ev := map[string]any{
			"channel":     e.Channel,
			"event":       e.Event,
			"name":        e.Name,
			"description": e.Description,
		}
		if e.MatchSchema != nil {
			ev["match_schema"] = e.MatchSchema
		}
		events[i] = ev
	}

	return map[string]any{
		"events":  events,
		"actions": actions,
	}, nil
}

func (h *handlers) connectors(c *connector.Ctx) (any, error) {
	return h.ops.ConnectorsList(), nil
}

func (h *handlers) skills(c *connector.Ctx) (any, error) {
	provider := c.Input("provider")
	return h.ops.SkillsList(ctxFrom(c), provider)
}

func (h *handlers) providers(c *connector.Ctx) (any, error) {
	return h.ops.ProvidersList(), nil
}

func (h *handlers) list(c *connector.Ctx) (any, error) {
	filter := strings.ToLower(c.Input("filter"))
	summaries, err := h.ops.List()
	if err != nil {
		return nil, err
	}
	if filter == "" {
		return summaries, nil
	}
	out := make([]wfmcp.Summary, 0)
	for _, s := range summaries {
		if strings.Contains(strings.ToLower(s.Name), filter) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (h *handlers) get(c *connector.Ctx) (any, error) {
	return h.ops.Get(c.Input("id"))
}

func (h *handlers) checkName(c *connector.Ctx) (any, error) {
	conflict, err := h.ops.Service.FindByName(c.Input("name"), c.Input("except_id"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"available":     conflict == "",
		"conflict_id": conflict,
	}, nil
}

func (h *handlers) listFiles(c *connector.Ctx) (any, error) {
	return h.ops.ListFiles(c.Input("id"))
}

func (h *handlers) readFile(c *connector.Ctx) (any, error) {
	data, err := h.ops.ReadFile(c.Input("id"), c.Input("path"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": string(data)}, nil
}

// ── Tier 2: write ──────────────────────────────────────────────────────

func (h *handlers) create(c *connector.Ctx) (any, error) {
	in := wfmcp.CreateInput{
		Name:     c.Input("name"),
		Template: c.Input("template"),
	}
	w, err := h.ops.Create(in)
	if err != nil {
		return nil, err
	}
	// Auto-publish + enable on create — user explicitly opted in by
	// calling create. Apply top-down canvas so the editor renders the
	// scaffold readably without manual dragging. Subsequent edits land
	// in draft and need an explicit workflow_publish to go live.
	w = topDownLayout(w)
	w.Enabled = true
	if err := h.ops.Service.Update(w.ID, w, nil); err != nil {
		return nil, fmt.Errorf("auto-publish: %w", err)
	}
	return map[string]any{
		"id":        w.ID,
		"name":      w.Name,
		"enabled":   true,
		"published": true,
	}, nil
}

func (h *handlers) writeFile(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	path := c.Input("path")
	content := []byte(c.Input("content"))

	// Edits to the main workflow YAML go to draft (workflow.draft.yaml)
	// so the live router keeps running the published version until the
	// user explicitly calls workflow_publish. Other files (nodes/*.md,
	// scripts, env.yaml, __tests__/) write through directly — they have
	// no draft/publish split.
	if path == "workflow.yaml" {
		// Parse the YAML so SaveDraft can validate + carry forward
		// ID/CreatedAt fields. Fail-fast on bad YAML rather than writing
		// a broken draft and surfacing it on next load.
		w, perr := parse.Parse(id, content)
		if perr != nil {
			return nil, fmt.Errorf("parse workflow.yaml: %w", perr)
		}
		w = topDownLayout(w)
		normalizeTriggerEntryNodes(&w)
		if err := h.ops.Service.SaveDraft(id, w); err != nil {
			return nil, err
		}
		return map[string]any{
			"ok":      true,
			"draft":   true,
			"message": "Saved to draft. Call workflow_publish to make it live.",
		}, nil
	}
	if err := h.ops.WriteFile(id, path, content); err != nil {
		return nil, err
	}
	return ok("file written"), nil
}

func (h *handlers) deleteFile(c *connector.Ctx) (any, error) {
	if err := h.ops.DeleteFile(c.Input("id"), c.Input("path")); err != nil {
		return nil, err
	}
	return ok("file deleted"), nil
}

func (h *handlers) deleteWorkflow(c *connector.Ctx) (any, error) {
	if err := h.ops.Delete(c.Input("id")); err != nil {
		return nil, err
	}
	return ok("workflow deleted"), nil
}

func (h *handlers) addNode(c *connector.Ctx) (any, error) {
	var node wf.Node
	if err := parseJSON(c.Input("node"), &node); err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	return h.ops.AddNode(c.Input("id"), node)
}

func (h *handlers) updateNode(c *connector.Ctx) (any, error) {
	var patch map[string]any
	if err := parseJSON(c.Input("patch"), &patch); err != nil {
		return nil, fmt.Errorf("patch: %w", err)
	}
	return h.ops.UpdateNode(c.Input("id"), c.Input("node_id"), patch)
}

func (h *handlers) deleteNode(c *connector.Ctx) (any, error) {
	return h.ops.DeleteNode(c.Input("id"), c.Input("node_id"))
}

func (h *handlers) connect(c *connector.Ctx) (any, error) {
	return h.ops.Connect(c.Input("id"), c.Input("from_id"), c.Input("to_id"), c.Input("case_label"))
}

func (h *handlers) disconnect(c *connector.Ctx) (any, error) {
	return h.ops.Disconnect(c.Input("id"), c.Input("from_id"), c.Input("to_id"))
}

func (h *handlers) moveNode(c *connector.Ctx) (any, error) {
	return h.ops.MoveNode(c.Input("id"), c.Input("node_id"), c.InputInt("x"), c.InputInt("y"))
}

func (h *handlers) setTriggers(c *connector.Ctx) (any, error) {
	var triggers []wf.Trigger
	if err := parseJSON(c.Input("triggers"), &triggers); err != nil {
		return nil, fmt.Errorf("triggers: %w", err)
	}
	return h.ops.SetTriggers(c.Input("id"), triggers)
}

func (h *handlers) toggle(c *connector.Ctx) (any, error) {
	enabled := c.Input("enabled") == "true"
	return h.ops.Toggle(c.Input("id"), enabled)
}

func (h *handlers) publish(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	enable := c.Input("enable") != "false" // default true
	w, err := h.ops.Service.Publish(id)
	if err != nil {
		return nil, err
	}
	if enable && !w.Enabled {
		if _, err := h.ops.Toggle(id, true); err != nil {
			return nil, fmt.Errorf("publish ok, enable failed: %w", err)
		}
		w.Enabled = true
	}
	return map[string]any{
		"ok":      true,
		"id":      w.ID,
		"enabled": w.Enabled,
		"message": "Draft promoted to live workflow.yaml.",
	}, nil
}

func (h *handlers) discardDraft(c *connector.Ctx) (any, error) {
	if err := h.ops.Service.DiscardDraft(c.Input("id")); err != nil {
		return nil, err
	}
	return ok("draft discarded"), nil
}

func (h *handlers) hasDraft(c *connector.Ctx) (any, error) {
	return map[string]any{"has_draft": h.ops.Service.HasDraft(c.Input("id"))}, nil
}

// ── Tier 3: action ─────────────────────────────────────────────────────

func (h *handlers) validate(c *connector.Ctx) (any, error) {
	return h.ops.Validate(c.Input("id")), nil
}

func (h *handlers) simulate(c *connector.Ctx) (any, error) {
	var evt wf.Event
	if err := parseJSON(c.Input("event"), &evt); err != nil {
		return nil, fmt.Errorf("event: %w", err)
	}
	return h.ops.Simulate(ctxFrom(c), c.Input("id"), evt)
}

func (h *handlers) runTests(c *connector.Ctx) (any, error) {
	if h.runner == nil {
		return nil, fmt.Errorf("test runner not configured — wire wftest.Runner via workflow.ModuleWithRunner")
	}
	id := c.Input("id")
	filter := c.Input("filter")

	results, cov, err := h.runner.RunAllWithCoverage(ctxFrom(c), id)
	if err != nil {
		return nil, err
	}

	type caseRow struct {
		Name       string   `json:"name"`
		Pass       bool     `json:"pass"`
		Failures   []string `json:"failures,omitempty"`
		DurationMs int64    `json:"duration_ms"`
	}
	rows := make([]caseRow, 0, len(results))
	for _, r := range results {
		if filter != "" && !matchesFilter(r.Name, filter) {
			continue
		}
		rows = append(rows, caseRow{
			Name:       r.Name,
			Pass:       r.Pass,
			Failures:   r.Failures,
			DurationMs: r.Duration.Milliseconds(),
		})
	}

	passCount := 0
	for _, r := range rows {
		if r.Pass {
			passCount++
		}
	}

	return map[string]any{
		"cases": rows,
		"total": len(rows),
		"pass":  passCount,
		"fail":  len(rows) - passCount,
		"coverage": map[string]any{
			"total_nodes":    cov.TotalNodes,
			"hit_nodes":      cov.HitCount(),
			"percent":        cov.Percent(),
			"untested_nodes": cov.Untested,
		},
	}, nil
}

func (h *handlers) testCoverage(c *connector.Ctx) (any, error) {
	if h.runner == nil {
		return nil, fmt.Errorf("test runner not configured")
	}
	_, cov, err := h.runner.RunAllWithCoverage(ctxFrom(c), c.Input("id"))
	if err != nil {
		return nil, err
	}
	hitList := make([]string, 0, len(cov.HitNodes))
	for id := range cov.HitNodes {
		hitList = append(hitList, id)
	}
	return map[string]any{
		"total_nodes":    cov.TotalNodes,
		"hit_nodes":      hitList,
		"untested_nodes": cov.Untested,
		"percent":        cov.Percent(),
	}, nil
}

func (h *handlers) recordTest(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	runID := c.Input("run_id")

	state, err := h.ops.StateStore.Load(id, runID)
	if err != nil {
		return nil, fmt.Errorf("load run %s: %w", runID, err)
	}

	// Shape matches wftest.Case so wftest.Runner.LoadCases can parse it.
	// Assertions cover path + final status; expected_output snapshots the
	// final per-node outputs as a regression check.
	assertions := []map[string]any{
		{"subject": "status", "operator": "==", "value": state.Status},
	}
	if len(state.Completed) > 0 {
		assertions = append(assertions, map[string]any{
			"subject": "path", "operator": "path_taken", "value": state.Completed,
		})
	}
	fixture := map[string]any{
		"name":            "auto-" + runID[:min(8, len(runID))],
		"input":           map[string]any{"Event": state.Event},
		"expected_output": state.Outputs,
		"assertions":      assertions,
	}
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return nil, err
	}
	path := "__tests__/auto-" + runID[:min(8, len(runID))] + ".json"
	if err := h.ops.WriteFile(id, path, data); err != nil {
		return nil, fmt.Errorf("write fixture: %w", err)
	}
	return map[string]any{"path": path, "fixture": string(data)}, nil
}

func (h *handlers) captureFixture(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	runID := c.Input("run_id")
	nodeID := c.Input("node_id")

	state, err := h.ops.StateStore.Load(id, runID)
	if err != nil {
		return nil, fmt.Errorf("load run: %w", err)
	}
	output, exists := state.Outputs[nodeID]
	if !exists {
		return nil, fmt.Errorf("node %q not found in run outputs (available: %v)", nodeID, outputKeys(state.Outputs))
	}
	// Single wftest.Case asserting the named node's output equals the
	// captured snapshot. Whole workflow runs but assertion targets one node.
	fixture := map[string]any{
		"name":  "node-" + nodeID + "-" + runID[:min(8, len(runID))],
		"input": map[string]any{"Event": state.Event},
		"expected_output": map[string]any{
			nodeID: output,
		},
	}
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return nil, err
	}
	path := "__tests__/node-" + nodeID + ".json"
	if err := h.ops.WriteFile(id, path, data); err != nil {
		return nil, fmt.Errorf("write fixture: %w", err)
	}
	return map[string]any{"path": path, "fixture": string(data)}, nil
}

func (h *handlers) runNow(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	var evt wf.Event
	rawEvt := strings.TrimSpace(c.Input("event"))
	if rawEvt != "" {
		if err := parseJSON(rawEvt, &evt); err != nil {
			return nil, fmt.Errorf("event: %w", err)
		}
	} else {
		evt = wf.Event{Type: string(wf.TriggerManual)}
	}
	if err := h.ops.RunNow(ctxFrom(c), id, evt); err != nil {
		return nil, err
	}
	return ok("run enqueued"), nil
}

func (h *handlers) getRuns(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	limit := c.InputInt("limit")
	if limit <= 0 {
		limit = 20
	}
	summaries, _, err := h.ops.GetRunSummaries(id, 0, limit)
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

func (h *handlers) getRun(c *connector.Ctx) (any, error) {
	state, err := h.ops.StateStore.Load(c.Input("id"), c.Input("run_id"))
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (h *handlers) getRunEvents(c *connector.Ctx) (any, error) {
	events, err := h.ops.StateStore.ListEvents(c.Input("id"), c.Input("run_id"))
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (h *handlers) copyRunToEditor(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	runID := c.Input("run_id")

	state, err := h.ops.StateStore.Load(id, runID)
	if err != nil {
		return nil, fmt.Errorf("run not found: %w", err)
	}
	w, err := h.ops.Service.Load(id)
	if err != nil {
		return nil, fmt.Errorf("load workflow: %w", err)
	}
	hadDraft := h.ops.Service.HasDraft(id)
	if err := h.ops.Service.SaveDraft(id, w); err != nil {
		return nil, fmt.Errorf("save draft: %w", err)
	}
	if len(state.Outputs) > 0 {
		mockData, _ := json.MarshalIndent(state.Outputs, "", "  ")
		_ = h.ops.Service.WriteFile(id, "runs/"+runID+"/mocks.json", mockData)
	}
	return map[string]any{
		"ok":        true,
		"id":        id,
		"run_id":    runID,
		"had_draft": hadDraft,
		"message":   "Workflow loaded as draft. Mocks written to runs/" + runID + "/mocks.json. Ask user before publishing.",
	}, nil
}

func (h *handlers) replayRun(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	runID := c.Input("run_id")
	state, err := h.ops.StateStore.Load(id, runID)
	if err != nil {
		return nil, fmt.Errorf("load run: %w", err)
	}
	if err := h.ops.RunNow(ctxFrom(c), id, state.Event); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":       true,
		"replayed": runID,
		"message":  "Replay enqueued with the original trigger event. Use workflow_get_runs to find the new run ID.",
	}, nil
}

func (h *handlers) listTestCases(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	files, err := h.ops.ListFiles(id)
	if err != nil {
		return nil, err
	}
	type row struct {
		Name           string `json:"name"`
		Path           string `json:"path"`
		AssertionCount int    `json:"assertion_count"`
	}
	rows := []row{}
	for _, f := range files {
		if !strings.HasPrefix(f, "__tests__/") || !strings.HasSuffix(f, ".json") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(f, "__tests__/"), ".json")
		data, err := h.ops.ReadFile(id, f)
		if err != nil {
			rows = append(rows, row{Name: name, Path: f})
			continue
		}
		var tc wftest.Case
		_ = json.Unmarshal(data, &tc)
		rows = append(rows, row{Name: name, Path: f, AssertionCount: len(tc.Assertions)})
	}
	return rows, nil
}

func (h *handlers) saveTestCase(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return nil, fmt.Errorf("name must be id-safe (a-z, 0-9, dash, underscore)")
		}
	}
	var in wftest.Input
	if err := parseJSON(c.Input("input"), &in); err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	var assertions []wftest.Assertion
	if raw := strings.TrimSpace(c.Input("assertions")); raw != "" {
		if err := parseJSON(raw, &assertions); err != nil {
			return nil, fmt.Errorf("assertions: %w", err)
		}
	}
	tc := wftest.Case{Name: name, Input: in, Assertions: assertions}
	data, err := json.MarshalIndent(tc, "", "  ")
	if err != nil {
		return nil, err
	}
	path := "__tests__/" + name + ".json"
	if err := h.ops.WriteFile(id, path, data); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "name": name, "path": path}, nil
}

func (h *handlers) deleteTestCase(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	name := c.Input("name")
	path := "__tests__/" + name + ".json"
	if err := h.ops.DeleteFile(id, path); err != nil {
		return nil, err
	}
	return ok("test case deleted"), nil
}

func (h *handlers) getRunLog(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	runID := c.Input("run_id")
	state, err := h.ops.StateStore.Load(id, runID)
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	events, _ := h.ops.StateStore.ListEvents(id, runID)

	// Compute per-node durations from start/complete pairs in events.jsonl.
	type nodeTiming struct {
		Node       string `json:"node"`
		Status     string `json:"status"`
		DurationMs int64  `json:"duration_ms"`
		Error      string `json:"error,omitempty"`
	}
	starts := map[string]time.Time{}
	timings := []nodeTiming{}
	for _, ev := range events {
		switch ev.Event {
		case wf.EventNodeStarted:
			starts[ev.Node] = ev.TS
		case wf.EventNodeCompleted, wf.EventNodeFailed:
			start, ok := starts[ev.Node]
			var dur int64
			if ok {
				dur = ev.TS.Sub(start).Milliseconds()
			}
			status := "success"
			errMsg := ""
			if ev.Event == wf.EventNodeFailed {
				status = "failed"
				if ev.Data != nil {
					if m, ok2 := ev.Data["error"].(string); ok2 {
						errMsg = m
					}
				}
			}
			timings = append(timings, nodeTiming{
				Node: ev.Node, Status: status, DurationMs: dur, Error: errMsg,
			})
		}
	}

	totalDur := int64(0)
	if state.EndedAt != nil {
		totalDur = state.EndedAt.Sub(state.StartedAt).Milliseconds()
	}

	return map[string]any{
		"run_id":        runID,
		"workflow_id":   id,
		"status":        state.Status,
		"started_at":    state.StartedAt,
		"ended_at":      state.EndedAt,
		"duration_ms":   totalDur,
		"entry":         state.Entry,
		"completed":     state.Completed,
		"failed":        state.Failed,
		"skipped":       state.Skipped,
		"error":         state.Error,
		"node_timings":  timings,
		"events_count":  len(events),
		"trigger_event": state.Event,
	}, nil
}

func (h *handlers) requestReview(c *connector.Ctx) (any, error) {
	id := c.Input("id")
	// Disable the workflow so it needs admin approval before going live.
	if _, err := h.ops.Toggle(id, false); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"message": "Workflow disabled and submitted for review. Admin must enable it after inspection.",
		"url":     "/tools/agents/workflows/edit/" + id,
	}, nil
}

// ── helpers ────────────────────────────────────────────────────────────

// runner is injected separately from ops so tests can wire it independently.
// Set via ModuleWithRunner.
func (h *handlers) withRunner(r *wftest.Runner) {
	h.runner = r
}

func matchesFilter(name, filter string) bool {
	filter = strings.ToLower(filter)
	name = strings.ToLower(name)
	if strings.HasPrefix(filter, "node:") {
		return strings.Contains(name, strings.TrimPrefix(filter, "node:"))
	}
	if strings.HasPrefix(filter, "integration:") {
		return strings.Contains(name, "integration")
	}
	return strings.Contains(name, filter)
}

func buildNodeMocks(outputs map[string]any) []map[string]any {
	mocks := make([]map[string]any, 0, len(outputs))
	for nodeID, out := range outputs {
		mocks = append(mocks, map[string]any{
			"node":     nodeID,
			"response": out,
		})
	}
	return mocks
}

func outputKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/workflow"
	wfengine "github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/guard"
	"github.com/yogasw/wick/internal/entity"
)

// SetLock flips workflow.Canvas["locked"]. Dedicated path so toggling
// works even while the workflow IS locked (the regular SaveDraft would
// be blocked by Service.SaveDraft's lock guard); also skips validation
// — locking shouldn't fail because the draft has a half-built node.
func (m *Ops) SetLock(id string, locked bool) error {
	if m.Service == nil {
		return errors.New("mcp: service not wired")
	}
	w, err := m.Service.LoadDraft(id)
	if err != nil {
		return err
	}
	if w.Canvas == nil {
		w.Canvas = map[string]any{}
	}
	if locked {
		w.Canvas["locked"] = true
	} else {
		delete(w.Canvas, "locked")
	}
	return m.Service.SaveDraft(id, w)
}

// GuardReport runs guard.Review against the draft and returns the
// report. Distinct from workflow_validate — guard inspects safety
// policy (destructive shell, secret leak, unparameterized SQL, network
// allowlist) while validate inspects graph structure (cycles, schema).
func (m *Ops) GuardReport(ctx context.Context, id string) (guard.Report, error) {
	if m.Service == nil {
		return guard.Report{}, errors.New("mcp: service not wired")
	}
	if m.Guard == nil {
		return guard.Report{}, errors.New("mcp: guard not wired")
	}
	w, err := m.Service.LoadDraft(id)
	if err != nil {
		return guard.Report{}, err
	}
	return m.Guard.Review(ctx, w), nil
}

// VersionSummary is the narrow projection of one workflow_versions row
// callers actually need. Body is held back from the list view —
// fetching every snapshot's full JSON would balloon the response.
type VersionSummary struct {
	ID         uint      `json:"id"`
	WorkflowID string    `json:"workflow_id"`
	Kind       string    `json:"kind"`
	Message    string    `json:"message,omitempty"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Versions returns the history rows for a workflow ordered newest
// first. Powers the SPA history panel and the workflow_versions MCP
// op. Empty list when no DB is wired.
func (m *Ops) Versions(id string) ([]VersionSummary, error) {
	if m.Repo == nil {
		return nil, errors.New("mcp: version history requires DB (Repo not wired)")
	}
	rows, err := m.Repo.Versions(id)
	if err != nil {
		return nil, err
	}
	out := make([]VersionSummary, len(rows))
	for i, r := range rows {
		out[i] = VersionSummary{
			ID:         r.ID,
			WorkflowID: r.WorkflowID,
			Kind:       r.Kind,
			Message:    r.Message,
			CreatedBy:  r.CreatedBy,
			CreatedAt:  r.CreatedAt,
		}
	}
	return out, nil
}

// VersionDetail returns one snapshot including its full body JSON.
// Used by the FE to populate the diff viewer when the user compares
// two versions side-by-side.
func (m *Ops) VersionDetail(versionID uint) (entity.WorkflowVersion, error) {
	if m.Repo == nil {
		return entity.WorkflowVersion{}, errors.New("mcp: version history requires DB (Repo not wired)")
	}
	row, err := m.Repo.Version(versionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return entity.WorkflowVersion{}, fmt.Errorf("version %d not found", versionID)
		}
		return entity.WorkflowVersion{}, err
	}
	return row, nil
}

// RestoreVersion writes a historic snapshot back to the draft slot. No
// auto-publish — the user must hit Publish to make the restore live.
// Returns the new draft snapshot id.
func (m *Ops) RestoreVersion(id string, versionID uint, createdBy string) (uint, error) {
	if m.Repo == nil {
		return 0, errors.New("mcp: version history requires DB (Repo not wired)")
	}
	return m.Repo.Restore(id, versionID, createdBy)
}

// ExecNodeInput is the request shape for ExecNode.
type ExecNodeInput struct {
	Node        workflow.Node             `json:"node"`
	Input       map[string]any            `json:"input"`
	Event       map[string]any            `json:"event"`
	ParentID    string                    `json:"parent_id"`
	NodeOutputs map[string]map[string]any `json:"node_outputs"`
}

// ExecNode runs one node in isolation — n8n's "Execute step" pattern.
// Returns the node output + latency. Nothing persists to runs/. The
// caller passes a prefill of upstream outputs via NodeOutputs so
// {{.Node.<upstream>}} template refs resolve.
func (m *Ops) ExecNode(ctx context.Context, id string, body ExecNodeInput) (map[string]any, error) {
	if m.Engine == nil {
		return nil, errors.New("mcp: engine not wired")
	}
	if body.Node.Type == "" {
		return nil, errors.New("node.type is required")
	}
	exec, ok := m.Engine.Executors[body.Node.Type]
	if !ok {
		return nil, fmt.Errorf("no executor for node type %s", body.Node.Type)
	}

	// Best-effort load of the workflow shell so env values + run ctx
	// fields resolve. Soft-fail when the workflow doesn't exist yet —
	// callers may invoke ExecNode against a synthetic node before the
	// first save.
	w, lerr := m.Service.LoadDraft(id)
	if lerr != nil {
		w = workflow.Workflow{ID: id}
	}
	envVals, _ := m.Service.LoadEnvValues(id)

	outputs := map[string]any{}
	nodeOutputs := map[string]workflow.NodeOutput{}
	for nodeID, out := range body.NodeOutputs {
		if out == nil {
			continue
		}
		no := workflow.NodeOutput{Fields: out}
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

	rc := &workflow.RunContext{
		Workflow:    w,
		Event:       eventFromExec(body.Event, body.Input),
		Outputs:     outputs,
		EnvValues:   envVals,
		RunID:       "step-" + time.Now().UTC().Format("20060102T150405.000000000"),
		NodeOutputs: nodeOutputs,
	}

	node, prerr := wfengine.PreRenderNode(body.Node, rc.RenderCtx())
	if prerr != nil {
		return nil, fmt.Errorf("pre-render: %w", prerr)
	}
	started := time.Now()
	out, runErr := exec.Execute(ctx, node, rc)
	resp := map[string]any{
		"ok":         runErr == nil,
		"latency_ms": time.Since(started).Milliseconds(),
		"output":     nodeOutputToMap(out),
	}
	if runErr != nil {
		resp["error"] = runErr.Error()
	}
	return resp, nil
}

// eventFromExec builds RunContext.Event for an Execute step. When the
// caller hands a full event blob (Replay → Execute pattern), unpack
// it; otherwise synthesise a manual event whose Payload mirrors the
// supplied input map.
func eventFromExec(evt map[string]any, input map[string]any) workflow.Event {
	if len(evt) == 0 {
		return workflow.Event{Type: string(workflow.TriggerManual), At: time.Now().UTC(), Payload: input}
	}
	out := workflow.Event{}
	if v, ok := evt["type"].(string); ok {
		out.Type = v
	}
	if out.Type == "" {
		out.Type = string(workflow.TriggerManual)
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

// nodeOutputToMap flattens a NodeOutput into the same shape the engine
// writes to RunContext.Outputs, so the FE renders execute-step output
// identically to full-run output.
func nodeOutputToMap(o workflow.NodeOutput) map[string]any {
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

// DiffVersions returns the body of two snapshots so the caller can
// render a diff. Body is shipped as JSON strings; the FE picks its own
// diff library to render.
type VersionDiff struct {
	From entity.WorkflowVersion `json:"from"`
	To   entity.WorkflowVersion `json:"to"`
}

// DiffVersions resolves both version ids on the same workflow and
// returns their full bodies for client-side diff rendering.
func (m *Ops) DiffVersions(id string, fromID, toID uint) (VersionDiff, error) {
	if m.Repo == nil {
		return VersionDiff{}, errors.New("mcp: version history requires DB (Repo not wired)")
	}
	from, err := m.Repo.Version(fromID)
	if err != nil {
		return VersionDiff{}, fmt.Errorf("from: %w", err)
	}
	if from.WorkflowID != id {
		return VersionDiff{}, fmt.Errorf("version %d does not belong to workflow %s", fromID, id)
	}
	to, err := m.Repo.Version(toID)
	if err != nil {
		return VersionDiff{}, fmt.Errorf("to: %w", err)
	}
	if to.WorkflowID != id {
		return VersionDiff{}, fmt.Errorf("version %d does not belong to workflow %s", toID, id)
	}
	return VersionDiff{From: from, To: to}, nil
}

// CanvasViewRow is one entry in the canvas table (node or trigger).
type CanvasViewRow struct {
	ID      string   `json:"id"`
	Label   string   `json:"label,omitempty"`
	Type    string   `json:"type"`
	X       int      `json:"x"`
	Y       int      `json:"y"`
	EdgesTo []string `json:"edges_to,omitempty"`
}

// CanvasStats summarises the canvas composition.
type CanvasStats struct {
	NodeCount    int `json:"node_count"`
	TriggerCount int `json:"trigger_count"`
	EdgeCount    int `json:"edge_count"`
	Unpositioned int `json:"unpositioned"`
}

// CanvasViewResult is the response for workflow_canvas_view.
type CanvasViewResult struct {
	Nodes    []CanvasViewRow `json:"nodes"`
	Triggers []CanvasViewRow `json:"triggers"`
	ASCII    string          `json:"ascii"`
	Stats    CanvasStats     `json:"stats"`
}

// CanvasView returns a human-readable table + ASCII sketch of the
// workflow canvas. Pure read — no side effects.
func (m *Ops) CanvasView(id string) (CanvasViewResult, error) {
	if m.Service == nil {
		return CanvasViewResult{}, errors.New("mcp: service not wired")
	}
	w, err := m.Service.LoadDraft(id)
	if err != nil {
		return CanvasViewResult{}, err
	}
	return buildCanvasView(w), nil
}

func buildCanvasView(w workflow.Workflow) CanvasViewResult {
	var positions map[string]any
	if w.Canvas != nil {
		positions, _ = w.Canvas["positions"].(map[string]any)
	}

	getPos := func(id string) (x, y int) {
		if positions == nil {
			return
		}
		p, ok := positions[id].(map[string]any)
		if !ok {
			return
		}
		switch v := p["x"].(type) {
		case float64:
			x = int(v)
		case int:
			x = v
		}
		switch v := p["y"].(type) {
		case float64:
			y = int(v)
		case int:
			y = v
		}
		return
	}

	// Build outgoing edge map.
	edgesFrom := make(map[string][]string)
	for _, e := range w.Graph.Edges {
		label := e.To
		if e.Case != "" {
			label = e.Case + ":" + e.To
		}
		edgesFrom[e.From] = append(edgesFrom[e.From], label)
	}
	for id := range edgesFrom {
		sortStrings(edgesFrom[id])
	}

	unpositioned := 0
	nodes := make([]CanvasViewRow, 0, len(w.Graph.Nodes))
	for _, n := range w.Graph.Nodes {
		x, y := getPos(n.ID)
		if x == 0 && y == 0 {
			unpositioned++
		}
		row := CanvasViewRow{
			ID:      n.ID,
			Label:   n.Label,
			Type:    string(n.Type),
			X:       x,
			Y:       y,
			EdgesTo: edgesFrom[n.ID],
		}
		nodes = append(nodes, row)
	}
	// Sort by Y then X so the table reads top-left → bottom-right.
	sortCanvasRows(nodes)

	triggers := make([]CanvasViewRow, 0, len(w.Triggers))
	for _, t := range w.Triggers {
		x, y := getPos(t.ID)
		triggers = append(triggers, CanvasViewRow{
			ID:      t.ID,
			Label:   t.Label,
			Type:    string(t.Type),
			X:       x,
			Y:       y,
			EdgesTo: []string{t.EntryNode},
		})
	}
	sortByX(triggers)

	return CanvasViewResult{
		Nodes:    nodes,
		Triggers: triggers,
		ASCII:    buildCanvasASCII(w.Name, nodes, triggers, w.Graph.Edges),
		Stats: CanvasStats{
			NodeCount:    len(nodes),
			TriggerCount: len(triggers),
			EdgeCount:    len(w.Graph.Edges),
			Unpositioned: unpositioned,
		},
	}
}

// buildCanvasASCII renders a text table + edge list for human / AI
// consumption. No pixel-perfect art — just readable column alignment.
func buildCanvasASCII(name string, nodes, triggers []CanvasViewRow, edges []workflow.Edge) string {
	var b strings.Builder
	sep := strings.Repeat("─", 72)

	fmt.Fprintf(&b, "CANVAS: %q\n%s\n", name, sep)

	if len(triggers) > 0 {
		fmt.Fprintf(&b, "\nTRIGGERS\n%-22s %-12s %-28s %6s %5s\n", "ID", "TYPE", "ENTRY→", "X", "Y")
		fmt.Fprintln(&b, strings.Repeat("-", 72))
		for _, t := range triggers {
			entry := ""
			if len(t.EdgesTo) > 0 {
				entry = t.EdgesTo[0]
			}
			shortEntry := shortID(entry)
			fmt.Fprintf(&b, "%-22s %-12s %-28s %6d %5d\n",
				truncate(t.ID, 22), truncate(string(t.Type), 12), shortEntry, t.X, t.Y)
		}
	}

	if len(nodes) > 0 {
		fmt.Fprintf(&b, "\nNODES\n%-10s %-14s %-12s %6s %5s  %s\n", "ID (short)", "LABEL", "TYPE", "X", "Y", "→ EDGES")
		fmt.Fprintln(&b, strings.Repeat("-", 72))
		for _, n := range nodes {
			edgeStr := ""
			if len(n.EdgesTo) > 0 {
				parts := make([]string, len(n.EdgesTo))
				for i, e := range n.EdgesTo {
					parts[i] = shortID(e)
				}
				edgeStr = "→ " + strings.Join(parts, ", ")
			}
			fmt.Fprintf(&b, "%-10s %-14s %-12s %6d %5d  %s\n",
				shortID(n.ID), truncate(n.Label, 14), truncate(n.Type, 12), n.X, n.Y, edgeStr)
		}
	}

	if len(edges) > 0 {
		fmt.Fprintf(&b, "\nEDGE LIST\n")
		fmt.Fprintln(&b, strings.Repeat("-", 40))
		for _, e := range edges {
			line := fmt.Sprintf("  %s  →  %s", shortID(e.From), shortID(e.To))
			if e.Case != "" {
				line += "  [case:" + e.Case + "]"
			}
			fmt.Fprintln(&b, line)
		}
	}

	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sortCanvasRows(rows []CanvasViewRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			if a.Y > b.Y || (a.Y == b.Y && a.X > b.X) {
				rows[j-1], rows[j] = rows[j], rows[j-1]
			} else {
				break
			}
		}
	}
}

func sortByX(rows []CanvasViewRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].X < rows[j-1].X; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// shortID returns the first 8 chars of a UUID or the full string if shorter.
func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// ensureJSON keeps the legacy callers happy when they decode partial
// MCP responses with json.Unmarshal directly.
var _ = json.Unmarshal

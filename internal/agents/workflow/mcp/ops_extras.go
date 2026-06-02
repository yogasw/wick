package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/workflow"
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

	started := time.Now()
	out, runErr := exec.Execute(ctx, body.Node, rc)
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

// ensureJSON keeps the legacy callers happy when they decode partial
// MCP responses with json.Unmarshal directly.
var _ = json.Unmarshal

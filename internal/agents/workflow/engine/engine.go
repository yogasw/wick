// Package engine walks a workflow graph and executes nodes via the
// registered Executors. One Engine instance can run many workflows
// concurrently — per-workflow FIFO queuing lives in package trigger.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

// Engine walks a workflow graph and dispatches each node to its
// Executor. Caller wires concrete executors via Register; the engine
// stays decoupled from individual node impls.
//
// OnEvent is an optional broadcast hook fired after every event hits
// the StateStore. The UI subscribes via SSE to paint per-node
// progress without polling state.json. Set via SetEventHook so
// existing callers stay source-compatible.
type Engine struct {
	Layout     config.Layout
	Service    service.Service
	StateStore state.Store
	Executors  map[workflow.NodeType]workflow.Executor
	Now        func() time.Time
	IDGen      func() string
	OnEvent    func(slug, runID string, ev workflow.RunEvent)
}

// New builds a bare engine — no executors registered. Caller must
// Register at least the node types the workflow uses.
func New(layout config.Layout, svc service.Service, ss state.Store) *Engine {
	return &Engine{
		Layout:     layout,
		Service:    svc,
		StateStore: ss,
		Executors:  map[workflow.NodeType]workflow.Executor{},
		Now:        func() time.Time { return time.Now().UTC() },
		IDGen:      uuid.NewString,
	}
}

// Register attaches an executor for a node type.
func (e *Engine) Register(t workflow.NodeType, ex workflow.Executor) {
	e.Executors[t] = ex
}

// SetEventHook installs the broadcast callback. Fires after each
// StateStore.AppendEvent so callers see persistent + ephemeral
// payloads in lockstep.
func (e *Engine) SetEventHook(fn func(slug, runID string, ev workflow.RunEvent)) {
	e.OnEvent = fn
}

// emit persists the event then fires the broadcast hook. Centralised
// so every event call site picks up SSE for free.
func (e *Engine) emit(slug, runID string, ev workflow.RunEvent) {
	_ = e.StateStore.AppendEvent(slug, runID, ev)
	if e.OnEvent != nil {
		e.OnEvent(slug, runID, ev)
	}
}

// Run starts a fresh run. Blocking; returns the final state.
//
// Caller may pre-assign a run ID via evt.Payload["run_id"] so the
// HTTP handler can return the ID to the browser before Run starts
// — letting the client SSE-subscribe in time to catch the first
// events. Falls back to IDGen when the payload doesn't supply one.
func (e *Engine) Run(ctx context.Context, w workflow.Workflow, evt workflow.Event) (workflow.RunState, error) {
	runID, _ := evt.Payload["run_id"].(string)
	if runID == "" {
		runID = e.IDGen()
	}
	entry := pickEntry(w, evt)
	st := workflow.RunState{
		RunID:      runID,
		WorkflowID: w.ID,
		Slug:       w.Slug,
		Version:    w.Version,
		Status:     workflow.StatusRunning,
		Entry:      entry,
		Current:    []string{entry},
		Outputs:    map[string]any{},
		Event:      evt,
		StartedAt:  e.Now(),
		UpdatedAt:  e.Now(),
	}
	if st.Entry == "" {
		return st, errors.New("no entry node (graph.entry + no trigger entry_node)")
	}
	envVals, _ := e.Service.LoadEnvValues(w.Slug)
	rc := &workflow.RunContext{
		Workflow:    w,
		Event:       evt,
		Outputs:     st.Outputs,
		EnvValues:   envVals,
		Secrets:     extractSecrets(w.Env, envVals),
		RunID:       runID,
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	startEv := workflow.RunEvent{Event: workflow.EventWorkflowStarted, Data: map[string]any{"trigger": evt.Type}}
	if err := e.StateStore.AppendEvent(w.Slug, runID, startEv); err != nil {
		return st, err
	}
	if e.OnEvent != nil {
		e.OnEvent(w.Slug, runID, startEv)
	}
	if err := e.StateStore.Save(w.Slug, runID, st); err != nil {
		return st, err
	}

	maxDuration := time.Duration(w.MaxDurationSec) * time.Second
	if maxDuration == 0 {
		maxDuration = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()

	err := e.walk(cctx, w, st.Entry, rc, &st)
	if err != nil {
		st.Status = workflow.StatusFailed
		if st.Error == nil {
			st.Error = &workflow.NodeError{Message: err.Error()}
		}
		e.emit(w.Slug, runID, workflow.RunEvent{Event: workflow.EventWorkflowFailed, Data: map[string]any{"error": err.Error()}})
	} else {
		st.Status = workflow.StatusSuccess
		e.emit(w.Slug, runID, workflow.RunEvent{Event: workflow.EventWorkflowCompleted})
	}
	end := e.Now()
	st.EndedAt = &end
	st.UpdatedAt = end
	st.Current = nil
	if err := e.StateStore.Save(w.Slug, runID, st); err != nil {
		return st, err
	}
	return st, err
}

func (e *Engine) walk(ctx context.Context, w workflow.Workflow, start string, rc *workflow.RunContext, st *workflow.RunState) error {
	nodesByID := indexNodes(w.Graph)
	queue := []string{start}
	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		head := queue[0]
		queue = queue[1:]

		if containsStr(st.Completed, head) {
			continue
		}

		n, ok := nodesByID[head]
		if !ok {
			return fmt.Errorf("walker: unknown node %q", head)
		}

		if n.Type == workflow.NodeMerge {
			if !mergeReady(n, st) {
				queue = append(queue, head)
				continue
			}
			started := e.Now()
			out, err := runMerge(n, rc)
			if err != nil {
				return e.failNode(w.Slug, st, n, err)
			}
			e.recordSuccess(w.Slug, st, rc, n, out, e.Now().Sub(started).Milliseconds())
			queue = append(queue, e.nextNodes(w, n, out)...)
			continue
		}

		if n.Type == workflow.NodeParallel {
			started := e.Now()
			out, err := e.runParallel(ctx, w, n, rc)
			if err != nil {
				return e.failNode(w.Slug, st, n, err)
			}
			e.recordSuccess(w.Slug, st, rc, n, out, e.Now().Sub(started).Milliseconds())
			queue = append(queue, e.nextNodes(w, n, out)...)
			continue
		}

		started := e.Now()
		out, err := e.runOne(ctx, n, rc)
		if err != nil {
			handled := e.applyOnFailure(ctx, w, st, n, err, rc)
			if handled == nil {
				continue
			}
			return handled
		}
		e.recordSuccess(w.Slug, st, rc, n, out, e.Now().Sub(started).Milliseconds())
		queue = append(queue, e.nextNodes(w, n, out)...)
	}
	return nil
}

func (e *Engine) runOne(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	exec, ok := e.Executors[n.Type]
	if !ok {
		return workflow.NodeOutput{}, fmt.Errorf("no executor registered for type %q", n.Type)
	}
	attempts := 1
	backoff := time.Second
	if n.Retry != nil {
		if n.Retry.Max > 0 {
			attempts = n.Retry.Max + 1
		}
		if n.Retry.BackoffSec > 0 {
			backoff = time.Duration(n.Retry.BackoffSec) * time.Second
		}
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		e.emit(rc.Workflow.Slug, rc.RunID, workflow.RunEvent{Event: workflow.EventNodeStarted, Node: n.ID, Data: map[string]any{"type": string(n.Type)}})
		out, err := exec.Execute(ctx, n, rc)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return workflow.NodeOutput{}, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return workflow.NodeOutput{}, lastErr
}

func (e *Engine) recordSuccess(slug string, st *workflow.RunState, rc *workflow.RunContext, n workflow.Node, out workflow.NodeOutput, latencyMs int64) {
	rc.NodeOutputs[n.ID] = out
	rc.Outputs[n.ID] = nodeOutputAsMap(out)
	st.Completed = append(st.Completed, n.ID)
	st.Outputs = rc.Outputs
	st.UpdatedAt = e.Now()
	e.emit(slug, st.RunID, workflow.RunEvent{
		Event: workflow.EventNodeCompleted,
		Node:  n.ID,
		Data: map[string]any{
			"verdict":    out.Verdict,
			"latency_ms": latencyMs,
			"output":     truncateForEvent(nodeOutputAsMap(out)),
		},
	})
	_ = e.StateStore.Save(slug, st.RunID, *st)
}

func (e *Engine) failNode(slug string, st *workflow.RunState, n workflow.Node, err error) error {
	st.Failed = append(st.Failed, n.ID)
	st.Error = &workflow.NodeError{Node: n.ID, Type: string(n.Type), Message: err.Error()}
	st.UpdatedAt = e.Now()
	e.emit(slug, st.RunID, workflow.RunEvent{Event: workflow.EventNodeFailed, Node: n.ID, Data: map[string]any{"error": err.Error()}})
	_ = e.StateStore.Save(slug, st.RunID, *st)
	return &workflow.ExecError{Node: n.ID, Type: n.Type, Wrapped: err}
}

func (e *Engine) applyOnFailure(ctx context.Context, w workflow.Workflow, st *workflow.RunState, n workflow.Node, err error, rc *workflow.RunContext) error {
	policy := n.OnFailure
	if policy == "" {
		policy = workflow.FailHalt
	}
	switch policy {
	case workflow.FailHalt:
		return e.failNode(w.Slug, st, n, err)
	case workflow.FailSkip:
		st.Skipped = append(st.Skipped, n.ID)
		e.emit(w.Slug, st.RunID, workflow.RunEvent{Event: workflow.EventNodeSkipped, Node: n.ID, Data: map[string]any{"reason": err.Error()}})
		return nil
	case workflow.FailFallback:
		if n.Fallback == "" {
			return e.failNode(w.Slug, st, n, fmt.Errorf("on_failure=fallback but fallback is empty: %w", err))
		}
		st.Failed = append(st.Failed, n.ID)
		e.emit(w.Slug, st.RunID, workflow.RunEvent{Event: workflow.EventNodeFailed, Node: n.ID, Data: map[string]any{"error": err.Error(), "fallback": n.Fallback}})
		_ = e.StateStore.Save(w.Slug, st.RunID, *st)
		st.Current = append(st.Current, n.Fallback)
		return nil
	}
	return e.failNode(w.Slug, st, n, err)
}

func (e *Engine) nextNodes(w workflow.Workflow, n workflow.Node, out workflow.NodeOutput) []string {
	outgoing := []workflow.Edge{}
	for _, ed := range w.Graph.Edges {
		if ed.From == n.ID {
			outgoing = append(outgoing, ed)
		}
	}
	if !n.Type.IsBranchSource() {
		ids := []string{}
		for _, ed := range outgoing {
			ids = append(ids, ed.To)
		}
		return ids
	}
	verdict := out.Verdict
	matched := []string{}
	for _, ed := range outgoing {
		if ed.Case == verdict {
			matched = append(matched, ed.To)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	for _, ed := range outgoing {
		if ed.Case == "default" {
			matched = append(matched, ed.To)
		}
	}
	return matched
}

func (e *Engine) runParallel(ctx context.Context, w workflow.Workflow, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	nodesByID := indexNodes(w.Graph)
	var wg sync.WaitGroup
	type res struct {
		id  string
		out workflow.NodeOutput
		err error
	}
	results := make(chan res, len(n.Branches))
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, bID := range n.Branches {
		bID := bID
		bNode, ok := nodesByID[bID]
		if !ok {
			results <- res{id: bID, err: fmt.Errorf("parallel branch %q not in graph", bID)}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			subRC := *rc
			subRC.NodeOutputs = copyNodeOutputs(rc.NodeOutputs)
			out, err := e.runOne(cctx, bNode, &subRC)
			results <- res{id: bID, out: out, err: err}
		}()
	}
	wg.Wait()
	close(results)

	branchOut := map[string]any{}
	var firstErr error
	policy := n.OnFailure
	if policy == "" {
		policy = workflow.FailHalt
	}
	for r := range results {
		if r.err != nil {
			if policy == workflow.FailHalt {
				cancel()
				if firstErr == nil {
					firstErr = r.err
				}
				branchOut[r.id] = map[string]any{"error": r.err.Error()}
				continue
			}
			branchOut[r.id] = map[string]any{"error": r.err.Error()}
			continue
		}
		branchOut[r.id] = nodeOutputAsMap(r.out)
		rc.NodeOutputs[r.id] = r.out
		rc.Outputs[r.id] = nodeOutputAsMap(r.out)
	}
	if firstErr != nil && policy == workflow.FailHalt {
		return workflow.NodeOutput{}, firstErr
	}
	return workflow.NodeOutput{Result: branchOut, Fields: map[string]any{"branches": branchOut}}, nil
}

func runMerge(n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	strategy := n.Strategy
	if strategy == "" {
		strategy = workflow.MergeObject
	}
	gathered := []workflow.NodeOutput{}
	gatheredByID := map[string]workflow.NodeOutput{}
	for _, inID := range n.Inputs {
		o, ok := rc.NodeOutputs[inID]
		if !ok {
			return workflow.NodeOutput{}, fmt.Errorf("merge input %q has no output (not completed)", inID)
		}
		gathered = append(gathered, o)
		gatheredByID[inID] = o
	}
	switch strategy {
	case workflow.MergeObject:
		m := map[string]any{}
		for id, o := range gatheredByID {
			m[id] = nodeOutputAsMap(o)
		}
		return workflow.NodeOutput{Result: m, Fields: map[string]any{"merged": m}}, nil
	case workflow.MergeArray:
		arr := []any{}
		for _, inID := range n.Inputs {
			arr = append(arr, nodeOutputAsMap(gatheredByID[inID]))
		}
		return workflow.NodeOutput{Result: arr, Fields: map[string]any{"merged": arr}}, nil
	case workflow.MergeFirst:
		if len(gathered) == 0 {
			return workflow.NodeOutput{}, fmt.Errorf("merge: no inputs")
		}
		return gathered[0], nil
	case workflow.MergeLast:
		if len(gathered) == 0 {
			return workflow.NodeOutput{}, fmt.Errorf("merge: no inputs")
		}
		return gathered[len(gathered)-1], nil
	default:
		return workflow.NodeOutput{}, fmt.Errorf("merge: unknown strategy %q", strategy)
	}
}

func mergeReady(n workflow.Node, st *workflow.RunState) bool {
	for _, in := range n.Inputs {
		if !containsStr(st.Completed, in) {
			return false
		}
	}
	return true
}

func indexNodes(g workflow.Graph) map[string]workflow.Node {
	m := map[string]workflow.Node{}
	for _, n := range g.Nodes {
		m[n.ID] = n
	}
	return m
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func nodeOutputAsMap(o workflow.NodeOutput) map[string]any {
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

func copyNodeOutputs(in map[string]workflow.NodeOutput) map[string]workflow.NodeOutput {
	out := make(map[string]workflow.NodeOutput, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func pickEntry(w workflow.Workflow, evt workflow.Event) string {
	for _, tr := range w.Triggers {
		if tr.EntryNode != "" && string(tr.Type) == evt.Type {
			return tr.EntryNode
		}
	}
	return w.Graph.Entry
}

// extractSecrets returns the subset of envVals declared as `widget:
// secret` in the schema.
func extractSecrets(schema []workflow.EnvField, vals map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range schema {
		if !f.IsSecret() {
			continue
		}
		if v, ok := vals[f.Name]; ok {
			out[f.Name] = v
		}
	}
	return out
}

// truncateForEvent caps any payload bound for an SSE/event-store
// event so a single noisy node can't blow up subscriber buffers.
// JSON-encodes the value, slices to maxEventPayload bytes, and
// re-unmarshals when it fits or returns a string preview otherwise.
const maxEventPayload = 4096

func truncateForEvent(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	if len(b) <= maxEventPayload {
		return v
	}
	preview := string(b[:maxEventPayload])
	return map[string]any{
		"_truncated": true,
		"_size":      len(b),
		"preview":    preview,
	}
}

// SortedKeys is a small util for stable test output (kept here so we
// don't need a util pkg).
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

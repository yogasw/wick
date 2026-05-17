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
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

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

// NewRunID returns a fresh run id. Plain UUID — chronological
// ordering for the Runs panel comes from the sharded index file
// (`runs/index/<date>-<seq>.jsonl`), so the per-run dir name
// doesn't need to encode time anymore.
func NewRunID() string {
	return uuid.NewString()
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
		IDGen:      NewRunID,
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
// so every event call site picks up SSE for free. Also emits a
// structured zerolog line via the request-scoped logger
// (zerolog.Ctx(ctx)) so request_id from the HTTP middleware
// auto-correlates engine output back to the originating request —
// operators can grep `wf_run_id=<id>` or `request_id=<id>` and pull
// the same trace from either side. The request_id is read off the
// per-run context the engine builds in Run() (Engine.Run derives a
// ctx with request_id set from evt.Payload["request_id"] so the
// queue-worker goroutine — whose own ctx outlives the HTTP request
// — still logs with the right correlation id).
//
// The whole RunEvent.Data (input on _started, output on _completed,
// error on _failed) is included after truncation so the grep target
// has the payload, not just an opaque event name.
func (e *Engine) emit(ctx context.Context, slug, runID string, ev workflow.RunEvent) {
	// Stamp ts once so AppendEvent (state) and OnEvent (SSE) agree.
	// Without this, FE backfill can't dedup state ↔ stream events.
	if ev.TS.IsZero() {
		ev.TS = e.Now()
	}
	_ = e.StateStore.AppendEvent(slug, runID, ev)
	lg := log.Ctx(ctx)
	logEvent := lg.Info()
	if ev.Event == workflow.EventNodeFailed || ev.Event == workflow.EventWorkflowFailed {
		logEvent = lg.Warn()
	}
	logEvent.
		Str("component", "wf").
		Str("wf_slug", slug).
		Str("wf_run_id", runID).
		Str("wf_node", ev.Node).
		Str("wf_event", ev.Event).
		Interface("data", truncateForEvent(ev.Data)).
		Msg("workflow event")
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
	// Build the per-run logger.
	//
	// Default base = the global &log.Logger (whatever sink wick
	// boot configured — stdout, file, whatever). This guarantees
	// engine output shows up no matter who triggered the run
	// (HTTP click, cron tick, channel inbound, MCP RPC).
	//
	// If the originating ctx already carries a scoped logger (HTTP
	// middleware injects one with request_id), prefer it so parent
	// fields propagate. log.Ctx returns zerolog's disabled
	// placeholder when ctx has nothing — we ignore that case and
	// stick with the global, otherwise every line gets swallowed.
	base := &log.Logger
	if l := log.Ctx(ctx); l.GetLevel() != zerolog.Disabled {
		base = l
	}
	reqID, _ := evt.Payload["request_id"].(string)
	lg := base.With().
		Str("wf_id", w.ID).
		Str("wf_run_id", runID)
	if reqID != "" {
		lg = lg.Str("request_id", reqID)
	}
	ctx = lg.Logger().WithContext(ctx)
	entry, firedTrigger := pickEntry(w, evt)
	st := workflow.RunState{
		RunID:      runID,
		WorkflowID: w.ID,
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
	envVals, _ := e.Service.LoadEnvValues(w.ID)
	rc := &workflow.RunContext{
		Workflow:    w,
		Event:       evt,
		Outputs:     st.Outputs,
		EnvValues:   envVals,
		Secrets:     extractSecrets(w.Env, envVals),
		RunID:       runID,
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	// Log which runID source we used so any future mismatch between
	// the UI-issued ID and the engine's effective ID is grep-able.
	idSource := "payload"
	if _, ok := evt.Payload["run_id"].(string); !ok {
		idSource = "generated"
	}
	startData := map[string]any{"trigger": evt.Type, "id_source": idSource}
	if firedTrigger != nil {
		if firedTrigger.ID != "" {
			startData["trigger_id"] = firedTrigger.ID
		}
		startData["trigger_type"] = string(firedTrigger.Type)
	} else if tid, _ := evt.Payload["trigger_id"].(string); tid != "" {
		startData["trigger_id"] = tid
	}
	startEv := workflow.RunEvent{
		TS:    e.Now(),
		Event: workflow.EventWorkflowStarted,
		Data:  startData,
	}
	if err := e.StateStore.AppendEvent(w.ID, runID, startEv); err != nil {
		return st, err
	}
	// Funnel through emit so the started event lands in both the
	// SSE stream and zerolog (with request_id from ctx) — same path
	// every other event takes.
	log.Ctx(ctx).Info().
		Str("component", "wf").
		Str("wf_id", w.ID).
		Str("wf_run_id", runID).
		Str("wf_event", workflow.EventWorkflowStarted).
		Str("wf_id_source", idSource).
		Msg("workflow run enqueued in engine")
	if e.OnEvent != nil {
		e.OnEvent(w.ID, runID, startEv)
	}
	if err := e.StateStore.Save(w.ID, runID, st); err != nil {
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
		e.emit(ctx, w.ID, runID, workflow.RunEvent{Event: workflow.EventWorkflowFailed, Data: map[string]any{"error": err.Error()}})
	} else {
		st.Status = workflow.StatusSuccess
		e.emit(ctx, w.ID, runID, workflow.RunEvent{Event: workflow.EventWorkflowCompleted})
	}
	end := e.Now()
	st.EndedAt = &end
	st.UpdatedAt = end
	st.Current = nil
	if err := e.StateStore.Save(w.ID, runID, st); err != nil {
		return st, err
	}
	// Persist a one-line summary to the sharded index file. The Runs
	// panel reads from this index instead of scanning every run dir
	// on disk, so listings stay constant-time as history grows. Log
	// the failure (warn) so a broken index doesn't hide silently;
	// don't return the error — the run itself already finished, and
	// missing index rows are a UX degradation, not a data loss.
	if ierr := e.StateStore.IndexAppend(w.ID, state.IndexEntry{
		ID:         runID,
		Status:     st.Status,
		StartedAt:  st.StartedAt,
		EndedAt:    st.EndedAt,
		DurationMs: end.Sub(st.StartedAt).Milliseconds(),
	}); ierr != nil {
		log.Ctx(ctx).Warn().Err(ierr).
			Str("component", "wf").
			Str("wf_id", w.ID).
			Str("wf_run_id", runID).
			Msg("index append failed; Runs panel may drop this run")
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
				return e.failNode(ctx, w.ID, st, n, err)
			}
			e.recordSuccess(ctx, w.ID, st, rc, n, out, e.Now().Sub(started).Milliseconds())
			queue = append(queue, e.nextNodes(w, n, out)...)
			continue
		}

		if n.Type == workflow.NodeParallel {
			started := e.Now()
			out, err := e.runParallel(ctx, w, n, rc)
			if err != nil {
				return e.failNode(ctx, w.ID, st, n, err)
			}
			e.recordSuccess(ctx, w.ID, st, rc, n, out, e.Now().Sub(started).Milliseconds())
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
		e.recordSuccess(ctx, w.ID, st, rc, n, out, e.Now().Sub(started).Milliseconds())
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
		e.emit(ctx, rc.Workflow.ID, rc.RunID, workflow.RunEvent{
			Event: workflow.EventNodeStarted,
			Node:  n.ID,
			Data: map[string]any{
				"type":   string(n.Type),
				"input":  truncateForEvent(parentOutputs(rc, n)),
				"config": truncateForEvent(nodeConfigForEvent(n)),
			},
		})
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

func (e *Engine) recordSuccess(ctx context.Context, slug string, st *workflow.RunState, rc *workflow.RunContext, n workflow.Node, out workflow.NodeOutput, latencyMs int64) {
	rc.NodeOutputs[n.ID] = out
	rc.Outputs[n.ID] = nodeOutputAsMap(out)
	st.Completed = append(st.Completed, n.ID)
	st.Outputs = rc.Outputs
	st.UpdatedAt = e.Now()
	e.emit(ctx, slug, st.RunID, workflow.RunEvent{
		Event: workflow.EventNodeCompleted,
		Node:  n.ID,
		Data: map[string]any{
			"verdict":    out.Verdict,
			"latency_ms": latencyMs,
			"output":     truncateForEvent(nodeOutputAsMap(out)),
		},
	})
	e.saveState(ctx, slug, st)
}

func (e *Engine) failNode(ctx context.Context, slug string, st *workflow.RunState, n workflow.Node, err error) error {
	st.Failed = append(st.Failed, n.ID)
	st.Error = &workflow.NodeError{Node: n.ID, Type: string(n.Type), Message: err.Error()}
	st.UpdatedAt = e.Now()
	e.emit(ctx, slug, st.RunID, workflow.RunEvent{Event: workflow.EventNodeFailed, Node: n.ID, Data: map[string]any{"error": err.Error()}})
	e.saveState(ctx, slug, st)
	return &workflow.ExecError{Node: n.ID, Type: n.Type, Wrapped: err}
}

func (e *Engine) applyOnFailure(ctx context.Context, w workflow.Workflow, st *workflow.RunState, n workflow.Node, err error, rc *workflow.RunContext) error {
	policy := n.OnFailure
	if policy == "" {
		policy = workflow.FailHalt
	}
	switch policy {
	case workflow.FailHalt:
		return e.failNode(ctx, w.ID, st, n, err)
	case workflow.FailSkip:
		st.Skipped = append(st.Skipped, n.ID)
		e.emit(ctx, w.ID, st.RunID, workflow.RunEvent{Event: workflow.EventNodeSkipped, Node: n.ID, Data: map[string]any{"reason": err.Error()}})
		return nil
	case workflow.FailFallback:
		if n.Fallback == "" {
			return e.failNode(ctx, w.ID, st, n, fmt.Errorf("on_failure=fallback but fallback is empty: %w", err))
		}
		st.Failed = append(st.Failed, n.ID)
		e.emit(ctx, w.ID, st.RunID, workflow.RunEvent{Event: workflow.EventNodeFailed, Node: n.ID, Data: map[string]any{"error": err.Error(), "fallback": n.Fallback}})
		e.saveState(ctx, w.ID, st)
		st.Current = append(st.Current, n.Fallback)
		return nil
	}
	return e.failNode(ctx, w.ID, st, n, err)
}

// saveState persists the run state and logs a warning if the write fails.
// Callers previously discarded the error with _ = — this surfaces it so
// operators can diagnose disk-full or permission issues without the engine
// silently continuing with stale state on disk.
func (e *Engine) saveState(ctx context.Context, slug string, st *workflow.RunState) {
	if err := e.StateStore.Save(slug, st.RunID, *st); err != nil {
		zerolog.Ctx(ctx).Warn().
			Str("slug", slug).
			Str("run_id", st.RunID).
			Err(err).
			Msg("engine: failed to persist run state — state on disk may be stale")
	}
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

// nodeConfigForEvent returns a compact view of the node's static
// configuration (module/op/args, channel/op/args, command, sql, …)
// for the node_started log line. Operators debugging a failed run
// want to see "what was this node configured to do" alongside the
// upstream input — the YAML preview tab has the full picture but
// the log line is what gets copied into bug reports.
//
// Returns nil when there's nothing meaningful to surface so the
// "config" field gets omitted from the event payload entirely.
func nodeConfigForEvent(n workflow.Node) map[string]any {
	cfg := map[string]any{}
	if n.Module != "" {
		cfg["module"] = n.Module
	}
	if n.Op != "" {
		cfg["op"] = n.Op
	}
	if n.ChannelName != "" {
		cfg["channel"] = n.ChannelName
	}
	if len(n.Args) > 0 {
		cfg["args"] = n.Args
	}
	if len(n.Command) > 0 {
		cfg["command"] = n.Command
	}
	if n.URL != "" {
		cfg["url"] = n.URL
	}
	if n.Method != "" {
		cfg["method"] = n.Method
	}
	if n.Prompt != "" {
		cfg["prompt"] = n.Prompt
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// parentOutputs returns the map of upstream NodeOutputs that feed
// node `n` — used for the "input" field on the node_started log
// line so operators can grep the request payload by run_id even
// when state.json is purged. For merge nodes the explicit Inputs
// list wins; otherwise we walk graph edges. Returns whatever has
// already completed (empty when the node sits at the trigger
// boundary).
func parentOutputs(rc *workflow.RunContext, n workflow.Node) map[string]any {
	out := map[string]any{}
	if len(n.Inputs) > 0 {
		for _, p := range n.Inputs {
			if v, ok := rc.Outputs[p]; ok {
				out[p] = v
			}
		}
		return out
	}
	for _, ed := range rc.Workflow.Graph.Edges {
		if ed.To != n.ID {
			continue
		}
		if v, ok := rc.Outputs[ed.From]; ok {
			out[ed.From] = v
		}
	}
	return out
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

// pickEntry resolves the entry node for a run and returns the trigger
// that fired (when one can be identified). A nil second return means
// the run fell back to graph.entry — no matching trigger row was
// found, which happens for direct graph runs and legacy events.
//
// Resolution order: (1) match by trigger_id in the event payload —
// the UI Execute picker forwards this, so multi-trigger workflows
// pick the exact row the user clicked; (2) match by trigger type
// when an entry_node is set on the trigger; (3) graph.entry fallback.
func pickEntry(w workflow.Workflow, evt workflow.Event) (string, *workflow.Trigger) {
	if tid, _ := evt.Payload["trigger_id"].(string); tid != "" {
		for i := range w.Triggers {
			if w.Triggers[i].ID == tid {
				tr := &w.Triggers[i]
				if tr.EntryNode != "" {
					return tr.EntryNode, tr
				}
				return w.Graph.Entry, tr
			}
		}
	}
	for i := range w.Triggers {
		tr := &w.Triggers[i]
		if tr.EntryNode != "" && string(tr.Type) == evt.Type {
			return tr.EntryNode, tr
		}
	}
	return w.Graph.Entry, nil
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

// Package trigger implements the workflow router: it receives
// workflow.Event values from channels / cron / webhooks, finds the
// workflows subscribed to that event, and enqueues a run per match.
//
// The router itself stays channel-agnostic. Two design choices
// underline that:
//
//  1. Trigger index — at Register time, each workflow's triggers
//     declare their route key(s) ("channel/slack/message", "cron",
//     "webhook"). Dispatch builds the event's route keys and looks up
//     the index in O(1) instead of iterating every workflow.
//
//  2. No match DSL in the router — workflow YAML's `match:` map is
//     not evaluated here. Workflows filter inside the graph (branch /
//     transform / shell / classify) which keeps the engine free of
//     channel-specific match grammars and lets operators express
//     arbitrary filter logic.
//
// Trigger-level checks the router still does: channel + event name
// (via route key), webhook path + method, error source workflow +
// severity. Everything that needs the payload contents happens in the
// graph.
package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/service"
)

// Router matches incoming events to registered workflows, applies
// dedup, and enqueues per workflow.
// webhookEntry holds the secretRef for one webhook path pattern.
// workflowID is the UUID of the owning workflow (workflow.Workflow.ID).
type webhookEntry struct {
	workflowID string
	secretRef  string
}

// workerHandle tracks one worker goroutine. cancel stops it; alive is
// flipped to false when runWorker returns, so Dispatch can tell whether a
// queue still has a LIVE consumer — the map entry alone is not proof the
// goroutine is running (a worker born from a cancelled ctx dies but its
// handle lingers until Unregister).
type workerHandle struct {
	cancel context.CancelFunc
	alive  atomic.Bool
}

type Router struct {
	mu      sync.RWMutex
	engine  *engine.Engine
	service service.Service
	// baseCtx is the server-lifetime context every worker goroutine is
	// spawned under. Set once via SetBaseCtx at boot. Register ignores
	// the ctx its caller passes for worker lifetime — HTTP handlers used
	// to pass the request ctx, which cancelled the moment the response
	// flushed and silently killed the just-spawned worker (queue lingered,
	// runs piled up, nothing drained). Falls back to context.Background()
	// if unset so tests and any pre-SetBaseCtx Register still get a
	// long-lived worker.
	baseCtx context.Context
	defs    map[string]workflow.Workflow
	queues  map[string]*Queue
	dedups  map[string]*Dedup
	workers map[string]*workerHandle
	// index maps a route key → list of (id, triggerIdx) pairs so
	// Dispatch can skip workflows that don't subscribe to this event.
	// Built/torn-down by Register/Unregister; never read without
	// holding mu.
	index map[string][]triggerRef
	// webhookIndex maps a webhook path pattern → webhookEntry for O(1)
	// secret lookup per incoming webhook request. Built/torn-down
	// alongside the main index in reindexLocked/removeFromIndexLocked.
	webhookIndex map[string]webhookEntry
	wg           sync.WaitGroup
	clock        func() time.Time
}

// triggerRef pins one trigger inside a registered workflow. TriggerIdx
// is the slot in Workflow.Triggers — needed because a workflow can
// have N triggers and the router-level check (path/method/severity)
// must run against the right one.
type triggerRef struct {
	ID         string
	TriggerIdx int
}

// SetService swaps the workflow service this router reads from. Used
// at boot when the DB-backed service replaces the file-based one — the
// router caches a Service pointer at New() time, so re-wiring after
// the swap keeps the queues + dedup tables pointing at the same data.
func (r *Router) SetService(svc service.Service) { r.service = svc }

// SetBaseCtx pins the server-lifetime context all workers are spawned
// under. Call once at boot (Bootstrap) before any Register. Workers
// spawned after this use baseCtx, so a caller passing a short-lived
// request ctx to Register can no longer kill them.
func (r *Router) SetBaseCtx(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseCtx = ctx
}

// NewRouter wires a Router to an Engine + Service.
func NewRouter(e *engine.Engine, svc service.Service) *Router {
	return &Router{
		engine:       e,
		service:      svc,
		defs:         map[string]workflow.Workflow{},
		queues:       map[string]*Queue{},
		dedups:       map[string]*Dedup{},
		workers:      map[string]*workerHandle{},
		index:        map[string][]triggerRef{},
		webhookIndex: map[string]webhookEntry{},
		clock:        func() time.Time { return time.Now() },
	}
}

// Register adds a workflow to the router and spawns its worker goroutine.
func (r *Router) Register(ctx context.Context, w workflow.Workflow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs[w.ID] = w
	if _, ok := r.queues[w.ID]; !ok {
		max := w.Queue.MaxSize
		if max == 0 {
			max = 20
		}
		policy := w.Queue.OnOverflow
		if policy == "" {
			policy = workflow.OverflowDropOldest
		}
		r.queues[w.ID] = NewQueue(max, policy)
		dedupTTL := 24 * time.Hour
		if t := firstChannelDedupTTL(w); t > 0 {
			dedupTTL = time.Duration(t) * time.Second
		}
		r.dedups[w.ID] = NewDedup(1024, dedupTTL)
		// Worker lifetime = server lifetime (baseCtx), NOT the ctx the
		// caller passed. HTTP publish/toggle handlers pass the request
		// ctx; honouring it here killed the worker the moment the
		// response flushed. baseCtx is set once at boot via SetBaseCtx.
		parent := r.baseCtx
		if parent == nil {
			parent = context.Background()
		}
		wctx, cancel := context.WithCancel(parent)
		h := &workerHandle{cancel: cancel}
		h.alive.Store(true)
		r.workers[w.ID] = h
		r.wg.Add(1)
		go r.runWorker(wctx, w.ID, h)
		log.Info().Str("component", "wf").Str("wf_id", w.ID).
			Int("queue_max", max).Msg("router: worker spawned")
	}
	r.reindexLocked(w)
}

// WorkerAlive reports whether id has a registered worker whose goroutine
// is currently running (not merely a lingering map handle). Exposed for
// health checks and tests asserting worker lifetime.
func (r *Router) WorkerAlive(id string) bool {
	r.mu.RLock()
	h := r.workers[id]
	r.mu.RUnlock()
	return h != nil && h.alive.Load()
}

// Unregister stops the worker for id and frees its queue.
func (r *Router) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.workers[id]; ok {
		h.cancel()
		delete(r.workers, id)
	}
	if q, ok := r.queues[id]; ok {
		q.Close()
		delete(r.queues, id)
	}
	delete(r.dedups, id)
	delete(r.defs, id)
	r.removeFromIndexLocked(id)
}

func (r *Router) Definition(id string) (workflow.Workflow, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.defs[id]
	return w, ok
}

// RunNow enqueues a manual run for one explicit id, bypassing
// Enabled + trigger-match checks. Used by the UI Run-Now button so
// admins can fire a disabled workflow (e.g. dry-run before enable)
// without going through the Dispatch matcher.
//
// Returns an error if the workflow isn't registered with the router
// (caller should HotReload first).
func (r *Router) RunNow(ctx context.Context, id string, evt workflow.Event) error {
	return r.RunNowWith(ctx, id, nil, evt)
}

// RunNowWith is RunNow with an explicit workflow override. Pass a
// non-nil `w` to execute that exact definition (typically the
// draft loaded from disk) instead of the published copy registered
// in Router.defs. The router still owns the per-id queue + worker
// machinery — the override only affects which Workflow value the
// engine receives.
//
// When `w` is nil, behaviour is identical to RunNow.
func (r *Router) RunNowWith(ctx context.Context, id string, w *workflow.Workflow, evt workflow.Event) error {
	r.mu.RLock()
	_, registered := r.defs[id]
	q := r.queues[id]
	r.mu.RUnlock()
	if q == nil {
		return fmt.Errorf("workflow %q has no router queue — register it first", id)
	}
	if w == nil && !registered {
		return fmt.Errorf("workflow %q not registered with router", id)
	}
	return q.Enqueue(WorkItem{ID: id, Event: evt, Workflow: w})
}

// RunNowWithDone enqueues like RunNowWith but returns a channel that
// receives the RunResult when the run completes. The caller is
// responsible for reading from the channel exactly once; not reading
// blocks the worker goroutine.
func (r *Router) RunNowWithDone(ctx context.Context, id string, w *workflow.Workflow, evt workflow.Event) (<-chan RunResult, error) {
	r.mu.RLock()
	_, registered := r.defs[id]
	q := r.queues[id]
	r.mu.RUnlock()
	if q == nil {
		return nil, fmt.Errorf("workflow %q has no router queue — register it first", id)
	}
	if w == nil && !registered {
		return nil, fmt.Errorf("workflow %q not registered with router", id)
	}
	done := make(chan RunResult, 1)
	if err := q.Enqueue(WorkItem{ID: id, Event: evt, Workflow: w, Done: done}); err != nil {
		return nil, err
	}
	return done, nil
}

// DispatchWithDone routes an event like Dispatch but returns a channel
// per matched workflow that delivers the RunResult on completion.
// Callers waiting on a specific run should use this instead of Dispatch.
func (r *Router) DispatchWithDone(ctx context.Context, evt workflow.Event) []<-chan RunResult {
	r.mu.RLock()
	type candidateKey struct {
		id  string
		idx int
	}
	type candidate struct {
		wfID string
		tr   workflow.Trigger
	}
	candidates := map[candidateKey]candidate{}
	for _, key := range eventRouteKeys(evt) {
		for _, ref := range r.index[key] {
			ck := candidateKey{id: ref.ID, idx: ref.TriggerIdx}
			if _, seen := candidates[ck]; seen {
				continue
			}
			w, ok := r.defs[ref.ID]
			if !ok || !w.Enabled {
				continue
			}
			if ref.TriggerIdx >= len(w.Triggers) {
				continue
			}
			candidates[ck] = candidate{wfID: ref.ID, tr: w.Triggers[ref.TriggerIdx]}
		}
	}
	r.mu.RUnlock()

	var results []<-chan RunResult
	for _, c := range candidates {
		if !triggerPassesRouterChecks(c.wfID, c.tr, evt) {
			continue
		}
		if !r.passesDedup(c.wfID, evt) {
			continue
		}
		r.mu.RLock()
		q := r.queues[c.wfID]
		r.mu.RUnlock()
		if q == nil {
			continue
		}
		done := make(chan RunResult, 1)
		perTrig := evt
		perTrig.TriggerID = c.tr.ID
		if err := q.Enqueue(WorkItem{ID: c.wfID, Event: perTrig, Done: done}); err == nil {
			results = append(results, done)
		}
	}
	return results
}

// Dispatch routes an event to every subscribed workflow.
//
// Pipeline:
//  1. Build the event's route keys (specific + wildcards).
//  2. Look each up in the trigger index, union the resulting
//     triggerRefs, dedup by workflow id so a workflow with
//     multiple matching triggers still enqueues once.
//  3. For each candidate, run the cheap router-side checks that need
//     the raw trigger (webhook path/method, error source, target),
//     then dedup the event, then enqueue.
//
// Returns the number of workflows that accepted the event.
func (r *Router) Dispatch(ctx context.Context, evt workflow.Event) int {
	r.mu.RLock()
	// Key = "wfID:triggerIdx" so a workflow with multiple triggers of the
	// same type (e.g. two webhook triggers) each get a candidate slot.
	type candidateKey struct {
		id  string
		idx int
	}
	type candidate struct {
		wfID string
		tr   workflow.Trigger
	}
	candidates := map[candidateKey]candidate{}
	for _, key := range eventRouteKeys(evt) {
		for _, ref := range r.index[key] {
			ck := candidateKey{id: ref.ID, idx: ref.TriggerIdx}
			if _, seen := candidates[ck]; seen {
				continue
			}
			w, ok := r.defs[ref.ID]
			if !ok || !w.Enabled {
				continue
			}
			if ref.TriggerIdx >= len(w.Triggers) {
				continue
			}
			candidates[ck] = candidate{wfID: ref.ID, tr: w.Triggers[ref.TriggerIdx]}
		}
	}
	r.mu.RUnlock()

	matched := 0
	for _, c := range candidates {
		if !triggerPassesRouterChecks(c.wfID, c.tr, evt) {
			continue
		}
		if !r.passesDedup(c.wfID, evt) {
			continue
		}
		r.mu.RLock()
		q := r.queues[c.wfID]
		h := r.workers[c.wfID]
		r.mu.RUnlock()
		// workerAlive reflects whether the goroutine is actually running,
		// not just whether a handle exists in the map — a worker spawned
		// from a cancelled ctx dies but its handle lingers until Unregister.
		workerAlive := h != nil && h.alive.Load()
		if q == nil {
			// Candidate matched but no queue exists — the workflow was
			// indexed without a live worker (stale index / failed
			// Register). Silent before; log so a run that vanishes here
			// leaves a trace.
			log.Warn().Str("component", "wf").Str("wf_id", c.wfID).
				Str("wf_event", string(c.tr.Type)).
				Msg("dispatch: matched trigger but no queue — run dropped")
			continue
		}
		if !workerAlive {
			// Queue exists but its consumer goroutine is dead — the run
			// would be enqueued and never drained (the original silent
			// bug). Loud now so this is debuggable instead of a vanished run.
			log.Warn().Str("component", "wf").Str("wf_id", c.wfID).
				Str("wf_event", string(c.tr.Type)).Int("queue_depth", q.Len()).
				Msg("dispatch: queue has no live worker — run will not drain")
		}
		// Per-trigger event copy with the originating Trigger.ID
		// stamped so workflow_watch(trigger_id=...) can filter back
		// to the entry that fired. Original evt stays untouched —
		// matters because a single inbound payload may fan out to
		// multiple triggers across workflows.
		perTrig := evt
		perTrig.TriggerID = c.tr.ID
		err := q.Enqueue(WorkItem{ID: c.wfID, Event: perTrig})
		l := log.With().Str("component", "wf").Str("wf_id", c.wfID).
			Str("wf_event", string(c.tr.Type)).Bool("worker", workerAlive).
			Int("queue_depth", q.Len()).Logger()
		if err != nil {
			// Overflow drop_new returns nil, so a non-nil err here is
			// reject/closed — the run will NOT execute. Make it loud.
			l.Warn().Err(err).Msg("dispatch: enqueue failed — run dropped")
			continue
		}
		l.Info().Msg("dispatch: run enqueued")
		matched++
	}
	return matched
}

// MatchTrigger is kept for backward-compatible test ergonomics — it
// runs the router-side checks (channel name, event subtype, webhook
// path/method, error source) against a single trigger.
func MatchTrigger(tr workflow.Trigger, evt workflow.Event) bool {
	if string(tr.Type) != evt.Type {
		return false
	}
	switch tr.Type {
	case workflow.TriggerChannel:
		if tr.ChannelName != "" && tr.ChannelName != "*" && tr.ChannelName != evt.Channel {
			return false
		}
		if tr.Event != "" && evt.Subtype != "" && tr.Event != evt.Subtype {
			return false
		}
	}
	return triggerPassesRouterChecks("", tr, evt)
}

// triggerPassesRouterChecks runs the small, payload-light checks the
// router owns: webhook path/method, error source/severity, channel
// target, and — when match_enabled is set — the per-event Match
// filter map. Filter eval uses generic key-equality with picker
// (JSON array of {id,name}) membership; events that need fancier
// semantics fall back to dump-all and filter inside the graph.
func triggerPassesRouterChecks(wfID string, tr workflow.Trigger, evt workflow.Event) bool {
	switch tr.Type {
	case workflow.TriggerChannel:
		if tr.Target != "" {
			gotChannel := payloadString(evt, "channel_id")
			if gotChannel == "" {
				gotChannel = evt.Channel
			}
			if tr.Target != gotChannel {
				return false
			}
		}
		if tr.MatchEnabled && !matchEventPayload(filterMatchSpec(tr.Match), evt.Payload) {
			return false
		}
		return true
	case workflow.TriggerWebhook:
		if tr.Path != "" {
			gotPath := payloadString(evt, "path")
			if !PathMatches(webhookFullPath(wfID, tr.Path), gotPath) {
				return false
			}
		}
		if tr.Method != "" {
			gotMethod := payloadString(evt, "method")
			if !strings.EqualFold(tr.Method, gotMethod) {
				return false
			}
		}
		return true
	case workflow.TriggerError:
		srcWF := payloadString(evt, "source_workflow")
		if tr.SourceWorkflow != "" && tr.SourceWorkflow != "*" && tr.SourceWorkflow != srcWF {
			return false
		}
		if len(tr.Severity) > 0 {
			gotSeverity := payloadString(evt, "severity")
			if !containsStr(tr.Severity, gotSeverity) {
				return false
			}
		}
		return true
	}
	return true
}

// filterMatchSpec strips UI-control keys from a match spec before the
// router evaluates it against event payload. The "mode" key is a
// MatchSchema UI convention (dropdown: all|whitelist) — its value
// ("all", "whitelist") is never present in event payloads, so passing
// it through matchEventPayload causes a false-negative: mode=whitelist
// vs payload["mode"]=nil → always rejects.
//
// Rule: if mode=all → return empty spec (no filter). If mode=whitelist
// (or absent) → return spec minus the "mode" key so only real payload
// keys (channel_id, user, text_contains, …) are evaluated.
func filterMatchSpec(spec map[string]any) map[string]any {
	if len(spec) == 0 {
		return spec
	}
	mode, _ := spec["mode"].(string)
	if mode == "all" {
		return nil
	}
	out := make(map[string]any, len(spec))
	for k, v := range spec {
		if k == "mode" {
			continue
		}
		out[k] = v
	}
	return out
}

// matchEventPayload evaluates the trigger's Match map against the
// incoming event payload. Generic semantics (no per-event registry
// lookup) so the router stays channel-agnostic:
//
//   - empty / missing spec value → key is skipped (not a filter)
//   - string spec → payload[key] equals spec
//   - JSON array `[{"id":..},..]` (picker output) → payload[key] is
//     a member of the id list
//
// Events that need fancier semantics (regex, set difference, custom
// transform) fall back to dump-all and filter inside the graph with
// a branch / transform node.
func matchEventPayload(spec map[string]any, payload map[string]any) bool {
	for k, raw := range spec {
		if !matchOne(raw, payload[k]) {
			return false
		}
	}
	return true
}

func matchOne(specVal, gotVal any) bool {
	// Empty spec = no filter on this key.
	switch s := specVal.(type) {
	case nil:
		return true
	case string:
		if strings.TrimSpace(s) == "" {
			return true
		}
		// Picker output rides through as a JSON string when the canvas
		// serializes inner.match — parse it back to []map[string]any
		// before treating as plain string equality. An empty parsed
		// list means "no chips selected" → no filter on this key
		// (inspector UX: toggling Filter on without picking chips
		// shouldn't kill the trigger).
		if isJSONArray(s) {
			var arr []map[string]any
			if err := json.Unmarshal([]byte(s), &arr); err == nil {
				if len(arr) == 0 {
					return true
				}
				return idMembership(arr, gotVal)
			}
		}
		got, _ := gotVal.(string)
		return s == got
	case []any:
		if len(s) == 0 {
			return true
		}
		arr := make([]map[string]any, 0, len(s))
		for _, it := range s {
			if m, ok := it.(map[string]any); ok {
				arr = append(arr, m)
			}
		}
		if len(arr) == 0 {
			return true
		}
		return idMembership(arr, gotVal)
	case bool:
		got, _ := gotVal.(bool)
		return s == got
	}
	return false
}

func idMembership(arr []map[string]any, gotVal any) bool {
	got, _ := gotVal.(string)
	for _, m := range arr {
		id, _ := m["id"].(string)
		if id == got {
			return true
		}
	}
	return false
}

func isJSONArray(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "[")
}

// triggerRouteKeys returns the index keys a trigger subscribes to.
//
// Channel triggers register under "channel/<channel>/<event>" with
// "*" wildcards in either slot when the trigger doesn't pin them.
// Other trigger families use a single bucket per type.
func triggerRouteKeys(tr workflow.Trigger) []string {
	switch tr.Type {
	case workflow.TriggerChannel:
		ch := tr.ChannelName
		if ch == "" || ch == "*" {
			ch = "*"
		}
		ev := tr.Event
		if ev == "" {
			ev = "*"
		}
		return []string{"channel/" + ch + "/" + ev}
	case workflow.TriggerWebhook:
		return []string{"webhook"}
	case workflow.TriggerCron:
		return []string{"cron"}
	case workflow.TriggerManual:
		return []string{"manual"}
	case workflow.TriggerError:
		return []string{"error"}
	case workflow.TriggerScheduleAt:
		return []string{"schedule_at"}
	}
	return nil
}

// eventRouteKeys returns the index keys an event lookups against, in
// most-specific-first order. Wildcards let a trigger subscribe to a
// whole channel ("channel/slack/*") or every channel event
// ("channel/*/*") without enumerating every (channel, event) pair.
func eventRouteKeys(evt workflow.Event) []string {
	switch evt.Type {
	case string(workflow.TriggerChannel):
		ch := evt.Channel
		ev := evt.Subtype
		keys := make([]string, 0, 4)
		if ch != "" && ev != "" {
			keys = append(keys, "channel/"+ch+"/"+ev)
		}
		if ch != "" {
			keys = append(keys, "channel/"+ch+"/*")
		}
		keys = append(keys, "channel/*/*")
		return keys
	case string(workflow.TriggerWebhook):
		return []string{"webhook"}
	case string(workflow.TriggerCron):
		return []string{"cron"}
	case string(workflow.TriggerManual):
		return []string{"manual"}
	case string(workflow.TriggerError):
		return []string{"error"}
	case string(workflow.TriggerScheduleAt):
		return []string{"schedule_at"}
	}
	return nil
}

// webhookFullPath constructs the canonical /hooks request path for a
// webhook trigger. Path is the user-supplied slug (e.g. "my-hook");
// wfID namespaces it so paths are unique across workflows without the
// user having to embed the workflow ID manually. The stored JSON only
// carries the slug — the router adds the prefix at match time.
//
// Example: wfID="abc-123", slug="orders" → "/abc-123/orders"
// Empty slug → "/{wfID}" (bare workflow namespace, allows all sub-paths).
// Empty wfID (test helpers) → use slug as-is, with a leading /.
func webhookFullPath(wfID, slug string) string {
	if wfID == "" {
		// test helper path — no namespace prefix available
		if strings.HasPrefix(slug, "/") {
			return slug
		}
		return "/" + slug
	}
	if slug == "" {
		return "/" + wfID
	}
	clean := strings.TrimPrefix(slug, "/")
	return "/" + wfID + "/" + clean
}

// reindexLocked rebuilds the index entries for one workflow. Caller
// must hold r.mu.
func (r *Router) reindexLocked(w workflow.Workflow) {
	r.removeFromIndexLocked(w.ID)
	for i, tr := range w.Triggers {
		for _, key := range triggerRouteKeys(tr) {
			r.index[key] = append(r.index[key], triggerRef{ID: w.ID, TriggerIdx: i})
		}
		if tr.Type == workflow.TriggerWebhook && tr.SecretRef != "" {
			// Index by full path so WebhookSecretFor can match against
			// the incoming r.URL.Path without knowing the workflow ID.
			// Empty slug → wildcard "*" (matches any path).
			var key string
			if tr.Path == "" {
				key = "*"
			} else {
				key = webhookFullPath(w.ID, tr.Path)
			}
			r.webhookIndex[key] = webhookEntry{workflowID: w.ID, secretRef: tr.SecretRef}
		}
	}
}

// removeFromIndexLocked drops every index entry for id. Caller must
// hold r.mu.
func (r *Router) removeFromIndexLocked(id string) {
	for key, refs := range r.index {
		filtered := refs[:0]
		for _, ref := range refs {
			if ref.ID != id {
				filtered = append(filtered, ref)
			}
		}
		if len(filtered) == 0 {
			delete(r.index, key)
		} else {
			r.index[key] = filtered
		}
	}
	for path, entry := range r.webhookIndex {
		if entry.workflowID == id {
			delete(r.webhookIndex, path)
		}
	}
}

func payloadString(evt workflow.Event, key string) string {
	if evt.Payload == nil {
		return ""
	}
	if v, ok := evt.Payload[key].(string); ok {
		return v
	}
	return ""
}

// PathMatches compares a trigger path template against an actual
// request path. Supports `{param}` segments.
func PathMatches(tmpl, got string) bool {
	tParts := strings.Split(strings.Trim(tmpl, "/"), "/")
	gParts := strings.Split(strings.Trim(got, "/"), "/")
	if len(tParts) != len(gParts) {
		return false
	}
	for i, tp := range tParts {
		if strings.HasPrefix(tp, "{") && strings.HasSuffix(tp, "}") {
			continue
		}
		if tp != gParts[i] {
			return false
		}
	}
	return true
}

func (r *Router) passesDedup(id string, evt workflow.Event) bool {
	r.mu.RLock()
	d := r.dedups[id]
	r.mu.RUnlock()
	if d == nil {
		return true
	}
	key := dedupKey(evt)
	if key == "" {
		return true
	}
	return !d.Seen(id + ":" + key)
}

func dedupKey(evt workflow.Event) string {
	if id, ok := evt.Payload["event_id"].(string); ok && id != "" {
		return evt.Channel + ":" + id
	}
	if id, ok := evt.Payload["message_id"].(string); ok && id != "" {
		return evt.Channel + ":" + id
	}
	return ""
}

func (r *Router) runWorker(ctx context.Context, id string, h *workerHandle) {
	defer r.wg.Done()
	// Mark dead on exit so Dispatch reports the queue as workerless
	// (truthful signal — the map handle alone outlives the goroutine).
	defer h.alive.Store(false)
	r.mu.RLock()
	q := r.queues[id]
	r.mu.RUnlock()
	if q == nil {
		return
	}
	for {
		item, ok := q.Dequeue(ctx)
		if !ok {
			// Dequeue returns false on ctx cancel or queue close. Either
			// way this worker stops serving runs for `id` — log it so a
			// silently-dead worker (enqueues pile up, never execute) is
			// visible. ctx.Err() distinguishes shutdown from Unregister.
			log.Info().Str("component", "wf").Str("wf_id", id).
				Bool("ctx_cancelled", ctx.Err() != nil).
				Msg("router: worker stopped")
			return
		}
		var w workflow.Workflow
		if item.Workflow != nil {
			w = *item.Workflow
		} else {
			r.mu.RLock()
			reg, defOK := r.defs[id]
			r.mu.RUnlock()
			if !defOK {
				continue
			}
			w = reg
		}
		st, err := r.engine.Run(ctx, w, item.Event)
		if item.Done != nil {
			item.Done <- RunResult{State: st, Err: err}
		}
		if err != nil {
			_ = r.fireErrorWorkflow(ctx, w, st, err)
		}
	}
}

func (r *Router) fireErrorWorkflow(ctx context.Context, w workflow.Workflow, st workflow.RunState, runErr error) error {
	if w.OnError == nil || w.OnError.TriggerWorkflow == "" {
		return nil
	}
	depth := 0
	if d, ok := st.Event.Payload["error_depth"].(int); ok {
		depth = d
	}
	if depth >= 3 {
		return fmt.Errorf("error workflow chain depth %d exceeded", depth)
	}
	payload := map[string]any{
		"source_workflow": w.ID,
		"source_run_id":   st.RunID,
		"error":           runErr.Error(),
		"severity":        w.OnError.Severity,
		"error_depth":     depth + 1,
	}
	if st.Error != nil {
		payload["failed_node"] = st.Error.Node
		payload["node_type"] = st.Error.Type
	}
	if w.OnError.IncludeState {
		payload["state_snapshot"] = st
	}
	if w.OnError.IncludeNodeOutput {
		payload["node_outputs"] = st.Outputs
	}
	errEvt := workflow.Event{Type: string(workflow.TriggerError), At: time.Now().UTC(), Payload: payload}
	r.Dispatch(ctx, errEvt)
	return nil
}

// Stop unregisters all and waits for workers to drain.
// StopTimeout is the deadline given to in-flight workers during Stop.
// Overridable in tests.
var StopTimeout = 30 * time.Second

func (r *Router) Stop() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.workers))
	for s := range r.workers {
		ids = append(ids, s)
	}
	r.mu.Unlock()
	for _, s := range ids {
		r.Unregister(s)
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(StopTimeout):
		log.Warn().Dur("timeout", StopTimeout).Msg("router stop: timed out waiting for in-flight workers to drain")
	}
}

// respondModeFor returns the RespondMode of the first webhook trigger
// whose path matches reqPath. Empty string means no match / default
// (immediately).
func (r *Router) respondModeFor(reqPath string, evt workflow.Event) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.defs {
		for _, tr := range w.Triggers {
			if tr.Type != workflow.TriggerWebhook {
				continue
			}
			full := webhookFullPath(w.ID, tr.Path)
			if PathMatches(full, reqPath) {
				return tr.RespondMode
			}
		}
	}
	return ""
}

// WebhookSecretFor returns the SecretRef for the incoming reqPath.
// Lookup is O(1) via webhookIndex: exact match first, then wildcard "*".
// Used by WebhookHandler to verify HMAC before dispatching.
func (r *Router) WebhookSecretFor(reqPath string) (secretRef string, found bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.webhookIndex[reqPath]; ok {
		return entry.secretRef, true
	}
	if entry, ok := r.webhookIndex["*"]; ok {
		return entry.secretRef, true
	}
	return "", false
}

func firstChannelDedupTTL(w workflow.Workflow) int {
	for _, tr := range w.Triggers {
		if tr.Type == workflow.TriggerChannel && tr.DedupTTLSec > 0 {
			return tr.DedupTTLSec
		}
	}
	return 0
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

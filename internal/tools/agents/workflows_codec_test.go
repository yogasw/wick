package agents

import (
	"strings"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

// ── Per-trigger routing (canvas → workflow.Triggers) ──────────────
//
// Each trigger NODE on the canvas owns its own outgoing line and
// translates to one entry in workflow.Triggers with its own
// Trigger.EntryNode. Engine.pickEntry picks the right chain by
// matching evt.Type to Trigger.Type, so:
//   - manual fire → uses the manual-trigger's EntryNode
//   - slack inbound → uses the channel-trigger's EntryNode
//   - cron tick → uses the cron-trigger's EntryNode
//   - … all independent of each other.
//
// Tests below cover every shape we expect the codec to produce.

// findTriggerByType returns the first trigger of the given type in
// the workflow, or nil if none exists. Helper for table tests.
func findTriggerByType(w wf.Workflow, t wf.TriggerType) *wf.Trigger {
	for i := range w.Triggers {
		if w.Triggers[i].Type == t {
			return &w.Triggers[i]
		}
	}
	return nil
}

// TestMultiTriggerEachFiresOwnChain — the user's mental model:
// canvas has a Slack trigger wired to `agent` AND a webhook trigger
// wired to `http`. Each fires its own chain.
func TestMultiTriggerEachFiresOwnChain(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"trigger-channel","class":"node-trigger","html":"",
	       "data":{"id":"trigger-channel","type":"trigger","data":{"triggerKind":"channel"}},
	       "pos_x":0,"pos_y":0,
	       "inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"3","output":"input_1"}]}}},
	  "2":{"id":2,"name":"trigger-webhook","class":"node-trigger","html":"",
	       "data":{"id":"trigger-webhook","type":"trigger","data":{"triggerKind":"webhook"}},
	       "pos_x":200,"pos_y":0,
	       "inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"4","output":"input_1"}]}}},
	  "3":{"id":3,"name":"agent","class":"node-agent","html":"",
	       "data":{"id":"agent","type":"agent","data":{"prompt":"go"}},
	       "pos_x":0,"pos_y":200,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{"output_1":{"connections":[]}}},
	  "4":{"id":4,"name":"http","class":"node-http","html":"",
	       "data":{"id":"http","type":"http","data":{"url":"x","method":"GET"}},
	       "pos_x":200,"pos_y":200,
	       "inputs":{"input_1":{"connections":[{"node":"2","input":"output_1"}]}},
	       "outputs":{"output_1":{"connections":[]}}}
	}}}}`
	w, err := drawflowJSONToWorkflow("t", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(w.Triggers) != 2 {
		t.Fatalf("expected 2 triggers, got %d: %+v", len(w.Triggers), w.Triggers)
	}
	ch := findTriggerByType(w, wf.TriggerChannel)
	if ch == nil || ch.EntryNode != "agent" {
		t.Errorf("channel trigger should fire agent; got %+v", ch)
	}
	wh := findTriggerByType(w, wf.TriggerWebhook)
	if wh == nil || wh.EntryNode != "http" {
		t.Errorf("webhook trigger should fire http; got %+v", wh)
	}
	// triggerHasEntry per type: manual should be refused (no manual
	// trigger on the canvas), the wired types accepted.
	if triggerHasEntry(w, wf.TriggerManual) {
		t.Errorf("manual run should be refused — no manual trigger node on canvas")
	}
	if !triggerHasEntry(w, wf.TriggerChannel) {
		t.Errorf("channel inbound should be accepted — trigger is wired to agent")
	}
	if !triggerHasEntry(w, wf.TriggerWebhook) {
		t.Errorf("webhook inbound should be accepted — trigger is wired to http")
	}
}

// TestTriggerDeletedFromCanvas — user deletes the entire trigger
// node on the canvas. After save, workflow.Triggers must NOT carry
// the deleted entry forward (canvas is source of truth for which
// triggers exist) and Run Now must be refused.
func TestTriggerDeletedFromCanvas(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "2":{"id":2,"name":"agent","class":"node-agent","html":"",
	       "data":{"id":"agent","type":"agent","data":{"prompt":"hi"}},
	       "pos_x":0,"pos_y":0,
	       "inputs":{"input_1":{"connections":[]}},
	       "outputs":{"output_1":{"connections":[]}}},
	  "3":{"id":3,"name":"http","class":"node-http","html":"",
	       "data":{"id":"http","type":"http","data":{"url":"x"}},
	       "pos_x":200,"pos_y":0,
	       "inputs":{"input_1":{"connections":[]}},
	       "outputs":{"output_1":{"connections":[]}}}
	}}}}`
	w, err := drawflowJSONToWorkflow("t", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Codec emits a default manual trigger when canvas has no
	// trigger nodes at all so workflow.Triggers stays non-empty,
	// but that fallback trigger has NO EntryNode — Run Now must
	// refuse.
	if triggerHasEntry(w, wf.TriggerManual) {
		t.Errorf("manual run should be refused: canvas has no wired trigger")
	}
	if w.Graph.Entry != "" {
		t.Errorf("graph.entry should be empty, got %q", w.Graph.Entry)
	}
}

// TestTriggerWithNoOutgoing — canvas has a manual trigger node but
// no outgoing line. Run Now must be refused (trigger exists but
// fires nothing).
func TestTriggerWithNoOutgoing(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"trigger-manual","class":"node-trigger","html":"",
	       "data":{"id":"trigger-manual","type":"trigger","data":{"triggerKind":"manual"}},
	       "pos_x":0,"pos_y":0,
	       "inputs":{},
	       "outputs":{"output_1":{"connections":[]}}},
	  "2":{"id":2,"name":"agent","class":"node-agent","html":"",
	       "data":{"id":"agent","type":"agent","data":{"prompt":"hi"}},
	       "pos_x":200,"pos_y":0,
	       "inputs":{"input_1":{"connections":[]}},
	       "outputs":{"output_1":{"connections":[]}}}
	}}}}`
	w, err := drawflowJSONToWorkflow("t", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	manual := findTriggerByType(w, wf.TriggerManual)
	if manual == nil {
		t.Fatalf("expected manual trigger entry, got: %+v", w.Triggers)
	}
	if manual.EntryNode != "" {
		t.Errorf("manual trigger has dangling EntryNode %q — should be empty", manual.EntryNode)
	}
	if triggerHasEntry(w, wf.TriggerManual) {
		t.Errorf("manual run should be refused: trigger has no outgoing line")
	}
}

// TestPerTriggerRoundtrip — encode then decode a workflow with two
// triggers and verify the canvas IDs + entries survive. Locks in
// the merge contract: Trigger.ID round-trips so save handler can
// fold prev's metadata back in by ID.
func TestPerTriggerRoundtrip(t *testing.T) {
	in := wf.Workflow{
		ID:      "t",
		Version: 1,
		Triggers: []wf.Trigger{
			{ID: "trigger-channel", Type: wf.TriggerChannel, ChannelName: "slack", EntryNode: "agent"},
			{ID: "trigger-webhook", Type: wf.TriggerWebhook, Path: "/x", EntryNode: "http"},
		},
		Graph: wf.Graph{
			Entry: "agent",
			Nodes: []wf.Node{
				{ID: "agent", Type: wf.NodeAgent, Prompt: "go"},
				{ID: "http", Type: wf.NodeHTTP, URL: "https://example.com", Method: "GET"},
			},
		},
	}
	body, err := workflowToDrawflowJSON(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(body, `"trigger-channel"`) || !strings.Contains(body, `"trigger-webhook"`) {
		t.Fatalf("encoded body missing trigger phantom IDs: %s", body)
	}
	out, err := drawflowJSONToWorkflow("t", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Triggers) != 2 {
		t.Fatalf("expected 2 triggers after round-trip, got %d: %+v", len(out.Triggers), out.Triggers)
	}
	for _, src := range in.Triggers {
		match := false
		for _, got := range out.Triggers {
			if got.ID == src.ID && got.Type == src.Type && got.EntryNode == src.EntryNode {
				match = true
				break
			}
		}
		if !match {
			t.Errorf("trigger %s lost in round-trip; got %+v", src.ID, out.Triggers)
		}
	}
}

// TestMergeTriggersCanvasWinsForEditorFields — the trigger inspector
// owns ChannelName / Event / Match / Schedule / Path etc. now, so
// canvas values are the source of truth. Prev only carries fields
// the inspector doesn't model (DedupTTLSec, Whitelist, RequireRole,
// SecretRef, …) so hand-edited YAML survives a canvas save.
func TestMergeTriggersCanvasWinsForEditorFields(t *testing.T) {
	canvas := []wf.Trigger{
		{ID: "trigger-channel", Type: wf.TriggerChannel, EntryNode: "agent",
			ChannelName: "slack", Event: "message"},
	}
	prev := []wf.Trigger{
		{ID: "trigger-channel", Type: wf.TriggerChannel, ChannelName: "slack",
			Event: "message", DedupTTLSec: 60, EntryNode: "old-target"},
	}
	got := mergeTriggers(canvas, prev)
	if len(got) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(got))
	}
	tr := got[0]
	if tr.ChannelName != "slack" || tr.Event != "message" {
		t.Errorf("canvas channel/event should survive: %+v", tr)
	}
	if tr.DedupTTLSec != 60 {
		t.Errorf("prev DedupTTLSec should carry over (canvas doesn't model it): got %d", tr.DedupTTLSec)
	}
	if tr.EntryNode != "agent" {
		t.Errorf("canvas EntryNode should win; got %q", tr.EntryNode)
	}
}

// TestMergeTriggersCanvasOverridesPrev — user edits the trigger
// inspector (e.g. switches channel from slack to telegram). Canvas
// must overwrite prev so the new selection sticks on the next load.
func TestMergeTriggersCanvasOverridesPrev(t *testing.T) {
	canvas := []wf.Trigger{
		{ID: "trigger-channel", Type: wf.TriggerChannel, EntryNode: "agent",
			ChannelName: "telegram", Event: "callback_query"},
	}
	prev := []wf.Trigger{
		{ID: "trigger-channel", Type: wf.TriggerChannel, ChannelName: "slack",
			Event: "message", EntryNode: "old"},
	}
	got := mergeTriggers(canvas, prev)
	if got[0].ChannelName != "telegram" {
		t.Errorf("canvas channel switch lost: got %q", got[0].ChannelName)
	}
	if got[0].Event != "callback_query" {
		t.Errorf("canvas event switch lost: got %q", got[0].Event)
	}
}

// TestMergeTriggersDropsRemovedFromCanvas — user removes a trigger
// on the canvas; prev still had it on disk. mergeTriggers must
// honour the canvas as source of truth and drop the removed entry.
func TestMergeTriggersDropsRemovedFromCanvas(t *testing.T) {
	canvas := []wf.Trigger{
		{ID: "trigger-manual", Type: wf.TriggerManual, EntryNode: "x"},
	}
	prev := []wf.Trigger{
		{ID: "trigger-manual", Type: wf.TriggerManual, EntryNode: "x"},
		{ID: "trigger-cron", Type: wf.TriggerCron, Schedule: "0 0 * * *", EntryNode: "y"},
	}
	got := mergeTriggers(canvas, prev)
	if len(got) != 1 || got[0].ID != "trigger-manual" {
		t.Errorf("cron trigger should be dropped (no canvas counterpart); got %+v", got)
	}
}

// TestPickTriggerByID — the picker is the contract between the
// Execute workflow menu and the engine. UI sends trigger_id; server
// must resolve it (or refuse explicitly) so multi-trigger workflows
// route correctly. Covered shapes:
//
//   - empty id + zero triggers → error (no trigger to fire)
//   - empty id + one trigger   → that one wins (legacy single-trigger
//                                YAML keeps the no-arg behaviour)
//   - empty id + many triggers → error (UI must show picker)
//   - explicit id matches      → that one wins regardless of order
//   - explicit id missing      → error (stale UI reference)
func TestPickTriggerByID(t *testing.T) {
	cases := []struct {
		name     string
		triggers []wf.Trigger
		askID    string
		wantID   string
		wantErr  bool
	}{
		{
			name:    "no triggers — refuse",
			wantErr: true,
		},
		{
			name:     "empty id, one trigger — pick it",
			triggers: []wf.Trigger{{ID: "trigger-manual", Type: wf.TriggerManual, EntryNode: "x"}},
			wantID:   "trigger-manual",
		},
		{
			name: "empty id, many triggers — refuse",
			triggers: []wf.Trigger{
				{ID: "trigger-manual", Type: wf.TriggerManual, EntryNode: "x"},
				{ID: "trigger-cron", Type: wf.TriggerCron, EntryNode: "y"},
			},
			wantErr: true,
		},
		{
			name: "explicit id matches",
			triggers: []wf.Trigger{
				{ID: "trigger-manual", Type: wf.TriggerManual, EntryNode: "x"},
				{ID: "trigger-cron", Type: wf.TriggerCron, EntryNode: "y"},
			},
			askID:  "trigger-cron",
			wantID: "trigger-cron",
		},
		{
			name:     "explicit id missing — refuse",
			triggers: []wf.Trigger{{ID: "trigger-manual", Type: wf.TriggerManual}},
			askID:    "trigger-cron",
			wantErr:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := wf.Workflow{Triggers: tc.triggers}
			got, err := pickTriggerByID(w, tc.askID)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (resolved to %+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ID != tc.wantID {
				t.Errorf("picked %q, want %q", got.ID, tc.wantID)
			}
		})
	}
}

// TestDraftEntryWinsOverStalePublished — locks in the fix for the
// production repro: YAML draft says `triggers[0].entry_node: http`,
// but the Router's registered copy is the published version with
// `entry: agent`. Run Now must walk the DRAFT, not the registered
// copy.
//
// The runWorkflowNow handler passes the loaded draft to MCP as an
// explicit `RunNowWith` override; the router worker prefers
// `item.Workflow` over `defs[id]`. This test asserts the data
// layer of that flow: the helpers Run Now relies on resolve from
// the workflow value supplied, not from any stale lookup.
func TestDraftEntryWinsOverStalePublished(t *testing.T) {
	draft := wf.Workflow{
		ID: "t",
		Triggers: []wf.Trigger{
			{ID: "trigger-manual", Type: wf.TriggerManual, EntryNode: "http"},
		},
		Graph: wf.Graph{
			Entry: "http",
			Nodes: []wf.Node{
				{ID: "agent", Type: wf.NodeAgent},
				{ID: "http", Type: wf.NodeHTTP},
			},
		},
	}
	if !triggerHasEntry(draft, wf.TriggerManual) {
		t.Fatalf("draft should accept manual run — wired to http")
	}
	if got := pickGraphEntry(draft); got != "http" {
		t.Errorf("draft pickGraphEntry = %q, want %q", got, "http")
	}
	stale := draft
	stale.Triggers = []wf.Trigger{
		{ID: "trigger-manual", Type: wf.TriggerManual, EntryNode: "agent"},
	}
	stale.Graph.Entry = "agent"
	if got := pickGraphEntry(stale); got != "agent" {
		t.Errorf("stale pickGraphEntry = %q, want %q", got, "agent")
	}
}

// TestTriggerNodesNotInGraphEdges — trigger nodes are visual-only,
// they must NEVER leak into Graph.Edges. The engine routes from
// workflow.Triggers + Graph.Entry, so polluting Graph.Edges with
// trigger sources would break the validator.
func TestTriggerNodesNotInGraphEdges(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"trigger-manual","class":"node-trigger","html":"",
	       "data":{"id":"trigger-manual","type":"trigger","data":{"triggerKind":"manual"}},
	       "pos_x":0,"pos_y":0,
	       "inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"only","class":"node-shell","html":"",
	       "data":{"id":"only","type":"shell","data":{"command":["x"]}},
	       "pos_x":100,"pos_y":100,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{"output_1":{"connections":[]}}}
	}}}}`
	w, err := drawflowJSONToWorkflow("t", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, e := range w.Graph.Edges {
		if strings.HasPrefix(e.From, "trigger-") {
			t.Errorf("trigger edge leaked into Graph.Edges: %+v", e)
		}
	}
}

// TestConnectorArgsRoundtrip — connector nodes carry an `args` map of
// per-input values. The UI renders one form field per declared op
// input; the codec must persist + restore those values through
// Drawflow JSON and back.
func TestConnectorArgsRoundtrip(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"__trigger__","class":"node-trigger","html":"",
	       "data":{"id":"__trigger__","type":"trigger","data":{"kind":"manual"}},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"call","class":"node-connector","html":"",
	       "data":{"id":"call","type":"connector","data":{
	         "module":"slack","op":"send_message",
	         "args":{"channel":"#general","text":"hi"}}},
	       "pos_x":100,"pos_y":200,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{"output_1":{"connections":[]}}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(w.Graph.Nodes) != 1 {
		t.Fatalf("expected 1 wf node, got %d", len(w.Graph.Nodes))
	}
	n := w.Graph.Nodes[0]
	if n.Module != "slack" || n.Op != "send_message" {
		t.Errorf("connector module/op lost: %+v", n)
	}
	if n.Args["channel"] != "#general" || n.Args["text"] != "hi" {
		t.Errorf("args lost: %+v", n.Args)
	}
	// Re-encode and confirm args round-trip to the JSON shape the
	// canvas will read back.
	out, err := workflowToDrawflowJSON(w)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(out, `"#general"`) || !strings.Contains(out, `"hi"`) {
		t.Errorf("args missing from round-tripped json: %s", out)
	}
}

// TestChannelArgsRoundtrip — same contract for channel nodes.
func TestChannelArgsRoundtrip(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"__trigger__","class":"node-trigger","html":"",
	       "data":{"id":"__trigger__","type":"trigger","data":{"kind":"manual"}},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"ping","class":"node-channel","html":"",
	       "data":{"id":"ping","type":"channel","data":{
	         "channel":"slack","op":"send_message",
	         "args":{"channel":"#ops","text":"alert"}}},
	       "pos_x":100,"pos_y":200,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{"output_1":{"connections":[]}}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	n := w.Graph.Nodes[0]
	if n.ChannelName != "slack" || n.Op != "send_message" {
		t.Errorf("channel/op lost: %+v", n)
	}
	if n.Args["channel"] != "#ops" || n.Args["text"] != "alert" {
		t.Errorf("channel args lost: %+v", n.Args)
	}
}

// TestFlowPatternBranchFanIn — classify fans out to 3 cases, all
// merging back into a single end node. Confirms multi-output edges
// (one per case) survive the codec round-trip and the case labels
// land on the right ports.
func TestFlowPatternBranchFanIn(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"__trigger__","class":"node-trigger","html":"",
	       "data":{"id":"__trigger__","type":"trigger","data":{"kind":"manual"}},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"route","class":"node-classify","html":"",
	       "data":{"id":"route","type":"classify","data":{
	         "prompt":"classify",
	         "cases":["bug","question","other"]}},
	       "pos_x":100,"pos_y":100,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{
	         "output_1":{"connections":[{"node":"3","output":"input_1"}]},
	         "output_2":{"connections":[{"node":"3","output":"input_1"}]},
	         "output_3":{"connections":[{"node":"3","output":"input_1"}]}}},
	  "3":{"id":3,"name":"done","class":"node-end","html":"",
	       "data":{"id":"done","type":"end","data":{}},
	       "pos_x":300,"pos_y":300,
	       "inputs":{"input_1":{"connections":[
	         {"node":"2","input":"output_1"},
	         {"node":"2","input":"output_2"},
	         {"node":"2","input":"output_3"}]}},
	       "outputs":{}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(w.Graph.Edges) != 3 {
		t.Fatalf("expected 3 edges (one per case), got %d: %+v", len(w.Graph.Edges), w.Graph.Edges)
	}
	cases := map[string]bool{}
	for _, e := range w.Graph.Edges {
		cases[e.Case] = true
	}
	for _, want := range []string{"bug", "question", "other"} {
		if !cases[want] {
			t.Errorf("case %q missing from edges: %+v", want, w.Graph.Edges)
		}
	}
}

// TestFlowPatternChain — linear chain of nodes (trigger → A → B → C).
// Lock that simple flows don't accidentally lose intermediate edges.
func TestFlowPatternChain(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"__trigger__","class":"node-trigger","html":"",
	       "data":{"id":"__trigger__","type":"trigger","data":{"kind":"manual"}},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"a","class":"node-shell","html":"",
	       "data":{"id":"a","type":"shell","data":{"command":["x"]}},
	       "pos_x":0,"pos_y":100,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{"output_1":{"connections":[{"node":"3","output":"input_1"}]}}},
	  "3":{"id":3,"name":"b","class":"node-shell","html":"",
	       "data":{"id":"b","type":"shell","data":{"command":["y"]}},
	       "pos_x":0,"pos_y":200,
	       "inputs":{"input_1":{"connections":[{"node":"2","input":"output_1"}]}},
	       "outputs":{"output_1":{"connections":[{"node":"4","output":"input_1"}]}}},
	  "4":{"id":4,"name":"c","class":"node-end","html":"",
	       "data":{"id":"c","type":"end","data":{}},
	       "pos_x":0,"pos_y":300,
	       "inputs":{"input_1":{"connections":[{"node":"3","input":"output_1"}]}},
	       "outputs":{}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(w.Graph.Edges) != 2 {
		t.Fatalf("expected 2 chain edges (a→b, b→c), got %d: %+v", len(w.Graph.Edges), w.Graph.Edges)
	}
	want := map[string]string{"a": "b", "b": "c"}
	for _, e := range w.Graph.Edges {
		if want[e.From] != e.To {
			t.Errorf("unexpected edge %s → %s", e.From, e.To)
		}
	}
	if w.Graph.Entry != "a" {
		t.Errorf("expected entry=a, got %q", w.Graph.Entry)
	}
}

// TestCanvasPositionsRoundtrip — node positions saved on the canvas
// survive load (the int-vs-float YAML decode bug had wiped them).
func TestCanvasPositionsRoundtrip(t *testing.T) {
	w := wf.Workflow{
		ID:       "p",
		Triggers: []wf.Trigger{{Type: wf.TriggerManual}},
		Graph: wf.Graph{
			Entry: "n1",
			Nodes: []wf.Node{{ID: "n1", Type: wf.NodeShell, Command: []string{"echo"}}},
		},
		Canvas: map[string]any{
			"positions": map[string]any{
				// YAML decoder yields ints for whole numbers — codec
				// must accept both int and float64.
				"n1": map[string]any{"x": 320, "y": 180},
			},
		},
	}
	got := canvasPositions(w)
	if got["n1"][0] != 320 || got["n1"][1] != 180 {
		t.Errorf("position lost: %+v", got)
	}
}

// TestChannelTriggerFieldsRoundtrip — full round-trip of the new
// trigger inspector data: canvas → workflow.Trigger → canvas JSON.
// Channel + event + match map + match_enabled + __arg_modes must
// all survive both directions so a save/refresh cycle keeps the
// inspector populated.
func TestChannelTriggerFieldsRoundtrip(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"trigger","class":"node-trigger","html":"",
	       "data":{"id":"trigger","type":"trigger","data":{
	         "triggerKind":"channel",
	         "channel":"slack",
	         "event":"message",
	         "match":{"mode":"whitelist","channel_id":"[{\"id\":\"C123\",\"name\":\"general\"}]"},
	         "match_enabled":true,
	         "__match_modes":{"mode":"fixed","channel_id":"fixed"}
	       }},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"end","class":"node-end","html":"",
	       "data":{"id":"end","type":"end","data":{}},
	       "pos_x":200,"pos_y":0,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	tr := findTriggerByType(w, wf.TriggerChannel)
	if tr == nil {
		t.Fatalf("channel trigger missing")
	}
	if tr.ChannelName != "slack" {
		t.Errorf("ChannelName lost: %+v", tr)
	}
	if tr.Event != "message" {
		t.Errorf("Event lost: %+v", tr)
	}
	if !tr.MatchEnabled {
		t.Errorf("MatchEnabled lost")
	}
	if tr.Match["mode"] != "whitelist" {
		t.Errorf("Match[mode] lost: %+v", tr.Match)
	}
	if tr.MatchModes["mode"] != "fixed" {
		t.Errorf("MatchModes[mode] lost: %+v", tr.MatchModes)
	}
	// Re-emit. Canvas inner data must contain the same fields so
	// hydrate restores the inspector on the next open.
	out, err := workflowToDrawflowJSON(w)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, want := range []string{`"slack"`, `"message"`, `"match_enabled":true`, `"whitelist"`, `"__match_modes"`} {
		if !strings.Contains(out, want) {
			t.Errorf("re-emitted JSON missing %s: %s", want, out)
		}
	}
}

// TestCronTriggerFieldsRoundtrip — schedule + timezone survive
// canvas → wf.Trigger → canvas. Regression guard against the bug
// where mergeTriggers used to clobber canvas-driven cron config.
func TestCronTriggerFieldsRoundtrip(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"trigger-cron","class":"node-trigger","html":"",
	       "data":{"id":"trigger-cron","type":"trigger","data":{
	         "triggerKind":"cron",
	         "schedule":"0 */15 * * * *",
	         "timezone":"Asia/Jakarta"
	       }},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"end","class":"node-end","html":"",
	       "data":{"id":"end","type":"end","data":{}},
	       "pos_x":200,"pos_y":0,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	tr := findTriggerByType(w, wf.TriggerCron)
	if tr == nil {
		t.Fatalf("cron trigger missing")
	}
	if tr.Schedule != "0 */15 * * * *" {
		t.Errorf("Schedule lost: %+v", tr)
	}
	if tr.Timezone != "Asia/Jakarta" {
		t.Errorf("Timezone lost: %+v", tr)
	}
	out, err := workflowToDrawflowJSON(w)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, want := range []string{`"0 */15 * * * *"`, `"Asia/Jakarta"`, `"triggerKind":"cron"`} {
		if !strings.Contains(out, want) {
			t.Errorf("re-emitted JSON missing %s: %s", want, out)
		}
	}
}

// TestChannelTriggerMatchValuesRoundtrip — guards the specific user
// scenario where the operator fills the match form (mode=all,
// text_contains=test) and reloads. Without the delegated input
// listener bound on document.body, edits inside the trigger match
// panel never fired updateNodeData, so the form looked blank after
// save+reload. This test covers the codec half — the JS half is
// asserted manually since it depends on DOM event delivery.
func TestChannelTriggerMatchValuesRoundtrip(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"trigger","class":"node-trigger","html":"",
	       "data":{"id":"trigger","type":"trigger","data":{
	         "triggerKind":"channel",
	         "channel":"slack",
	         "event":"message",
	         "match_enabled":true,
	         "match":{"mode":"all","text_contains":"test"}
	       }},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"end","class":"node-end","html":"",
	       "data":{"id":"end","type":"end","data":{}},
	       "pos_x":200,"pos_y":0,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	tr := findTriggerByType(w, wf.TriggerChannel)
	if tr == nil {
		t.Fatalf("channel trigger missing")
	}
	if tr.Match["mode"] != "all" {
		t.Errorf("mode lost: %+v", tr.Match)
	}
	if tr.Match["text_contains"] != "test" {
		t.Errorf("text_contains lost: %+v", tr.Match)
	}
	out, err := workflowToDrawflowJSON(w)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, want := range []string{`"mode":"all"`, `"text_contains":"test"`, `"match_enabled":true`} {
		if !strings.Contains(out, want) {
			t.Errorf("re-emitted JSON missing %s: %s", want, out)
		}
	}
}

// TestWebhookTriggerFieldsRoundtrip — path + method survive canvas
// → wf.Trigger → canvas.
func TestWebhookTriggerFieldsRoundtrip(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"trigger-webhook","class":"node-trigger","html":"",
	       "data":{"id":"trigger-webhook","type":"trigger","data":{
	         "triggerKind":"webhook",
	         "path":"/hooks/pagerduty",
	         "method":"POST"
	       }},
	       "pos_x":0,"pos_y":0,"inputs":{},
	       "outputs":{"output_1":{"connections":[{"node":"2","output":"input_1"}]}}},
	  "2":{"id":2,"name":"end","class":"node-end","html":"",
	       "data":{"id":"end","type":"end","data":{}},
	       "pos_x":200,"pos_y":0,
	       "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	       "outputs":{}}
	}}}}`
	w, err := drawflowJSONToWorkflow("c", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	tr := findTriggerByType(w, wf.TriggerWebhook)
	if tr == nil {
		t.Fatalf("webhook trigger missing")
	}
	if tr.Path != "/hooks/pagerduty" {
		t.Errorf("Path lost: %+v", tr)
	}
	if tr.Method != "POST" {
		t.Errorf("Method lost: %+v", tr)
	}
}


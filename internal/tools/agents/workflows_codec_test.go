package agents

import (
	"strings"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

// TestTriggerFanoutRoundtrip locks in the bug fix where multiple
// trigger → node edges drawn on the canvas survived save + reload.
// Before the fix, only the entry node kept its trigger edge — every
// other fan-out target lost its line on refresh.
func TestTriggerFanoutRoundtrip(t *testing.T) {
	// Drawflow payload: trigger fans out to two regular nodes.
	body := `{
	  "drawflow":{"Home":{"data":{
	    "1":{"id":1,"name":"__trigger__","class":"node-trigger","html":"",
	         "data":{"id":"__trigger__","type":"trigger","data":{"kind":"manual"}},
	         "pos_x":100,"pos_y":50,
	         "inputs":{},
	         "outputs":{"output_1":{"connections":[
	             {"node":"2","output":"input_1"},
	             {"node":"3","output":"input_1"}
	         ]}}},
	    "2":{"id":2,"name":"alpha","class":"node-shell","html":"",
	         "data":{"id":"alpha","type":"shell","data":{"command":["echo","a"]}},
	         "pos_x":300,"pos_y":80,
	         "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	         "outputs":{"output_1":{"connections":[]}}},
	    "3":{"id":3,"name":"beta","class":"node-shell","html":"",
	         "data":{"id":"beta","type":"shell","data":{"command":["echo","b"]}},
	         "pos_x":300,"pos_y":260,
	         "inputs":{"input_1":{"connections":[{"node":"1","input":"output_1"}]}},
	         "outputs":{"output_1":{"connections":[]}}}
	  }}}}`

	w, err := drawflowJSONToWorkflow("t", body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Codec stores the fan-out targets under _canvas.trigger_edges.
	tedges := triggerTargetsFromCanvas(w)
	if len(tedges) != 2 {
		t.Fatalf("expected 2 trigger fan-out targets, got %d (%v)", len(tedges), tedges)
	}
	has := map[string]bool{}
	for _, n := range tedges {
		has[n] = true
	}
	if !has["alpha"] || !has["beta"] {
		t.Errorf("trigger fan-out missing target: %v", tedges)
	}

	// Round-trip back to Drawflow JSON and confirm the phantom
	// trigger still emits two outgoing connections.
	out, err := workflowToDrawflowJSON(w)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(out, `"alpha"`) || !strings.Contains(out, `"beta"`) {
		t.Errorf("round-trip output missing target node names: %s", out)
	}
}

// TestTriggerEdgesNotInGraphEdges — the trigger fan-out lives in
// _canvas.trigger_edges, never in Graph.Edges. The engine routes from
// workflow.Triggers + Graph.Entry, so polluting Graph.Edges with
// trigger sources would break the validator.
func TestTriggerEdgesNotInGraphEdges(t *testing.T) {
	body := `{"drawflow":{"Home":{"data":{
	  "1":{"id":1,"name":"__trigger__","class":"node-trigger","html":"",
	       "data":{"id":"__trigger__","type":"trigger","data":{"kind":"manual"}},
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
		if e.From == "__trigger__" {
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
		Slug:     "p",
		ID:       "id-p",
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

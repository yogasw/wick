package http

import (
	"reflect"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

func TestModuleRegistered(t *testing.T) {
	mod := registry.ByType(wf.NodeHTTP)
	if mod == nil {
		t.Fatal("http module not registered in registry — init() did not run")
	}
	if mod.NodeType() != wf.NodeHTTP {
		t.Errorf("NodeType = %q, want %q", mod.NodeType(), wf.NodeHTTP)
	}
}

func TestRenderShape(t *testing.T) {
	m := &module{}
	r := m.Render()
	if r.Head != "http" || r.CSSType != "http" || r.Inputs != 1 || r.Outputs != 1 {
		t.Errorf("Render = %+v, want head/cssType=http inputs=1 outputs=1", r)
	}
}

func TestPaletteMetadata(t *testing.T) {
	m := &module{}
	if m.PaletteSection() != "Action" {
		t.Errorf("PaletteSection = %q, want Action", m.PaletteSection())
	}
	if got := m.PaletteItem().Type; got != "http" {
		t.Errorf("PaletteItem.Type = %q, want http", got)
	}
}

func TestInspectorScript(t *testing.T) {
	m := &module{}
	if got := m.InspectorScript(); got != "http/inspector.js" {
		t.Errorf("InspectorScript = %q, want http/inspector.js", got)
	}
}

// TestRoundTripFull exercises every field the inspector saves so a
// regression on any field (e.g. dropped headers map) trips the test.
func TestRoundTripFull(t *testing.T) {
	m := &module{}
	orig := wf.Node{
		ID:            "call_api",
		Type:          wf.NodeHTTP,
		URL:           "https://api.example.com/tickets",
		Method:        "POST",
		Headers:       map[string]string{"Content-Type": "application/json", "X-Trace": "abc"},
		Query:         map[string]string{"page": "1", "limit": "10"},
		Body:          `{"title": "{{.Event.Payload.text}}"}`,
		ParseResponse: "json",
		TimeoutSec:    45,
		ArgModes:      map[string]string{"url": "fixed", "body": "expression"},
	}
	inner := m.DrawflowDataFromYAML(orig)
	got := m.YAMLFromDrawflowData(orig.ID, inner)

	if got.ID != orig.ID || got.Type != orig.Type {
		t.Errorf("ID/Type mismatch: got %q/%q want %q/%q", got.ID, got.Type, orig.ID, orig.Type)
	}
	if got.URL != orig.URL {
		t.Errorf("URL = %q, want %q", got.URL, orig.URL)
	}
	if got.Method != orig.Method {
		t.Errorf("Method = %q, want %q", got.Method, orig.Method)
	}
	if !reflect.DeepEqual(got.Headers, orig.Headers) {
		t.Errorf("Headers = %v, want %v", got.Headers, orig.Headers)
	}
	if !reflect.DeepEqual(got.Query, orig.Query) {
		t.Errorf("Query = %v, want %v", got.Query, orig.Query)
	}
	if got.Body != orig.Body {
		t.Errorf("Body = %q, want %q", got.Body, orig.Body)
	}
	if got.ParseResponse != orig.ParseResponse {
		t.Errorf("ParseResponse = %q, want %q", got.ParseResponse, orig.ParseResponse)
	}
	if got.TimeoutSec != orig.TimeoutSec {
		t.Errorf("TimeoutSec = %d, want %d", got.TimeoutSec, orig.TimeoutSec)
	}
	if !reflect.DeepEqual(got.ArgModes, orig.ArgModes) {
		t.Errorf("ArgModes = %v, want %v", got.ArgModes, orig.ArgModes)
	}
}

// TestRoundTripMinimal — empty optional fields must NOT appear as
// noisy keys in the canvas blob; the YAML reverse should drop the
// matching wf.Node fields back to their zero values.
func TestRoundTripMinimal(t *testing.T) {
	m := &module{}
	orig := wf.Node{ID: "h", Type: wf.NodeHTTP, URL: "https://x.test", Method: "GET"}
	inner := m.DrawflowDataFromYAML(orig)
	for _, k := range []string{"headers", "query", "body", "parse_response", "timeout_sec", "__arg_modes"} {
		if _, has := inner[k]; has {
			t.Errorf("inner[%q] present for minimal node — should be omitted", k)
		}
	}
	got := m.YAMLFromDrawflowData(orig.ID, inner)
	if got.URL != orig.URL || got.Method != orig.Method {
		t.Errorf("URL/Method mismatch: got %q/%q want %q/%q", got.URL, got.Method, orig.URL, orig.Method)
	}
	if got.Headers != nil || got.Query != nil {
		t.Errorf("expected nil maps, got headers=%v query=%v", got.Headers, got.Query)
	}
	if got.Body != "" || got.ParseResponse != "" || got.TimeoutSec != 0 {
		t.Errorf("expected zero optionals, got body=%q parse=%q timeout=%d", got.Body, got.ParseResponse, got.TimeoutSec)
	}
}

// TestTimeoutSecCoercion — JSON unmarshalled canvas blobs deliver
// numbers as float64. The codec must coerce that back to int without
// truncation surprises.
func TestTimeoutSecCoercion(t *testing.T) {
	m := &module{}
	cases := []struct {
		name string
		raw  any
		want int
	}{
		{"int", 30, 30},
		{"float64", float64(45), 45},
		{"string-ignored", "60", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := m.YAMLFromDrawflowData("h", map[string]any{"timeout_sec": c.raw})
			if n.TimeoutSec != c.want {
				t.Errorf("TimeoutSec = %d, want %d", n.TimeoutSec, c.want)
			}
		})
	}
}

// TestHeadersFromJSONUnmarshal — when the canvas blob comes off the
// wire as map[string]any (the JSON-decoded shape), stringMap must
// coerce string children and drop non-string values.
func TestHeadersFromJSONUnmarshal(t *testing.T) {
	m := &module{}
	inner := map[string]any{
		"headers": map[string]any{
			"Content-Type": "application/json",
			"X-Trace":      "abc",
			"X-Bogus":      123, // non-string — dropped
		},
	}
	n := m.YAMLFromDrawflowData("h", inner)
	want := map[string]string{"Content-Type": "application/json", "X-Trace": "abc"}
	if !reflect.DeepEqual(n.Headers, want) {
		t.Errorf("Headers = %v, want %v", n.Headers, want)
	}
}

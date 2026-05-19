package nodes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// captureServer returns a stub HTTP server that records the last
// inbound request so tests can assert on the rendered URL / body /
// headers / query. Callers should defer ts.Close().
func captureServer(t *testing.T, captured *http.Request, capturedBody *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		*capturedBody = string(buf)
		// Clone preserves URL + Header into new heap-owned struct so
		// the test reads valid pointers after the handler returns.
		*captured = *r.Clone(context.Background())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
}

func newHTTPRC() *workflow.RunContext {
	return &workflow.RunContext{
		Workflow: workflow.Workflow{ID: "wf-http"},
		RunID:    "run-1",
		Outputs:  map[string]any{},
		Event: workflow.Event{
			Type: "manual",
			Payload: map[string]any{
				"text": "hello world",
				"raw":  "{{.NotATemplate}}",
			},
		},
	}
}

func TestHTTP_DefaultRendersTemplate(t *testing.T) {
	var req http.Request
	var body string
	ts := captureServer(t, &req, &body)
	defer ts.Close()

	exec := NewHTTPExecutor()
	n := workflow.Node{
		Type:   workflow.NodeHTTP,
		URL:    ts.URL + `/echo`,
		Method: "POST",
		Query:  map[string]string{"msg": "{{.Event.Payload.text}}"},
		Body:   `{"t":"{{.Event.Payload.text}}"}`,
	}

	_, err := exec.Execute(context.Background(), n, newHTTPRC())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := req.URL.Query().Get("msg"); got != "hello world" {
		t.Errorf("url query msg = %q, want hello world", got)
	}
	if body != `{"t":"hello world"}` {
		t.Errorf("body = %q, want rendered", body)
	}
}

func TestHTTP_FixedModeSkipsRender(t *testing.T) {
	var req http.Request
	var body string
	ts := captureServer(t, &req, &body)
	defer ts.Close()

	exec := NewHTTPExecutor()
	n := workflow.Node{
		Type:   workflow.NodeHTTP,
		URL:    ts.URL + "/raw",
		Method: "POST",
		Body:   `{"raw": "{{.Event.Payload.raw}}"}`,
		ArgModes: map[string]string{
			"body": "fixed",
		},
	}

	_, err := exec.Execute(context.Background(), n, newHTTPRC())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Fixed mode means the body is sent verbatim — template marker
	// stays untouched.
	want := `{"raw": "{{.Event.Payload.raw}}"}`
	if body != want {
		t.Errorf("body = %q, want %q (raw, no render)", body, want)
	}
}

func TestHTTP_FixedURLSkipsRender(t *testing.T) {
	var req http.Request
	var body string
	ts := captureServer(t, &req, &body)
	defer ts.Close()

	exec := NewHTTPExecutor()
	// URL contains literal template-looking text; fixed mode must
	// pass it through as-is. Use a path that contains "{{" — Go's
	// http client will percent-encode the braces, but the path
	// before encoding must NOT have been template-rendered.
	literalPath := "/raw/%7B%7Bx%7D%7D"
	n := workflow.Node{
		Type:   workflow.NodeHTTP,
		URL:    ts.URL + literalPath,
		Method: "GET",
		ArgModes: map[string]string{
			"url": "fixed",
		},
	}

	_, err := exec.Execute(context.Background(), n, newHTTPRC())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if req.URL.Path != "/raw/{{x}}" {
		t.Errorf("url path = %q, want /raw/{{x}} after url-decode", req.URL.Path)
	}
}

func TestHTTP_FixedHeadersSkipRender(t *testing.T) {
	var req http.Request
	var body string
	ts := captureServer(t, &req, &body)
	defer ts.Close()

	exec := NewHTTPExecutor()
	n := workflow.Node{
		Type:   workflow.NodeHTTP,
		URL:    ts.URL,
		Method: "GET",
		Headers: map[string]string{
			"X-Literal": "{{.Event.Payload.text}}",
		},
		ArgModes: map[string]string{
			"headers": "fixed",
		},
	}

	_, err := exec.Execute(context.Background(), n, newHTTPRC())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := req.Header.Get("X-Literal"); got != "{{.Event.Payload.text}}" {
		t.Errorf("X-Literal header = %q, want raw template (no render)", got)
	}
}

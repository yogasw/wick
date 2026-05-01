package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

func TestNegotiateProtocolVersion(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		want      string
	}{
		{"echo current", "2024-11-05", "2024-11-05"},
		{"echo streamable", "2025-03-26", "2025-03-26"},
		{"unknown falls back to latest", "1999-01-01", latestProtocolVersion},
		{"empty falls back to latest", "", latestProtocolVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := negotiateProtocolVersion(tc.requested); got != tc.want {
				t.Fatalf("negotiateProtocolVersion(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

func TestHandleInitializeEchoesClientVersion(t *testing.T) {
	cases := []struct {
		name     string
		clientPV string
		wantPV   string
	}{
		{"old client", "2024-11-05", "2024-11-05"},
		{"streamable client", "2025-03-26", "2025-03-26"},
		{"unknown client", "9999-99-99", latestProtocolVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + tc.clientPV + `"}}`)
			req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			rr := httptest.NewRecorder()

			h := &Handler{}
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			var resp struct {
				Result initializeResult `json:"result"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v\nbody=%s", err, rr.Body.String())
			}
			if resp.Result.ProtocolVersion != tc.wantPV {
				t.Fatalf("protocolVersion = %q, want %q", resp.Result.ProtocolVersion, tc.wantPV)
			}
			if resp.Result.ServerInfo.Name != "wick" {
				t.Fatalf("serverInfo.name = %q, want %q", resp.Result.ServerInfo.Name, "wick")
			}
		})
	}
}

func TestWantsSSE(t *testing.T) {
	cases := []struct {
		name   string
		accept []string
		want   bool
	}{
		{"empty", nil, false},
		{"json only", []string{"application/json"}, false},
		{"event-stream only", []string{"text/event-stream"}, true},
		{"both in one header", []string{"application/json, text/event-stream"}, true},
		{"both as separate headers", []string{"application/json", "text/event-stream"}, true},
		{"with q weight", []string{"text/event-stream;q=0.9, application/json"}, true},
		{"case-insensitive", []string{"TEXT/EVENT-STREAM"}, true},
		{"surrounding whitespace", []string{"  text/event-stream  "}, true},
		{"unrelated", []string{"text/plain"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			for _, v := range tc.accept {
				req.Header.Add("Accept", v)
			}
			if got := wantsSSE(req); got != tc.want {
				t.Fatalf("wantsSSE = %v, want %v", got, tc.want)
			}
		})
	}
}

// flushRecorder is httptest.ResponseRecorder + Flush. The real one
// doesn't implement http.Flusher, so newSSESession would refuse it.
type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (*flushRecorder) Flush() {}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func TestSSESessionWritesMessageFrame(t *testing.T) {
	rr := newFlushRecorder()
	sess, ok := newSSESession(rr)
	if !ok {
		t.Fatal("newSSESession returned false on flushable writer")
	}
	defer sess.close()

	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rr.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}

	if err := sess.writeMessage(map[string]any{"foo": "bar"}); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}

	body := rr.Body.String()
	want := "event: message\ndata: {\"foo\":\"bar\"}\n\n"
	if body != want {
		t.Fatalf("frame = %q, want %q", body, want)
	}
}

func TestSSESessionWritesKeepalive(t *testing.T) {
	rr := newFlushRecorder()
	sess, _ := newSSESession(rr)
	defer sess.close()

	sess.writeKeepalive()
	if got := rr.Body.String(); got != ": keepalive\n\n" {
		t.Fatalf("keepalive frame = %q", got)
	}
}

// plainWriter implements http.ResponseWriter but NOT http.Flusher,
// so newSSESession should refuse it.
type plainWriter struct {
	hdr http.Header
}

func (p *plainWriter) Header() http.Header        { return p.hdr }
func (p *plainWriter) Write(b []byte) (int, error) { return len(b), nil }
func (*plainWriter) WriteHeader(int)               {}

func TestSSESessionRefusesUnflushableWriter(t *testing.T) {
	if _, ok := newSSESession(&plainWriter{hdr: http.Header{}}); ok {
		t.Fatal("newSSESession should refuse a non-flushable writer")
	}
}

func TestSSESessionWriteAfterCloseFails(t *testing.T) {
	rr := newFlushRecorder()
	sess, _ := newSSESession(rr)
	sess.close()
	if err := sess.writeMessage(map[string]any{"x": 1}); err == nil {
		t.Fatal("writeMessage after close: want error, got nil")
	}
}

func TestChannelReporterDropsWhenFull(t *testing.T) {
	ch := make(chan progressEvent, 1)
	r := &channelReporter{ch: ch}

	r.Report(1, 10, "first")  // fills buffer
	r.Report(2, 10, "second") // dropped, must not block
	r.Report(3, 10, "third")  // dropped, must not block

	got := <-ch
	if got.progress != 1 || got.message != "first" {
		t.Fatalf("first event = %+v, want progress=1 message=first", got)
	}
	select {
	case extra := <-ch:
		t.Fatalf("expected channel empty after drain, got %+v", extra)
	default:
	}
}

func TestProgressNotifShape(t *testing.T) {
	cases := []struct {
		name string
		ev   progressEvent
		want string
	}{
		{
			"progress only",
			progressEvent{progress: 1},
			`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1,"progressToken":"tok"}}`,
		},
		{
			"with total",
			progressEvent{progress: 3, total: 10},
			`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":3,"progressToken":"tok","total":10}}`,
		},
		{
			"with message",
			progressEvent{progress: 5, total: 10, message: "halfway"},
			`{"jsonrpc":"2.0","method":"notifications/progress","params":{"message":"halfway","progress":5,"progressToken":"tok","total":10}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := progressNotif(json.RawMessage(`"tok"`), tc.ev)
			got, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("notif = %s\n  want %s", got, tc.want)
			}
		})
	}
}

func TestBufferingWriterCapturesBody(t *testing.T) {
	buf := newBufferingWriter()
	buf.Header().Set("X-Whatever", "ignored")
	buf.WriteHeader(http.StatusTeapot)
	if _, err := buf.Write([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.body.String() != `{"a":1}` {
		t.Fatalf("body = %q", buf.body.String())
	}
	if buf.code != http.StatusTeapot {
		t.Fatalf("code = %d", buf.code)
	}
}

// TestServeHTTPRoutesToSSEForToolsCall verifies the Accept-based
// branch in ServeHTTP: when the client lists text/event-stream and
// asks for tools/call, the response is framed as SSE. We use an
// unknown tool name so the handler hits its default-branch error
// path, which lets us run with a nil connectors service.
func TestServeHTTPRoutesToSSEForToolsCall(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req = req.WithContext(login.WithUser(req.Context(), &entity.User{ID: "u1"}, nil))

	rr := httptest.NewRecorder()
	(&Handler{}).ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream\nbody=%s", got, rr.Body.String())
	}
	out := rr.Body.String()
	if !strings.HasPrefix(out, "event: message\ndata: ") {
		t.Fatalf("missing SSE frame prefix:\n%s", out)
	}
	if !strings.Contains(out, `"error":`) {
		t.Fatalf("expected error envelope in frame:\n%s", out)
	}
	if !strings.Contains(out, "unknown tool") {
		t.Fatalf("expected 'unknown tool' message in frame:\n%s", out)
	}
}

// TestServeHTTPUsesJSONWhenAcceptOmitsSSE verifies the JSON path is
// kept for clients that don't opt into streaming.
func TestServeHTTPUsesJSONWhenAcceptOmitsSSE(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(login.WithUser(req.Context(), &entity.User{ID: "u1"}, nil))

	rr := httptest.NewRecorder()
	(&Handler{}).ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if strings.Contains(rr.Body.String(), "event:") {
		t.Fatalf("JSON path should not emit SSE frames:\n%s", rr.Body.String())
	}
}

// TestServeHTTPInitializeAlwaysJSON ensures initialize ignores Accept
// — streaming has no benefit for handshake and the spec lets us
// pick JSON regardless.
func TestServeHTTPInitializeAlwaysJSON(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")

	rr := httptest.NewRecorder()
	(&Handler{}).ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestHandleInitializeWithoutParams(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	(&Handler{}).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp struct {
		Result initializeResult `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result.ProtocolVersion != latestProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q", resp.Result.ProtocolVersion, latestProtocolVersion)
	}
}

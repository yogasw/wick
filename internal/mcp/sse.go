package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

// sseHeartbeatInterval controls how often we emit ":keepalive" comment
// frames during an in-flight tool call. 15s is comfortably below the
// idle-kill thresholds of common reverse proxies (nginx 60s, cloudflare
// 100s) without saturating the wire on long calls.
const sseHeartbeatInterval = 15 * time.Second

// sseProgressBufferSize caps the in-memory queue of progress events
// waiting to be framed. A slow client (or a connector that fires a
// burst of progress) past this cap drops events instead of blocking the
// connector — see channelReporter.Report for the policy.
const sseProgressBufferSize = 32

// sseExecuteTimeout caps how long one wick_execute call may take. It
// guards against a connector that hangs waiting on a slow upstream:
// without this, a stuck upstream would keep the goroutine alive
// forever (and accumulate one such goroutine per request even after
// the client disconnects). 5 minutes is generous for normal API calls
// while still bounding worst-case bleed. Per-tool timeouts can be
// added later as a connector-level config when individual operations
// need shorter or longer ceilings.
const sseExecuteTimeout = 5 * time.Minute

// Streamable HTTP (MCP spec 2025-03-26) lets the server respond to a
// POST /mcp with a text/event-stream body instead of a single JSON
// document. The body is one or more SSE frames:
//
//	event: message
//	data: {"jsonrpc":"2.0",...}
//
// Per spec the final frame is the JSON-RPC response for the request id;
// preceding frames may be related notifications (e.g. progress) or
// server→client requests. After the final frame the server closes the
// stream. Comment lines (": ...") are SSE keepalives and are ignored
// by clients — wick uses them to prevent reverse-proxy idle timeouts.

// wantsSSE reports whether the client opted into the Streamable HTTP
// SSE response by listing text/event-stream in its Accept header.
//
// Per the 2025-03-26 spec the client SHOULD send "Accept: application/
// json, text/event-stream" — listing both lets the server choose. Wick
// chooses SSE only when the method actually benefits from streaming
// (currently just tools/call); other methods always reply JSON.
func wantsSSE(r *http.Request) bool {
	for _, raw := range r.Header.Values("Accept") {
		for _, part := range strings.Split(raw, ",") {
			// Strip ";q=…" weight and surrounding whitespace.
			mime, _, _ := strings.Cut(strings.TrimSpace(part), ";")
			if strings.EqualFold(strings.TrimSpace(mime), "text/event-stream") {
				return true
			}
		}
	}
	return false
}

// sseSession is the thread-safe writer for one SSE response. The
// Streamable HTTP flow has two writers: the goroutine running the tool
// (which emits progress events through the reporter) and the dispatch
// loop (which writes heartbeats and the final result). Without the
// mutex they would interleave bytes mid-frame and corrupt the stream.
type sseSession struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
	closed  bool
}

// newSSESession writes the SSE response headers and flushes them so the
// client unblocks its read. Returns false when the underlying writer
// can't flush — callers should fall back to JSON in that (rare) case.
//
// X-Accel-Buffering: no disables nginx response buffering, which would
// otherwise hold every frame until the request finishes and defeat the
// point of streaming. Cache-Control: no-cache is required by the SSE
// spec; Connection: keep-alive is the conventional hint.
func newSSESession(w http.ResponseWriter) (*sseSession, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &sseSession{w: w, flusher: flusher}, true
}

// writeMessage frames a JSON-RPC payload as one SSE "message" event and
// flushes it. Returns the underlying write error so the caller can stop
// emitting further frames after a client disconnect.
func (s *sseSession) writeMessage(payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sse marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("sse session closed")
	}
	if _, err := fmt.Fprintf(s.w, "event: message\ndata: %s\n\n", body); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// writeKeepalive emits an SSE comment frame. Clients ignore it; reverse
// proxies treat it as activity so the connection isn't reaped during
// long executions.
func (s *sseSession) writeKeepalive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	_, _ = fmt.Fprint(s.w, ": keepalive\n\n")
	s.flusher.Flush()
}

// close marks the session as no-write. Used after the final frame so
// late-arriving progress events (delivered after Execute returns but
// before the goroutine exits) don't try to write to a finished stream.
func (s *sseSession) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// progressEvent buffers one connector.Ctx.ReportProgress call until the
// SSE dispatch loop can frame and flush it.
type progressEvent struct {
	progress int
	total    int
	message  string
}

// channelReporter implements connector.ProgressReporter by enqueueing
// events on a buffered channel. Drops events when the channel is full
// — the contract is that ReportProgress never blocks the connector,
// even if the client has stopped reading.
type channelReporter struct {
	ch chan<- progressEvent
}

func (r *channelReporter) Report(progress, total int, message string) {
	select {
	case r.ch <- progressEvent{progress: progress, total: total, message: message}:
	default:
		// Buffer full — drop. The client is slow or gone; we'd rather
		// lose a tick than block the connector.
	}
}

// writeRawMessage frames already-marshaled JSON-RPC bytes as one SSE
// event. Used by the static-tool path that captures the existing JSON
// handler's output through bufferingWriter — re-marshaling would be
// wasted work.
func (s *sseSession) writeRawMessage(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("sse session closed")
	}
	if _, err := fmt.Fprintf(s.w, "event: message\ndata: %s\n\n", payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// bufferingWriter is a tiny http.ResponseWriter that captures the body
// in memory. Used to reuse the existing JSON-handler functions
// (handleWickList / handleWickSearch / handleWickGet) under the SSE
// path: run them against this buffer, then take the captured JSON body
// and frame it as one SSE event. Avoids duplicating their auth and
// schema-shaping logic in two parallel code paths.
type bufferingWriter struct {
	h    http.Header
	code int
	body bytes.Buffer
}

func newBufferingWriter() *bufferingWriter {
	return &bufferingWriter{h: http.Header{}, code: http.StatusOK}
}

func (b *bufferingWriter) Header() http.Header         { return b.h }
func (b *bufferingWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferingWriter) WriteHeader(c int)           { b.code = c }

// ── SSE dispatch for tools/call ─────────────────────────────────────

// handleToolsCallSSE is the Streamable HTTP path for tools/call. The
// fast meta-tools (wick_list/search/get) buffer their existing JSON
// response and emit a single SSE frame; wick_execute runs Execute on a
// goroutine so progress events can interleave with heartbeats and the
// final response frame.
func (h *Handler) handleToolsCallSSE(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	user := login.GetUser(r.Context())
	if user == nil {
		writeRPCError(w, req.ID, errInternal, "no authenticated user on context", nil)
		return
	}
	tagIDs := login.GetUserTagIDs(r.Context())

	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, errInvalidParams, "invalid params: "+err.Error(), nil)
		return
	}

	sess, ok := newSSESession(w)
	if !ok {
		// http.ResponseWriter doesn't support flushing — fall back to
		// the JSON path so the request still completes. This is rare
		// (only happens behind transports that buffer aggressively).
		h.handleToolsCall(w, r, req)
		return
	}
	defer sess.close()

	switch p.Name {
	case "wick_list":
		h.sseStaticTool(sess, func(buf *bufferingWriter) {
			h.handleWickList(buf, r, req, tagIDs, user.IsAdmin())
		})
	case "wick_search":
		h.sseStaticTool(sess, func(buf *bufferingWriter) {
			h.handleWickSearch(buf, r, req, p.Arguments, tagIDs, user.IsAdmin())
		})
	case "wick_get":
		h.sseStaticTool(sess, func(buf *bufferingWriter) {
			h.handleWickGet(buf, r, req, p.Arguments, tagIDs, user.IsAdmin())
		})
	case "wick_execute":
		h.sseWickExecute(sess, r, req, p, user, tagIDs)
	default:
		_ = sess.writeMessage(rpcErrorResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   rpcError{Code: errInvalidParams, Message: "unknown tool: " + p.Name},
		})
	}
}

// sseStaticTool runs a JSON-shaped handler against an in-memory buffer
// and frames the captured response as one SSE event. Strips the
// trailing newline json.NewEncoder writes so the SSE data field is
// exactly the JSON-RPC envelope.
func (h *Handler) sseStaticTool(sess *sseSession, run func(*bufferingWriter)) {
	buf := newBufferingWriter()
	run(buf)
	body := bytes.TrimRight(buf.body.Bytes(), "\n")
	_ = sess.writeRawMessage(body)
}

// sseWickExecute is the only path that emits more than one frame. It
// runs Execute on a goroutine so the dispatch loop can interleave
// progress events (when the client supplied a progressToken),
// heartbeats, and the final response.
func (h *Handler) sseWickExecute(sess *sseSession, r *http.Request, req rpcRequest, p toolCallParams, user *entity.User, tagIDs []string) {
	args := p.Arguments
	toolID, _ := args["tool_id"].(string)
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		sseWriteToolError(sess, req, "tool_id is required", "")
		return
	}

	connectorID, opKey, err := parseToolID(toolID)
	if err != nil {
		sseWriteToolError(sess, req, err.Error(), toolID)
		return
	}

	allowed, err := h.connectors.IsVisibleTo(r.Context(), connectorID, tagIDs, user.IsAdmin())
	if err != nil || !allowed {
		sseWriteToolError(sess, req, "tool_id not found or not accessible", toolID)
		return
	}

	rawParams, _ := args["params"].(map[string]any)
	input := stringifyArgs(rawParams)

	// Only attach a reporter when the client supplied a progressToken.
	// Without a token, the spec gives clients nothing to correlate
	// progress notifs against, so emitting them is wasted bandwidth.
	var progressToken json.RawMessage
	if p.Meta != nil {
		progressToken = p.Meta.ProgressToken
	}
	progressCh := make(chan progressEvent, sseProgressBufferSize)
	var reporter connector.ProgressReporter
	if len(progressToken) > 0 {
		reporter = &channelReporter{ch: progressCh}
	}

	// Wrap the request context with our execution ceiling. The goroutine
	// passes this derived ctx to Execute, so when the deadline fires the
	// connector's outbound HTTP calls (provided they use NewRequestWith-
	// Context — see audit notes in pkg/connector/connector.go) cancel
	// promptly and the goroutine winds down instead of leaking.
	execCtx, cancelExec := context.WithTimeout(r.Context(), sseExecuteTimeout)
	defer cancelExec()

	type execOut struct {
		res *connectors.ExecuteResult
		err error
	}
	resCh := make(chan execOut, 1)
	go func() {
		res, err := h.connectors.Execute(execCtx, connectors.ExecuteParams{
			ConnectorID:  connectorID,
			OperationKey: opKey,
			Input:        input,
			Source:       entity.ConnectorRunSourceMCP,
			UserID:       user.ID,
			IPAddress:    clientIP(r),
			UserAgent:    r.Header.Get("User-Agent"),
			Progress:     reporter,
		})
		resCh <- execOut{res: res, err: err}
	}()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-execCtx.Done():
			// Two things land here: our timeout firing, or the request
			// context cancelling because the client disconnected. We
			// distinguish by inspecting r.Context() directly — if it
			// is still alive, the deadline must be ours.
			if r.Context().Err() != nil {
				// Client gone — writer is dead, no point framing
				// anything. The Execute goroutine sees the cancel via
				// execCtx and unwinds on its own (assuming the
				// connector respects its ctx).
				return
			}
			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				sseWriteToolError(sess, req,
					fmt.Sprintf("tool execution exceeded %s timeout", sseExecuteTimeout),
					toolID)
			}
			return
		case <-heartbeat.C:
			sess.writeKeepalive()
		case ev := <-progressCh:
			// A failed write means the client hung up mid-stream. Bail
			// — any further frames would just thrash a dead writer
			// (and burn CPU re-marshalling for nobody).
			if err := sess.writeMessage(progressNotif(progressToken, ev)); err != nil {
				return
			}
		case out := <-resCh:
			// Drain any progress events that landed between Execute
			// returning and resCh receiving. Without this, late
			// notifications would be lost when the session closes.
			for drain := true; drain; {
				select {
				case ev := <-progressCh:
					if err := sess.writeMessage(progressNotif(progressToken, ev)); err != nil {
						return
					}
				default:
					drain = false
				}
			}
			if out.err != nil {
				body := out.err.Error()
				if out.res != nil && out.res.ResponseJSON != "" {
					body = out.res.ResponseJSON
				}
				sseWriteToolError(sess, req, body, toolID)
				return
			}
			_ = sess.writeMessage(rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: toolCallResult{
					Content: []toolContent{{Type: "text", Text: out.res.ResponseJSON}},
					IsError: false,
				},
			})
			return
		}
	}
}

// progressNotif builds a notifications/progress JSON-RPC message per
// the 2025-03-26 spec. Omits "total" and "message" when unset to keep
// the payload terse — clients render the progressToken as a spinner
// when total is absent, and skip the label when message is empty.
func progressNotif(token json.RawMessage, ev progressEvent) any {
	params := map[string]any{
		"progressToken": token,
		"progress":      ev.progress,
	}
	if ev.total > 0 {
		params["total"] = ev.total
	}
	if ev.message != "" {
		params["message"] = ev.message
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params":  params,
	}
}

// sseWriteToolError emits the SSE-equivalent of writeToolError — a
// final tools/call response frame with isError=true and a JSON-shaped
// body the LLM can read and try to recover from.
func sseWriteToolError(sess *sseSession, req rpcRequest, message, toolID string) {
	body := map[string]string{"error": message}
	if toolID != "" {
		body["tool_id"] = toolID
	}
	bodyBytes, _ := json.Marshal(body)
	_ = sess.writeMessage(rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: toolCallResult{
			Content: []toolContent{{Type: "text", Text: string(bodyBytes)}},
			IsError: true,
		},
	})
}

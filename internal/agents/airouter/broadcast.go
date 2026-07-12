package airouter

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// broadcaster is a fan-out hub for live API-request events. It holds no
// history: an event is delivered to whatever subscribers are connected at
// the moment it is published, then dropped. Nothing is written to disk or
// kept in a ring buffer — when no browser is watching, requests are simply
// proxied to the router and never captured at all (see hasSubscribers).
//
// Each subscriber is the Requests tab of an open AI Router page, connected
// over Server-Sent Events. Closing the tab removes the subscriber; the full
// request/response bodies live only in that browser's memory for as long as
// the tab is open.
type broadcaster struct {
	mu     sync.Mutex
	subs   map[int]chan ReqEvent
	nextID int
	count  atomic.Int32 // mirror of len(subs) for a lock-free fast path
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: make(map[int]chan ReqEvent)}
}

// ReqEvent is one proxied API request, streamed live to subscribers. It
// carries the FULL request and response bodies (never truncated) — they are
// held only in the receiving browser, so size is the client's concern, not
// the server's. Field names are the JSON keys the UI reads.
type ReqEvent struct {
	Time       string `json:"time"`        // wall-clock "15:04:05" of when the request landed
	Method     string `json:"method"`      // HTTP method
	Path       string `json:"path"`        // request path (already stripped of the /airouter/<id> prefix)
	Host       string `json:"host"`        // inbound Host header
	RemoteAddr string `json:"remote_addr"` // TCP peer as wick saw it
	ClientIP   string `json:"client_ip"`   // best-effort real client IP
	External   bool   `json:"external"`    // true when the caller reached us from off-machine
	Auth       string `json:"auth"`        // redacted Authorization / x-api-key ("" when none)
	UserAgent  string `json:"user_agent"`  // caller UA
	Model      string `json:"model"`       // model name sniffed from the request body ("" when unknown)
	Status     int    `json:"status"`      // upstream response status
	DurationMS int64  `json:"duration_ms"` // round-trip duration in milliseconds
	ReqBody    string `json:"req_body"`    // FULL request body
	RespBody   string `json:"resp_body"`   // FULL response body
}

// subChanBuffer bounds how many events a slow subscriber may queue before we
// drop new ones for it. A watching browser reads promptly; this only guards
// against a stalled client wedging the publisher.
const subChanBuffer = 64

// subscribe registers a new subscriber and returns its channel plus an
// unsubscribe func. The caller (the SSE handler) drains the channel until
// the client disconnects, then calls unsubscribe.
func (b *broadcaster) subscribe() (<-chan ReqEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan ReqEvent, subChanBuffer)
	b.subs[id] = ch
	b.count.Store(int32(len(b.subs)))
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
			b.count.Store(int32(len(b.subs)))
		}
	}
}

// hasSubscribers reports whether any browser is currently watching. The
// proxy consults this BEFORE capturing bodies: with no watcher, the request
// is proxied untouched and nothing is buffered. Lock-free.
func (b *broadcaster) hasSubscribers() bool { return b.count.Load() > 0 }

// publish delivers e to every current subscriber, non-blocking. A subscriber
// whose buffer is full misses this event rather than stalling the publisher
// (the browser will still get subsequent events).
func (b *broadcaster) publish(e ReqEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // slow subscriber — drop this event for it
		}
	}
}

// ── small helpers shared by the proxy capture path ──────────────────────

// redactAuth turns a raw Authorization/api-key value into a safe preview
// that shows only enough to identify the key, never the full secret.
// "Bearer sk_9router_abcdef" -> "sk_9r…". Empty in, empty out.
func redactAuth(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if i := strings.IndexByte(v, ' '); i >= 0 {
		v = strings.TrimSpace(v[i+1:])
	}
	if v == "" {
		return ""
	}
	const keep = 5
	if len(v) <= keep {
		return v + "…"
	}
	return v[:keep] + "…"
}

// authFromRequest pulls the caller's credential from the standard header
// spots routers honour (Authorization: Bearer, x-api-key, x-goog-api-key)
// and returns it redacted.
func authFromRequest(r *http.Request) string {
	if a := r.Header.Get("Authorization"); a != "" {
		return redactAuth(a)
	}
	if a := r.Header.Get("x-api-key"); a != "" {
		return redactAuth(a)
	}
	if a := r.Header.Get("x-goog-api-key"); a != "" {
		return redactAuth(a)
	}
	return ""
}

// sniffModel best-effort extracts the "model" field from a JSON request body
// without a full parse — cheap enough to run on every request. Returns ""
// when not found (e.g. non-JSON or a form we don't recognise).
func sniffModel(body []byte) string {
	s := string(body)
	i := strings.Index(s, `"model"`)
	if i < 0 {
		return ""
	}
	rest := s[i+len(`"model"`):]
	j := 0
	for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t' || rest[j] == ':') {
		j++
	}
	if j >= len(rest) || rest[j] != '"' {
		return ""
	}
	rest = rest[j+1:]
	if end := strings.IndexByte(rest, '"'); end >= 0 {
		return rest[:end]
	}
	return ""
}

// captureWriter wraps a ResponseWriter to record the upstream status and a
// FULL copy of the response body while streaming it through to the client
// untouched. It flushes on every Write so SSE token streams still reach the
// caller in real time. Only used when a subscriber is watching.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   []byte
	wrote  bool
}

func (c *captureWriter) WriteHeader(status int) {
	if !c.wrote {
		c.status = status
		c.wrote = true
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if !c.wrote {
		c.wrote = true
	}
	c.body = append(c.body, p...)
	n, err := c.ResponseWriter.Write(p)
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}

// Flush proxies through so httputil.ReverseProxy's own flushing works.
func (c *captureWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// isLoopbackHost reports whether hostport (a Host header or RemoteAddr, with
// or without a port) refers to the loopback interface.
func isLoopbackHost(hostport string) bool {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

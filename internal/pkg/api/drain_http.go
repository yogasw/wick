package api

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// httpInflight counts the requests this process is still handling, so a
// graceful upgrade waits for them to finish instead of cutting them off when
// a shutdown deadline expires. A click that POSTs a config change is exactly
// as unfinished as a cron job mid-write; the drain should treat it that way.
//
// Streams are deliberately NOT counted. An SSE response does not "finish"
// while the client is watching it, so waiting for one would mean the process
// never settles. A request drops out of the count the moment it declares
// itself a stream — Content-Type: text/event-stream — or hijacks the
// connection for a websocket. Those connections are dropped at the very end
// of the drain, once everything else is done, and the browser reconnects to
// the successor.
type httpInflight struct {
	mu   sync.Mutex
	seq  uint64
	reqs map[uint64]string // id -> "METHOD /path"
}

func newHTTPInflight() *httpInflight {
	return &httpInflight{reqs: map[uint64]string{}}
}

// Count is how many non-stream requests are in flight right now.
func (h *httpInflight) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.reqs)
}

// Names lists them as "METHOD /path", so a drain log line says which request
// is holding the handover rather than just how many.
func (h *httpInflight) Names() []string {
	h.mu.Lock()
	out := make([]string, 0, len(h.reqs))
	for _, v := range h.reqs {
		out = append(out, v)
	}
	h.mu.Unlock()
	sort.Strings(out)
	return out
}

func (h *httpInflight) add(method, path string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	id := h.seq
	h.reqs[id] = method + " " + path
	return id
}

func (h *httpInflight) done(id uint64) {
	h.mu.Lock()
	delete(h.reqs, id)
	h.mu.Unlock()
}

// middleware registers each request for the duration of its handler.
func (h *httpInflight) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipDrainCount(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		id := h.add(r.Method, r.URL.Path)
		var once sync.Once
		release := func() { once.Do(func() { h.done(id) }) }
		defer release()
		next.ServeHTTP(&drainWriter{ResponseWriter: w, release: release}, r)
	})
}

// skipDrainCount excludes the endpoints that exist to OBSERVE the drain. A
// status poll that counts itself as work would show the process as busy for
// as long as somebody keeps the page open, and the wait would never settle.
func skipDrainCount(path string) bool {
	switch path {
	case "/health", "/boot-status", "/admin/advanced/software-update/serving":
		return true
	}
	// Static asset trees are served from embed.FS and depend on nothing a
	// drain waits for. Counting them is technically true and practically
	// noise: a page load turns the outstanding list into a wall of fonts and
	// scripts, which buries the agent turn or workflow run that actually
	// matters.
	return strings.HasPrefix(path, "/public/") || strings.HasPrefix(path, "/modules/")
}

// drainWriter releases a request from the in-flight set as soon as it turns
// out to be a stream. Mirrors responseWriter's wrapper contract: Unwrap for
// http.NewResponseController, Hijack for websocket upgraders that type-assert.
type drainWriter struct {
	http.ResponseWriter
	release func()
	checked bool
}

// checkStream runs once, on the first write: by then the handler has set its
// Content-Type, which is the only honest way to know a stream from a normal
// response without keeping a list of paths that would go stale.
func (w *drainWriter) checkStream() {
	if w.checked {
		return
	}
	w.checked = true
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		w.release()
	}
}

func (w *drainWriter) WriteHeader(code int) {
	w.checkStream()
	w.ResponseWriter.WriteHeader(code)
}

func (w *drainWriter) Write(b []byte) (int, error) {
	w.checkStream()
	return w.ResponseWriter.Write(b)
}

func (w *drainWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush is how an SSE handler usually commits its headers: set Content-Type,
// WriteHeader, Flush — and some flush without ever writing a body through
// this wrapper. Without this hook such a stream stayed in the in-flight set
// for as long as the browser watched it, and showed up in the drain list as
// work that would never finish.
func (w *drainWriter) Flush() {
	w.checkStream()
	//nolint:errcheck // a non-flushable writer is not an error worth surfacing here
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *drainWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
	}
	// A hijacked connection (websocket) is a stream by definition: it lives
	// as long as the client keeps it, so it must not hold the handover.
	w.release()
	return hj.Hijack()
}

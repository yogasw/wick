package agents

import (
	"io/fs"
	"net/http"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

// recordingRouter captures the paths Register declares, so a test can
// assert the route table rather than probe HTTP.
//
// An HTTP probe cannot do this job here: the agents auth gate runs before
// routing and answers 403 for registered and unregistered paths alike, so
// a passing probe would prove nothing about whether the route exists.
type recordingRouter struct{ seen map[string]bool }

func (r *recordingRouter) mark(method, path string) {
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[method+" "+path] = true
}

func (r *recordingRouter) GET(p string, _ tool.HandlerFunc)                       { r.mark("GET", p) }
func (r *recordingRouter) POST(p string, _ tool.HandlerFunc)                      { r.mark("POST", p) }
func (r *recordingRouter) PUT(p string, _ tool.HandlerFunc)                       { r.mark("PUT", p) }
func (r *recordingRouter) DELETE(p string, _ tool.HandlerFunc)                    { r.mark("DELETE", p) }
func (r *recordingRouter) PATCH(p string, _ tool.HandlerFunc)                     { r.mark("PATCH", p) }
func (r *recordingRouter) Use(string, tool.Middleware)                            {}
func (r *recordingRouter) HandleRaw(string, func(tool.ConfigReader) http.Handler) {}
func (r *recordingRouter) Static(string, fs.FS)                                   {}
func (r *recordingRouter) Meta() tool.Tool                                        { return tool.Tool{} }

// The Resources page and its APIs must actually be registered — a missing
// route only shows up as a 404 in production, long after this change.
func TestResourceRoutesRegistered(t *testing.T) {
	rec := &recordingRouter{}
	Register(rec)

	for _, want := range []string{
		"GET /resources",
		"GET /api/memory",
		"GET /api/memory/series",
		"GET /api/processes",
		"POST /api/memory/apply-suggested",
	} {
		if !rec.seen[want] {
			t.Fatalf("route %q was never registered", want)
		}
	}
}

package hostapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/internal/login"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// Rest limits, the same on both doors: a request body of at most
// RestMaxRequestBytes, a buffered response of at most RestMaxResponseBytes,
// at most RestPerPlugin calls in flight per plugin (the responses are held
// in memory), RestDefaultTimeout when the call has no deadline and never
// more than RestMaxTimeout.
const (
	RestMaxRequestBytes  = 16 << 20
	RestMaxResponseBytes = 64 << 20
	RestPerPlugin        = 4
	RestDefaultTimeout   = 30 * time.Second
	RestMaxTimeout       = 60 * time.Second
)

// restDroppedHeaders are request headers a plugin never gets to set: wick
// stamps the identity itself, and client-address headers would let a plugin
// pose as another origin.
var restDroppedHeaders = map[string]bool{
	"Authorization": true, "Proxy-Authorization": true, "Cookie": true,
	"Host": true, "Forwarded": true, "X-Real-Ip": true,
	"Connection": true, "Keep-Alive": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Content-Length": true,
}

func restHeaderDropped(k string) bool {
	k = http.CanonicalHeaderKey(k)
	return restDroppedHeaders[k] || strings.HasPrefix(k, "X-Wick-") || strings.HasPrefix(k, "X-Forwarded-")
}

// checkRestPath validates a Rest path and splits off its query. Only
// /api/... and /manager/api/... are reachable; dot segments (plain or
// percent-encoded), encoded slashes or backslashes, empty segments,
// fragments and anything that is not a plain absolute path are refused,
// never normalised.
func checkRestPath(p string) (path, rawQuery string, err error) {
	if p == "" || p[0] != '/' || strings.HasPrefix(p, "//") {
		return "", "", fmt.Errorf("path must be an absolute wick path")
	}
	if strings.ContainsAny(p, "\\#") || strings.Contains(p, "://") {
		return "", "", fmt.Errorf("path must not contain a backslash, fragment or scheme")
	}
	for _, r := range p {
		if r < 0x21 || r == 0x7f {
			return "", "", fmt.Errorf("path must not contain spaces or control characters")
		}
	}
	path, rawQuery, _ = strings.Cut(p, "?")
	low := strings.ToLower(path)
	for _, bad := range []string{"%2e", "%2f", "%5c", "%00"} {
		if strings.Contains(low, bad) {
			return "", "", fmt.Errorf("path must not contain an encoded dot, slash, backslash or NUL")
		}
	}
	if _, err := url.PathUnescape(path); err != nil {
		return "", "", fmt.Errorf("path has a bad escape")
	}
	if !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/manager/api/") {
		return "", "", fmt.Errorf("only /api/ and /manager/api/ paths are reachable")
	}
	if wickplugin.RestPathReserved(path) {
		return "", "", fmt.Errorf("plugin management paths are not reachable from a plugin")
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", "", fmt.Errorf("path must not contain empty or dot segments")
		}
	}
	return path, rawQuery, nil
}

// restItem returns the admin item a call to path reaches: the first
// declared pattern of the same method (HEAD also matches GET) that matches,
// else the concrete "METHOD path", which Decide reports as not declared.
func restItem(p PluginInfo, method, path string) string {
	if p.Resources != nil {
		for _, x := range p.Resources.Rest {
			m := strings.ToUpper(strings.TrimSpace(x.Method))
			if (m == method || (method == http.MethodHead && m == http.MethodGet)) && wickplugin.MatchRestPattern(x.Path, path) {
				return wickplugin.RestItem(x.Method, x.Path)
			}
		}
	}
	return method + " " + path
}

// Rest calls wick's own REST API in-process, as the acting user: the request
// enters the same router the public listener serves, with the user stamped
// on its context exactly as the PAT middleware stamps a token owner
// (login.WithUser), so every route keeps its own authorization checks. GET
// and HEAD need rest.read, other methods rest.write; the path must match a
// declared host_resources.rest pattern. The response (3xx included) is
// returned as the router wrote it, minus Set-Cookie.
func (s *Server) Rest(ctx context.Context, in *hostpb.RestRequest) (*hostpb.RestResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(in.GetMethod()))
	if method == "" {
		method = http.MethodGet
	}
	scope := wickplugin.ScopeHostRestWrite
	if method == http.MethodGet || method == http.MethodHead {
		scope = wickplugin.ScopeHostRestRead
	}
	path, rawQuery, perr := checkRestPath(in.GetPath())
	g := gate{rpc: "Rest", scope: scope, item: method + " " + in.GetPath()}
	if len(in.GetBody()) > RestMaxRequestBytes {
		perr = fmt.Errorf("body larger than %d bytes", RestMaxRequestBytes)
	}
	if perr != nil {
		g.reject = status.Error(codes.InvalidArgument, "rest: "+perr.Error())
	} else if caller, ok := CallerFrom(ctx); ok {
		g.item = restItem(s.pluginInfo(caller), method, path)
		g.note = method + " " + path
	}
	var out *hostpb.RestResponse
	err := s.runGate(ctx, g, func(c *call) error {
		if s.d.Router == nil {
			return unavailable("Rest")
		}
		p, err := s.principal(ctx, c, "Rest")
		if err != nil {
			return err
		}
		ctx, cancel := restDeadline(ctx)
		defer cancel()
		release, err := s.restSlot(ctx, c.plugin.Key)
		if err != nil {
			return err
		}
		defer release()
		target := path
		if rawQuery != "" {
			target += "?" + rawQuery
		}
		// The user as the PAT middleware stamps one, plus the machine mark:
		// routes that treat token calls apart from the UI (the per-project
		// ticket REST toggle, the "api" actor) treat a plugin the same way.
		rctx := login.WithMachineCaller(login.WithUser(ctx, p.User, p.TagIDs), "plugin:"+c.plugin.Key)
		req, err := http.NewRequestWithContext(rctx, method, target, bytes.NewReader(in.GetBody()))
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		req.RequestURI = target
		req.ContentLength = int64(len(in.GetBody()))
		for k, v := range in.GetHeaders() {
			if !restHeaderDropped(k) {
				req.Header.Set(k, v)
			}
		}
		rec := &capWriter{header: http.Header{}, limit: RestMaxResponseBytes}
		if err := serveSafely(s.d.Router, rec, req); err != nil {
			return err
		}
		if rec.overflow {
			return status.Errorf(codes.ResourceExhausted, "rest response larger than %d bytes", RestMaxResponseBytes)
		}
		out = &hostpb.RestResponse{Status: int32(rec.statusCode()), Body: rec.body.Bytes(), Headers: map[string]string{}}
		if method == http.MethodHead {
			out.Body = nil
		}
		for k, vs := range rec.header {
			if k == "Set-Cookie" || strings.HasPrefix(k, "X-Wick-Host-Api") {
				continue
			}
			out.Headers[k] = strings.Join(vs, ", ")
		}
		return nil
	})
	return out, err
}

// restDeadline applies the default Rest deadline and caps a longer one.
func restDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	d, ok := ctx.Deadline()
	switch {
	case !ok:
		return context.WithTimeout(ctx, RestDefaultTimeout)
	case time.Until(d) > RestMaxTimeout:
		return context.WithTimeout(ctx, RestMaxTimeout)
	}
	return context.WithCancel(ctx)
}

// restSlot waits for one of the plugin's RestPerPlugin slots.
func (s *Server) restSlot(ctx context.Context, plugin string) (func(), error) {
	s.restMu.Lock()
	if s.restSem == nil {
		s.restSem = map[string]chan struct{}{}
	}
	sem, ok := s.restSem[plugin]
	if !ok {
		sem = make(chan struct{}, RestPerPlugin)
		s.restSem[plugin] = sem
	}
	s.restMu.Unlock()
	select { // a free slot wins even when ctx is already done
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	default:
	}
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, status.Errorf(codes.ResourceExhausted, "rest: %d calls of this plugin already in flight", RestPerPlugin)
	}
}

// serveSafely runs h, turning a handler panic into Internal instead of
// taking wick down (net/http would have recovered it on a real request).
func serveSafely(h http.Handler, w http.ResponseWriter, r *http.Request) (err error) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				err = status.Error(codes.Aborted, "rest: handler aborted the response")
				return
			}
			err = status.Errorf(codes.Internal, "rest: handler panicked: %v", v)
		}
	}()
	h.ServeHTTP(w, r)
	return nil
}

// capWriter records a response in memory up to limit bytes.
type capWriter struct {
	header   http.Header
	code     int
	body     bytes.Buffer
	limit    int
	overflow bool
}

func (w *capWriter) Header() http.Header { return w.header }

func (w *capWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func (w *capWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	if w.overflow || w.body.Len()+len(b) > w.limit {
		w.overflow = true
		return 0, fmt.Errorf("response larger than %d bytes", w.limit)
	}
	return w.body.Write(b)
}

// Flush satisfies http.Flusher for streaming handlers; the body is
// buffered whole anyway.
func (w *capWriter) Flush() {}

func (w *capWriter) statusCode() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

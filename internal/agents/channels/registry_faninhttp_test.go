package channels

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeKeyedChannel is a minimal Channel that exposes one shared HTTP route
// and claims a request when the X-Instance header matches its key. Mirrors
// the per-user Slack/REST instances that all mount the same path.
type fakeKeyedChannel struct {
	key      string
	served   *string // set to key when this instance serves a request
	matchAll bool    // when true, does NOT implement RequestRouter (catch-all)
}

func (f *fakeKeyedChannel) Name() string                { return "fake" }
func (f *fakeKeyedChannel) Start(context.Context) error { return nil }
func (f *fakeKeyedChannel) Stop()                       {}
func (f *fakeKeyedChannel) IsConfigured() bool          { return true }

func (f *fakeKeyedChannel) HTTPHandlers() map[string]http.Handler {
	return map[string]http.Handler{
		"POST /shared/route": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*f.served = f.key
			w.WriteHeader(http.StatusOK)
		}),
	}
}

// routerChannel adds RequestRouter; matchAll instances embed the base only.
type routerChannel struct{ *fakeKeyedChannel }

func (r routerChannel) OwnsRequest(req *http.Request) bool {
	return req.Header.Get("X-Instance") == r.key
}

// TestHTTPHandlersFanIn proves the registry no longer drops all-but-one
// instance on a shared route (the maps.Copy clobber bug). With two keyed
// instances mounting the same path, a request is routed to the instance
// that claims it — not silently to whichever happened to be registered last.
func TestHTTPHandlersFanIn(t *testing.T) {
	var served string
	a := routerChannel{&fakeKeyedChannel{key: "owner", served: &served}}
	b := routerChannel{&fakeKeyedChannel{key: "user-b", served: &served}}

	reg := NewRegistry()
	reg.AddKeyed("fake:owner", a, nil)
	reg.AddKeyed("fake:user-b", b, nil)

	handlers := reg.HTTPHandlers()
	h, ok := handlers["POST /shared/route"]
	if !ok {
		t.Fatal("shared route not mounted")
	}

	// Request tagged for user-b must reach user-b, not the last-registered.
	served = ""
	req := httptest.NewRequest("POST", "/shared/route", nil)
	req.Header.Set("X-Instance", "user-b")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if served != "user-b" {
		t.Errorf("served by %q, want user-b (fan-in misrouted)", served)
	}

	// Request tagged for owner must reach owner.
	served = ""
	req = httptest.NewRequest("POST", "/shared/route", nil)
	req.Header.Set("X-Instance", "owner")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if served != "owner" {
		t.Errorf("served by %q, want owner", served)
	}
}

// TestHTTPHandlersFanInFallback verifies an unclaimed request falls back to
// a catch-all (non-RequestRouter) instance, preserving single-instance
// behaviour when no instance explicitly owns the request.
func TestHTTPHandlersFanInFallback(t *testing.T) {
	var served string
	// owner implements RequestRouter (claims only X-Instance=owner);
	// fallback does NOT implement RequestRouter → catch-all.
	owner := routerChannel{&fakeKeyedChannel{key: "owner", served: &served}}
	fallback := &fakeKeyedChannel{key: "fallback", served: &served}

	reg := NewRegistry()
	reg.AddKeyed("fake:owner", owner, nil)
	reg.AddKeyed("fake:fallback", fallback, nil)

	h := reg.HTTPHandlers()["POST /shared/route"]
	if h == nil {
		t.Fatal("shared route not mounted")
	}

	served = ""
	req := httptest.NewRequest("POST", "/shared/route", nil) // no X-Instance
	h.ServeHTTP(httptest.NewRecorder(), req)
	if served != "fallback" {
		t.Errorf("unclaimed request served by %q, want fallback", served)
	}
}

// servingChannel is a catch-all instance that can stop serving (a
// switched-off Slack instance whose route is still mounted).
type servingChannel struct {
	*fakeKeyedChannel
	on bool
}

func (s servingChannel) Serving() bool { return s.on }

// The fallback skips an instance that stopped serving, so a switched-off
// instance mounted first does not swallow another instance's events.
func TestHTTPHandlersFanInSkipsStoppedInstance(t *testing.T) {
	var served string
	off := servingChannel{&fakeKeyedChannel{key: "off", served: &served, matchAll: true}, false}
	live := servingChannel{&fakeKeyedChannel{key: "live", served: &served, matchAll: true}, true}
	reg := NewRegistry()
	reg.AddKeyed("fake:off", off, nil)
	reg.AddKeyed("fake:live", live, nil)
	h := reg.HTTPHandlers()["POST /shared/route"]
	if h == nil {
		t.Fatal("shared route not mounted")
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/shared/route", nil))
	if served != "live" {
		t.Fatalf("served by %q, want the serving instance", served)
	}
}

// verifierChannel is a RequestRouter that also verifies requests by a
// per-instance signature (the X-Sig header names its key) — the shape of
// the Slack events route, where no instance ever claims a request.
type verifierChannel struct{ routerChannel }

func (v verifierChannel) VerifiesRoute(string) bool { return true }

func (v verifierChannel) VerifiesRequest(h http.Header, _ []byte) bool {
	return h.Get("X-Sig") == v.key
}

func newVerifier(key string, served *string) verifierChannel {
	return verifierChannel{routerChannel{&fakeKeyedChannel{key: key, served: served}}}
}

// Mounted once, the handler follows the registry: a removed instance stops
// serving, and the instance added again under its key (a switch turned
// back on) serves without re-mounting.
func TestHTTPHandlersFollowLiveRegistry(t *testing.T) {
	var served string
	reg := NewRegistry()
	reg.AddKeyed("fake:a", newVerifier("a", &served), nil)
	h := reg.HTTPHandlers()["POST /shared/route"]
	post := func(sig string) *httptest.ResponseRecorder {
		served = ""
		r := httptest.NewRequest(http.MethodPost, "/shared/route", nil)
		r.Header.Set("X-Sig", sig)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if post("a"); served != "a" {
		t.Fatalf("served by %q, want a", served)
	}
	reg.RemoveKeyed("fake:a")
	if w := post("a"); served != "" || w.Header().Get("X-Wick-Ignored") == "" || w.Code != http.StatusOK {
		t.Fatalf("removed instance: served=%q %d %v", served, w.Code, w.Header())
	}
	var servedNew string
	reg.AddKeyed("fake:a", newVerifier("a", &servedNew), nil)
	post("a")
	if servedNew != "a" {
		t.Fatalf("the re-added instance did not serve (old served %q)", served)
	}
}

// With RequestRouter candidates nobody claims (Slack events), the request
// goes to the instance whose signature verifies it; one only a removed
// instance verifies is acknowledged and dropped, not handed to another
// instance that would reject it and make the sender retry.
func TestHTTPHandlersRouteBySignature(t *testing.T) {
	var served string
	reg := NewRegistry()
	reg.AddKeyed("fake:a", newVerifier("a", &served), nil)
	reg.AddKeyed("fake:b", newVerifier("b", &served), nil)
	h := reg.HTTPHandlers()["POST /shared/route"]
	post := func(sig string) *httptest.ResponseRecorder {
		served = ""
		r := httptest.NewRequest(http.MethodPost, "/shared/route", nil)
		r.Header.Set("X-Sig", sig)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if post("b"); served != "b" {
		t.Fatalf("signed for b, served by %q", served)
	}
	reg.RemoveKeyed("fake:a")
	if w := post("a"); served != "" || w.Header().Get("X-Wick-Ignored") == "" {
		t.Fatalf("signed for removed a: served=%q %v", served, w.Header())
	}
	if post("nobody"); served != "b" {
		t.Fatalf("unverified falls back to a live instance, got %q", served)
	}
}

// bodyChannel serves a signed route ("POST /events") and an unsigned one
// ("POST /send"), recording how many body bytes its handler read.
type bodyChannel struct {
	got *int
}

func (b bodyChannel) Name() string                   { return "fake" }
func (b bodyChannel) Start(context.Context) error    { return nil }
func (b bodyChannel) Stop()                          {}
func (b bodyChannel) IsConfigured() bool             { return true }
func (b bodyChannel) OwnsRequest(*http.Request) bool { return false }
func (b bodyChannel) VerifiesRoute(path string) bool { return path == "POST /events" }
func (b bodyChannel) VerifiesRequest(h http.Header, _ []byte) bool {
	return h.Get("X-Sig") == "ok"
}
func (b bodyChannel) HTTPHandlers() map[string]http.Handler {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		*b.got = int(n)
	})
	return map[string]http.Handler{"POST /send": read, "POST /events": read}
}

// Only a signed route's body is read for routing: an unsigned route
// (Slack /send) streams through whole whatever its size, and a signed
// one over the bound is refused with 413, not cut short.
func TestHTTPHandlersBodyReadOnlyForVerifiedRoute(t *testing.T) {
	var got int
	reg := NewRegistry()
	reg.AddKeyed("fake:a", bodyChannel{got: &got}, nil)
	hs := reg.HTTPHandlers()
	big := strings.Repeat("x", 2<<20)

	r := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(big))
	hs["POST /send"].ServeHTTP(httptest.NewRecorder(), r)
	if got != len(big) {
		t.Fatalf("/send body reached the handler with %d bytes, want %d", got, len(big))
	}

	got = 0
	r = httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(big))
	r.Header.Set("X-Sig", "ok")
	w := httptest.NewRecorder()
	hs["POST /events"].ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge || got != 0 {
		t.Fatalf("oversized signed body: %d, handler read %d", w.Code, got)
	}

	r = httptest.NewRequest(http.MethodPost, "/events", strings.NewReader("small"))
	r.Header.Set("X-Sig", "ok")
	hs["POST /events"].ServeHTTP(httptest.NewRecorder(), r)
	if got != len("small") {
		t.Fatalf("signed body restored with %d bytes, want %d", got, len("small"))
	}
}

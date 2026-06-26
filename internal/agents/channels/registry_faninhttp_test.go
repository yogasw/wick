package channels

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func (f *fakeKeyedChannel) Name() string                  { return "fake" }
func (f *fakeKeyedChannel) Start(context.Context) error   { return nil }
func (f *fakeKeyedChannel) Stop()                          {}
func (f *fakeKeyedChannel) IsConfigured() bool             { return true }

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

// Package conntest provides lightweight test helpers for writing connector
// unit tests. It eliminates the boilerplate of wiring up connector.Ctx and
// httptest.Server so authors can focus on testing connector logic, not setup.
//
// Typical usage:
//
//	func TestMyOp(t *testing.T) {
//	    srv := conntest.JSONServer(t, map[string]any{"ok": true, "id": 42})
//	    c := conntest.Ctx(t,
//	        map[string]string{"base_url": srv.URL, "token": "secret"},
//	        map[string]string{"resource": "users"},
//	    )
//	    result, err := myOp(c)
//	    require.NoError(t, err)
//	    // assert result...
//	}
package conntest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// Ctx creates a connector.Ctx with the given configs and input maps.
// Uses http.DefaultClient and a background context. Pass nil for either
// map to get an empty map.
func Ctx(t testing.TB, configs, input map[string]string) *connector.Ctx {
	t.Helper()
	if configs == nil {
		configs = map[string]string{}
	}
	if input == nil {
		input = map[string]string{}
	}
	return connector.NewCtx(context.Background(), "test-instance", configs, input, http.DefaultClient, nil, nil)
}

// Server starts an httptest.Server using fn as its handler and registers
// t.Cleanup to close it. The server URL is accessible via srv.URL.
func Server(t testing.TB, fn http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	return srv
}

// CtxWithServer combines Server and Ctx: starts a mock server, returns a
// Ctx whose HTTP client is wired to hit it. Use srv.URL when building
// config values like base_url.
//
//	srv, c := conntest.CtxWithServer(t, handler, configs, input)
//	// configs["base_url"] should use srv.URL
func CtxWithServer(t testing.TB, fn http.HandlerFunc, configs, input map[string]string) (*connector.Ctx, *httptest.Server) {
	t.Helper()
	srv := Server(t, fn)
	if configs == nil {
		configs = map[string]string{}
	}
	if input == nil {
		input = map[string]string{}
	}
	c := connector.NewCtx(context.Background(), "test-instance", configs, input, srv.Client(), nil, nil)
	return c, srv
}

// JSONServer starts a mock server that always returns payload as JSON with
// HTTP 200. Use when the connector only needs a static response.
func JSONServer(t testing.TB, payload any) *httptest.Server {
	t.Helper()
	return Server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("conntest.JSONServer: encode: %v", err)
		}
	})
}

// JSONServerWithStatus starts a mock server that returns payload as JSON
// with the given HTTP status code. Use for testing error handling paths.
func JSONServerWithStatus(t testing.TB, status int, payload any) *httptest.Server {
	t.Helper()
	return Server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("conntest.JSONServerWithStatus: encode: %v", err)
		}
	})
}

// CaptureRequest starts a mock server that records the last request it
// received and returns payload as JSON. The returned *http.Request pointer
// is populated after the first call. Useful for asserting request shape
// (headers, method, body) alongside response handling.
func CaptureRequest(t testing.TB, payload any) (*httptest.Server, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	srv := Server(t, func(w http.ResponseWriter, r *http.Request) {
		cap.Method = r.Method
		cap.URL = r.URL.String()
		cap.Header = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("conntest.CaptureRequest: encode: %v", err)
		}
	})
	return srv, cap
}

// capturedRequest holds fields from the most recent request the mock
// server received. Check after calling the operation under test.
type capturedRequest struct {
	Method string
	URL    string
	Header http.Header
}

package httprest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/pkg/connector"
)

// newTestCtx builds a minimal *connector.Ctx for unit tests.
// configs seeds the per-instance credential map; inputs the per-call args.
func newTestCtx(configs map[string]string) *connector.Ctx {
	return connector.NewCtx(context.Background(), "test-id", configs, map[string]string{}, http.DefaultClient, nil, nil)
}

func newTestCtxWithInput(configs, input map[string]string) *connector.Ctx {
	return connector.NewCtx(context.Background(), "test-id", configs, input, http.DefaultClient, nil, nil)
}

// mockServer starts a test HTTP server that responds with the given status
// and JSON body for any request. The returned *httptest.Server is closed
// automatically when the test ends.
func mockServer(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetOp(t *testing.T) {
	srv := mockServer(t, 200, map[string]any{"id": 1, "name": "Alice"})

	c := newTestCtxWithInput(
		map[string]string{"base_url": srv.URL},
		map[string]string{"path": "/users/1", "query": ""},
	)

	result, err := getOp(c)
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Alice", m["name"])
}

func TestGetOpWithQuery(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(srv.Close)

	c := newTestCtxWithInput(
		map[string]string{"base_url": srv.URL},
		map[string]string{"path": "/users", "query": `{"page": 2, "limit": 5}`},
	)

	_, err := getOp(c)
	require.NoError(t, err)
	assert.Contains(t, capturedQuery, "page=2")
	assert.Contains(t, capturedQuery, "limit=5")
}

func TestPostOp(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 99})
	}))
	t.Cleanup(srv.Close)

	c := newTestCtxWithInput(
		map[string]string{"base_url": srv.URL},
		map[string]string{"path": "/users", "body": `{"name":"Bob"}`, "content_type": ""},
	)

	result, err := postOp(c)
	require.NoError(t, err)
	assert.Equal(t, "Bob", capturedBody["name"])

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(99), m["id"])
}

func TestDeleteOpEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)

	c := newTestCtxWithInput(
		map[string]string{"base_url": srv.URL},
		map[string]string{"path": "/users/42"},
	)

	result, err := deleteOp(c)
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, m["ok"])
	assert.Equal(t, "/users/42", m["path"])
}

func TestAuthHeader(t *testing.T) {
	var capturedAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	t.Cleanup(srv.Close)

	c := newTestCtxWithInput(
		map[string]string{
			"base_url":    srv.URL,
			"auth_header": "Authorization",
			"auth_value":  "Bearer test-token",
		},
		map[string]string{"path": "/secure"},
	)

	_, err := getOp(c)
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token", capturedAuthHeader)
}

func TestNonJSONResponseFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("plain text response"))
	}))
	t.Cleanup(srv.Close)

	c := newTestCtxWithInput(
		map[string]string{"base_url": srv.URL},
		map[string]string{"path": "/plain"},
	)

	result, err := getOp(c)
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "plain text response", m["body"])
}

func TestUpstreamErrorReturnsError(t *testing.T) {
	srv := mockServer(t, 404, map[string]any{"error": "not found"})

	c := newTestCtxWithInput(
		map[string]string{"base_url": srv.URL},
		map[string]string{"path": "/missing"},
	)

	_, err := getOp(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestMissingBaseURL(t *testing.T) {
	c := newTestCtxWithInput(
		map[string]string{},
		map[string]string{"path": "/users"},
	)

	_, err := getOp(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

func TestMeta(t *testing.T) {
	m := Meta()
	assert.Equal(t, Key, m.Key)
	assert.NotEmpty(t, m.Name)
	assert.NotEmpty(t, m.Description)
}

func TestOperationsCount(t *testing.T) {
	ops := Operations()
	assert.Len(t, ops, 5)

	keys := make([]string, len(ops))
	for i, op := range ops {
		keys[i] = op.Key
	}
	assert.ElementsMatch(t, []string{"get", "post", "put", "patch", "delete"}, keys)
}

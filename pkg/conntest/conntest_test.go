package conntest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/pkg/conntest"
	"github.com/yogasw/wick/pkg/connector"
)

func TestCtx_ConfigAndInput(t *testing.T) {
	c := conntest.Ctx(t,
		map[string]string{"token": "abc", "base_url": "http://example.com"},
		map[string]string{"channel": "C123"},
	)
	assert.Equal(t, "abc", c.Cfg("token"))
	assert.Equal(t, "http://example.com", c.Cfg("base_url"))
	assert.Equal(t, "C123", c.Input("channel"))
}

func TestCtx_NilMapsAreSafe(t *testing.T) {
	c := conntest.Ctx(t, nil, nil)
	assert.Equal(t, "", c.Cfg("missing"))
	assert.Equal(t, "", c.Input("missing"))
}

func TestServer_ReceivesRequest(t *testing.T) {
	called := false
	srv := conntest.Server(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, "GET", r.Method)
		w.WriteHeader(http.StatusOK)
	})

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	resp.Body.Close()
	assert.True(t, called)
}

func TestCtxWithServer_ClientHitsServer(t *testing.T) {
	c, srv := conntest.CtxWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}, map[string]string{"base_url": ""}, nil)

	req, _ := http.NewRequestWithContext(c.Context(), "GET", srv.URL+"/ping", nil)
	resp, err := c.HTTP.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "world")
}

func TestJSONServer_ReturnsPayload(t *testing.T) {
	srv := conntest.JSONServer(t, map[string]any{"ok": true, "count": 42})
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, float64(42), result["count"])
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestJSONServerWithStatus_Returns404(t *testing.T) {
	srv := conntest.JSONServerWithStatus(t, http.StatusNotFound, map[string]string{"error": "not found"})
	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestCaptureRequest_RecordsMethod(t *testing.T) {
	srv, cap := conntest.CaptureRequest(t, map[string]any{"ok": true})

	req, _ := http.NewRequest("POST", srv.URL+"/api/test", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, "POST", cap.Method)
	assert.Equal(t, "/api/test", cap.URL)
	assert.Equal(t, "Bearer tok", cap.Header.Get("Authorization"))
}

// TestConnectorIntegration shows the full pattern a connector test uses.
func TestConnectorIntegration(t *testing.T) {
	// Simulate a connector operation that calls an external API
	myOp := func(c *connector.Ctx) (any, error) {
		req, _ := http.NewRequestWithContext(c.Context(), "GET", c.Cfg("base_url")+"/users", nil)
		req.Header.Set("Authorization", "Bearer "+c.Cfg("token"))
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var result any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return result, nil
	}

	srv := conntest.JSONServer(t, []map[string]any{{"id": 1, "name": "Alice"}})
	c := conntest.Ctx(t,
		map[string]string{"base_url": srv.URL, "token": "secret"},
		nil,
	)

	result, err := myOp(c)
	require.NoError(t, err)
	users := result.([]any)
	assert.Len(t, users, 1)
}

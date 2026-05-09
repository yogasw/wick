package slack

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

func newCtx(configs, input map[string]string) *connector.Ctx {
	return connector.NewCtx(context.Background(), "test", configs, input, http.DefaultClient, nil, nil)
}

func slackMock(t *testing.T, payload map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSendMessageOK(t *testing.T) {
	// Slack always returns 200; success signaled by ok:true
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, "Bearer xoxb-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1234.5678", "channel": "C123"})
	}))
	t.Cleanup(srv.Close)

	// Override apiBase for test
	orig := apiBase
	_ = orig // apiBase is const; we'll call doRequest directly with full URL
	c := newCtx(
		map[string]string{"bot_token": "xoxb-test"},
		map[string]string{"channel": "C123", "text": "hello"},
	)
	result, err := doRequest(c, "POST", srv.URL, map[string]any{"channel": "C123", "text": "hello"})
	require.NoError(t, err)
	require.True(t, called)
	m := result.(map[string]any)
	assert.Equal(t, true, m["ok"])
}

func TestSlackAPIError(t *testing.T) {
	srv := slackMock(t, map[string]any{"ok": false, "error": "channel_not_found"})
	c := newCtx(map[string]string{"bot_token": "xoxb-test"}, map[string]string{})
	_, err := doRequest(c, "GET", srv.URL, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel_not_found")
}

func TestMissingToken(t *testing.T) {
	srv := slackMock(t, map[string]any{"ok": true})
	c := newCtx(map[string]string{}, map[string]string{"channel": "C123", "text": "hi"})
	_, err := doRequest(c, "GET", srv.URL, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bot_token")
}

func TestSendMessageMissingChannel(t *testing.T) {
	c := newCtx(map[string]string{"bot_token": "xoxb-test"}, map[string]string{"text": "hi"})
	_, err := sendMessage(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel")
}

func TestSendMessageMissingText(t *testing.T) {
	c := newCtx(map[string]string{"bot_token": "xoxb-test"}, map[string]string{"channel": "C123"})
	_, err := sendMessage(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text")
}

func TestGetUserMissingID(t *testing.T) {
	c := newCtx(map[string]string{"bot_token": "xoxb-test"}, map[string]string{})
	_, err := getUser(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user_id")
}

func TestSlackField(t *testing.T) {
	resp := map[string]any{"ok": true, "channels": []any{"#general"}}
	result, err := slackField(resp, "channels")
	require.NoError(t, err)
	assert.Equal(t, []any{"#general"}, result)
}

func TestPickFields(t *testing.T) {
	resp := map[string]any{"ok": true, "ts": "123", "channel": "C1", "extra": "ignored"}
	result, err := pickFields(resp, "ok", "ts", "channel")
	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, true, m["ok"])
	assert.Equal(t, "123", m["ts"])
	_, hasExtra := m["extra"]
	assert.False(t, hasExtra)
}

func TestMeta(t *testing.T) {
	m := Meta()
	assert.Equal(t, Key, m.Key)
	assert.NotEmpty(t, m.Name)
}

func TestOperationsCount(t *testing.T) {
	ops := Operations()
	assert.Len(t, ops, 4)
}

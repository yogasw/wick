package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/pkg/connector"
)

func newCtx(t *testing.T, configs map[string]string) *connector.Ctx {
	t.Helper()
	return connector.NewCtx(context.Background(), "test-row", configs, map[string]string{}, http.DefaultClient, nil, nil)
}

// withBaseURL points the slack package at a test server for the duration
// of one test, then restores the production constant.
func withBaseURL(t *testing.T, url string) {
	t.Helper()
	prev := baseURLOverride
	baseURLOverride = url
	t.Cleanup(func() { baseURLOverride = prev })
}

// mockSlack returns a fake Slack endpoint that always responds with
// {ok:true} plus the supplied X-OAuth-Scopes header value.
func mockSlack(t *testing.T, scopesHeader string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-OAuth-Scopes", scopesHeader)
		_, _ = w.Write([]byte(`{"ok":true,"team":"acme","user":"botuser","user_id":"U1","team_id":"T1","bot_id":"B1"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEvalScopeRule(t *testing.T) {
	granted := map[string]struct{}{
		"chat:write":       {},
		"channels:read":    {},
		"channels:history": {},
	}
	tests := []struct {
		name    string
		rule    [][]string
		wantOK  bool
		wantLen int
	}{
		{"single scope satisfied", [][]string{{"chat:write"}}, true, 0},
		{"any-of satisfied by one match", [][]string{{"groups:read", "channels:read"}}, true, 0},
		{"missing scope", [][]string{{"users:read"}}, false, 1},
		{"empty rule always ok", nil, true, 0},
		{"multi-group, one satisfied one missing", [][]string{{"chat:write"}, {"reactions:write"}}, false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, missing := evalScopeRule(tc.rule, granted)
			assert.Equal(t, tc.wantOK, ok)
			assert.Len(t, missing, tc.wantLen)
		})
	}
}

func TestFormatMissingScopes(t *testing.T) {
	assert.Equal(t, "needs scope: chat:write", formatMissingScopes([][]string{{"chat:write"}}))
	assert.Equal(t, "needs scope: one of: channels:read, groups:read", formatMissingScopes([][]string{{"channels:read", "groups:read"}}))
	assert.Equal(t, "needs scope: chat:write; also reactions:write", formatMissingScopes([][]string{{"chat:write"}, {"reactions:write"}}))
	assert.Equal(t, "permission check failed", formatMissingScopes(nil))
}

func TestParseScopeHeader(t *testing.T) {
	assert.Equal(t,
		[]string{"chat:write", "channels:read", "users:read"},
		parseScopeHeader("chat:write,channels:read, users:read"),
	)
	assert.Nil(t, parseScopeHeader(""))
}

func TestRunHealthCheck_AllOK(t *testing.T) {
	srv := mockSlack(t, "channels:read,groups:read,im:read,mpim:read,channels:history,groups:history,im:history,mpim:history,users:read,users:read.email,chat:write,reactions:write")
	withBaseURL(t, srv.URL)
	c := newCtx(t, map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-test"})

	report, err := runHealthCheck(c)
	require.NoError(t, err)
	require.NotEmpty(t, report)
	for _, h := range report {
		assert.Truef(t, h.OK, "expected op %q to be ok, reason=%q", h.Key, h.Reason)
		assert.Emptyf(t, h.Reason, "ok op should have no reason: %q", h.Key)
	}
}

func TestRunHealthCheck_MissingScopes(t *testing.T) {
	srv := mockSlack(t, "channels:read,channels:history,users:read")
	withBaseURL(t, srv.URL)
	c := newCtx(t, map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-test"})

	report, err := runHealthCheck(c)
	require.NoError(t, err)

	byOp := map[string]connector.OpHealth{}
	for _, h := range report {
		byOp[h.Key] = h
	}
	assert.True(t, byOp["list_channels"].OK)
	assert.True(t, byOp["get_channel_history"].OK)
	assert.True(t, byOp["list_users"].OK)
	assert.False(t, byOp["send_message"].OK)
	assert.Contains(t, byOp["send_message"].Reason, "chat:write")
	assert.False(t, byOp["add_reaction"].OK)
	assert.Contains(t, byOp["add_reaction"].Reason, "reactions:write")
}

func TestRunHealthCheck_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	c := newCtx(t, map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-bad"})
	report, err := runHealthCheck(c)
	require.Error(t, err)
	assert.Nil(t, report)
	assert.Contains(t, err.Error(), "invalid_auth")
}

func TestPickToken_ModeSwitch(t *testing.T) {
	c := newCtx(t, map[string]string{
		"auth_mode":  "user_token",
		"bot_token":  "xoxb-bot",
		"user_token": "xoxp-user",
	})
	tok, err := pickToken(c)
	require.NoError(t, err)
	assert.Equal(t, "xoxp-user", tok)
}

func TestPickToken_LegacyFallback(t *testing.T) {
	c := newCtx(t, map[string]string{"token": "xoxb-legacy"})
	tok, err := pickToken(c)
	require.NoError(t, err)
	assert.Equal(t, "xoxb-legacy", tok)
}

func TestPickToken_Missing(t *testing.T) {
	c := newCtx(t, map[string]string{"auth_mode": "bot_token"})
	_, err := pickToken(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

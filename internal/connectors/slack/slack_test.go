package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	srv := mockSlack(t, "channels:read,groups:read,im:read,mpim:read,channels:history,groups:history,im:history,mpim:history,users:read,users:read.email,chat:write,reactions:write,files:write")
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

func newCtxWithInput(t *testing.T, inputs map[string]string) *connector.Ctx {
	t.Helper()
	return connector.NewCtx(context.Background(), "test-row",
		map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-test"},
		inputs, http.DefaultClient, nil, nil)
}

// ── upload_file tests ────────────────────────────────────────────────

func TestUploadFile_HappyPath(t *testing.T) {
	var uploadedFilename string
	var uploadedContent []byte
	var completedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			fmt.Fprintf(w, `{"ok":true,"upload_url":"http://%s/upload/v1","file_id":"F9999"}`, r.Host)
		case "/upload/v1":
			require.Empty(t, r.Header.Get("Authorization"), "upload_url must not carry Bearer token")
			mr, err := r.MultipartReader()
			require.NoError(t, err)
			part, err := mr.NextPart()
			require.NoError(t, err)
			uploadedFilename = part.FileName()
			uploadedContent, _ = io.ReadAll(part)
			w.WriteHeader(http.StatusOK)
		case "/files.completeUploadExternal":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&completedBody))
			fmt.Fprint(w, `{"ok":true,"files":[{"id":"F9999","name":"report.txt","title":"July Report","permalink":"https://slack.com/F9999","url_private":"https://files.slack.com/F9999"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	result, err := uploadFile(newCtxWithInput(t, map[string]string{
		"filename":        "report.txt",
		"content":         "Monthly summary",
		"channel_id":      "C123",
		"title":           "July Report",
		"initial_comment": "See attached",
	}))
	require.NoError(t, err)
	assert.Equal(t, "report.txt", uploadedFilename)
	assert.Equal(t, []byte("Monthly summary"), uploadedContent)
	assert.Equal(t, "C123", completedBody["channel_id"])
	assert.Equal(t, "See attached", completedBody["initial_comment"])
	out := result.(map[string]any)
	assert.Equal(t, "F9999", out["file_id"])
	assert.Equal(t, "C123", out["channel"])
	assert.Equal(t, "July Report", out["title"])
}

func TestUploadFile_NoChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			fmt.Fprintf(w, `{"ok":true,"upload_url":"http://%s/upload/v1","file_id":"F001"}`, r.Host)
		case "/upload/v1":
			w.WriteHeader(http.StatusOK)
		case "/files.completeUploadExternal":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Nil(t, body["channel_id"], "channel_id must be absent when not provided")
			fmt.Fprint(w, `{"ok":true,"files":[{"id":"F001","name":"data.txt","title":"data.txt"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	result, err := uploadFile(newCtxWithInput(t, map[string]string{
		"filename": "data.txt",
		"content":  "hello",
	}))
	require.NoError(t, err)
	out := result.(map[string]any)
	assert.Equal(t, "F001", out["file_id"])
	assert.Nil(t, out["channel"])
}

func TestUploadFile_ThreadTSRequiresChannelID(t *testing.T) {
	_, err := uploadFile(newCtxWithInput(t, map[string]string{
		"filename":  "x.txt",
		"content":   "y",
		"thread_ts": "1700000000.000100",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel_id is required")
}

func TestUploadFile_MissingFilename(t *testing.T) {
	_, err := uploadFile(newCtxWithInput(t, map[string]string{"content": "data"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filename is required")
}

func TestUploadFile_MissingContent(t *testing.T) {
	_, err := uploadFile(newCtxWithInput(t, map[string]string{"filename": "file.txt"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "content is required")
}

func TestUploadFile_GetURLFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":false,"error":"not_authed"}`)
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := uploadFile(newCtxWithInput(t, map[string]string{
		"filename": "x.txt",
		"content":  "data",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not_authed")
}

func TestUploadFile_UploadFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			fmt.Fprintf(w, `{"ok":true,"upload_url":"http://%s/upload/v1","file_id":"F001"}`, r.Host)
		case "/upload/v1":
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := uploadFile(newCtxWithInput(t, map[string]string{
		"filename": "x.txt",
		"content":  "data",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload file bytes")
}

func TestUploadFile_CompleteFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			fmt.Fprintf(w, `{"ok":true,"upload_url":"http://%s/upload/v1","file_id":"F001"}`, r.Host)
		case "/upload/v1":
			w.WriteHeader(http.StatusOK)
		case "/files.completeUploadExternal":
			fmt.Fprint(w, `{"ok":false,"error":"invalid_file_id"}`)
		}
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := uploadFile(newCtxWithInput(t, map[string]string{
		"filename":   "x.txt",
		"content":    "data",
		"channel_id": "C123",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_file_id")
}

func TestUploadFile_HealthCheckScope(t *testing.T) {
	srv := mockSlack(t, "channels:read,chat:write") // files:write absent
	withBaseURL(t, srv.URL)
	c := newCtx(t, map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-test"})

	report, err := runHealthCheck(c)
	require.NoError(t, err)
	byOp := map[string]connector.OpHealth{}
	for _, h := range report {
		byOp[h.Key] = h
	}
	assert.False(t, byOp["upload_file"].OK)
	assert.Contains(t, byOp["upload_file"].Reason, "files:write")
}

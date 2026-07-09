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

func newCtx(t *testing.T, configs map[string]string) *connector.Ctx {
	t.Helper()
	return connector.NewCtx(context.Background(), "test-row", configs, map[string]string{}, http.DefaultClient, nil, nil)
}

func newCtxWithInput(t *testing.T, input map[string]string) *connector.Ctx {
	t.Helper()
	return connector.NewCtx(context.Background(), "test-row", map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-test"}, input, http.DefaultClient, nil, nil)
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

func TestIsTextMimetype(t *testing.T) {
	textual := []string{"text/plain", "text/csv", "TEXT/HTML; charset=utf-8", "application/json", "application/x-yaml"}
	for _, mt := range textual {
		assert.Truef(t, isTextMimetype(mt), "expected %q to be text", mt)
	}
	binary := []string{"image/png", "image/jpeg", "application/pdf", "application/octet-stream", ""}
	for _, mt := range binary {
		assert.Falsef(t, isTextMimetype(mt), "expected %q to be binary", mt)
	}
}

func TestShapeReadFile_TextInline(t *testing.T) {
	out := shapeReadFile("F1", "notes.txt", "text/plain", []byte("hello world"))
	m := out.(map[string]any)
	assert.Equal(t, true, m["is_text"])
	assert.Equal(t, "hello world", m["content"])
	assert.NotContains(t, m, "content_base64")
	assert.Equal(t, 11, m["size"])
}

func TestShapeReadFile_BinaryBase64(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a} // PNG magic
	out := shapeReadFile("F2", "shot.png", "image/png", png)
	m := out.(map[string]any)
	assert.Equal(t, false, m["is_text"])
	assert.Equal(t, "iVBORw0KGgo=", m["content_base64"])
	assert.NotContains(t, m, "content")
}

func TestShapeReadFile_TextMimetypeButInvalidUTF8IsBase64(t *testing.T) {
	// mimetype claims text but bytes aren't valid UTF-8 → fall back to base64
	// so we never emit a corrupt string.
	out := shapeReadFile("F3", "weird.txt", "text/plain", []byte{0xff, 0xfe, 0x00})
	m := out.(map[string]any)
	assert.Equal(t, false, m["is_text"])
	assert.Contains(t, m, "content_base64")
}

func TestShapeReactions_FromMessage(t *testing.T) {
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"ok":true,
		"type":"message",
		"message":{"reactions":[{"name":"thumbsup","count":2,"users":["U1","U2"]}]}
	}`), &raw))
	out := shapeReactions(raw).(map[string]any)
	reacts := out["reactions"].([]map[string]any)
	require.Len(t, reacts, 1)
	assert.Equal(t, "thumbsup", reacts[0]["name"])
	assert.EqualValues(t, 2, reacts[0]["count"])
}

func TestShapeReactions_FromFile(t *testing.T) {
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"ok":true,"type":"file",
		"file":{"reactions":[{"name":"eyes","count":1}]}
	}`), &raw))
	out := shapeReactions(raw).(map[string]any)
	reacts := out["reactions"].([]map[string]any)
	require.Len(t, reacts, 1)
	assert.Equal(t, "eyes", reacts[0]["name"])
}

func TestGetReactions_RequiresTarget(t *testing.T) {
	c := newCtxWithInput(t, map[string]string{}) // no channel/ts/file
	_, err := getReactions(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "either file, or both channel and ts")
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
	srv := mockSlack(t, "channels:read,groups:read,im:read,mpim:read,channels:history,groups:history,im:history,mpim:history,users:read,users:read.email,chat:write,reactions:write,reactions:read,canvases:read,canvases:write,files:read,files:write")
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
	assert.False(t, byOp["create_canvas"].OK)
	assert.Contains(t, byOp["create_canvas"].Reason, "canvases:write")
	assert.False(t, byOp["lookup_canvas_sections"].OK)
	assert.Contains(t, byOp["lookup_canvas_sections"].Reason, "canvases:read")
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

func TestCreateCanvas(t *testing.T) {
	var path string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte(`{"ok":true,"canvas_id":"F123"}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	result, err := createCanvas(newCtxWithInput(t, map[string]string{
		"title":      "Incident details",
		"markdown":   "# Summary\nEvidence",
		"channel_id": "C123",
	}))
	require.NoError(t, err)
	assert.Equal(t, "/canvases.create", path)
	assert.Equal(t, "Incident details", captured["title"])
	assert.Equal(t, "C123", captured["channel_id"])
	assert.Equal(t, map[string]any{"type": "markdown", "markdown": "# Summary\nEvidence"}, captured["document_content"])
	assert.Equal(t, "F123", result.(map[string]any)["canvas_id"])
}

func TestCreateChannelCanvas(t *testing.T) {
	var path string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte(`{"ok":true,"canvas_id":"F234"}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := createChannelCanvas(newCtxWithInput(t, map[string]string{
		"channel_id": "C234",
		"title":      "Support",
		"markdown":   "Details",
	}))
	require.NoError(t, err)
	assert.Equal(t, "/conversations.canvases.create", path)
	assert.Equal(t, "C234", captured["channel_id"])
	assert.Equal(t, "Support", captured["title"])
}

func TestEditCanvas(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := editCanvas(newCtxWithInput(t, map[string]string{
		"canvas_id":  "F123",
		"operation":  "replace",
		"section_id": "temp:C:section",
		"markdown":   "Updated evidence",
	}))
	require.NoError(t, err)
	changes := captured["changes"].([]any)
	change := changes[0].(map[string]any)
	assert.Equal(t, "replace", change["operation"])
	assert.Equal(t, "temp:C:section", change["section_id"])
	assert.Equal(t, map[string]any{"type": "markdown", "markdown": "Updated evidence"}, change["document_content"])
}

func TestEditCanvasRequiresSectionForDelete(t *testing.T) {
	_, err := editCanvas(newCtxWithInput(t, map[string]string{
		"canvas_id": "F123",
		"operation": "delete",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "section_id")
}

func TestLookupCanvasSections(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte(`{"ok":true,"sections":[{"id":"temp:C:section"}]}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := lookupCanvasSections(newCtxWithInput(t, map[string]string{
		"canvas_id": "F123",
		"criteria":  `{"section_types":["any_header"],"contains_text":"Incident"}`,
	}))
	require.NoError(t, err)
	assert.Equal(t, "F123", captured["canvas_id"])
	assert.Equal(t, "Incident", captured["criteria"].(map[string]any)["contains_text"])
}

func TestSetCanvasAccess(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := setCanvasAccess(newCtxWithInput(t, map[string]string{
		"canvas_id":    "F123",
		"access_level": "write",
		"channel_ids":  "C123, C234",
	}))
	require.NoError(t, err)
	assert.Equal(t, "write", captured["access_level"])
	assert.Equal(t, []any{"C123", "C234"}, captured["channel_ids"])
}

func TestSetCanvasAccessRejectsMixedEntities(t *testing.T) {
	_, err := setCanvasAccess(newCtxWithInput(t, map[string]string{
		"canvas_id":   "F123",
		"channel_ids": "C123",
		"user_ids":    "U123",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined")
}

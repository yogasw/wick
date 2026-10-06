package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/pkg/slackmd"
)

const mdTable = "| Area | Bisa apa | Contoh |\n|---|---|---|\n| Logs | cek error | Loki |\n| Tiket | buat tiket | Notion |"

// captureSlack serves one Slack Web API call and keeps its JSON body.
func captureSlack(t *testing.T) *map[string]any {
	t.Helper()
	captured := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000100"}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)
	return &captured
}

func TestSendMessageMarkdownTableUsesMarkdownBlock(t *testing.T) {
	captured := captureSlack(t)
	_, err := sendMessage(newCtxWithInput(t, map[string]string{"channel": "C1", "text": mdTable}))
	require.NoError(t, err)

	body := *captured
	blocks := body["blocks"].([]any)
	require.Len(t, blocks, 2) // markdown block + signed footer
	assert.Equal(t, map[string]any{"type": "markdown", "text": mdTable}, blocks[0])
	assert.Equal(t, "context", blocks[1].(map[string]any)["type"])
	assert.Equal(t, slackmd.Fallback(mdTable), body["text"])
	if out, err := json.MarshalIndent(body, "", "  "); err == nil {
		t.Logf("send_message payload:\n%s", out)
	}
}

func TestSendMessagePlainTextKeepsSection(t *testing.T) {
	captured := captureSlack(t)
	_, err := sendMessage(newCtxWithInput(t, map[string]string{"channel": "C1", "text": "halo *tim*"}))
	require.NoError(t, err)

	body := *captured
	blocks := body["blocks"].([]any)
	assert.Equal(t, "section", blocks[0].(map[string]any)["type"])
	assert.Equal(t, "halo *tim*", body["text"])
}

func TestSendMessageCallerBlocksUntouched(t *testing.T) {
	captured := captureSlack(t)
	_, err := sendMessage(newCtxWithInput(t, map[string]string{
		"channel": "C1", "text": mdTable,
		"blocks": `[{"type":"section","text":{"type":"mrkdwn","text":"mine"}}]`,
	}))
	require.NoError(t, err)

	body := *captured
	assert.Equal(t, "section", body["blocks"].([]any)[0].(map[string]any)["type"])
	assert.Equal(t, mdTable, body["text"])
}

func TestUpdateMessageMarkdownTableUsesMarkdownBlock(t *testing.T) {
	captured := captureSlack(t)
	_, err := updateMessage(newCtxWithInput(t, map[string]string{"channel": "C1", "ts": "1700000000.000100", "text": mdTable}))
	require.NoError(t, err)

	body := *captured
	blocks := body["blocks"].([]any)
	assert.Equal(t, map[string]any{"type": "markdown", "text": mdTable}, blocks[0])
	assert.Equal(t, slackmd.Fallback(mdTable), body["text"])
}

package slack

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
)

// footerText pulls the rendered footer string out of the context block.
func footerText(t *testing.T, block map[string]any) string {
	t.Helper()
	els, _ := block["elements"].([]any)
	if len(els) == 0 {
		t.Fatalf("footer block has no elements: %v", block)
	}
	el, _ := els[0].(map[string]any)
	s, _ := el["text"].(string)
	return s
}

// newFooterCtx builds a connector Ctx for footer tests, optionally stamping
// the pre-resolved session-owner bot id.
func newFooterCtx(ownerBotID string) *connector.Ctx {
	c := connector.NewCtx(context.Background(), "row",
		map[string]string{"auth_mode": "user_token", "user_token": "xoxp-yoga", "bot_token": "xoxb-bot"},
		map[string]string{}, http.DefaultClient, nil, nil)
	if ownerBotID != "" {
		c.SetOwnerBotID(ownerBotID)
	}
	return c
}

// TestSignedFooter_OwnerBot verifies the footer names the SESSION OWNER's
// bot, regardless of the connector's own (here user_token) auth mode — so a
// user-token send can never surface the human (@Yoga) as the footer.
func TestSignedFooter_OwnerBot(t *testing.T) {
	c := newFooterCtx("UOWNER")
	if got := footerText(t, signedFooterBlock(c)); got != "Sent using <@UOWNER>" {
		t.Errorf("footer = %q, want Sent using <@UOWNER>", got)
	}
}

// TestSignedFooter_NoOwnerFallsBackToAppName verifies that a call with no
// resolved owner bot (cron/UI/REST, or unresolved) falls back to the app
// name, never to a token-derived identity.
func TestSignedFooter_NoOwnerFallsBackToAppName(t *testing.T) {
	c := newFooterCtx("") // no owner bot
	got := footerText(t, signedFooterBlock(c))
	if !strings.HasPrefix(got, "Sent using *") {
		t.Errorf("no-owner footer = %q, want app-name fallback", got)
	}
}

// TestSessionIDInputKey pins the SendMessageInput.SessionID field's derived
// input key to "session_id" — the connector's footer resolution and the MCP
// service both read input["session_id"], so the reflected key must match.
func TestSessionIDInputKey(t *testing.T) {
	got := entity.StructToConfigs(SendMessageInput{})
	found := false
	keys := make([]string, 0, len(got))
	for _, c := range got {
		keys = append(keys, c.Key)
		if c.Key == "session_id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("SendMessageInput has no 'session_id' input key; got %v", keys)
	}
}

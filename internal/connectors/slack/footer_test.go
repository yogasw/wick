package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
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

// TestSignedFooter_PerInstanceBot verifies the footer reflects the calling
// connector instance's OWN bot, resolved from its token — not a process
// global. Two instances with different tokens must yield different @mentions.
func TestSignedFooter_PerInstanceBot(t *testing.T) {
	botIDByToken = atomic.Value{} // reset cache for isolation

	// auth.test echoes a user_id derived from the bearer token so each
	// instance maps to a distinct bot.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := "Uunknown"
		switch {
		case strings.Contains(r.Header.Get("Authorization"), "xoxb-alice"):
			uid = "UALICE"
		case strings.Contains(r.Header.Get("Authorization"), "xoxb-bob"):
			uid = "UBOB"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"user_id":"` + uid + `"}`))
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	alice := connector.NewCtx(context.Background(), "row-alice",
		map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-alice"},
		map[string]string{}, http.DefaultClient, nil, nil)
	bob := connector.NewCtx(context.Background(), "row-bob",
		map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-bob"},
		map[string]string{}, http.DefaultClient, nil, nil)

	if got := footerText(t, signedFooterBlock(alice)); got != "Sent using <@UALICE>" {
		t.Errorf("alice footer = %q, want Sent using <@UALICE>", got)
	}
	if got := footerText(t, signedFooterBlock(bob)); got != "Sent using <@UBOB>" {
		t.Errorf("bob footer = %q, want Sent using <@UBOB>", got)
	}
}

// TestSignedFooter_FallbackOnEmptyToken verifies the app-name fallback when
// the instance has no token (auth.test never called).
func TestSignedFooter_FallbackOnEmptyToken(t *testing.T) {
	botIDByToken = atomic.Value{}

	c := connector.NewCtx(context.Background(), "row-empty",
		map[string]string{"auth_mode": "bot_token", "bot_token": ""},
		map[string]string{}, http.DefaultClient, nil, nil)

	got := footerText(t, signedFooterBlock(c))
	if !strings.HasPrefix(got, "Sent using *") {
		t.Errorf("empty-token footer = %q, want app-name fallback", got)
	}
}

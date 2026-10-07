// Self-test: the self-send op, which is the half of this connector that
// needs no database, no push service and no browser to be wrong.
package notifications

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestClickURLDefaultsToTheCallingConversation(t *testing.T) {
	// The reason the option exists: a notification that says "done" and opens
	// a blank app makes you hunt for what it was about.
	url, mode := clickURL("", "", "sess-1")
	if url != "/tools/agents/sessions/sess-1" || mode != "conversation" {
		t.Fatalf("got %q/%q, want the calling session's page", url, mode)
	}
	if u2, _ := clickURL("conversation", "", "sess-1"); u2 != url {
		t.Fatalf("explicit conversation = %q, want the same as the default", u2)
	}
}

func TestClickURLHomeAndCustom(t *testing.T) {
	if url, mode := clickURL("home", "/ignored", "sess-1"); url != "/" || mode != "home" {
		t.Fatalf("home = %q/%q, want //home", url, mode)
	}
	if url, mode := clickURL("custom", "/tools/agents", "sess-1"); url != "/tools/agents" || mode != "custom" {
		t.Fatalf("custom = %q/%q, want /tools/agents/custom", url, mode)
	}
	// custom with nothing to go to is a mistake, not a reason to refuse the
	// notification — the front page is a worse target than what was asked
	// for, and a better one than a broken link.
	if url, mode := clickURL("custom", "  ", "sess-1"); url != "/" || mode != "home" {
		t.Fatalf("empty custom = %q/%q, want //home", url, mode)
	}
}

// A REST or scheduled call has no conversation to open. It still gets its
// notification — the click target is not worth failing a send over.
func TestClickURLWithoutASessionFallsBack(t *testing.T) {
	if url, mode := clickURL("conversation", "", ""); url != "/" || !strings.HasPrefix(mode, "home") {
		t.Fatalf("no session = %q/%q, want the front page", url, mode)
	}
	// …unless the caller named somewhere, which beats guessing.
	if url, mode := clickURL("conversation", "/admin/users", ""); url != "/admin/users" || mode != "custom" {
		t.Fatalf("no session + custom url = %q/%q, want /admin/users/custom", url, mode)
	}
}

func TestClickURLIgnoresCase(t *testing.T) {
	if _, mode := clickURL("  HOME ", "", "sess-1"); mode != "home" {
		t.Fatalf("mode = %q, want home — the dropdown value should not be case-sensitive", mode)
	}
}

// Without a caller there is no "me". The op says so and names the way to
// address somebody explicitly, rather than failing with "not authenticated"
// on a call that IS authenticated — just not by a person.
func TestSendToMeRefusesACallWithNoUser(t *testing.T) {
	h := handlers{}
	c := connector.NewCtx(context.Background(), "self-test", nil, map[string]string{"body": "hi"}, nil, nil, nil)
	_, err := h.sendToMe(c)
	if err == nil {
		t.Fatal("expected a refusal when the call has no user behind it")
	}
	if !strings.Contains(err.Error(), "send_to_push_id") {
		t.Fatalf("error should point at the op that CAN address someone: %v", err)
	}
}

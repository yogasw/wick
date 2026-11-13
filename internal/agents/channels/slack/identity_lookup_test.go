package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"
)

// usersInfoAPI is a Slack API whose users.info either fails (as a rate
// limit or an outage does) or returns one person with an email.
func usersInfoAPI(t *testing.T, fail bool) *slackgo.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fail || !strings.HasSuffix(r.URL.Path, "users.info") {
			_, _ = w.Write([]byte(`{"ok":false,"error":"fatal_error"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U01TESTUSR","name":"user-a","profile":{"email":"user-a@example.com","real_name":"User A"}}}`))
	}))
	t.Cleanup(srv.Close)
	return slackgo.New("xoxb-test", slackgo.OptionAPIURL(srv.URL+"/"))
}

// When users.info fails the sender is unknown. The message must be
// refused with a retry hint instead of running as the channel owner.
func TestUsersInfoFailureRefusesSender(t *testing.T) {
	c := &Channel{api: usersInfoAPI(t, true), users: fakeUsers{id: "user-a"}, ownerUserID: "user-owner"}
	msg := c.checkSenderIdentity("U01TESTUSR")
	if msg == "" {
		t.Fatal("a failed users.info let the message through to run as the channel owner")
	}
	if !strings.Contains(strings.ToLower(msg), "try again") {
		t.Errorf("refusal should tell the sender to retry: %q", msg)
	}
}

// The second lookup (the one that picks who the turn runs as) reports the
// failure instead of falling back to the channel owner.
func TestUsersInfoFailureIsReportedNotOwner(t *testing.T) {
	c := &Channel{api: usersInfoAPI(t, true), users: fakeUsers{id: "user-a"}, ownerUserID: "user-owner"}
	id, ok, failed := c.resolveSessionOwner("U01TESTUSR")
	if ok || id != "" || !failed {
		t.Fatalf("got id=%q ok=%v failed=%v, want an unresolved, failed lookup", id, ok, failed)
	}
}

// Legitimate paths are unchanged: a working lookup resolves the sender, a
// sender linked through OAuth still gets in while users.info is down, and
// a channel without identity mapping keeps its legacy shared identity.
func TestSenderIdentityLegitimatePaths(t *testing.T) {
	ok := &Channel{api: usersInfoAPI(t, false), users: fakeUsers{id: "user-a"}, ownerUserID: "user-owner"}
	if msg := ok.checkSenderIdentity("U01TESTUSR"); msg != "" {
		t.Errorf("known sender refused: %q", msg)
	}
	if id, resolved, failed := ok.resolveSessionOwner("U01TESTUSR"); id != "user-a" || !resolved || failed {
		t.Errorf("known sender: id=%q resolved=%v failed=%v", id, resolved, failed)
	}

	linked := &Channel{api: usersInfoAPI(t, true), users: fakeUsers{id: "user-a"}, ownerUserID: "user-owner",
		wickUserIDFn: func(context.Context, string) (string, bool) { return "user-linked", true }}
	if msg := linked.checkSenderIdentity("U01TESTUSR"); msg != "" {
		t.Errorf("oauth-linked sender refused during a users.info outage: %q", msg)
	}
	if id, resolved, failed := linked.resolveSessionOwner("U01TESTUSR"); id != "user-linked" || !resolved || failed {
		t.Errorf("oauth-linked sender: id=%q resolved=%v failed=%v", id, resolved, failed)
	}

	legacy := &Channel{api: usersInfoAPI(t, true), ownerUserID: "user-owner"}
	if msg := legacy.checkSenderIdentity("U01TESTUSR"); msg != "" {
		t.Errorf("channel without identity mapping must keep the legacy path: %q", msg)
	}
	if _, resolved, failed := legacy.resolveSessionOwner("U01TESTUSR"); resolved || failed {
		t.Errorf("legacy channel: resolved=%v failed=%v, want owner fallback", resolved, failed)
	}
}

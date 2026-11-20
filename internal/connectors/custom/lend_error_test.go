package custom

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// lentCall runs one tools/call as caller on a lend from lender asked by
// requester, with names for every user, and returns the error.
func (f *tokenFixture) lentCall(t *testing.T, serverID, instanceID, caller, lender, requester, accountID string) error {
	t.Helper()
	c := connector.NewCtx(context.Background(), instanceID, map[string]string{}, map[string]string{}, http.DefaultClient, nil, nil)
	c.SetCallerUserID(caller)
	if lender != "" {
		c.SetLend(lender, requester)
	}
	c.SetAccountID(accountID)
	c.SetUserNameResolver(func(id string) (string, bool) {
		return map[string]string{"user-owner": "Owner One", "user-a": "Asker A", "user-b": "Bee B"}[id], true
	})
	_, err := f.svc.executeMCP(c, MCPSource{ServerID: serverID, ToolName: "list_mocks"}, nil)
	return err
}

// A lent per-user connector whose lender never added a token must say so
// plainly: it is lent, by whom, for whom, and who must connect — not tell
// the asker to add "your own" token, which they cannot fix.
func TestLentPerUserTokenErrorNamesLenderAndAsker(t *testing.T) {
	f := newTokenFixture(t)
	srv, _, instanceID := f.save(t, headerForm("Mocker Lend Msg", true), false)
	if err := f.svc.SaveUserToken(context.Background(), instanceID, "user-a", "Asker A", map[string]string{"X-Api-Key": "key-a"}); err != nil {
		t.Fatal(err)
	}
	err := f.lentCall(t, srv.ID, instanceID, "user-owner", "user-owner", "user-a", "")
	if err == nil {
		t.Fatal("lent call without the lender's token must fail")
	}
	msg := err.Error()
	for _, want := range []string{"lent", "Owner One", "user-owner", "Asker A", "user-a", "Mocker Lend Msg", instanceID} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "key-a") {
		t.Fatalf("message leaked a token: %s", msg)
	}
}

// Naming someone else's private account says whose it is and who called.
func TestPerUserAccountNotYoursNamesOwnerAndCaller(t *testing.T) {
	f := newTokenFixture(t)
	srv, _, instanceID := f.save(t, headerForm("Mocker Owner Msg", true), false)
	if err := f.svc.SaveUserToken(context.Background(), instanceID, "user-b", "Bee B", map[string]string{"X-Api-Key": "key-b"}); err != nil {
		t.Fatal(err)
	}
	accs, _ := f.conns.ListAccounts(context.Background(), instanceID)
	if len(accs) != 1 {
		t.Fatalf("accounts = %d", len(accs))
	}
	err := f.lentCall(t, srv.ID, instanceID, "user-a", "", "", accs[0].ID)
	if err == nil {
		t.Fatal("another user's private account must be refused")
	}
	msg := err.Error()
	for _, want := range []string{accs[0].ID, "belongs to another user", "user-b", "Bee B", "user-a", "not lent"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "key-b") {
		t.Fatalf("message leaked a token: %s", msg)
	}
}

// Without a lend the first sentence is unchanged: the caller is told to
// add their own token.
func TestPerUserTokenErrorWithoutLendKeepsWording(t *testing.T) {
	f := newTokenFixture(t)
	srv, _, instanceID := f.save(t, headerForm("Mocker Plain Msg", true), false)
	err := f.lentCall(t, srv.ID, instanceID, "user-a", "", "", "")
	if err == nil || !strings.HasPrefix(err.Error(), "you have not added your own Mocker Plain Msg token yet") {
		t.Fatalf("plain message changed: %v", err)
	}
}

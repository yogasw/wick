package custom

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// A per-account tag share must work on a custom per-user connector the same
// way it shows on the connector page: a caller carrying the account's filter
// tag may run as it, a caller without the tag is still refused, and the
// pool itself stays private.
func TestPerUserAccountSharedByTag(t *testing.T) {
	f := newPerUserFixture(t)
	srv, _, instanceID := f.savePerUser(t, "Helpdesk PU Tag")
	f.connect(t, srv, instanceID, "user-a", "code-A")

	accs, err := f.conns.ListAccounts(context.Background(), instanceID)
	if err != nil || len(accs) != 1 {
		t.Fatalf("accounts = %v, %v", accs, err)
	}
	accA := accs[0].ID

	userTags := map[string][]string{}
	f.conns.SetUserFilterTagResolver(func(_ context.Context, userID string) []string {
		return userTags[userID]
	})

	// No share yet: user-b naming user-a's account is refused.
	if err := f.call(t, srv.ID, instanceID, "user-b", accA); err == nil || !strings.Contains(err.Error(), "belongs to another user") {
		t.Fatalf("unshared: err = %v", err)
	}

	tag := &entity.Tag{Name: "team-x", IsFilter: true}
	if err := f.db.Create(tag).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&entity.ToolTag{ToolPath: connectors.AccountTagPath(accA), TagID: tag.ID}).Error; err != nil {
		t.Fatal(err)
	}

	// Account tagged, but user-b does not carry the tag: still refused.
	if err := f.call(t, srv.ID, instanceID, "user-b", accA); err == nil || !strings.Contains(err.Error(), "belongs to another user") {
		t.Fatalf("tagged account, untagged caller: err = %v", err)
	}

	// user-b carries the tag: runs as user-a's account.
	userTags["user-b"] = []string{tag.ID}
	if err := f.call(t, srv.ID, instanceID, "user-b", accA); err != nil {
		t.Fatalf("shared by tag: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-A" {
		t.Errorf("shared-by-tag call used %q", got)
	}

	// A third user without the tag stays refused.
	if err := f.call(t, srv.ID, instanceID, "user-c", accA); err == nil || !strings.Contains(err.Error(), "belongs to another user") {
		t.Fatalf("other caller: err = %v", err)
	}
}

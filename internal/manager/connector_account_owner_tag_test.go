package manager

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
)

// newOwnerTagHandler wires the two services this rule spans — connectors (the
// row + its accounts) and tags (the "owner:{rowID}" grant) — onto one DB, the
// way the real handler is wired.
func newOwnerTagHandler(t *testing.T) (*Handler, *connectors.Service, *tags.Service, *gorm.DB) {
	t.Helper()
	db := newAPISQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	connSvc := connectors.NewServiceFromDB(db)
	connSvc.SetConfigs(cfgsSvc)
	if err := connSvc.Bootstrap(context.Background(), []connector.Module{apiDetailModule("slack")}); err != nil {
		t.Fatalf("connectors bootstrap: %v", err)
	}
	tagSvc := tags.NewService(db)
	return &Handler{connectors: connSvc, tags: tagSvc}, connSvc, tagSvc, db
}

// A multi-account row with one account per person, each private to whoever
// connected it — the shape of a shared Slack bot row.
func seedSharedSlackRow(t *testing.T, svc *connectors.Service) entity.Connector {
	t.Helper()
	ctx := context.Background()
	row, err := svc.Create(ctx, "slack", "Slack: shared bot", map[string]string{}, "u-creator")
	if err != nil {
		t.Fatalf("create row: %v", err)
	}
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true, AllowOthersConnectSSO: true}); err != nil {
		t.Fatalf("access policy: %v", err)
	}
	for _, u := range []struct{ wickID, name string }{
		{"u-creator", "creator"},
		{"u-hana", "hana"},
		{"u-anggun", "anggun"},
	} {
		if err := svc.SaveAccount(ctx, row.ID, u.wickID, "ext-"+u.name, u.name, "tok-"+u.name); err != nil {
			t.Fatalf("save account %s: %v", u.name, err)
		}
	}
	fresh, err := svc.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("reload row: %v", err)
	}
	return *fresh
}

// TestCanSeeAllAccountsIgnoresOwnerTag is the regression: carrying
// "owner:{rowID}" is a configure grant, not "I administer every account on
// this row". Before the fix, ownsConnectorRow accepted the tag here, so a
// user who merely connected their own account to a shared row saw — and could
// manage — everybody else's connected account.
func TestCanSeeAllAccountsIgnoresOwnerTag(t *testing.T) {
	h, connSvc, tagSvc, db := newOwnerTagHandler(t)
	row := seedSharedSlackRow(t, connSvc)
	ctx := context.Background()

	// Hand Hana the owner tag, exactly as CreateOwnerTag does.
	if err := tagSvc.CreateOwnerTag(ctx, row.ID, "u-hana"); err != nil {
		t.Fatalf("create owner tag: %v", err)
	}
	hana := &entity.User{ID: "u-hana", Role: entity.RoleUser, Approved: true}
	creator := &entity.User{ID: "u-creator", Role: entity.RoleUser, Approved: true}

	if h.canSeeAllAccounts(hana, row) {
		t.Fatal("owner:{rowID} tag still grants sight of every connected account")
	}
	if !h.canSeeAllAccounts(creator, row) {
		t.Fatal("the user who created the row lost sight of its account pool")
	}

	// …and the listing agrees: Hana sees her own account, nobody else's.
	mine := h.visibleAccountsForRow(ctx, row, hana, tagIDsOf(t, db, "u-hana"))
	if len(mine) != 1 || mine[0].DisplayName != "hana" {
		t.Fatalf("expected only hana's own account, got %v", accountNames(mine))
	}
	all := h.visibleAccountsForRow(ctx, row, creator, nil)
	if len(all) != 3 {
		t.Fatalf("creator should still see the whole pool, got %v", accountNames(all))
	}
}

// TestOwnerTagStillConfigures pins the other half: the tag keeps meaning what
// it was created for — configuring the row — so narrowing account visibility
// does not quietly revoke a grant an admin handed out.
func TestOwnerTagStillConfigures(t *testing.T) {
	h, connSvc, tagSvc, _ := newOwnerTagHandler(t)
	row := seedSharedSlackRow(t, connSvc)
	if err := tagSvc.CreateOwnerTag(context.Background(), row.ID, "u-hana"); err != nil {
		t.Fatalf("create owner tag: %v", err)
	}
	hana := &entity.User{ID: "u-hana", Role: entity.RoleUser, Approved: true}
	if !h.canConfigureRow(hana, &row) {
		t.Fatal("owner tag no longer grants configure — the grant was revoked, not narrowed")
	}
}

// tagIDsOf reads the caller's filter tags the way the request path does —
// straight from user_tags — so the test exercises the real tag set.
func tagIDsOf(t *testing.T, db *gorm.DB, userID string) []string {
	t.Helper()
	var ids []string
	err := db.Table("user_tags ut").
		Joins("JOIN tags t ON t.id = ut.tag_id").
		Where("ut.user_id = ? AND t.is_filter = ?", userID, true).
		Pluck("ut.tag_id", &ids).Error
	if err != nil {
		t.Fatalf("user tag ids: %v", err)
	}
	return ids
}

func accountNames(accs []entity.ConnectorAccount) []string {
	out := make([]string, 0, len(accs))
	for _, a := range accs {
		out = append(out, a.DisplayName)
	}
	return out
}

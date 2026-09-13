package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// ssoAdminModule is a minimal OAuth connector so its rows can hold accounts.
func ssoAdminModule() connector.Module {
	return connector.Module{
		Meta: connector.Meta{Key: "sso-admin", Name: "SSO Admin", Description: "admin accounts test"},
		Operations: []connector.Category{
			connector.Cat("", "", connector.Op("ping", "Ping", "noop", struct{}{},
				func(c *connector.Ctx) (any, error) { return "ok", nil }, wickdocs.Docs{})),
		},
		OAuth: &connector.OAuthMeta{DisplayName: "SSO Admin"},
	}
}

func newAdminConnectorsHandler(t *testing.T) (*Handler, *connectors.Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)

	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := connectors.NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{ssoAdminModule()}))
	return &Handler{repo: newRepo(db), connectors: svc}, svc, db
}

// The admin connectors page lists each instance's connected accounts, with
// who connected them and the tags that share them.
func TestConnectorAccountsAdminListsAccountsWithOwnerAndTags(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, "sso-admin", "Row", nil, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}))
	require.NoError(t, db.Create(&entity.User{ID: "u-alice", Name: "Alice", Email: "alice@x.test"}).Error)
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-a"))

	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, accs, 1)
	tag := &entity.Tag{Name: "team-support", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.ToolTag{ToolPath: connectors.AccountTagPath(accs[0].ID), TagID: tag.ID}).Error)

	byRow, tagsByPath, owners := h.connectorAccountsAdmin(ctx, []entity.Connector{*row})
	require.Len(t, byRow[row.ID], 1)
	require.Equal(t, []string{tag.ID}, tagsByPath[connectors.AccountTagPath(accs[0].ID)])
	require.Equal(t, "Alice (alice@x.test)", owners["u-alice"])
}

// The per-account tag form writes to the same tool_tags table as every other
// taggable thing, on the account path.
func TestSetConnectorAccountTagsAdmin(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, "sso-admin", "Row", nil, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-a"))
	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	tag := &entity.Tag{Name: "team-support", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)

	req := httptest.NewRequest(http.MethodPost, "/admin/connectors/"+row.ID+"/accounts/"+accs[0].ID+"/tags",
		strings.NewReader("tags_submitted=1&tag_ids[]="+tag.ID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", row.ID)
	req.SetPathValue("accountID", accs[0].ID)
	rec := httptest.NewRecorder()
	h.setConnectorAccountTagsAdmin(rec, req)
	require.Equal(t, http.StatusFound, rec.Code)

	tagIDs, err := svc.AccountTagIDs(ctx, accs)
	require.NoError(t, err)
	require.Equal(t, []string{tag.ID}, tagIDs[accs[0].ID])

	// An unknown account id is refused rather than writing a stray tag row.
	req2 := httptest.NewRequest(http.MethodPost, "/admin/connectors/"+row.ID+"/accounts/nope/tags", strings.NewReader(""))
	req2.SetPathValue("id", row.ID)
	req2.SetPathValue("accountID", "nope")
	rec2 := httptest.NewRecorder()
	h.setConnectorAccountTagsAdmin(rec2, req2)
	require.Equal(t, http.StatusNotFound, rec2.Code)
}

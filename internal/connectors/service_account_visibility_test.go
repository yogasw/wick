package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/pkg/adminscope"
	wickentity "github.com/yogasw/wick/pkg/entity"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// accountVisibilityModule is an OAuth connector whose single op echoes the
// token it ran with, so a test can tell WHICH account executed.
func accountVisibilityModule() connector.Module {
	echo := func(c *connector.Ctx) (any, error) { return c.Cfg("user_token"), nil }
	return connector.Module{
		Meta: connector.Meta{Key: "acct-vis", Name: "Acct Vis", Description: "account visibility test"},
		Operations: []connector.Category{
			connector.Cat("", "", connector.Op("whoami", "Who am I", "echo the token", struct{}{}, echo, wickdocs.Docs{})),
		},
		OAuth: &connector.OAuthMeta{DisplayName: "Acct Vis"},
	}
}

func newSvcAccountVis(t *testing.T) *Service {
	t.Helper()
	svc, _ := newSvcAccountVisDB(t)
	return svc
}

// newSvcAccountVisDB also hands back the DB so a test can attach tags
// straight to the tool_tags table the way the admin page does.
func newSvcAccountVisDB(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{accountVisibilityModule()}))
	return svc, db
}

// tagAccount attaches a filter tag to one account, exactly as the admin
// connectors page does (tag row + tool_tags link on AccountTagPath).
func tagAccount(t *testing.T, db *gorm.DB, accountID, tagName string) string {
	t.Helper()
	tag := &entity.Tag{Name: tagName, IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.ToolTag{ToolPath: AccountTagPath(accountID), TagID: tag.ID}).Error)
	return tag.ID
}

// seedTwoAccounts returns a multi-account row owned by "u-owner" with one
// account connected by alice and one by bob.
func seedTwoAccounts(t *testing.T, svc *Service) entity.Connector {
	t.Helper()
	ctx := context.Background()
	row, err := svc.Create(ctx, "acct-vis", "Row", nil, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, AccessPolicy{EnableSSO: true, MultiAccount: true}))
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-alice"))
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-bob", "ext-b", "bob", "tok-bob"))
	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)
	return *fresh
}

func TestListAccountsVisibleToKeepsAccountsPrivateByDefault(t *testing.T) {
	svc := newSvcAccountVis(t)
	row := seedTwoAccounts(t, svc)
	ctx := context.Background()

	mine, err := svc.ListAccountsVisibleTo(ctx, row, AccountAccess{UserID: "u-bob"})
	require.NoError(t, err)
	require.Len(t, mine, 1, "a plain user sees only the account they connected")
	require.Equal(t, "bob", mine[0].DisplayName)

	all, err := svc.ListAccountsVisibleTo(ctx, row, AccountAccess{UserID: "u-owner", Privileged: true})
	require.NoError(t, err)
	require.Len(t, all, 2, "admin/owner sees the whole pool")

	none, err := svc.ListAccountsVisibleTo(ctx, row, AccountAccess{UserID: "u-carol"})
	require.NoError(t, err)
	require.Empty(t, none, "a user with no account of their own sees no account at all")
}

func TestListAccountsVisibleToSharedPool(t *testing.T) {
	svc := newSvcAccountVis(t)
	row := seedTwoAccounts(t, svc)
	ctx := context.Background()
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, AccessPolicy{EnableSSO: true, MultiAccount: true, AllowOthersSeeAccounts: true}))
	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)

	accs, err := svc.ListAccountsVisibleTo(ctx, *fresh, AccountAccess{UserID: "u-bob"})
	require.NoError(t, err)
	require.Len(t, accs, 2, "AllowOthersSeeAccounts puts every account back in view")
}

// An unowned account (connected before ownership was recorded) stays visible,
// so an existing install does not lose the account it runs on.
func TestAccountVisibleToLegacyAccountStaysVisible(t *testing.T) {
	row := entity.Connector{ID: "row-1", CreatedBy: "u-owner"}
	acc := entity.ConnectorAccount{ID: "acc-1", ConnectorID: "row-1"}
	require.True(t, AccountVisibleTo(row, acc, nil, AccountAccess{UserID: "u-someone"}))
}

func TestExecuteRejectsAnotherUsersAccount(t *testing.T) {
	svc := newSvcAccountVis(t)
	row := seedTwoAccounts(t, svc)
	ctx := context.Background()
	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	var alice entity.ConnectorAccount
	for _, a := range accs {
		if a.WickUserID == "u-alice" {
			alice = a
		}
	}
	require.NotEmpty(t, alice.ID)

	run := func(userID string, isAdmin bool) (*ExecuteResult, error) {
		return svc.Execute(ctx, ExecuteParams{
			ConnectorID:  row.ID,
			OperationKey: "whoami",
			Input:        map[string]string{},
			Source:       entity.ConnectorRunSourceMCP,
			UserID:       userID,
			IsAdmin:      isAdmin,
			AccountID:    alice.ID,
		})
	}

	_, err = run("u-bob", false)
	require.Error(t, err, "bob must not be able to run as alice's private account")
	require.Contains(t, err.Error(), "not accessible")

	res, err := run("u-alice", false)
	require.NoError(t, err, "alice runs as her own account")
	require.Contains(t, res.ResponseJSON, "tok-alice")

	res, err = run("u-admin", true)
	require.NoError(t, err, "an admin administers the instance and may run as any account")
	require.Contains(t, res.ResponseJSON, "tok-alice")

	res, err = run("u-owner", false)
	require.NoError(t, err, "the instance owner may run as any account on their row")
	require.Contains(t, res.ResponseJSON, "tok-alice")
}

// A per-account tag is the granular share: one account handed to a team,
// without opening every other account on the row.
func TestAccountSharedByTagIsVisibleToTagHolders(t *testing.T) {
	svc, db := newSvcAccountVisDB(t)
	row := seedTwoAccounts(t, svc)
	ctx := context.Background()
	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	var alice entity.ConnectorAccount
	for _, a := range accs {
		if a.WickUserID == "u-alice" {
			alice = a
		}
	}
	require.NotEmpty(t, alice.ID)
	tagID := tagAccount(t, db, alice.ID, "team-support")

	// Bob carries the tag → he sees his own account AND alice's.
	shared, err := svc.ListAccountsVisibleTo(ctx, row, AccountAccess{UserID: "u-bob", TagIDs: []string{tagID}})
	require.NoError(t, err)
	require.Len(t, shared, 2)

	// Carol carries nothing and connected nothing → still sees nothing.
	none, err := svc.ListAccountsVisibleTo(ctx, row, AccountAccess{UserID: "u-carol"})
	require.NoError(t, err)
	require.Empty(t, none)

	// …and the share is real: bob may execute as the tagged account.
	res, err := svc.Execute(ctx, ExecuteParams{
		ConnectorID:  row.ID,
		OperationKey: "whoami",
		Input:        map[string]string{},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "u-bob",
		TagIDs:       []string{tagID},
		AccountID:    alice.ID,
	})
	require.NoError(t, err)
	require.Contains(t, res.ResponseJSON, "tok-alice")

	// Carol, with no tag, is still refused.
	_, err = svc.Execute(ctx, ExecuteParams{
		ConnectorID:  row.ID,
		OperationKey: "whoami",
		Input:        map[string]string{},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       "u-carol",
		AccountID:    alice.ID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not accessible")
}

// The admin role is one switch per surface: admin_see_all_connectors decides
// whether being an admin is, by itself, enough to see every instance and
// every connected account. Off = an admin is scoped like anybody else.
func TestAdminSeeAllConnectorsKnob(t *testing.T) {
	svc, db := newSvcAccountVisDB(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, "acct-vis", "Tagged row", nil, "u-owner")
	require.NoError(t, err)

	// Tag the row with a filter tag the admin does not carry.
	tag := &entity.Tag{Name: "team-only", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.ToolTag{ToolPath: "/connectors/" + row.ID, TagID: tag.ID}).Error)

	// Default (config never written) = legacy behaviour: admin sees it.
	require.True(t, svc.AdminSeesAllConnectors())
	rows, err := svc.ListVisibleTo(ctx, nil, true)
	require.NoError(t, err)
	require.True(t, containsRow(rows, row.ID), "admin should see the tagged row while the knob is on")

	// Seed the config row the agents config struct declares at boot, then
	// turn the knob off.
	require.NoError(t, svc.cfgs.EnsureOwned(ctx, "agents", wickentity.Config{Key: adminscope.KeyAdminSeeAllConnectors, Value: "true", Type: "bool"}))
	require.NoError(t, svc.cfgs.SetOwned(ctx, "agents", adminscope.KeyAdminSeeAllConnectors, "false"))
	require.False(t, svc.AdminSeesAllConnectors())
	rows, err = svc.ListVisibleTo(ctx, nil, true)
	require.NoError(t, err)
	require.False(t, containsRow(rows, row.ID), "with the knob off an admin is scoped by tags like anyone else")

	// Carrying the tag brings it back — the admin is now a normal grantee.
	rows, err = svc.ListVisibleTo(ctx, []string{tag.ID}, true)
	require.NoError(t, err)
	require.True(t, containsRow(rows, row.ID))

	// Same answer at the single-row check the dispatch path uses.
	ok, err := svc.IsVisibleTo(ctx, row.ID, nil, true)
	require.NoError(t, err)
	require.False(t, ok)
}

func containsRow(rows []entity.Connector, id string) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

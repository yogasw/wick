package connectors

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// sharedCredModule is an OAuth connector that declares the instance-level
// token slots, and whose op echoes the token it ran with.
func sharedCredModule() connector.Module {
	echo := func(c *connector.Ctx) (any, error) { return "token=" + c.Cfg("user_token"), nil }
	return connector.Module{
		Meta: connector.Meta{Key: "shared-cred", Name: "Shared Cred", Description: "shared credential test"},
		Configs: []entity.Config{
			{Key: "user_token", Type: "text", IsSecret: true},
			{Key: "refresh_token", Type: "text", IsSecret: true},
		},
		Operations: []connector.Category{
			connector.Cat("", "", connector.Op("whoami", "Who am I", "echo the token", struct{}{}, echo, wickdocs.Docs{})),
		},
		OAuth: &connector.OAuthMeta{DisplayName: "Acme Cloud"},
	}
}

func newSvcSharedCred(t *testing.T) *Service {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{sharedCredModule()}))
	return svc
}

// seedSharedCredRow builds an SSO row holding a legacy instance token, with
// one account connected by alice.
func seedSharedCredRow(t *testing.T, svc *Service, multi bool) entity.Connector {
	t.Helper()
	ctx := context.Background()
	row, err := svc.Create(ctx, "shared-cred", "Team drive", map[string]string{"user_token": "tok-legacy", "refresh_token": "rt-legacy"}, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, AccessPolicy{EnableSSO: true, MultiAccount: multi}))
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-alice"))
	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)
	return *fresh
}

func runWhoami(svc *Service, row entity.Connector, userID, sessionID string) (*ExecuteResult, error) {
	return svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  row.ID,
		OperationKey: "whoami",
		Input:        map[string]string{},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       userID,
		SessionID:    sessionID,
	})
}

func TestExecuteAutoPicksCallersOwnAccount(t *testing.T) {
	svc := newSvcSharedCred(t)
	row := seedSharedCredRow(t, svc, true)

	res, err := runWhoami(svc, row, "u-alice", "")
	require.NoError(t, err)
	require.Contains(t, res.ResponseJSON, "tok-alice", "no account named: the caller's own account runs")

	runs, err := svc.repo.ListRunsByConnector(context.Background(), row.ID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	require.NotEmpty(t, runs[0].AccountID, "the auto-picked account is recorded on the run")
}

func TestExecuteAutoPickUsesSessionOwner(t *testing.T) {
	svc := newSvcSharedCred(t)
	row := seedSharedCredRow(t, svc, true)
	svc.SetSessionOwnerUserResolver(func(sid string) (string, bool) { return "u-alice", sid == "sess-1" })

	res, err := runWhoami(svc, row, "u-agent", "sess-1")
	require.NoError(t, err, "an agent session runs as the account of the human who owns it")
	require.Contains(t, res.ResponseJSON, "tok-alice")
}

func TestExecuteWithoutAccountFailsWhenSharedCredentialOff(t *testing.T) {
	svc := newSvcSharedCred(t)
	row := seedSharedCredRow(t, svc, true)

	_, err := runWhoami(svc, row, "u-carol", "")
	require.Error(t, err, "multi-account rows default to no shared credential")
	require.Contains(t, err.Error(), `no connected Acme Cloud account for you on "Team drive"`)
	require.Contains(t, err.Error(), "/manager/connectors/shared-cred/"+row.ID)
	require.NotContains(t, err.Error(), "tok-legacy")

	require.NoError(t, svc.repo.SetSharedCredentialPolicy(context.Background(), row.ID, true))
	res, err := runWhoami(svc, row, "u-carol", "")
	require.NoError(t, err, "turned on, the shared credential is the fallback again")
	require.Contains(t, res.ResponseJSON, "tok-legacy")
}

func TestSingleAccountRowKeepsSharedCredentialByDefault(t *testing.T) {
	svc := newSvcSharedCred(t)
	row := seedSharedCredRow(t, svc, false)

	allowed, err := svc.SharedCredentialAllowed(context.Background(), row)
	require.NoError(t, err)
	require.True(t, allowed)
	res, err := runWhoami(svc, row, "u-carol", "")
	require.NoError(t, err)
	require.Contains(t, res.ResponseJSON, "token=", "single-account rows keep running for everyone")
}

func TestSharedCredentialPolicyIgnoredWithoutSSO(t *testing.T) {
	svc := newSvcSharedCred(t)
	row := seedSharedCredRow(t, svc, true)
	require.NoError(t, svc.SetAccessPolicy(context.Background(), row.ID, AccessPolicy{MultiAccount: true}))
	fresh, err := svc.Get(context.Background(), row.ID)
	require.NoError(t, err)

	res, err := runWhoami(svc, *fresh, "u-alice", "")
	require.NoError(t, err)
	require.Contains(t, res.ResponseJSON, "tok-legacy", "SSO off: manual instance credentials, no account auto-pick")
}

func TestSetSharedCredentialOffClearsStoredTokens(t *testing.T) {
	svc := newSvcSharedCred(t)
	row := seedSharedCredRow(t, svc, true)
	require.True(t, svc.SharedCredentialStored(row))

	require.NoError(t, svc.SetSharedCredentialAllowed(context.Background(), row, false))
	require.False(t, svc.SharedCredentialStored(row))
	cfg := svc.LoadConfigs(row)
	require.Empty(t, cfg["user_token"])
	require.Empty(t, cfg["refresh_token"])

	require.NoError(t, svc.SetSharedCredentialAllowed(context.Background(), row, true))
	allowed, err := svc.SharedCredentialAllowed(context.Background(), row)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestOwnAccountPrefersHealthyThenNewest(t *testing.T) {
	svc := newSvcSharedCred(t)
	row := seedSharedCredRow(t, svc, true)
	ctx := context.Background()
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a2", "alice-2", "tok-alice-2"))

	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	byName := map[string]entity.ConnectorAccount{}
	for _, a := range accs {
		byName[a.DisplayName] = a
	}
	older, newer := byName["alice"], byName["alice-2"]
	now := time.Now()
	require.NoError(t, svc.repo.db.Model(&entity.ConnectorAccount{}).Where("id = ?", older.ID).Update("updated_at", now.Add(-time.Hour)).Error)
	require.NoError(t, svc.repo.db.Model(&entity.ConnectorAccount{}).Where("id = ?", newer.ID).Update("updated_at", now).Error)

	got, err := svc.OwnAccount(ctx, row.ID, "u-alice")
	require.NoError(t, err)
	require.Equal(t, newer.ID, got.ID, "the most recently updated account wins")

	require.NoError(t, svc.repo.SetAccountAuthError(ctx, newer.ID, "token revoked"))
	got, err = svc.OwnAccount(ctx, row.ID, "u-alice")
	require.NoError(t, err)
	require.Equal(t, older.ID, got.ID, "an account that still works beats a newer one needing a reconnect")

	none, err := svc.OwnAccount(ctx, row.ID, "u-carol")
	require.NoError(t, err)
	require.Nil(t, none)
}

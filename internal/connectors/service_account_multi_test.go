package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// One wick user connecting a second provider account ("Connect another")
// must add an account, not overwrite the first one. Reconnecting the same
// provider account refreshes its row instead of duplicating it.
func TestSaveAccount_MultiAccountKeysOnProviderIdentity(t *testing.T) {
	svc := newSvcAccountVis(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, "acct-vis", "Row", nil, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, AccessPolicy{EnableSSO: true, MultiAccount: true}))

	require.NoError(t, svc.SaveAccountTokens(ctx, row.ID, "u-yoga", "ext-work", "work", "tok-work", "ref-work", nil))
	require.NoError(t, svc.SaveAccountTokens(ctx, row.ID, "u-yoga", "ext-personal", "personal", "tok-personal", "ref-personal", nil))

	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, accs, 2, "second account of the same wick user must not replace the first")

	// Reconnect the first account; providers may omit the refresh token.
	require.NoError(t, svc.SaveAccountTokens(ctx, row.ID, "u-yoga", "ext-work", "work", "tok-work-2", "", nil))
	accs, err = svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, accs, 2)
	byExt := map[string]string{}
	refresh := map[string]string{}
	for _, a := range accs {
		byExt[a.ExternalUserID] = a.AccessToken
		refresh[a.ExternalUserID] = a.RefreshToken
	}
	require.Equal(t, "tok-work-2", byExt["ext-work"])
	require.Equal(t, "tok-personal", byExt["ext-personal"])
	require.Equal(t, "ref-work", refresh["ext-work"], "an omitted refresh token keeps the stored one")
}

// A row saved before external_user_id was keyed on is adopted by the
// owner's next connect instead of being left behind as a duplicate.
func TestSaveAccount_MultiAccountAdoptsLegacyBlankIdentity(t *testing.T) {
	svc := newSvcAccountVis(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, "acct-vis", "Row", nil, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, AccessPolicy{EnableSSO: true, MultiAccount: true}))

	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-yoga", "", "legacy", "tok-old"))
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-yoga", "ext-work", "work", "tok-new"))

	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, accs, 1)
	require.Equal(t, "ext-work", accs[0].ExternalUserID)
	require.Equal(t, "tok-new", accs[0].AccessToken)
}

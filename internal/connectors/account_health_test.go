package connectors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/pkg/connector"
)

func TestAccountHealthCheckIsolationAndRecovery(t *testing.T) {
	var report []connector.OpHealth
	var probeErr error
	mod := healthModule(&report, &probeErr)
	mod.HealthCheck = func(c *connector.Ctx) ([]connector.OpHealth, error) {
		if probeErr != nil {
			return nil, probeErr
		}
		require.Empty(t, c.Cfg("refresh_token"), "never use shared refresh token")
		return []connector.OpHealth{{Key: "a", OK: c.Cfg("user_token") == "personal-good", Reason: "needs scope"}, {Key: "b", OK: true}}, nil
	}
	svc, id := newSvcHealth(t, mod)
	ctx := t.Context()
	require.NoError(t, svc.SetAccessPolicy(ctx, id, AccessPolicy{EnableSSO: true, MultiAccount: true}))
	require.NoError(t, svc.SaveAccountTokens(ctx, id, "owner", "personal", "Personal", "personal-good", "", nil))
	require.NoError(t, svc.SaveAccountTokens(ctx, id, "owner", "work", "Work", "work-bad", "", nil))
	accounts, err := svc.ListAccounts(ctx, id)
	require.NoError(t, err)
	ids := map[string]string{}
	for _, acc := range accounts {
		ids[acc.ExternalUserID] = acc.ID
	}
	require.Len(t, ids, 2)
	require.NoError(t, svc.repo.SetSystemDisabled(ctx, id, "a", "shared scope missing"))
	states, err := svc.OperationStatesFull(ctx, id, mod.Meta.Key)
	require.NoError(t, err)
	personal, err := svc.GetAccount(ctx, ids["personal"])
	require.NoError(t, err)
	work, err := svc.GetAccount(ctx, ids["work"])
	require.NoError(t, err)
	require.True(t, AccountOpEnabled(personal, "a", states["a"]))
	require.False(t, AccountOpEnabled(work, "a", states["a"]))
	require.NoError(t, svc.SetAccountOpOverride(ctx, work.ID, "a", AccountOpOff))
	probeErr = errors.New("provider unavailable")
	_, err = svc.RunAccountHealthCheck(ctx, id, work.ID, "owner")
	require.Error(t, err)
	work, err = svc.GetAccount(ctx, work.ID)
	require.NoError(t, err)
	require.Contains(t, accountHealthLocks(work), "a")
	probeErr = nil
	require.NoError(t, svc.SaveAccountTokens(ctx, id, "owner", "work", "Work", "personal-good", "", nil))
	work, err = svc.GetAccount(ctx, work.ID)
	require.NoError(t, err)
	require.NotContains(t, accountHealthLocks(work), "a", "reconnect automatically clears recovered permissions")
	require.False(t, AccountOpEnabled(work, "a", states["a"]), "manual off is preserved")
	require.NoError(t, svc.SetAccountOpOverride(ctx, work.ID, "a", AccountOpInherit))
	work, err = svc.GetAccount(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, AccountOpEnabled(work, "a", states["a"]))
}

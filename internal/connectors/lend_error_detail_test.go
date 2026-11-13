package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

// A refused @account names the account's owner, who the call ran as and
// that it was not lent — ids and names, never the token.
func TestAccountRefusalNamesOwnerAndCaller(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvcAccountVisDB(t)
	row := seedTwoAccounts(t, svc)
	svc.SetUserNameResolver(func(id string) (string, bool) {
		return map[string]string{"u-alice": "Alice A", "u-bob": "Bob B"}[id], true
	})
	accs, err := svc.ListAccountsVisibleTo(ctx, row, AccountAccess{UserID: "u-owner", Privileged: true})
	require.NoError(t, err)
	var aliceAcc string
	for _, a := range accs {
		if a.WickUserID == "u-alice" {
			aliceAcc = a.ID
		}
	}
	require.NotEmpty(t, aliceAcc)
	_, err = svc.Execute(ctx, ExecuteParams{
		ConnectorID: row.ID, OperationKey: "whoami", Input: map[string]string{},
		Source: entity.ConnectorRunSourceMCP, UserID: "u-bob", AccountID: aliceAcc,
	})
	require.Error(t, err)
	msg := err.Error()
	for _, want := range []string{"is not accessible: it belongs to another user", aliceAcc, "u-alice (Alice A)", "runs as u-bob (Bob B)", "not lent", row.ID} {
		assert.Contains(t, msg, want)
	}
	assert.NotContains(t, msg, "tok-alice")
}

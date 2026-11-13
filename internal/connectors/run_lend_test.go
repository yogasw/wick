package connectors

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

// lendAgentScope is lendScope that also names the Team agent.
type lendAgentScope struct{ lendScope }

func (lendAgentScope) AgentID() string { return "agent-1" }

// A lent run records who really asked next to the lender, and a run that
// was not lent records nothing.
func TestLentRunRecordsRequester(t *testing.T) {
	bg := context.Background()
	svc, db := newSvcAccountVisDB(t)
	row, err := svc.Create(bg, "acct-vis", "Lent Row", nil, "u-lender")
	require.NoError(t, err)
	tagRow(t, db, row.ID, "custom:lent_audit")
	svc.SetIdentityLookup(func(_ context.Context, id string) (*entity.User, []string, error) {
		if id != "u-lender" {
			return nil, nil, errors.New("unknown user")
		}
		return &entity.User{ID: id}, nil, nil
	})
	svc.SetSessionOwnerUserResolver(func(sid string) (string, bool) { return "u-asker", sid == "sess-1" })
	ctx := WithAgentScope(bg, lendAgentScope{lendScope{conn: row.ID}.withLender("u-lender")})

	run := func(ctx context.Context, user, session string) string {
		res, err := svc.Execute(ctx, ExecuteParams{
			ConnectorID: row.ID, OperationKey: "whoami", Input: map[string]string{},
			Source: entity.ConnectorRunSourceMCP, UserID: user, SessionID: session,
		})
		require.NotNil(t, res, "execute: %v", err)
		return res.RunID
	}
	lentID := run(ctx, "u-stranger", "sess-1")
	ownID := run(bg, "u-lender", "")

	lends, err := svc.RunLends(bg, []string{lentID, ownID})
	require.NoError(t, err)
	l, ok := lends[lentID]
	require.True(t, ok, "the lent run must have a lend record")
	assert.Equal(t, "u-lender", l.OnBehalfOf)
	assert.Equal(t, "u-asker", l.CallerUserID, "the session owner is who asked")
	assert.Equal(t, "agent-1", l.AgentID)
	assert.Equal(t, "sess-1", l.SessionID)
	_, ownLent := lends[ownID]
	assert.False(t, ownLent, "a run that was not lent records no lend")

	got, err := svc.GetRun(bg, lentID)
	require.NoError(t, err)
	assert.Equal(t, "u-lender", got.UserID, "the run row keeps the lender as before")
}

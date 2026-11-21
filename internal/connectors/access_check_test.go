package connectors

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

// readOnlyScope has the connector on the agent's list but refuses every op.
type readOnlyScope struct{ conn string }

func (s readOnlyScope) AllowConnector(id string) bool     { return id == s.conn }
func (s readOnlyScope) AllowAccount(id, _ string) bool    { return id == s.conn }
func (s readOnlyScope) AllowOp(string, string, bool) bool { return false }
func (readOnlyScope) AgentID() string                     { return "agent-1" }

// An op the agent's own access refuses runs only when the access check
// names someone who reaches the connector, and then as that person.
func TestAccessCheckOverridesRefusal(t *testing.T) {
	bg := context.Background()
	svc, db := newSvcAccountVisDB(t)
	row, err := svc.Create(bg, "acct-vis", "Gated Row", nil, "u-approver")
	require.NoError(t, err)
	tagRow(t, db, row.ID, "custom:gated_row")
	svc.SetIdentityLookup(func(_ context.Context, id string) (*entity.User, []string, error) {
		switch id {
		case "u-approver", "u-stranger":
			return &entity.User{ID: id}, nil, nil
		}
		return nil, nil, errors.New("unknown user")
	})
	ctx := WithAgentScope(bg, readOnlyScope{conn: row.ID})
	exec := func() (*ExecuteResult, error) {
		return svc.Execute(ctx, ExecuteParams{
			ConnectorID: row.ID, OperationKey: "whoami", Input: map[string]string{"k": "v"},
			Source: entity.ConnectorRunSourceMCP, UserID: "u-agent-user", SessionID: "sess-1",
		})
	}

	t.Run("project has access requests off: refused as before, nothing asked", func(t *testing.T) {
		asked := false
		svc.SetAccessCheck(func(context.Context, AccessQuery) (string, string, error) { asked = true; return "u-approver", "", nil })
		_, err := exec()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "wick_request_access")
		assert.False(t, asked, "the access check ran for a project with access requests off")
		svc.SetAccessCheck(nil)
	})
	svc.SetAccessGate(func(string) bool { return true })

	t.Run("no check: refused with the way forward", func(t *testing.T) {
		_, err := exec()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed for this agent")
		assert.Contains(t, err.Error(), "wick_request_access")
	})

	var seen AccessQuery
	t.Run("a grant runs it as the approver", func(t *testing.T) {
		svc.SetAccessCheck(func(_ context.Context, q AccessQuery) (string, string, error) {
			seen = q
			return "u-approver", "grant g1", nil
		})
		res, err := exec()
		require.NoError(t, err)
		assert.Equal(t, "sess-1", seen.SessionID)
		assert.Equal(t, "agent-1", seen.AgentID)
		assert.Equal(t, "whoami", seen.OpKey)
		assert.Equal(t, "v", seen.Input["k"])
		lends, err := svc.RunLends(bg, []string{res.RunID})
		require.NoError(t, err)
		assert.Equal(t, "u-approver", lends[res.RunID].OnBehalfOf, "the run is filed under the approver")
	})

	t.Run("an approver who cannot reach the row lends nothing", func(t *testing.T) {
		svc.SetAccessCheck(func(context.Context, AccessQuery) (string, string, error) { return "u-stranger", "", nil })
		_, err := exec()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed for this agent")
	})

	t.Run("a deny rule refuses outright", func(t *testing.T) {
		svc.SetAccessCheck(func(context.Context, AccessQuery) (string, string, error) {
			return "", "", errors.New(`denied by access rule "no-writes"`)
		})
		_, err := exec()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no-writes")
	})

	t.Run("CanRunOwn and DescribeOp", func(t *testing.T) {
		ok, _ := svc.CanRunOwn(bg, "u-approver", row.ID, "whoami")
		assert.True(t, ok)
		ok, why := svc.CanRunOwn(bg, "u-stranger", row.ID, "whoami")
		assert.False(t, ok)
		assert.NotEmpty(t, why)
		info, err := svc.DescribeOp(bg, row.ID, "whoami")
		require.NoError(t, err)
		assert.Equal(t, "acct-vis", info.ConnectorKey)
		_, err = svc.DescribeOp(bg, row.ID, "nope")
		assert.Error(t, err)
	})
}

package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

// stubScope is a minimal AgentScope: one allowed connector, read-only
// unless allDestructive, and an optional account allow-list.
type stubScope struct {
	conn           string
	allDestructive bool
	accounts       map[string]bool // nil = every account
}

func (s stubScope) AllowConnector(id string) bool { return id == s.conn }
func (s stubScope) AllowAccount(id, acc string) bool {
	return id == s.conn && (s.accounts == nil || s.accounts[acc])
}
func (s stubScope) AllowOp(id, _ string, destructive bool) bool {
	return id == s.conn && (s.allDestructive || !destructive)
}

func TestAgentScopeOnContext(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, AgentScopeFrom(ctx))
	assert.Equal(t, ctx, WithAgentScope(ctx, nil), "nil scope must leave ctx untouched")
	assert.NotNil(t, AgentScopeFrom(WithAgentScope(ctx, stubScope{conn: "c"})))
}

func TestFilterHelpersByScope(t *testing.T) {
	rows := []entity.Connector{{ID: "a"}, {ID: "b"}}
	accs := []entity.ConnectorAccount{{ID: "x"}, {ID: "y"}}

	assert.Len(t, filterRowsByScope(context.Background(), rows), 2, "no scope = unchanged")
	ctx := WithAgentScope(context.Background(), stubScope{conn: "a", accounts: map[string]bool{"y": true}})
	got := filterRowsByScope(ctx, rows)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].ID)
	assert.Len(t, rows, 2, "input slice must not be mutated")

	gotAccs := filterAccountsByScope(ctx, "a", accs)
	require.Len(t, gotAccs, 1)
	assert.Equal(t, "y", gotAccs[0].ID)
	assert.Empty(t, filterAccountsByScope(ctx, "b", accs))
}

func TestServiceHonoursAgentScope(t *testing.T) {
	svc, id := newSvcACL(t)
	bg := context.Background()
	readOnly := WithAgentScope(bg, stubScope{conn: id})
	elsewhere := WithAgentScope(bg, stubScope{conn: "some-other-connector"})

	t.Run("ListVisibleTo drops rows off the checklist", func(t *testing.T) {
		rows, err := svc.ListVisibleTo(readOnly, "user-1", nil, true)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		rows, err = svc.ListVisibleTo(elsewhere, "user-1", nil, true)
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("IsVisibleTo agrees", func(t *testing.T) {
		ok, err := svc.IsVisibleTo(readOnly, id, "user-1", nil, true)
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = svc.IsVisibleTo(elsewhere, id, "user-1", nil, true)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("OperationStates hides destructive ops under read-only", func(t *testing.T) {
		st, err := svc.OperationStates(bg, id, "acl-stub")
		require.NoError(t, err)
		assert.True(t, st["write"], "unscoped baseline has write on")
		st, err = svc.OperationStates(readOnly, id, "acl-stub")
		require.NoError(t, err)
		assert.True(t, st["read"])
		assert.False(t, st["write"])
	})

	t.Run("Execute enforces connector and op", func(t *testing.T) {
		_, err := svc.Execute(readOnly, params(id, "read", true))
		require.NoError(t, err)
		_, err = svc.Execute(readOnly, params(id, "write", true))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed for this agent")
		_, err = svc.Execute(elsewhere, params(id, "read", true))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access list")
		full := WithAgentScope(bg, stubScope{conn: id, allDestructive: true})
		_, err = svc.Execute(full, params(id, "write", true))
		require.NoError(t, err)
	})

	t.Run("Execute enforces the instance identity as an account", func(t *testing.T) {
		noBot := WithAgentScope(bg, stubScope{conn: id, accounts: map[string]bool{"acc-1": true}})
		_, err := svc.Execute(noBot, params(id, "read", true))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "as that account")
	})

	t.Run("Session instance follows the base connector's grant", func(t *testing.T) {
		virt := func(op string) ExecuteParams {
			return ExecuteParams{
				ConnectorID:     "sw_agent-instance",
				OperationKey:    op,
				Input:           map[string]string{"v": "x"},
				Source:          entity.ConnectorRunSourceTest,
				UserID:          "user-1",
				IsAdmin:         true,
				SessionInstance: &SessionInstanceTarget{BaseKey: "acl-stub", Label: "Stub (session)"},
			}
		}
		_, err := svc.Execute(readOnly, virt("read"))
		require.NoError(t, err)
		_, err = svc.Execute(readOnly, virt("write"))
		require.Error(t, err, "read-only grant on the base must cover its session instances")
		assert.Contains(t, err.Error(), "session instance")
		_, err = svc.Execute(elsewhere, virt("read"))
		require.Error(t, err, "a base off the checklist grants its instances nothing")
		full := WithAgentScope(bg, stubScope{conn: id, allDestructive: true})
		_, err = svc.Execute(full, virt("write"))
		require.NoError(t, err)
		_, err = svc.Execute(bg, virt("write"))
		require.NoError(t, err, "outside an agent session instances are unscoped")
	})
}

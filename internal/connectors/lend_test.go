package connectors

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

// lendScope allows one connector at every op and lends it from lender.
type lendScope struct {
	conn, lender string
}

func (s lendScope) AllowConnector(id string) bool          { return id == s.conn }
func (s lendScope) AllowAccount(id, _ string) bool         { return id == s.conn }
func (s lendScope) AllowOp(id, _ string, _ bool) bool      { return id == s.conn }
func (s lendScope) LentBy(id string) (string, bool)        { return s.lender, id == s.conn && s.lender != "" }
func (s lendScope) LentIDs() []string                      { return []string{s.conn} }
func (s lendScope) withLender(l string) lendScope          { s.lender = l; return s }
func (s lendScope) ctx(bg context.Context) context.Context { return WithAgentScope(bg, s) }

// A tag-gated row lent by the person who created it: a stranger reaches
// it through the lend and runs as the lender, the lender keeps running as
// themselves, and nothing is lent without the lookup or the scope.
func TestLentConnectorRunsAsLender(t *testing.T) {
	bg := context.Background()
	svc, db := newSvcAccountVisDB(t)
	row, err := svc.Create(bg, "acct-vis", "Lent Row", nil, "u-lender")
	require.NoError(t, err)
	tagRow(t, db, row.ID, "custom:lent_row")

	scope := lendScope{conn: row.ID}.withLender("u-lender")
	ctx := scope.ctx(bg)
	has := func(rows []entity.Connector) bool {
		for _, r := range rows {
			if r.ID == row.ID {
				return true
			}
		}
		return false
	}

	t.Run("no identity lookup lends nothing", func(t *testing.T) {
		ok, err := svc.IsVisibleTo(ctx, row.ID, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	svc.SetIdentityLookup(func(_ context.Context, id string) (*entity.User, []string, error) {
		if id != "u-lender" {
			return nil, nil, errors.New("unknown user")
		}
		return &entity.User{ID: id}, nil, nil
	})

	t.Run("a stranger sees and lists the lent row", func(t *testing.T) {
		ok, err := svc.IsVisibleTo(ctx, row.ID, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.True(t, ok)
		rows, err := svc.ListVisibleTo(ctx, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.True(t, has(rows), "a lent row must list where it dispatches")
	})

	t.Run("the stranger runs as the lender", func(t *testing.T) {
		lender, _, ok := svc.lendIdentity(ctx, row.ID, "u-stranger", nil, false)
		require.True(t, ok)
		assert.Equal(t, "u-lender", lender.ID)
	})

	t.Run("the lender keeps their own identity", func(t *testing.T) {
		_, _, ok := svc.lendIdentity(ctx, row.ID, "u-lender", nil, false)
		assert.False(t, ok)
	})

	t.Run("without the scope a stranger sees nothing", func(t *testing.T) {
		ok, err := svc.IsVisibleTo(bg, row.ID, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
		ok, err = svc.IsVisibleTo(lendScope{conn: row.ID}.ctx(bg), row.ID, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.False(t, ok, "a scope that lends nothing lends nothing")
	})

	t.Run("a lender who lost the row lends nothing", func(t *testing.T) {
		gone := lendScope{conn: row.ID}.withLender("u-other").ctx(bg)
		ok, err := svc.IsVisibleTo(gone, row.ID, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

// The caller an op sees: the session's owner normally, the lender when the
// connector is lent (the owner must not win back after the swap).
func TestEffectiveCaller(t *testing.T) {
	svc := &Service{}
	svc.SetSessionOwnerUserResolver(func(sid string) (string, bool) {
		if sid == "sess-1" {
			return "u-owner", true
		}
		return "", false
	})

	assert.Equal(t, "u-owner", svc.effectiveCaller("u-principal", "sess-1", false), "session owner is the caller")
	assert.Equal(t, "u-lender", svc.effectiveCaller("u-lender", "sess-1", true), "a lent call keeps the lender")
	assert.Equal(t, "u-principal", svc.effectiveCaller("u-principal", "", false), "no session keeps the principal")
	assert.Equal(t, "u-principal", svc.effectiveCaller("u-principal", "sess-x", false), "unknown session keeps the principal")
}

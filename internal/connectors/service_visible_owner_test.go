package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// ListVisibleTo/IsVisibleTo are the MCP-side twins of
// ListForManager/IsManageableBy, and they used to ask only two questions:
// does a tag match, or does the admin knob let this caller past. Neither
// answers yes for the person who CREATED a tag-gated row, so a connector
// could be administered from /admin/connectors by the very person whose
// agent session could not see it — present in the dashboard, absent from
// wick_list.
//
// The row here is tagged the way a real custom connector is: creating one
// auto-attaches custom:<key> with IsFilter=true, held by nobody. That is
// what makes the negative cases meaningful — an untagged row is visible to
// everyone by design and cannot tell ownership apart from that rule.
func TestListVisibleToIncludesOwnedTaggedRow(t *testing.T) {
	ctx := context.Background()
	svc, db := newSvcAccountVisDB(t)

	row, err := svc.Create(ctx, "acct-vis", "Qiscus Coolify (Yoga)", nil, "u-creator")
	require.NoError(t, err)
	tagRow(t, db, row.ID, "custom:qiscus_coolify")

	has := func(rows []entity.Connector, id string) bool {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}

	t.Run("the creator sees it with no tag at all", func(t *testing.T) {
		rows, err := svc.ListVisibleTo(ctx, "u-creator", nil, false)
		require.NoError(t, err)
		assert.True(t, has(rows, row.ID), "the person who created a row must see it in their own tool list")
	})

	t.Run("and can dispatch against it", func(t *testing.T) {
		// A row that lists but refuses to run is a listing that lies, so
		// IsVisibleTo has to agree with ListVisibleTo — including here.
		ok, err := svc.IsVisibleTo(ctx, row.ID, "u-creator", nil, false)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("a stranger with no matching tag still does not", func(t *testing.T) {
		rows, err := svc.ListVisibleTo(ctx, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.False(t, has(rows, row.ID), "the fix must not degrade into everyone-sees-everything")

		ok, err := svc.IsVisibleTo(ctx, row.ID, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("an unidentified caller gains nothing", func(t *testing.T) {
		// CreatedBy "" must never match userID "" — the one direction where
		// a bug hands every ownerless row to an anonymous request.
		rows, err := svc.ListVisibleTo(ctx, "", nil, false)
		require.NoError(t, err)
		assert.False(t, has(rows, row.ID))

		ok, err := svc.IsVisibleTo(ctx, row.ID, "", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("a scoped sub-agent inherits nothing from its parent", func(t *testing.T) {
		// A sub-agent token carries its triggering user's id but a tag slice
		// already intersected down to the profile's allow list. If ownership
		// applied here, the child would reach every row its parent ever
		// created and the narrowing would be decorative.
		scoped := login.WithScopedUser(ctx,
			&entity.User{ID: "u-creator", Role: entity.RoleUser, Approved: true}, nil)

		rows, err := svc.ListVisibleTo(scoped, "u-creator", nil, false)
		require.NoError(t, err)
		assert.False(t, has(rows, row.ID), "ownership must not widen an already-narrowed principal")

		ok, err := svc.IsVisibleTo(scoped, row.ID, "u-creator", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("the same session unscoped still sees it", func(t *testing.T) {
		// Positive control for the subtest above: the marker is what makes
		// the difference, not the plain context or the nil tag slice.
		unscoped := login.WithUser(ctx,
			&entity.User{ID: "u-creator", Role: entity.RoleUser, Approved: true}, nil)
		rows, err := svc.ListVisibleTo(unscoped, "u-creator", nil, false)
		require.NoError(t, err)
		assert.True(t, has(rows, row.ID))
	})

	t.Run("a non-admin with the row's tag still gets in", func(t *testing.T) {
		tagID := tagRow(t, db, row.ID, "second-team")
		rows, err := svc.ListVisibleTo(ctx, "u-tagged", []string{tagID}, false)
		require.NoError(t, err)
		assert.True(t, has(rows, row.ID), "the tag path must keep working")
	})

	t.Run("a row that is both owned and tagged is listed once", func(t *testing.T) {
		tagID := tagRow(t, db, row.ID, "creator-also-tagged")
		rows, err := svc.ListVisibleTo(ctx, "u-creator", []string{tagID}, false)
		require.NoError(t, err)
		var n int
		for _, r := range rows {
			if r.ID == row.ID {
				n++
			}
		}
		assert.Equal(t, 1, n, "the owned-rows union must not double-list")
	})
}

// Disabled is an off-switch, not a visibility rule: the tag query already
// excludes disabled rows from every MCP/test surface, and an owner is no
// exception — otherwise turning a row off would still leave its creator
// able to call it.
func TestOwnedRowStaysHiddenWhileDisabled(t *testing.T) {
	ctx := context.Background()
	svc, db := newSvcAccountVisDB(t)

	row, err := svc.Create(ctx, "acct-vis", "Mine", nil, "u-creator")
	require.NoError(t, err)
	tagRow(t, db, row.ID, "custom:mine")
	require.NoError(t, db.Model(&entity.Connector{}).Where("id = ?", row.ID).
		Update("disabled", true).Error)

	rows, err := svc.ListVisibleTo(ctx, "u-creator", nil, false)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, row.ID, r.ID, "a disabled row must stay out even for its owner")
	}

	ok, err := svc.IsVisibleTo(ctx, row.ID, "u-creator", nil, false)
	require.NoError(t, err)
	assert.False(t, ok)
}

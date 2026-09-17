package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// tagRow attaches a filter tag to a connector ROW, the way the admin
// connectors page does (tag row + tool_tags link on the row's path). This is
// the state a freshly created instance lands in: the connector type's
// default tags are filter tags, and the person who created it does not
// automatically carry them.
func tagRow(t *testing.T, db *gorm.DB, rowID, tagName string) string {
	t.Helper()
	tag := &entity.Tag{Name: tagName, IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.ToolTag{ToolPath: "/connectors/" + rowID, TagID: tag.ID}).Error)
	return tag.ID
}

// The bug: create an instance, open it, get "connector not found" — on a row
// you own, as the person who made it. Manager visibility asked only two
// questions (admin while admin_see_all_connectors is on, or does a tag
// match) and a new row carries filter tags its creator does not have, so the
// creator fell through both.
//
// The row here is tagged exactly as a real one is, which is what makes the
// negative cases meaningful: an untagged row is visible to everybody by
// design, so it cannot tell ownership apart from that rule.
func TestOwnerSeesOwnTaggedInstance(t *testing.T) {
	ctx := context.Background()
	svc, db := newSvcAccountVisDB(t)

	row, err := svc.Create(ctx, "acct-vis", "Mine", nil, "u-creator")
	require.NoError(t, err)
	tagRow(t, db, row.ID, "restricted-team")

	t.Run("the creator gets in with no tag at all", func(t *testing.T) {
		ok, err := svc.IsManageableBy(ctx, row.ID, "u-creator", nil, false)
		require.NoError(t, err)
		assert.True(t, ok, "the person who created a row must be able to open it")
	})

	t.Run("a stranger with no matching tag still does not", func(t *testing.T) {
		// The fix must not degrade into "everyone sees everything".
		ok, err := svc.IsManageableBy(ctx, row.ID, "u-stranger", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("an unidentified caller gains nothing from ownership", func(t *testing.T) {
		// CreatedBy "" must never match userID "" — that is the one
		// direction where a bug hands every row to an anonymous caller.
		ok, err := svc.IsManageableBy(ctx, row.ID, "", nil, false)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.False(t, OwnsConnector(*row, ""))
	})

	t.Run("a non-admin with the row's tag gets in as before", func(t *testing.T) {
		tagID := tagRow(t, db, row.ID, "second-team")
		ok, err := svc.IsManageableBy(ctx, row.ID, "u-tagged", []string{tagID}, false)
		require.NoError(t, err)
		assert.True(t, ok, "the tag path must keep working")
	})

	t.Run("the listing includes a row you own but have no tag for", func(t *testing.T) {
		rows, err := svc.ListForManager(ctx, "u-creator", nil, false)
		require.NoError(t, err)
		var found bool
		for _, r := range rows {
			if r.ID == row.ID {
				found = true
			}
		}
		assert.True(t, found, "reachable by URL but missing from the list is still lost")
	})

	t.Run("the listing does not leak it to a stranger", func(t *testing.T) {
		rows, err := svc.ListForManager(ctx, "u-stranger", nil, false)
		require.NoError(t, err)
		for _, r := range rows {
			assert.NotEqual(t, row.ID, r.ID, "a tagged row must stay out of a stranger's list")
		}
	})

	t.Run("the listing does not duplicate a row that is both owned and tagged", func(t *testing.T) {
		tagID := tagRow(t, db, row.ID, "creator-also-tagged")
		rows, err := svc.ListForManager(ctx, "u-creator", []string{tagID}, false)
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

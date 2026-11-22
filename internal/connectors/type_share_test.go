package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

// Type-level sharing sits above instance tags: a restricted connector type
// disappears for everyone it is not shared with, while its owner, the
// people it is shared with, holders of a linked tag and admins keep it.
func TestTypeSharing(t *testing.T) {
	ctx := context.Background()
	svc, db := newSvcAccountVisDB(t)
	svc.SetTypeOwnerResolver(func(_ context.Context, key string) string {
		if key == "acct-vis" {
			return "u-owner"
		}
		return ""
	})

	// An untagged instance created by someone else: visible to everybody
	// at the instance level, so only the type gate can hide it.
	row, err := svc.Create(ctx, "acct-vis", "Shared row", nil, "u-other")
	require.NoError(t, err)

	has := func(rows []entity.Connector, id string) bool {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	sees := func(userID string, isAdmin bool) bool {
		rows, err := svc.ListVisibleTo(ctx, userID, nil, isAdmin)
		require.NoError(t, err)
		listed := has(rows, row.ID)
		ok, err := svc.IsVisibleTo(ctx, row.ID, userID, nil, isAdmin)
		require.NoError(t, err)
		require.Equal(t, listed, ok, "listing and dispatch must agree")
		return listed && svc.TypeVisibleTo(ctx, "acct-vis", userID, isAdmin)
	}

	t.Run("open by default", func(t *testing.T) {
		assert.True(t, sees("u-stranger", false))
		sh, err := svc.GetTypeSharing(ctx, "acct-vis")
		require.NoError(t, err)
		assert.False(t, sh.Restricted)
	})

	t.Run("sharing with a person needs a restricted type", func(t *testing.T) {
		assert.ErrorIs(t, svc.ShareTypeWithUser(ctx, "acct-vis", "u-friend"), ErrTypeOpen)
	})

	require.NoError(t, svc.RestrictType(ctx, "acct-vis", "u-owner"))

	t.Run("restricted hides it from strangers", func(t *testing.T) {
		assert.False(t, sees("u-stranger", false))
		assert.False(t, sees("", false), "an anonymous caller gains nothing")
	})

	t.Run("owner and admin still see it", func(t *testing.T) {
		assert.True(t, svc.TypeVisibleTo(ctx, "acct-vis", "u-owner", false))
		assert.True(t, sees("u-admin", true))
	})

	t.Run("the instance's own creator keeps their row", func(t *testing.T) {
		rows, err := svc.ListVisibleTo(ctx, "u-other", nil, false)
		require.NoError(t, err)
		assert.True(t, has(rows, row.ID))
		assert.False(t, svc.TypeVisibleTo(ctx, "acct-vis", "u-other", false), "but not the type itself")
	})

	t.Run("share by person, then remove", func(t *testing.T) {
		require.NoError(t, svc.ShareTypeWithUser(ctx, "acct-vis", "u-friend"))
		assert.True(t, sees("u-friend", false))
		sh, err := svc.GetTypeSharing(ctx, "acct-vis")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"u-owner", "u-friend"}, sh.UserIDs)

		require.NoError(t, svc.UnshareTypeUser(ctx, "acct-vis", "u-friend"))
		assert.False(t, sees("u-friend", false))
	})

	t.Run("share with a team tag", func(t *testing.T) {
		team := &entity.Tag{Name: "team-a", IsFilter: true}
		require.NoError(t, db.Create(team).Error)
		require.NoError(t, db.Create(&entity.UserTag{UserID: "u-member", TagID: team.ID}).Error)
		assert.False(t, sees("u-member", false))

		require.NoError(t, svc.LinkTypeTag(ctx, "acct-vis", team.ID))
		assert.True(t, sees("u-member", false))
		sh, err := svc.GetTypeSharing(ctx, "acct-vis")
		require.NoError(t, err)
		require.Len(t, sh.Tags, 1)
		assert.Equal(t, "team-a", sh.Tags[0].Name)

		require.NoError(t, svc.UnlinkTypeTag(ctx, "acct-vis", team.ID))
		assert.False(t, sees("u-member", false))
	})

	t.Run("a non-filter tag cannot be linked", func(t *testing.T) {
		cat := &entity.Tag{Name: "category-x", IsGroup: true}
		require.NoError(t, db.Create(cat).Error)
		assert.Error(t, svc.LinkTypeTag(ctx, "acct-vis", cat.ID))
	})

	t.Run("open to everyone keeps the people list for later", func(t *testing.T) {
		require.NoError(t, svc.RestrictType(ctx, "acct-vis", ""))
		require.NoError(t, svc.ShareTypeWithUser(ctx, "acct-vis", "u-friend"))
		require.NoError(t, svc.OpenType(ctx, "acct-vis"))
		assert.True(t, sees("u-stranger", false))

		require.NoError(t, svc.RestrictType(ctx, "acct-vis", ""))
		assert.True(t, sees("u-friend", false))
		assert.False(t, sees("u-stranger", false))
	})

	t.Run("shareable tags skip per-resource markers", func(t *testing.T) {
		tagsOut, err := svc.ShareableTags(ctx)
		require.NoError(t, err)
		var names []string
		for _, tg := range tagsOut {
			names = append(names, tg.Name)
		}
		assert.Contains(t, names, "team-a")
		assert.NotContains(t, names, ShareTagName("acct-vis"))
		assert.NotContains(t, names, "category-x")
	})
}

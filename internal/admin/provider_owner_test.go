package admin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The provider Owner column reads back what the picker saved: one holder,
// the latest pick.
func TestProviderSoleOwnerRoundTrip(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	approvedUser(t, db, "11111111-1111-1111-1111-111111111111", "One")
	approvedUser(t, db, "22222222-2222-2222-2222-222222222222", "Two")
	id := providerOwnerResourceID("claude", "enginer")
	name := "owner:" + id

	require.NoError(t, h.repo.SetSoleOwnerTag(ctx, id, "11111111-1111-1111-1111-111111111111"))
	owners, err := h.repo.OwnerTagHolders(ctx, []string{name})
	require.NoError(t, err)
	require.Len(t, owners[name], 1)
	require.Equal(t, "11111111-1111-1111-1111-111111111111", owners[name][0].ID)

	require.NoError(t, h.repo.SetSoleOwnerTag(ctx, id, "22222222-2222-2222-2222-222222222222"))
	owners, err = h.repo.OwnerTagHolders(ctx, []string{name})
	require.NoError(t, err)
	require.Len(t, owners[name], 1)
	require.Equal(t, "22222222-2222-2222-2222-222222222222", owners[name][0].ID)
}

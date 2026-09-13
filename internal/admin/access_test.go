package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// seedUserWithTag creates an approved user carrying one filter tag, the shape
// every access question is asked about.
func seedUserWithTag(t *testing.T, db *gorm.DB, userID, email, tagID string) {
	t.Helper()
	require.NoError(t, db.Create(&entity.User{ID: userID, Name: userID, Email: email, Approved: true, Role: entity.RoleUser}).Error)
	if tagID != "" {
		require.NoError(t, db.Create(&entity.UserTag{UserID: userID, TagID: tagID}).Error)
	}
}

func seedFilterTag(t *testing.T, db *gorm.DB, name string) string {
	t.Helper()
	tag := &entity.Tag{Name: name, IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	return tag.ID
}

func tagPath(t *testing.T, db *gorm.DB, path, tagID string) {
	t.Helper()
	require.NoError(t, db.Create(&entity.ToolTag{ToolPath: path, TagID: tagID}).Error)
}

// An untagged item is PUBLIC — wick's rule — and its reach is every approved
// user, not zero. Getting this backwards would tell an admin a wide-open row
// was locked down.
func TestAccessUserCountsUntaggedPathIsPublic(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	seedUserWithTag(t, db, "u-1", "one@x.test", "")
	seedUserWithTag(t, db, "u-2", "two@x.test", "")

	sums := h.accessSummaries(ctx, []string{"/connectors/never-tagged"})
	got := sums["/connectors/never-tagged"]
	require.True(t, got.Public, "untagged path reported as restricted")
	require.Equal(t, 2, got.Reach(), "public reach must be every approved user")
}

// A tagged item counts only the people carrying one of its tags — and a tag
// nobody carries must read as 0, not as public.
func TestAccessUserCountsTaggedPath(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	orphan := seedFilterTag(t, db, "nobody-has-this")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	seedUserWithTag(t, db, "u-2", "two@x.test", support)
	seedUserWithTag(t, db, "u-3", "three@x.test", "")
	tagPath(t, db, "/tools/shared", support)
	tagPath(t, db, "/tools/orphaned", orphan)

	sums := h.accessSummaries(ctx, []string{"/tools/shared", "/tools/orphaned"})

	shared := sums["/tools/shared"]
	require.False(t, shared.Public)
	require.Equal(t, 2, shared.Reach())

	orphaned := sums["/tools/orphaned"]
	require.False(t, orphaned.Public, "a tagged row with no tag-holders must not read as public")
	require.Equal(t, 0, orphaned.Reach())
}

// Unapproved users cannot log in, so they are not part of anybody's reach.
func TestAccessCountsSkipUnapprovedUsers(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	require.NoError(t, db.Create(&entity.User{ID: "u-pending", Name: "Pending", Email: "p@x.test", Approved: false}).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-pending", TagID: support}).Error)
	tagPath(t, db, "/jobs/nightly", support)

	sums := h.accessSummaries(ctx, []string{"/jobs/nightly"})
	require.Equal(t, 1, sums["/jobs/nightly"].Reach(), "a pending account must not count as access")
}

// The modal says WHY each person gets in — the tag that matched.
func TestAccessDetailNamesTheMatchingTag(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	tagPath(t, db, "/projects/p1", support)

	detail, err := h.repo.AccessDetail(ctx, "/projects/p1")
	require.NoError(t, err)
	require.False(t, detail.Public)
	require.Len(t, detail.Users, 1)
	require.Equal(t, []string{"support"}, detail.Users[0].ViaTags)
	require.Equal(t, []string{"support"}, detail.Tags)
}

// A connected account's reach is NOT just its tags: the person who connected
// it and the row's creator are always in, which is the number that matters
// when the question is "who can post as this Slack identity".
func TestAccountAccessCountsImplicitGrants(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, "sso-admin", "Row", nil, "u-creator")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}))
	seedUserWithTag(t, db, "u-creator", "creator@x.test", "")
	seedUserWithTag(t, db, "u-alice", "alice@x.test", "")
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-a"))

	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	require.Len(t, accs, 1)

	users, err := h.accountAccessUsers(ctx, *mustGet(t, svc, row.ID), accs[0])
	require.NoError(t, err)
	require.Len(t, users, 2, "the connector and the row creator both reach the account")
	byID := map[string][]string{}
	for _, u := range users {
		byID[u.ID] = u.ViaTags
	}
	require.Equal(t, []string{reasonConnected}, byID["u-alice"])
	require.Equal(t, []string{reasonOwner}, byID["u-creator"])
}

// With AllowOthersSeeAccounts on, the whole pool is shared with everyone who
// can see the row — the one case where a plain user legitimately sees somebody
// else's account, so the count has to grow.
func TestAccountAccessGrowsWithSharedPool(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	row, err := svc.Create(ctx, "sso-admin", "Row", nil, "u-creator")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{
		EnableSSO: true, MultiAccount: true, AllowOthersSeeAccounts: true,
	}))
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-creator", "creator@x.test", "")
	seedUserWithTag(t, db, "u-alice", "alice@x.test", "")
	seedUserWithTag(t, db, "u-bob", "bob@x.test", support)
	tagPath(t, db, "/connectors/"+row.ID, support)
	require.NoError(t, svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-a"))

	accs, err := svc.ListAccounts(ctx, row.ID)
	require.NoError(t, err)
	users, err := h.accountAccessUsers(ctx, *mustGet(t, svc, row.ID), accs[0])
	require.NoError(t, err)

	ids := map[string]bool{}
	for _, u := range users {
		ids[u.ID] = true
	}
	require.True(t, ids["u-bob"], "pool sharing must put the row's tag holders in reach of the account")
	require.True(t, ids["u-alice"])
	require.True(t, ids["u-creator"])
}

// The Tags page counters: people carrying the tag, things it opens.
func TestTagUsageCounts(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	seedUserWithTag(t, db, "u-2", "two@x.test", support)
	tagPath(t, db, "/tools/a", support)
	tagPath(t, db, "/jobs/b", support)
	tagPath(t, db, "/projects/c", support)

	counts, err := h.repo.TagUsageCounts(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, counts[support].UserCount)
	require.Equal(t, 3, counts[support].ItemCount)

	detail, err := h.repo.TagUsageDetail(ctx, support)
	require.NoError(t, err)
	require.Equal(t, "support", detail.TagName)
	require.Len(t, detail.Items, 3)
}

// The JSON endpoint behind the badge answers with the same numbers the page
// rendered, so clicking never contradicts the badge.
func TestAccessUsersEndpoint(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	tagPath(t, db, "/tools/a", support)

	req := httptest.NewRequest(http.MethodGet, "/admin/access/users?path=/tools/a", nil)
	rec := httptest.NewRecorder()
	h.accessUsersPage(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got AccessDetail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.False(t, got.Public)
	require.Equal(t, "Tools", got.Kind)
	require.Len(t, got.Users, 1)
	require.Equal(t, "one@x.test", got.Users[0].Email)
}

func TestAccessUsersEndpointRejectsEmptyPath(t *testing.T) {
	h, _, _ := newAdminConnectorsHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/access/users", nil)
	rec := httptest.NewRecorder()
	h.accessUsersPage(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func mustGet(t *testing.T, svc *connectors.Service, id string) *entity.Connector {
	t.Helper()
	row, err := svc.Get(context.Background(), id)
	require.NoError(t, err)
	return row
}

// ── Admin bypass ──────────────────────────────────────────────────────────
//
// The tag holders are never the whole answer: on most surfaces the admin role
// walks past the tags, so a reach that counted only tags would understate who
// really sees the row.

// Tools and jobs let ANY admin through unconditionally (login.CanAccessTool
// returns true for an admin before it ever looks at the tags).
func TestReachIncludesAdminsOnToolPaths(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	require.NoError(t, db.Create(&entity.User{
		ID: "u-admin", Name: "Root", Email: "root@x.test", Approved: true, Role: entity.RoleAdmin,
	}).Error)
	tagPath(t, db, "/tools/secret", support)

	sums := h.accessSummaries(ctx, []string{"/tools/secret"})
	require.Equal(t, 2, sums["/tools/secret"].Reach(), "the admin sees it too and must be counted")

	req := httptest.NewRequest(http.MethodGet, "/admin/access/users?path=/tools/secret", nil)
	rec := httptest.NewRecorder()
	h.accessUsersPage(rec, req)
	var got AccessDetail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Users, 2)
	byID := map[string][]string{}
	for _, u := range got.Users {
		byID[u.ID] = u.ViaTags
	}
	require.Equal(t, []string{"support"}, byID["u-1"])
	require.Equal(t, []string{"admin role"}, byID["u-admin"], "the modal must say WHY the admin is in the list")
}

// Connectors are behind a knob, so the same admin counts or does not count
// depending on admin_see_all_connectors. Getting this wrong is what makes the
// number a lie.
func TestReachFollowsTheConnectorAdminKnob(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	require.NoError(t, db.Create(&entity.User{
		ID: "u-admin", Name: "Root", Email: "root@x.test", Approved: true, Role: entity.RoleAdmin,
	}).Error)
	tagPath(t, db, "/connectors/row-1", support)

	// Default is ON — the admin is in reach.
	sums := h.accessSummaries(ctx, []string{"/connectors/row-1"})
	require.Equal(t, 2, sums["/connectors/row-1"].Reach())

	// Turned off, the admin is scoped like anybody else.
	setAgentsKnob(t, h, "admin_see_all_connectors", "false")
	sums = h.accessSummaries(ctx, []string{"/connectors/row-1"})
	require.Equal(t, 1, sums["/connectors/row-1"].Reach(), "knob off must drop the admin from the count")
}

// Projects follow the OTHER knob, which is off by default — so an admin is
// NOT in reach of a tagged project until admin_see_all_sessions is turned on.
func TestReachFollowsTheSessionsAdminKnob(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	require.NoError(t, db.Create(&entity.User{
		ID: "u-admin", Name: "Root", Email: "root@x.test", Approved: true, Role: entity.RoleAdmin,
	}).Error)
	tagPath(t, db, "/projects/p1", support)

	sums := h.accessSummaries(ctx, []string{"/projects/p1"})
	require.Equal(t, 1, sums["/projects/p1"].Reach(), "admin_see_all_sessions is off by default")

	setAgentsKnob(t, h, "admin_see_all_sessions", "true")
	sums = h.accessSummaries(ctx, []string{"/projects/p1"})
	require.Equal(t, 2, sums["/projects/p1"].Reach())
}

// An admin who ALSO carries the tag is one person. Adding two numbers instead
// of unioning two sets would report three people where there are two.
func TestReachCountsATaggedAdminOnce(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	support := seedFilterTag(t, db, "support")
	seedUserWithTag(t, db, "u-1", "one@x.test", support)
	require.NoError(t, db.Create(&entity.User{
		ID: "u-admin", Name: "Root", Email: "root@x.test", Approved: true, Role: entity.RoleAdmin,
	}).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-admin", TagID: support}).Error)
	tagPath(t, db, "/tools/secret", support)

	sums := h.accessSummaries(ctx, []string{"/tools/secret"})
	require.Equal(t, 2, sums["/tools/secret"].Reach())

	req := httptest.NewRequest(http.MethodGet, "/admin/access/users?path=/tools/secret", nil)
	rec := httptest.NewRecorder()
	h.accessUsersPage(rec, req)
	var got AccessDetail
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Users, 2, "the tagged admin must appear once, not twice")
	for _, u := range got.Users {
		if u.ID == "u-admin" {
			require.Equal(t, []string{"support", "admin role"}, u.ViaTags, "both reasons on one row")
		}
	}
}

// setAgentsKnob writes one of the adminscope knobs the way the Agents tool
// declares it. EnsureOwned registers the key first: SetOwned refuses a config
// no module has declared, and in this package no module has.
func setAgentsKnob(t *testing.T, h *Handler, key, value string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.configs.EnsureOwned(ctx, "agents", entity.Config{Key: key, Type: "bool"}))
	require.NoError(t, h.configs.SetOwned(ctx, "agents", key, value))
}

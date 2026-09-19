package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	adminview "github.com/yogasw/wick/internal/admin/view"
	agentproject "github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/entity"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func approvedUser(t *testing.T, db *gorm.DB, id, name string) {
	t.Helper()
	require.NoError(t, db.Create(&entity.User{
		ID: id, Name: name, Email: name + "@x.test", Approved: true,
	}).Error)
}

func ownerPost(t *testing.T, h http.HandlerFunc, path, id, owner string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("owner_user_id="+owner))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// carriesOwnerTag reports whether the user is linked to "owner:<resourceID>".
func carriesOwnerTag(t *testing.T, db *gorm.DB, resourceID, userID string) bool {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&entity.UserTag{}).
		Joins("JOIN tags ON tags.id = user_tags.tag_id").
		Where("tags.name = ? AND user_tags.user_id = ?", "owner:"+resourceID, userID).
		Count(&n).Error)
	return n > 0
}

// ── connectors ───────────────────────────────────────────────────────────────

// Handing an instance to somebody else has to move BOTH facts: the CreatedBy
// column that decides who may configure it and see its account pool, and the
// owner tag that carries the configure grant. Moving one without the other is
// how a previous owner keeps reaching a row that is no longer theirs.
func TestSetConnectorOwnerMovesColumnAndTag(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	approvedUser(t, db, "u-old", "Old")
	approvedUser(t, db, "u-new", "New")
	approvedUser(t, db, "u-grantee", "Grantee")

	row, err := svc.Create(ctx, "sso-admin", "Row", nil, "u-old")
	require.NoError(t, err)
	tag := &entity.Tag{Name: "owner:" + row.ID, IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-old", TagID: tag.ID}).Error)
	// An admin also granted the owner tag to somebody else from the Tags page.
	// That is a deliberate share and must survive the transfer.
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-grantee", TagID: tag.ID}).Error)

	rec := ownerPost(t, h.setConnectorOwner, "/admin/connectors/"+row.ID+"/owner", row.ID, "u-new")
	require.Equal(t, http.StatusFound, rec.Code)

	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "u-new", fresh.CreatedBy)
	require.True(t, carriesOwnerTag(t, db, row.ID, "u-new"), "new owner must carry the owner tag")
	require.False(t, carriesOwnerTag(t, db, row.ID, "u-old"), "previous owner must lose the owner tag")
	require.True(t, carriesOwnerTag(t, db, row.ID, "u-grantee"), "an unrelated grant must not be revoked")

	// The connector's owner tag is also linked to the row's tool path, which
	// is what makes the grant readable on every access surface.
	var links int64
	require.NoError(t, db.Model(&entity.ToolTag{}).
		Where("tool_path = ? AND tag_id = ?", "/connectors/"+row.ID, tag.ID).
		Count(&links).Error)
	require.Equal(t, int64(1), links)
}

// An instance with no owner tag yet (seeded rows have none) still gets one, so
// the new owner's configure grant is real and not just a column value.
func TestSetConnectorOwnerCreatesMissingOwnerTag(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	approvedUser(t, db, "u-new", "New")
	row, err := svc.Create(ctx, "sso-admin", "Seeded", nil, "")
	require.NoError(t, err)

	rec := ownerPost(t, h.setConnectorOwner, "/admin/connectors/"+row.ID+"/owner", row.ID, "u-new")
	require.Equal(t, http.StatusFound, rec.Code)

	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "u-new", fresh.CreatedBy)
	require.True(t, carriesOwnerTag(t, db, row.ID, "u-new"))
}

// Clearing the owner is a real choice — the row goes back to admin-only — and
// it must not leave the previous owner holding the grant.
func TestSetConnectorOwnerCanClear(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	approvedUser(t, db, "u-old", "Old")
	row, err := svc.Create(ctx, "sso-admin", "Row", nil, "u-old")
	require.NoError(t, err)
	tag := &entity.Tag{Name: "owner:" + row.ID, IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-old", TagID: tag.ID}).Error)

	rec := ownerPost(t, h.setConnectorOwner, "/admin/connectors/"+row.ID+"/owner", row.ID, "")
	require.Equal(t, http.StatusFound, rec.Code)

	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "", fresh.CreatedBy)
	require.False(t, carriesOwnerTag(t, db, row.ID, "u-old"))
}

// The picker only offers approved users, but the endpoint is reachable without
// it. An id naming nobody — or naming the synthetic internal principal — would
// leave the row administered by an account nobody can sign into.
func TestSetConnectorOwnerRejectsBadUsers(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	require.NoError(t, db.Create(&entity.User{
		ID: "u-pending", Name: "Pending", Email: "pending@x.test", Approved: false,
	}).Error)
	row, err := svc.Create(ctx, "sso-admin", "Row", nil, "u-old")
	require.NoError(t, err)

	for _, candidate := range []string{"u-nobody", "u-pending", internalAgentUserID} {
		rec := ownerPost(t, h.setConnectorOwner, "/admin/connectors/"+row.ID+"/owner", row.ID, candidate)
		require.Equal(t, http.StatusBadRequest, rec.Code, "candidate %q must be refused", candidate)
		fresh, err := svc.Get(ctx, row.ID)
		require.NoError(t, err)
		require.Equal(t, "u-old", fresh.CreatedBy, "a refused transfer must not touch the row")
	}
}

func TestSetConnectorOwnerUnknownInstance(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	approvedUser(t, db, "u-new", "New")
	rec := ownerPost(t, h.setConnectorOwner, "/admin/connectors/nope/owner", "nope", "u-new")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ── projects ─────────────────────────────────────────────────────────────────

type fakeProjects struct {
	byID    map[string]agentproject.Project
	written *agentproject.Meta
	err     error
}

func (f *fakeProjects) Projects() map[string]agentproject.Project { return f.byID }

func (f *fakeProjects) UpdateProject(_ context.Context, id string, meta agentproject.Meta) (agentproject.Project, error) {
	if f.err != nil {
		return agentproject.Project{}, f.err
	}
	f.written = &meta
	p := agentproject.Project{Meta: meta}
	f.byID[id] = p
	return p, nil
}

func newProjectOwnerHandler(t *testing.T, id, owner string) (*Handler, *fakeProjects, *gorm.DB) {
	t.Helper()
	h, _, db := newAdminConnectorsHandler(t)
	fp := &fakeProjects{byID: map[string]agentproject.Project{
		id: {Meta: agentproject.Meta{ID: id, Name: "Proj", OwnerUserID: owner}},
	}}
	h.projects = fp
	h.projectWriter = fp
	return h, fp, db
}

func TestSetProjectOwnerWritesMetaAndMovesTag(t *testing.T) {
	h, fp, db := newProjectOwnerHandler(t, "p-1", "u-old")
	approvedUser(t, db, "u-old", "Old")
	approvedUser(t, db, "u-new", "New")
	tag := &entity.Tag{Name: "owner:p-1", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-old", TagID: tag.ID}).Error)

	rec := ownerPost(t, h.setProjectOwner, "/admin/projects/p-1/owner", "p-1", "u-new")
	require.Equal(t, http.StatusFound, rec.Code)
	require.NotNil(t, fp.written)
	require.Equal(t, "u-new", fp.written.OwnerUserID)
	require.Equal(t, "Proj", fp.written.Name, "a transfer must not disturb the rest of the meta")
	require.True(t, carriesOwnerTag(t, db, "p-1", "u-new"))
	require.False(t, carriesOwnerTag(t, db, "p-1", "u-old"))

	// A project's owner tag links the user only — writing a tool-path link
	// here would change how the project's access reads everywhere else.
	var links int64
	require.NoError(t, db.Model(&entity.ToolTag{}).Where("tag_id = ?", tag.ID).Count(&links).Error)
	require.Equal(t, int64(0), links)
}

func TestSetProjectOwnerUnknownProject(t *testing.T) {
	h, _, db := newProjectOwnerHandler(t, "p-1", "u-old")
	approvedUser(t, db, "u-new", "New")
	rec := ownerPost(t, h.setProjectOwner, "/admin/projects/p-2/owner", "p-2", "u-new")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// The owner tag must not move when the meta write failed: that would revoke the
// only access the project still has.
func TestSetProjectOwnerKeepsTagWhenWriteFails(t *testing.T) {
	h, fp, db := newProjectOwnerHandler(t, "p-1", "u-old")
	approvedUser(t, db, "u-old", "Old")
	approvedUser(t, db, "u-new", "New")
	tag := &entity.Tag{Name: "owner:p-1", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-old", TagID: tag.ID}).Error)
	fp.err = gorm.ErrInvalidDB

	rec := ownerPost(t, h.setProjectOwner, "/admin/projects/p-1/owner", "p-1", "u-new")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.True(t, carriesOwnerTag(t, db, "p-1", "u-old"))
	require.False(t, carriesOwnerTag(t, db, "p-1", "u-new"))
}

func TestSetProjectOwnerUnavailableWithoutWriter(t *testing.T) {
	h, _, db := newProjectOwnerHandler(t, "p-1", "u-old")
	approvedUser(t, db, "u-new", "New")
	h.projectWriter = nil
	rec := ownerPost(t, h.setProjectOwner, "/admin/projects/p-1/owner", "p-1", "u-new")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// ── schedules ────────────────────────────────────────────────────────────────

type fakeSchedules struct {
	patch *schedule.SchedulePatch
}

func (f *fakeSchedules) ListAll(context.Context, int) ([]entity.ScheduledMessage, error) {
	return nil, nil
}

func (f *fakeSchedules) Reschedule(_ context.Context, _ string, patch schedule.SchedulePatch) error {
	f.patch = &patch
	return nil
}

func TestSetScheduleOwnerPatchesOwnerOnly(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	approvedUser(t, db, "u-new", "New")
	fs := &fakeSchedules{}
	h.schedules = fs

	rec := ownerPost(t, h.setScheduleOwner, "/admin/schedule/s-1/owner", "s-1", "u-new")
	require.Equal(t, http.StatusFound, rec.Code)
	require.NotNil(t, fs.patch)
	require.NotNil(t, fs.patch.OwnerUserID)
	require.Equal(t, "u-new", *fs.patch.OwnerUserID)
	require.Nil(t, fs.patch.RunAsUserID, "changing the owner must not clear a run-as override")
}

// An empty value un-owns the schedule rather than being read as "no change" —
// which is why the patch field is a pointer.
func TestSetScheduleOwnerCanClear(t *testing.T) {
	h, _, _ := newAdminConnectorsHandler(t)
	fs := &fakeSchedules{}
	h.schedules = fs

	rec := ownerPost(t, h.setScheduleOwner, "/admin/schedule/s-1/owner", "s-1", "")
	require.Equal(t, http.StatusFound, rec.Code)
	require.NotNil(t, fs.patch.OwnerUserID)
	require.Equal(t, "", *fs.patch.OwnerUserID)
}

func TestSetScheduleOwnerNotConfigured(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	approvedUser(t, db, "u-new", "New")
	rec := ownerPost(t, h.setScheduleOwner, "/admin/schedule/s-1/owner", "s-1", "u-new")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// ── owner labels ─────────────────────────────────────────────────────────────

// The Owner column exists to say WHO owns a row. A uuid names nobody, so the
// row carries the resolved display name — and falls back to the id rather than
// to blank when it resolves to no user at all.
func TestResourceRowsCarryOwnerNames(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	approvedUser(t, db, "u-hana", "Hana")

	rows := h.decorateResourceRows(context.Background(), []adminview.ResourceAdminRow{
		{ID: "p-1", Path: "/projects/p-1", CreatedBy: "u-hana"},
		{ID: "p-2", Path: "/projects/p-2", CreatedBy: "u-deleted"},
		{ID: "p-3", Path: "/projects/p-3"},
	}, nil)

	require.Equal(t, "Hana", rows[0].OwnerLabel)
	require.Equal(t, "u-deleted", rows[1].OwnerLabel, "an unresolvable id beats an empty cell")
	require.Equal(t, "", rows[2].OwnerLabel, "an ownerless row stays ownerless")
}

// ── workflows ────────────────────────────────────────────────────────────────

// fakeWorkflows is both the lister the page reads and the writer the transfer
// goes through, so a test can assert the stamp and the tag moved together.
type fakeWorkflows struct {
	info map[string]WorkflowInfo
	err  error
}

func (f *fakeWorkflows) List() ([]string, error) {
	out := make([]string, 0, len(f.info))
	for id := range f.info {
		out = append(out, id)
	}
	return out, nil
}

func (f *fakeWorkflows) LoadInfo(id string) (WorkflowInfo, error) {
	w, ok := f.info[id]
	if !ok {
		return WorkflowInfo{}, gorm.ErrRecordNotFound
	}
	return w, nil
}

func (f *fakeWorkflows) SetOwner(id, userID string) error {
	if f.err != nil {
		return f.err
	}
	w := f.info[id]
	w.CreatedBy = userID
	f.info[id] = w
	return nil
}

func newWorkflowOwnerHandler(t *testing.T, id, owner string) (*Handler, *fakeWorkflows, *gorm.DB) {
	t.Helper()
	h, _, db := newAdminConnectorsHandler(t)
	fw := &fakeWorkflows{info: map[string]WorkflowInfo{
		id: {Name: "Flow", CreatedBy: owner},
	}}
	h.workflows = fw
	h.workflowOwner = fw
	return h, fw, db
}

// A workflow's owner is stamped once at create and nothing could move it, so a
// workflow built by someone who has left — or created before ownership was
// recorded — stayed admin-only forever. The transfer has to move both facts:
// the stamp the listing filters on, and the tag that carries the reach.
func TestSetWorkflowOwnerMovesStampAndTag(t *testing.T) {
	h, fw, db := newWorkflowOwnerHandler(t, "wf-1", "u-old")
	approvedUser(t, db, "u-old", "Old")
	approvedUser(t, db, "u-new", "New")
	tag := &entity.Tag{Name: "owner:wf-1", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-old", TagID: tag.ID}).Error)

	rec := ownerPost(t, h.setWorkflowOwner, "/admin/workflows/wf-1/owner", "wf-1", "u-new")
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "u-new", fw.info["wf-1"].CreatedBy)
	require.Equal(t, "Flow", fw.info["wf-1"].Name, "a transfer must not disturb the rest of the row")
	require.True(t, carriesOwnerTag(t, db, "wf-1", "u-new"))
	require.False(t, carriesOwnerTag(t, db, "wf-1", "u-old"))
}

// An ownerless workflow is the case this page mostly exists for: nothing to
// unlink, and the tag has to be created so the new owner can actually reach it.
func TestSetWorkflowOwnerAdoptsOwnerlessWorkflow(t *testing.T) {
	h, fw, db := newWorkflowOwnerHandler(t, "wf-1", "")
	approvedUser(t, db, "u-new", "New")

	rec := ownerPost(t, h.setWorkflowOwner, "/admin/workflows/wf-1/owner", "wf-1", "u-new")
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "u-new", fw.info["wf-1"].CreatedBy)
	require.True(t, carriesOwnerTag(t, db, "wf-1", "u-new"))
}

func TestSetWorkflowOwnerUnknownWorkflow(t *testing.T) {
	h, _, db := newWorkflowOwnerHandler(t, "wf-1", "u-old")
	approvedUser(t, db, "u-new", "New")
	rec := ownerPost(t, h.setWorkflowOwner, "/admin/workflows/wf-2/owner", "wf-2", "u-new")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// The tag must not move when the stamp failed — that would revoke the only
// access the workflow still has.
func TestSetWorkflowOwnerKeepsTagWhenWriteFails(t *testing.T) {
	h, fw, db := newWorkflowOwnerHandler(t, "wf-1", "u-old")
	approvedUser(t, db, "u-old", "Old")
	approvedUser(t, db, "u-new", "New")
	tag := &entity.Tag{Name: "owner:wf-1", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)
	require.NoError(t, db.Create(&entity.UserTag{UserID: "u-old", TagID: tag.ID}).Error)
	fw.err = gorm.ErrInvalidDB

	rec := ownerPost(t, h.setWorkflowOwner, "/admin/workflows/wf-1/owner", "wf-1", "u-new")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.True(t, carriesOwnerTag(t, db, "wf-1", "u-old"))
	require.False(t, carriesOwnerTag(t, db, "wf-1", "u-new"))
}

func TestSetWorkflowOwnerUnavailableWithoutWriter(t *testing.T) {
	h, _, db := newWorkflowOwnerHandler(t, "wf-1", "u-old")
	approvedUser(t, db, "u-new", "New")
	h.workflowOwner = nil
	rec := ownerPost(t, h.setWorkflowOwner, "/admin/workflows/wf-1/owner", "wf-1", "u-new")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	adminview "github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/agents/trash"
)

type fakeTrash struct {
	items    []trash.Manifest
	restored []string
	err      error
}

func (f *fakeTrash) DeletedProjects(context.Context) ([]trash.Manifest, error) { return f.items, nil }

func (f *fakeTrash) RestoreDeletedProject(_ context.Context, key string) error {
	if f.err != nil {
		return f.err
	}
	f.restored = append(f.restored, key)
	return nil
}

func TestProjectShowDefaultsToActive(t *testing.T) {
	for q, want := range map[string]string{
		"": adminview.ShowActive, "?show=bogus": adminview.ShowActive,
		"?show=deleted": adminview.ShowDeleted, "?show=all": adminview.ShowAll,
	} {
		r := httptest.NewRequest(http.MethodGet, "/admin/projects"+q, nil)
		require.Equal(t, want, projectShow(r), q)
	}
}

func TestDeletedProjectRows(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	rows := deletedProjectRows([]trash.Manifest{{
		Key: "acme__1", ID: "acme", Name: "Acme", OwnerUserID: "user-a", DeletedBy: "user-b",
		DeletedAt: now.Add(-5 * 24 * time.Hour), SessionIDs: []string{"s1", "s2"},
	}}, map[string]string{"user-a": "User A", "user-b": "User B"}, now)
	require.Len(t, rows, 1)
	d := rows[0].Deleted
	require.NotNil(t, d)
	require.Equal(t, "acme__1", d.Key)
	require.Equal(t, "User B", d.By)
	require.Equal(t, 25, d.DaysLeft)
	require.Equal(t, 2, d.Chats)
	require.Equal(t, "User A", rows[0].OwnerLabel)
}

func TestRestoreDeletedProjectRoute(t *testing.T) {
	h := &Handler{}
	ft := &fakeTrash{}
	h.SetProjectTrash(ft)
	post := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/admin/projects-trash/"+key+"/restore", nil)
		r.SetPathValue("key", key)
		w := httptest.NewRecorder()
		h.restoreDeletedProject(w, r)
		return w
	}
	w := post("acme__1")
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "/admin/projects?show=deleted", w.Header().Get("Location"))
	require.Equal(t, []string{"acme__1"}, ft.restored)

	ft.err = fmt.Errorf("cannot restore: a project with this id already exists")
	require.Equal(t, http.StatusConflict, post("acme__1").Code)
	ft.err = trash.ErrNotFound
	require.Equal(t, http.StatusNotFound, post("nope").Code)

	h.SetProjectTrash(nil)
	require.Equal(t, http.StatusServiceUnavailable, post("acme__1").Code)
}

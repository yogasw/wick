package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

type fakeTeamAgents []TeamAgent

func (f fakeTeamAgents) TeamAgents(context.Context) ([]TeamAgent, error) { return f, nil }

func teamTagPost(t *testing.T, h *Handler, id, tagID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/team-agents/"+id+"/tags",
		strings.NewReader(tagSubmitMarker+"=1&tag_ids[]="+tagID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.setTeamAgentTags(rec, req)
	return rec
}

func TestSetTeamAgentTags(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	h.teamAgents = fakeTeamAgents{
		{ID: "a-1", Handle: "ops", OwnerUserID: "u-1"},
		{ID: "cap", Handle: "captain", OwnerUserID: "u-1", Block: "The Captain runs your Team and cannot be shared."},
	}
	tag := &entity.Tag{Name: "support", IsFilter: true}
	require.NoError(t, db.Create(tag).Error)

	tagged := func(id string) int64 {
		var n int64
		require.NoError(t, db.Model(&entity.ToolTag{}).Where("tool_path = ?", teamAgentPrefix+id).Count(&n).Error)
		return n
	}

	rec := teamTagPost(t, h, "a-1", tag.ID)
	require.Equal(t, http.StatusFound, rec.Code)
	require.EqualValues(t, 1, tagged("a-1"))

	rec = teamTagPost(t, h, "cap", tag.ID)
	require.Equal(t, http.StatusConflict, rec.Code, "the Captain is never shared")
	require.EqualValues(t, 0, tagged("cap"))

	rec = teamTagPost(t, h, "nope", tag.ID)
	require.Equal(t, http.StatusNotFound, rec.Code)

	require.Equal(t, "u-1", h.resourceOwnerID(teamAgentPrefix+"a-1"))
	require.False(t, h.adminBypassFor(teamAgentPrefix+"a-1"), "a tag share is not an admin pass")
}

func TestTeamAgentsPageListsCaptainLocked(t *testing.T) {
	h, _, _ := newAdminConnectorsHandler(t)
	h.teamAgents = fakeTeamAgents{
		{ID: "a-1", Handle: "ops", OwnerUserID: "u-1"},
		{ID: "cap-1", Handle: "captain", OwnerUserID: "u-1", Block: "The Captain runs your Team and cannot be shared."},
	}
	rec := httptest.NewRecorder()
	h.teamAgentsAdminPage(rec, httptest.NewRequest(http.MethodGet, "/admin/team-agents", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "resource-cap-1", "the Captain is listed")
	require.Contains(t, body, "Not shareable")
	require.NotContains(t, body, "/admin/team-agents/cap-1/tags", "no tag form for the Captain")
	require.Contains(t, body, "/admin/team-agents/a-1/tags")
}

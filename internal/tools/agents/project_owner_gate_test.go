package agents

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// gateStatus runs h behind projectAccessMW, the way every /projects/{id}
// route is mounted, as user u on project id; 200 means h was reached.
func gateStatus(t *testing.T, u *entity.User, id string, h tool.HandlerFunc) int {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/projects/"+id, nil, map[string]string{"id": id})
	projectAccessMW(h)(c)
	return w.Code
}

func reached(c *tool.Ctx) { c.JSON(http.StatusOK, map[string]string{"status": "ok"}) }

// A project is deleted with every session and ticket in it, so settings
// and delete are the owner's alone. A member holding a tag grant still
// works the board every day; an ownerless project stays the admin's.
func TestProjectOwnerGate(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "acme", "user-a")
	seedTeamProject(t, "legacy", "")
	owner := &entity.User{ID: "user-a", Role: entity.RoleUser, Approved: true}
	member := &entity.User{ID: "user-b", Role: entity.RoleUser, Approved: true}
	admin := &entity.User{ID: "user-admin", Role: entity.RoleAdmin, Approved: true}
	// A cached grant stands in for the tag that shares acme with user-b.
	grantCache.Store(member.ID, grantCacheEntry{set: map[string]struct{}{"acme": {}}, expiry: time.Now().Add(time.Hour)})
	t.Cleanup(func() { grantCache.Delete(member.ID) })

	settings := projectOwnerMW(reached)
	cases := []struct {
		name string
		u    *entity.User
		id   string
		h    tool.HandlerFunc
		want int
	}{
		{"owner opens settings", owner, "acme", settings, http.StatusOK},
		{"owner on the board", owner, "acme", reached, http.StatusOK},
		{"member refused settings", member, "acme", settings, http.StatusForbidden},
		{"member on the board", member, "acme", reached, http.StatusOK},
		{"admin refused another owner's settings", admin, "acme", settings, http.StatusForbidden},
		{"admin opens ownerless settings", admin, "legacy", settings, http.StatusOK},
		{"member refused ownerless settings", member, "legacy", settings, http.StatusForbidden},
	}
	for _, tc := range cases {
		if got := gateStatus(t, tc.u, tc.id, tc.h); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}

	// The project picker feed tells the UI whether to offer settings.
	if got := getProjectOptions(t, member, "/projects/options")["acme"]; got == nil || got["can_manage"] != false {
		t.Errorf("member options row for acme = %v, want can_manage false", got)
	}
	if got := getProjectOptions(t, owner, "/projects/options")["acme"]; got == nil || got["can_manage"] != true {
		t.Errorf("owner options row for acme = %v, want can_manage true", got)
	}

	w, c := teamReq(t, member, http.MethodDelete, "/projects/acme", nil, map[string]string{"id": "acme"})
	projectAccessMW(projectOwnerMW(reached))(c)
	if !strings.Contains(w.Body.String(), team.ErrNotProjectOwner.Error()) {
		t.Errorf("refusal should say why, got %q", w.Body.String())
	}
}

// A member who follows a link to the settings page gets a page explaining
// the refusal, with a way back to the board, rather than a raw error body.
func TestProjectSettingsForbiddenPage(t *testing.T) {
	var buf bytes.Buffer
	comp := view.ProjectSettingsForbidden(view.AgentsLayoutVM{}, "/tools/agents", "acme", team.ErrNotProjectOwner.Error())
	if err := comp.Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{team.ErrNotProjectOwner.Error(), `href="/tools/agents/sessions?project=acme"`} {
		if !strings.Contains(out, want) {
			t.Errorf("page misses %q", want)
		}
	}
}

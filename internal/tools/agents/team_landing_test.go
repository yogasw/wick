package agents

import (
	"context"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The bare Agents landing goes to Team only once "Open Team when I open
// Agents" is turned on (it is off by default); a query, another path, the
// toggle off, no sign-in or no Team app all keep the landing.
func TestTeamLandingRedirect(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	check := func(name string, user *entity.User, target string, want bool) {
		t.Helper()
		_, c := teamReq(t, user, http.MethodGet, target, nil, nil)
		if got := teamLandingRedirect(c); got != want {
			t.Errorf("%s (%s): redirect=%v, want %v", name, target, got, want)
		}
	}
	check("default off", u, "/tools/agents", false)

	if err := globalTeam.SaveSettings(context.Background(), &entity.TeamSettings{UserID: u.ID, TeamHome: true}); err != nil {
		t.Fatal(err)
	}
	check("toggle on", u, "/tools/agents", true)
	check("trailing slash", u, "/tools/agents/", true)
	check("classic marker", u, "/tools/agents?view=classic", false)
	check("scoped landing", u, "/tools/agents/?project=p1", false)
	check("deep link", u, "/tools/agents/sessions/s1", false)
	check("not signed in", &entity.User{}, "/tools/agents", false)

	if err := globalTeam.SaveSettings(context.Background(), &entity.TeamSettings{UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	check("toggle off", u, "/tools/agents", false)
	check("another user keeps their default", &entity.User{ID: "u2"}, "/tools/agents", false)

	if err := globalTeam.SaveSettings(context.Background(), &entity.TeamSettings{UserID: u.ID, TeamHome: true}); err != nil {
		t.Fatal(err)
	}
	globalTeam = nil
	check("no Team app", u, "/tools/agents", false)
}

// End to end through the landing handler: a 302 to /team, before the
// pinned-project redirect gets a say.
func TestNewSessionComposeRedirectsToTeam(t *testing.T) {
	withTeamWorld(t)
	if err := globalTeam.SaveSettings(context.Background(), &entity.TeamSettings{UserID: "u1", TeamHome: true}); err != nil {
		t.Fatal(err)
	}
	w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodGet, "/tools/agents", nil, nil)
	newSessionCompose(c)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/tools/agents/team" {
		t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
	}
}

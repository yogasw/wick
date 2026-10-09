package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// spawnGateWorld wires a temp layout with one session "s1" owned by the
// given user, the knob set to adminAll, and a tag store that grants only
// the instance named "mine". Everything is restored after the test.
func spawnGateWorld(t *testing.T, owner string, adminAll bool) {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "s1", Origin: session.OriginUI, UserID: owner,
	}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	prevMgr, prevLayout, prevPool := globalMgr, globalLayout, globalPool
	prevTag, prevKnob := providerAccessTagAllows, adminSeeAllProviders
	globalMgr, globalLayout = registry.NewManager(reg), layout
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool { return name == "mine" }
	adminSeeAllProviders = func() bool { return adminAll }
	t.Cleanup(func() {
		globalMgr, globalLayout, globalPool = prevMgr, prevLayout, prevPool
		providerAccessTagAllows, adminSeeAllProviders = prevTag, prevKnob
	})
}

func formCtx(u *entity.User, form url.Values) (*httptest.ResponseRecorder, *tool.Ctx) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(login.WithUser(r.Context(), u, nil))
	w := httptest.NewRecorder()
	return w, tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
}

func sessionCount(t *testing.T) int {
	t.Helper()
	return len(globalMgr.Registry().Sessions())
}

var (
	gateAdmin = &entity.User{ID: "admin", Role: entity.RoleAdmin, Approved: true}
	gateUser  = &entity.User{ID: "user", Role: entity.RoleUser, Approved: true}
)

// Creating a session with an instance the caller cannot pick is refused
// before anything is written — for a non-admin always, for an admin once
// admin_see_all_provider_instances is off.
func TestCreateSessionRefusesProviderWithoutAccess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		u        *entity.User
		adminAll bool
	}{
		{"non-admin", gateUser, true},
		{"admin with knob off", gateAdmin, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spawnGateWorld(t, tc.u.ID, tc.adminAll)
			before := sessionCount(t)
			for _, h := range []func(*tool.Ctx){createSession, startNewSession} {
				w, c := formCtx(tc.u, url.Values{"provider": {"claude/other::some-model"}, "message": {"hi"}})
				h(c)
				if w.Code != http.StatusForbidden {
					t.Fatalf("code = %d, want 403 (%s)", w.Code, w.Body.String())
				}
				if !strings.Contains(w.Body.String(), "no access to provider claude/other") {
					t.Fatalf("body = %q", w.Body.String())
				}
			}
			if got := sessionCount(t); got != before {
				t.Fatalf("a refused create still wrote a session: %d → %d", before, got)
			}
		})
	}
}

// The allowed side: an instance the tags reach, or any instance for an
// admin with the knob on, gets past the gate (whatever happens after it).
func TestCreateSessionAllowsProviderWithAccess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		u        *entity.User
		adminAll bool
		prov     string
	}{
		{"non-admin with tag", gateUser, true, "claude/mine"},
		{"admin knob on, untagged-for-them instance", gateAdmin, true, "claude/other"},
		{"admin knob off, with tag", gateAdmin, false, "claude/mine"},
		{"nothing chosen", gateUser, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spawnGateWorld(t, tc.u.ID, tc.adminAll)
			w, c := formCtx(tc.u, url.Values{"provider": {tc.prov}})
			createSession(c)
			if w.Code == http.StatusForbidden {
				t.Fatalf("refused: %s", w.Body.String())
			}
		})
	}
}

// Switching a session's provider — the endpoint and the "#tag" message
// prefix — answers 403 for an instance the caller cannot pick.
func TestSwitchProviderRefusesWithoutAccess(t *testing.T) {
	spawnGateWorld(t, gateAdmin.ID, false)
	path := map[string]string{"id": "s1"}

	w, c := postCtx(t, gateAdmin, "/", `{"provider":"claude/other"}`, path)
	switchProvider(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("switch: code = %d, want 403 (%s)", w.Code, w.Body.String())
	}

	// The "#tag" form never switches to it either; it is ignored rather
	// than refused (TestTeamChatProviderSwitchGates covers the notice).
	w, c = postCtx(t, gateAdmin, "/", `{"text":"#claude/other hello"}`, path)
	sendMessage(c)
	if strings.Contains(w.Body.String(), `"switched"`) {
		t.Fatalf("#tag: switched to an instance without access (%d %s)", w.Code, w.Body.String())
	}

	// With the tag the gate lets it through (the switch itself then
	// fails on the empty temp layout, which is not a 403).
	w, c = postCtx(t, gateAdmin, "/", `{"provider":"claude/mine"}`, path)
	switchProvider(c)
	if w.Code == http.StatusForbidden {
		t.Fatalf("switch to a tagged instance refused: %s", w.Body.String())
	}
}

// The key forms the client sends all resolve to the same check.
func TestProviderKeyAllowedForms(t *testing.T) {
	spawnGateWorld(t, gateUser.ID, true)
	_, c := postCtx(t, gateUser, "/", "{}", nil)
	for key, want := range map[string]bool{
		"":                   true,
		"claude/mine":        true,
		"claude/mine::opus":  true,
		"claude/other":       false,
		"claude/other::opus": false,
		"claude":             false, // bare type = instance "claude"
		"  claude/other  ":   false,
	} {
		if got := providerKeyAllowed(c, key); got != want {
			t.Errorf("providerKeyAllowed(%q) = %v, want %v", key, got, want)
		}
	}
}

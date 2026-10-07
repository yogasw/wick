package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// groupsSlack answers usergroups.list with a membership the test can change,
// counting the calls so the cache is observable.
type groupsSlack struct {
	srv     *httptest.Server
	mu      sync.Mutex
	members map[string][]string
	calls   int
}

func newGroupsSlack(t *testing.T, members map[string][]string) *groupsSlack {
	t.Helper()
	f := &groupsSlack{members: members}
	mux := http.NewServeMux()
	mux.HandleFunc("/usergroups.list", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		groups := []map[string]any{}
		for id, users := range f.members {
			groups = append(groups, map[string]any{"id": id, "users": users})
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "usergroups": groups})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *groupsSlack) set(group string, users ...string) {
	f.mu.Lock()
	f.members[group] = users
	f.mu.Unlock()
}

func (f *groupsSlack) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Scenario 1: users [User A] + groups [Group B] + bots specific [Bot A] +
// every channel. People pass through either list, Bot A through the bots
// list; anyone else is refused and other bots are ignored, in any channel.
func TestAccessScenarioUsersGroupsBotsAllChannels(t *testing.T) {
	gs := newGroupsSlack(t, map[string][]string{"SGROUPB": {"UMEMBERB"}})
	s := &Channel{botUserID: "USELF", api: slackgo.New("xoxb-test", slackgo.OptionAPIURL(gs.srv.URL+"/"))}
	cfg := agentconfig.DefaultSlackChannelConfig()
	cfg.UsersMode, cfg.AllowedUsers = "whitelist", `[{"id":"UUSERA","name":"User A"}]`
	cfg.GroupsMode, cfg.AllowedGroups = "whitelist", `[{"id":"SGROUPB","name":"Group B"}]`
	cfg.BotsMode, cfg.AllowedBots = "whitelist", `[{"id":"UBOTA","name":"Bot A"}]`
	cfg.ChannelsMode = "all"

	for _, ch := range []string{"CANY1", "CANY2"} {
		people := []struct {
			user   string
			ok     bool
			reason string
		}{
			{"UUSERA", true, ""},
			{"UMEMBERB", true, ""},
			{"UOTHER", false, "identity"},
		}
		for _, p := range people {
			ok, reason, _ := s.allowedSender(cfg, p.user, ch)
			if ok != p.ok || reason != p.reason {
				t.Errorf("%s in %s: ok=%v reason=%q, want %v/%q", p.user, ch, ok, reason, p.ok, p.reason)
			}
		}
		if ok, reason := s.allowedBotCfg(cfg, &slackevents.MessageEvent{User: "UBOTA", BotID: "BA"}, ch); !ok {
			t.Errorf("Bot A in %s refused: %q", ch, reason)
		}
		if ok, reason := s.allowedBotCfg(cfg, &slackevents.MessageEvent{User: "UBOTZ", BotID: "BZ"}, ch); ok || reason != "bots" {
			t.Errorf("other bot in %s: ok=%v reason=%q, want ignored (bots)", ch, ok, reason)
		}
	}
}

// Scenario 2: channels [Channel X] + groups [Group A, Group B] + users
// [User C]. Members of either group and User C pass in Channel X only; the
// same people elsewhere are refused for "channels", strangers for
// "identity". Membership comes from usergroups.list, cached for
// userGroupsTTL, so a new member passes once the cache runs out.
func TestAccessScenarioChannelGroupsUsers(t *testing.T) {
	gs := newGroupsSlack(t, map[string][]string{"SGROUPA": {"UMEMBERA"}, "SGROUPB": {"UMEMBERB"}})
	s := &Channel{api: slackgo.New("xoxb-test", slackgo.OptionAPIURL(gs.srv.URL+"/"))}
	cfg := agentconfig.DefaultSlackChannelConfig()
	cfg.ChannelsMode, cfg.AllowedChannels = "whitelist", `[{"id":"CX","name":"#x"}]`
	cfg.GroupsMode, cfg.AllowedGroups = "whitelist", `[{"id":"SGROUPA","name":"Group A"},{"id":"SGROUPB","name":"Group B"}]`
	cfg.UsersMode, cfg.AllowedUsers = "whitelist", `[{"id":"UUSERC","name":"User C"}]`

	cases := []struct {
		user, channel string
		ok            bool
		reason        string
	}{
		{"UMEMBERA", "CX", true, ""},
		{"UMEMBERB", "CX", true, ""},
		{"UUSERC", "CX", true, ""},
		{"UMEMBERA", "CY", false, "channels"},
		{"UMEMBERB", "CY", false, "channels"},
		{"UUSERC", "CY", false, "channels"},
		{"UOUTSIDER", "CX", false, "identity"},
		{"UOUTSIDER", "CY", false, "identity"},
	}
	for _, c := range cases {
		ok, reason, _ := s.allowedSender(cfg, c.user, c.channel)
		if ok != c.ok || reason != c.reason {
			t.Errorf("%s in %s: ok=%v reason=%q, want %v/%q", c.user, c.channel, ok, reason, c.ok, c.reason)
		}
	}
	if n := gs.count(); n != 1 {
		t.Errorf("usergroups.list called %d times for %d checks, want 1 (cached)", n, len(cases))
	}

	// Someone joins Group A in Slack: refused while the snapshot is fresh,
	// let in once it is older than userGroupsTTL — no restart.
	gs.set("SGROUPA", "UMEMBERA", "UNEWBIE")
	if ok, _, _ := s.allowedSender(cfg, "UNEWBIE", "CX"); ok {
		t.Error("new member passed before the cache ran out")
	}
	s.userGroupsMu.Lock()
	s.userGroupsAt = time.Now().Add(-userGroupsTTL - time.Second)
	s.userGroupsMu.Unlock()
	if ok, reason, groups := s.allowedSender(cfg, "UNEWBIE", "CX"); !ok || !slices.Contains(groups, "SGROUPA") {
		t.Errorf("new member after TTL: ok=%v reason=%q groups=%v, want allowed via SGROUPA", ok, reason, groups)
	}
	if n := gs.count(); n != 2 {
		t.Errorf("usergroups.list called %d times, want 2 after the TTL", n)
	}
}

// The manifest an agent app is created from asks for usergroups:read, the
// scope usergroups.list needs.
func TestManifestAsksForUsergroupsRead(t *testing.T) {
	if !slices.Contains(RequiredBotScopes(), "usergroups:read") {
		t.Fatalf("bot scopes %v miss usergroups:read", RequiredBotScopes())
	}
}

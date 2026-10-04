package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
	pkgentity "github.com/yogasw/wick/pkg/entity"
)

// withOwnerLookup seeds owner u1 with an email and swaps the Slack stub for
// one that also answers users.lookupByEmail. found=false makes Slack answer
// users_not_found.
func withOwnerLookup(t *testing.T, found bool) {
	t.Helper()
	if err := globalDB.AutoMigrate(&entity.User{}); err != nil {
		t.Fatal(err)
	}
	if err := globalDB.Create(&entity.User{ID: "u1", Email: "owner@example.test", Name: "Owner"}).Error; err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "users.list") || strings.Contains(r.URL.Path, "users.info") {
			t.Errorf("owner resolution called %s: only users.lookupByEmail may identify the owner", r.URL.Path)
		}
		if strings.Contains(r.URL.Path, "users.lookupByEmail") {
			if !found || r.Form.Get("email") != "owner@example.test" {
				_, _ = w.Write([]byte(`{"ok":false,"error":"users_not_found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"UOWNER","name":"owner","real_name":"Owner Person"}}`))
			return
		}
		tok := r.Form.Get("token")
		bot := tok[strings.LastIndex(tok, "-")+1:]
		_, _ = w.Write([]byte(`{"ok":true,"user_id":"U` + bot + `","bot_id":"` + bot + `","user":"bot","team":"T"}`))
	}))
	prev := slackAPIURL
	slackAPIURL = srv.URL + "/"
	t.Cleanup(func() { slackAPIURL = prev; srv.Close() })
}

func getSlackSettings(t *testing.T, u *entity.User, agentID string) AgentSlackSettings {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+agentID+"/slack/settings", nil, map[string]string{"id": agentID})
	apiTeamAgentSlackSettingsGet(c)
	if w.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	var st AgentSlackSettings
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	return st
}

func settingValue(st AgentSlackSettings, key string) (string, bool) {
	for _, f := range st.Fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

// A brand-new app is "Only me": the owner, found by email in the app's
// workspace, is the only allowed user.
func TestAgentSlackNewAppDefaultsToOwnerOnly(t *testing.T) {
	withAgentSlackWorld(t)
	withOwnerLookup(t, true)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	st := getSlackSettings(t, u, a.ID)
	if mode, _ := settingValue(st, "users_mode"); mode != "whitelist" {
		t.Fatalf("users_mode = %q, want whitelist", mode)
	}
	if list, _ := settingValue(st, "allowed_users"); !strings.Contains(list, `"UOWNER"`) {
		t.Fatalf("allowed_users = %q, want the owner", list)
	}
	if st.OwnerID != "UOWNER" || st.OwnerName != "Owner Person" || st.OwnerHandle != "owner" {
		t.Fatalf("owner = %q/%q/%q", st.OwnerID, st.OwnerName, st.OwnerHandle)
	}
}

// When Slack cannot find the owner nothing is guessed by name: the app is
// closed (whitelist, empty list) and the settings carry no owner, so the
// card warns and asks for a manual pick.
func TestAgentSlackOwnerNotFoundStaysClosed(t *testing.T) {
	withAgentSlackWorld(t)
	withOwnerLookup(t, false)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	st := getSlackSettings(t, u, a.ID)
	if mode, _ := settingValue(st, "users_mode"); mode != "whitelist" {
		t.Fatalf("users_mode = %q, want whitelist", mode)
	}
	if list, _ := settingValue(st, "allowed_users"); list != "[]" {
		t.Fatalf("allowed_users = %q, want an empty list", list)
	}
	if st.OwnerID != "" {
		t.Fatalf("owner resolved without Slack: %q", st.OwnerID)
	}
}

// A connection that already existed keeps its access on reconnect.
func TestAgentSlackExistingConnectionKeepsAccess(t *testing.T) {
	withAgentSlackWorld(t)
	withOwnerLookup(t, true)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	if err := agentchannels.SaveAgentSlack(globalDB, a.ID, "u1", map[string]string{"bot_token": "xoxb-old-B1", "app_token": "xapp-1", "mode": "socket"}, true); err != nil {
		t.Fatal(err)
	}
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	if mode, _ := settingValue(getSlackSettings(t, u, a.ID), "users_mode"); mode != "all" {
		t.Fatalf("existing connection changed to %q", mode)
	}
}

// The card edits the four setting groups and shows Connection and Routing
// read-only; only the editable keys save.
func TestAgentSlackSettingsGroupsAndPatch(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	st := getSlackSettings(t, u, a.ID)
	var order []string
	editable := map[string]bool{}
	for _, f := range st.Fields {
		if len(order) == 0 || order[len(order)-1] != f.Group {
			order = append(order, f.Group)
		}
		editable[f.Group] = editable[f.Group] || !f.ReadOnly
		if f.Group == "Access Control" && f.GroupDesc == "" {
			t.Errorf("field %q lost its group description", f.Key)
		}
		if f.ReadOnly && f.Note == "" {
			t.Errorf("read-only field %q has no note", f.Key)
		}
		if (f.Key == "bot_token" || f.Key == "app_token") && f.Value != "set" {
			t.Errorf("%s = %q, want only \"set\"", f.Key, f.Value)
		}
	}
	want := []string{"Access Control", "Agent Behaviour", "Reaction Auto-Reply", "Approval Gates", "Connection", "Routing"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("groups = %v, want %v", order, want)
	}
	if editable["Connection"] || editable["Routing"] {
		t.Errorf("Connection/Routing editable: %v", editable)
	}
	if v, ok := settingValue(st, "bots_mode"); !ok || v != "none" {
		t.Errorf("bots_mode = %q (present %v), want none", v, ok)
	}

	patch := func(key, value string) *httptest.ResponseRecorder {
		w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+a.ID+"/slack/settings", map[string]any{"key": key, "value": value}, map[string]string{"id": a.ID})
		apiTeamAgentSlackSettingsPatch(c)
		return w
	}
	for _, k := range []string{"bot_token", "mode", "project_id", "public_url"} {
		if w := patch(k, "x"); w.Code != http.StatusBadRequest {
			t.Fatalf("patching read-only %s: %d, want 400", k, w.Code)
		}
	}
	if w := patch("bots_mode", "whitelist"); w.Code != http.StatusOK {
		t.Fatalf("patch bots_mode: %d %s", w.Code, w.Body.String())
	}
	if v, _ := settingValue(getSlackSettings(t, u, a.ID), "bots_mode"); v != "whitelist" {
		t.Fatalf("bots_mode after patch = %q", v)
	}
}

// users.lookupByEmail needs users:read.email, so the app manifest asks for it.
func TestAgentSlackManifestAsksForEmailScope(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	b, err := json.Marshal(agentSlackManifest(a))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"users:read.email"`) {
		t.Fatalf("manifest lacks users:read.email: %s", b)
	}
}

// Every field of the Channels page schema reaches the Team card: the four
// groups editable with the same tags, everything else read-only with a note.
func TestAgentSlackSettingsCoverSchema(t *testing.T) {
	rows := pkgentity.StructToConfigs(agentconfig.DefaultSlackChannelConfig())
	got := map[string]agentSlackField{}
	for _, f := range agentSlackSettingFields(nil, nil) {
		got[f.Key] = f
	}
	if len(got) != len(rows) {
		t.Errorf("card has %d fields, schema %d", len(got), len(rows))
	}
	for _, r := range rows {
		f, ok := got[r.Key]
		title, _, _ := strings.Cut(r.Group, "|")
		switch {
		case !ok:
			t.Errorf("schema field %q missing from the card", r.Key)
		case f.Group != title:
			t.Errorf("%q in group %q, schema says %q", r.Key, f.Group, title)
		case agentSlackSettingGroups[title] && !r.IsSecret:
			if f.ReadOnly || f.Type != r.Type || f.Options != r.Options || f.VisibleWhen != r.VisibleWhen {
				t.Errorf("%q differs from the schema: %+v vs %+v", r.Key, f, r)
			}
		default:
			if !f.ReadOnly || agentSlackReadOnlyNotes[r.Key] == "" {
				t.Errorf("%q should be read-only with a note: %+v", r.Key, f)
			}
		}
	}
}

// Yoga's scenario 1 is set from the card: users and groups whitelists both
// on, bots specific, every channel — each key saved on the agent's row.
func TestAgentSlackSettingsScenarioUsersGroupsBots(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "captain", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	set := map[string]string{
		"users_mode": "whitelist", "allowed_users": `[{"id":"UA","name":"User A"}]`,
		"groups_mode": "whitelist", "allowed_groups": `[{"id":"SB","name":"Group B"}]`,
		"bots_mode": "whitelist", "allowed_bots": `[{"id":"UBA","name":"Bot A"}]`,
		"channels_mode": "all",
	}
	for k, v := range set {
		w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+a.ID+"/slack/settings", map[string]any{"key": k, "value": v}, map[string]string{"id": a.ID})
		apiTeamAgentSlackSettingsPatch(c)
		if w.Code != http.StatusOK {
			t.Fatalf("patch %s: %d %s", k, w.Code, w.Body.String())
		}
	}
	m, _ := agentchannels.AgentSlackConfig(globalDB, a.ID)
	var cfg agentconfig.SlackChannelConfig
	pkgentity.MapToStruct(m, &cfg)
	if cfg.UsersMode != "whitelist" || cfg.GroupsMode != "whitelist" || cfg.BotsMode != "whitelist" || cfg.ChannelsMode != "all" ||
		cfg.AllowedUsers != set["allowed_users"] || cfg.AllowedGroups != set["allowed_groups"] || cfg.AllowedBots != set["allowed_bots"] {
		t.Fatalf("saved config = %+v", cfg)
	}
}

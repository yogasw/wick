package agents

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	agenttelegram "github.com/yogasw/wick/internal/agents/channels/telegram"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// withChannelsSlackBot runs bot botUserID as userID's Slack instance on the
// Channels page ("slack:<userID>") in a fresh registry.
func withChannelsSlackBot(t *testing.T, userID, botUserID string) *agentchannels.Registry {
	t.Helper()
	reg := agentchannels.NewRegistry()
	prev := globalChannels
	globalChannels = reg
	t.Cleanup(func() { globalChannels = prev })
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", userID, map[string]string{"bot_token": "xoxb-page"}); err != nil {
		t.Fatal(err)
	}
	ch := agentslack.NewWithOwnerCached(agentconfig.SlackChannelConfig{BotToken: "xoxb-page"}, userID, botUserID, "pagebot", "T")
	reg.AddKeyed(channelsSlackKey(userID), ch, stubSource{})
	return reg
}

// stubSource registers an instance under its key (HasKey reads the
// sources) without a hot-reload behind it.
type stubSource struct{}

func (stubSource) Hash() string                 { return "" }
func (stubSource) Reload(context.Context) error { return nil }

// Fail closed: an instance still in the registry holds its bot even when
// its row says off; once the switch has really stopped it, it does not.
func TestCheckBotUniqueFailsClosedUntilStopped(t *testing.T) {
	withAgentSlackWorld(t)
	reg := withChannelsSlackBot(t, "u9", "UB7")
	var taken errBotTaken
	if err := checkBotUnique("agent-x", "B7", "UB7"); !errorsAs(err, &taken) || taken.instance != "slack:u9" {
		t.Fatalf("running instance: err = %v (%+v)", err, taken)
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	if err := checkBotUnique("agent-x", "B7", "UB7"); !errorsAs(err, &taken) {
		t.Fatalf("switched off but still registered must still block: %v", err)
	}
	// The real switch path: the sync stops it and drops it from the registry.
	syncChannelInstance(context.Background(), "slack", "u9")
	if reg.HasKey("slack:u9") {
		t.Fatal("sync left a switched-off instance in the registry")
	}
	if err := checkBotUnique("agent-x", "B7", "UB7"); err != nil {
		t.Fatalf("stopped instance still blocks: %v", err)
	}
}

func errorsAs(err error, target *errBotTaken) bool {
	t, ok := err.(errBotTaken)
	if ok {
		*target = t
	}
	return ok
}

func claim(t *testing.T, u *entity.User, agentID, botID, botUserID string, takeOver bool) (bool, int, string) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPut, "/api/team/agents/"+agentID+"/slack", nil, map[string]string{"id": agentID})
	_, ok := claimSlackBot(c, agentID, botID, botUserID, "pagebot", takeOver, "")
	return ok, w.Code, w.Body.String()
}

// Without take_over the 409 is structured; a non-admin cannot take over;
// an owner can, which switches the Channels instance off and stops it.
func TestClaimSlackBotTakeOver(t *testing.T) {
	withAgentSlackWorld(t)
	reg := withChannelsSlackBot(t, "u9", "UB7")

	ok, code, body := claim(t, &entity.User{ID: "u1"}, "agent-x", "B7", "UB7", false)
	var impact botTakenByChannel
	_ = json.Unmarshal([]byte(body), &impact)
	if ok || code != http.StatusConflict || impact.Code != codeBotTakenByChannel || impact.InstanceKey != "slack:u9" ||
		impact.SessionPrefix != "slack-u9-" || impact.WorkflowRefs == nil {
		t.Fatalf("no take over: ok=%v %d %s", ok, code, body)
	}
	if impact.CanTakeOver || impact.InstanceName != "slack:u9" || len(impact.WorkflowRefs) != 0 {
		t.Fatalf("a member gets no take over and no instance details: %+v", impact)
	}

	ok, code, _ = claim(t, &entity.User{ID: "u1"}, "agent-x", "B7", "UB7", true)
	if ok || code != http.StatusForbidden || !reg.HasKey("slack:u9") {
		t.Fatalf("member take over: ok=%v code=%d running=%v", ok, code, reg.HasKey("slack:u9"))
	}

	ok, code, body = claim(t, &entity.User{ID: "u1", IsOwner: true}, "agent-x", "B7", "UB7", true)
	if !ok {
		t.Fatalf("owner take over: %d %s", code, body)
	}
	if reg.HasKey("slack:u9") {
		t.Fatal("the Channels instance still runs after take over")
	}
	m, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "u9")
	if m["enabled"] != "false" || m["bot_token"] == "" {
		t.Fatalf("instance row after take over = %v (switched off, tokens kept)", m)
	}
}

// A bot another agent holds is refused even for an owner asking to take over.
func TestClaimSlackBotOfAnotherAgentStillRefused(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	if err := agentchannels.SaveAgentSlack(globalDB, a.ID, "u1", map[string]string{agentSlackKeyBotID: "B1"}, true); err != nil {
		t.Fatal(err)
	}
	ok, code, body := claim(t, &entity.User{ID: "u1", IsOwner: true}, "agent-other", "B1", "UB1", true)
	if ok || code != http.StatusConflict || !strings.Contains(body, "@rekap") || strings.Contains(body, codeBotTakenByChannel) {
		t.Fatalf("agent-held bot: ok=%v %d %s", ok, code, body)
	}
}

// A blank secret keeps the stored value; only Clear (clear=1) drops it.
func TestSecretSaveKeeps(t *testing.T) {
	secret := channelSecretKeys("slack")
	if !secretSaveKeeps(secret, "bot_token", "", false) {
		t.Fatal("blank bot_token must keep the stored value")
	}
	if secretSaveKeeps(secret, "bot_token", "", true) {
		t.Fatal("clear=1 must drop the stored value")
	}
	if secretSaveKeeps(secret, "bot_token", "xoxb-new", false) || secretSaveKeeps(secret, "users_mode", "", false) {
		t.Fatal("a value, or a blank non-secret, is written as is")
	}
}

// Clear on the agent connect drops a stored secret instead of keeping it.
func TestAgentSlackConnectClearsSecret(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	u := &entity.User{ID: "u1"}
	if w := connectSlack(t, u, a.ID, map[string]any{"bot_token": "xoxb-x-B1", "app_token": "xapp-1", "signing_secret": "sig"}); w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	w := connectSlack(t, u, a.ID, map[string]any{"clear": []string{"signing_secret"}})
	var st AgentSlackStatus
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != http.StatusOK || st.Secrets["signing_secret"] || !st.Secrets["bot_token"] || !st.Secrets["app_token"] {
		t.Fatalf("clear: %d %+v", w.Code, st)
	}
}

func convertSlack(t *testing.T, u *entity.User, agentID string, confirm bool) (int, string) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+agentID+"/slack/convert", map[string]any{"confirm": confirm}, map[string]string{"id": agentID})
	c.R.Header.Set("Content-Type", "application/json")
	apiTeamAgentSlackConvert(c)
	return w.Code, w.Body.String()
}

// seedChannelsSlackSocket stores a Socket Mode bot on userID's Channels
// row, run as bot user UB7. The agent's bot "comes online" at once:
// waitAgentSlackOnline stands in for Slack's hello.
func seedChannelsSlackSocket(t *testing.T, userID string) *agentchannels.Registry {
	t.Helper()
	reg := withChannelsSlackBot(t, userID, "UB7")
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", userID, map[string]string{
		"bot_token": "xoxb-x-B7", "mode": "socket", "app_token": "xapp-7",
	}); err != nil {
		t.Fatal(err)
	}
	prev := waitAgentSlackOnline
	waitAgentSlackOnline = func(entity.AgentPersona, string, time.Time) error { return nil }
	t.Cleanup(func() { waitAgentSlackOnline = prev })
	return reg
}

// Convert to agent: the preview only reports the impact; confirming copies
// the stored tokens to the agent, switches the instance off and connects.
func TestConvertChannelsSlackToAgent(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	owner := &entity.User{ID: "u1", IsOwner: true}
	reg := seedChannelsSlackSocket(t, "") // the App Owner row

	code, body := convertSlack(t, owner, a.ID, false)
	var impact botTakenByChannel
	_ = json.Unmarshal([]byte(body), &impact)
	if code != http.StatusOK || impact.InstanceKey != "slack:__owner__" || !impact.CanTakeOver || impact.WorkflowRefs == nil ||
		!strings.Contains(impact.AccessNote, "Only me") || impact.ReplacesBot != "" {
		t.Fatalf("preview: %d %s", code, body)
	}
	if !reg.HasKey("slack:__owner__") {
		t.Fatal("the preview must not touch the instance")
	}
	if m, _ := agentchannels.AgentSlackConfig(globalDB, a.ID); m["bot_token"] != "" {
		t.Fatal("the preview must not connect the agent")
	}

	code, body = convertSlack(t, owner, a.ID, true)
	if code != http.StatusOK {
		t.Fatalf("convert: %d %s", code, body)
	}
	if reg.HasKey("slack:__owner__") {
		t.Fatal("the Channels instance still runs after convert")
	}
	m, _ := agentchannels.AgentSlackConfig(globalDB, a.ID)
	if m["bot_token"] != "xoxb-x-B7" || m["app_token"] != "xapp-7" || m["mode"] != "socket" || m[agentSlackKeyBotID] != "B7" {
		t.Fatalf("agent row after convert = %v", m)
	}
	row, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "")
	if row["enabled"] != "false" || row["bot_token"] == "" {
		t.Fatalf("instance row after convert = %v (switched off, tokens kept)", row)
	}
	if strings.Contains(body, "xoxb-x-B7") || strings.Contains(body, "xapp-7") {
		t.Fatalf("convert response leaks a secret: %s", body)
	}
}

// A member converting a running instance hits the take over gate (403);
// once the member switched it off, the preview offers Confirm and the
// convert goes through. Without a stored bot there is nothing to convert.
func TestConvertChannelsSlackGates(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p2", "u2")
	b := seedTeamAgent(t, "u2", "loki", "p2")
	member := &entity.User{ID: "u2"}
	if code, _ := convertSlack(t, member, b.ID, true); code != http.StatusNotFound {
		t.Fatalf("no stored bot: %d", code)
	}
	reg := seedChannelsSlackSocket(t, "u2")
	code, body := convertSlack(t, member, b.ID, false)
	if code != http.StatusOK || strings.Contains(body, `"can_take_over":true`) {
		t.Fatalf("member preview: %d %s", code, body)
	}
	if code, _ := convertSlack(t, member, b.ID, true); code != http.StatusForbidden || !reg.HasKey("slack:u2") {
		t.Fatalf("member convert: %d running=%v", code, reg.HasKey("slack:u2"))
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u2", map[string]string{"enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	syncChannelInstance(context.Background(), "slack", "u2")
	if code, body := convertSlack(t, member, b.ID, false); code != http.StatusOK || !strings.Contains(body, `"can_take_over":true`) {
		t.Fatalf("member preview, instance off: %d %s", code, body)
	}
	if code, body := convertSlack(t, member, b.ID, true); code != http.StatusOK {
		t.Fatalf("member convert, instance off: %d %s", code, body)
	}
	if m, _ := agentchannels.AgentSlackConfig(globalDB, b.ID); m[agentSlackKeyBotID] != "B7" {
		t.Fatalf("agent row after convert = %v", m)
	}
}

// A convert that fails after switching the instance off switches it back
// on: the bot is never left with nobody serving it.
func TestConvertRollsBackWhenSaveFails(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	reg := seedChannelsSlackSocket(t, "")
	prev := saveAgentSlack
	saveAgentSlack = func(*gorm.DB, string, string, map[string]string, bool) error { return errors.New("db down") }
	t.Cleanup(func() { saveAgentSlack = prev })

	code, body := convertSlack(t, &entity.User{ID: "u1", IsOwner: true}, a.ID, true)
	if code != http.StatusInternalServerError || !strings.Contains(body, "db down") {
		t.Fatalf("convert: %d %s", code, body)
	}
	row, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "")
	if row["enabled"] != "true" {
		t.Fatalf("instance row after a failed convert = %v, want switched back on", row)
	}
	if !reg.HasKey("slack:__owner__") {
		t.Fatal("the Channels instance was not started again")
	}
}

// Convert reads JSON only, so a cross-site form cannot trigger it.
func TestConvertRequiresJSON(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	w, c := teamReq(t, &entity.User{ID: "u1", IsOwner: true}, http.MethodPost, "/api/team/agents/"+a.ID+"/slack/convert", map[string]any{"confirm": true}, map[string]string{"id": a.ID})
	c.R.Header.Set("Content-Type", "text/plain")
	apiTeamAgentSlackConvert(c)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain convert: %d", w.Code)
	}
}

// Switching a Channels instance back on (or pasting a token into a live
// row) is refused while a Team agent holds the same bot.
func TestChannelsSlackEnableRefusedWhileAgentHoldsBot(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"bot_token": "xoxb-x-B7", "enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	if err := agentchannels.SaveAgentSlack(globalDB, a.ID, "u1", map[string]string{agentSlackKeyBotID: "B7"}, true); err != nil {
		t.Fatal(err)
	}
	if _, msg := slackEnableConflict("u9", "enabled", "true"); !strings.Contains(msg, "@rekap") {
		t.Fatalf("enable while the agent holds the bot: %q", msg)
	}
	if _, msg := slackEnableConflict("u9", "users_mode", "all"); msg != "" {
		t.Fatalf("other keys save freely: %q", msg)
	}
	// Pasting into a row that is off starts nothing.
	if _, msg := slackEnableConflict("u9", "bot_token", "xoxb-y-B7"); msg != "" {
		t.Fatalf("token into an off row: %q", msg)
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"enabled": "true"}); err != nil {
		t.Fatal(err)
	}
	if _, msg := slackEnableConflict("u9", "bot_token", "xoxb-y-B7"); !strings.Contains(msg, "@rekap") {
		t.Fatalf("token into a live row: %q", msg)
	}
	if err := agentchannels.DeleteAgentSlack(globalDB, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, msg := slackEnableConflict("u9", "enabled", "true"); msg != "" {
		t.Fatalf("after the agent disconnects: %q", msg)
	}
}

// The agent's bot not coming online rolls the take over back: the Channels
// instance runs again and the agent's connection is what it was before,
// its previous bot included.
func TestConvertRollsBackWhenAgentDoesNotComeOnline(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	if err := agentchannels.SaveAgentSlack(globalDB, a.ID, "u1", map[string]string{agentSlackKeyBotID: "B1", "bot_token": "xoxb-old-B1"}, true); err != nil {
		t.Fatal(err)
	}
	reg := seedChannelsSlackSocket(t, "")
	waitAgentSlackOnline = func(entity.AgentPersona, string, time.Time) error { return errors.New("socket refused") }

	code, body := convertSlack(t, &entity.User{ID: "u1", IsOwner: true}, a.ID, true)
	if code != http.StatusBadGateway || !strings.Contains(body, "socket refused") {
		t.Fatalf("convert: %d %s", code, body)
	}
	if m, _ := agentchannels.AgentSlackConfig(globalDB, a.ID); m[agentSlackKeyBotID] != "B1" || m["bot_token"] != "xoxb-old-B1" {
		t.Fatalf("agent row after rollback = %v, want the previous bot back", m)
	}
	row, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "")
	if row["enabled"] != "true" || !reg.HasKey("slack:__owner__") {
		t.Fatalf("instance after rollback: row=%v running=%v", row, reg.HasKey("slack:__owner__"))
	}
}

// A disabled agent is no target: hidden from the panel, blocked in the
// preview, refused on confirm with the instance left running.
func TestConvertRefusesDisabledAgent(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	a.Disabled = true
	if err := globalTeam.Update(context.Background(), &a); err != nil {
		t.Fatal(err)
	}
	reg := seedChannelsSlackSocket(t, "")
	owner := &entity.User{ID: "u1", IsOwner: true}
	for _, p := range convertTargetAgents(context.Background(), "u1") {
		if p.ID == a.ID {
			t.Fatal("a disabled agent is offered as a convert target")
		}
	}
	code, body := convertSlack(t, owner, a.ID, false)
	var impact botTakenByChannel
	_ = json.Unmarshal([]byte(body), &impact)
	if code != http.StatusOK || impact.CanTakeOver || !strings.Contains(impact.Blocker, "disabled") {
		t.Fatalf("preview: %d %s", code, body)
	}
	if code, body := convertSlack(t, owner, a.ID, true); code != http.StatusBadRequest || !reg.HasKey("slack:__owner__") {
		t.Fatalf("confirm: %d %s running=%v", code, body, reg.HasKey("slack:__owner__"))
	}
}

// Instant agents riding the instance are named in the impact; an HTTP-mode
// instance cannot be converted (an agent's bot runs over Socket Mode).
func TestConvertImpactInstantAgentsAndHTTPMode(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	rider := seedTeamAgent(t, "u1", "helper", "p1")
	if err := saveInstantRow(globalDB, rider, instantConfig{SharedChannel: "slack:__owner__"}); err != nil {
		t.Fatal(err)
	}
	seedChannelsSlackSocket(t, "")
	owner := &entity.User{ID: "u1", IsOwner: true}
	_, body := convertSlack(t, owner, a.ID, false)
	var impact botTakenByChannel
	_ = json.Unmarshal([]byte(body), &impact)
	if len(impact.InstantAgents) != 1 || impact.InstantAgents[0] != "@helper" {
		t.Fatalf("instant agents = %v (%s)", impact.InstantAgents, body)
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "", map[string]string{"mode": "http", "signing_secret": "sig"}); err != nil {
		t.Fatal(err)
	}
	if code, body := convertSlack(t, owner, a.ID, false); code != http.StatusBadRequest || !strings.Contains(body, "Socket Mode") {
		t.Fatalf("http mode: %d %s", code, body)
	}
}

// channelSave posts one Channels-page field the way the form does.
func channelSave(t *testing.T, u *entity.User, typ, key string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/channels/"+typ+"/"+key, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(login.WithUser(r.Context(), u, nil))
	r.SetPathValue("key", key)
	w := httptest.NewRecorder()
	saveChannelKey(tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil), typ, channelSecretKeys(typ))
	return w
}

// Handler level: switching an instance on while a Team agent holds its
// bot is 409 and starts nothing; Slack unreachable is 503, not a pass.
func TestChannelsSaveGuardsAgentHeldBot(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	reg := agentchannels.NewRegistry()
	prev := globalChannels
	globalChannels = reg
	t.Cleanup(func() { globalChannels = prev })
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"bot_token": "xoxb-x-B7", "enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	if err := agentchannels.SaveAgentSlack(globalDB, a.ID, "u1", map[string]string{agentSlackKeyBotID: "B7"}, true); err != nil {
		t.Fatal(err)
	}
	member := &entity.User{ID: "u9"}
	w := channelSave(t, member, "slack", "enabled", url.Values{"value": {"true"}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "@rekap") {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	if row, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "u9"); row["enabled"] != "false" || reg.HasKey("slack:u9") {
		t.Fatalf("refused enable changed state: row=%v running=%v", row, reg.HasKey("slack:u9"))
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"bot_token": "xoxb-down-B7"}); err != nil {
		t.Fatal(err)
	}
	if w := channelSave(t, member, "slack", "enabled", url.Values{"value": {"true"}}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("Slack down: %d %s", w.Code, w.Body.String())
	}
}

// Handler level: a blank secret keeps the stored one; clear=1 drops it and
// a cleared bot token stops the instance.
func TestChannelsSaveClearSecret(t *testing.T) {
	withAgentSlackWorld(t)
	reg := withChannelsSlackBot(t, "u9", "UB7")
	member := &entity.User{ID: "u9"}
	if w := channelSave(t, member, "slack", "bot_token", url.Values{"value": {""}}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "kept") {
		t.Fatalf("blank: %d %s", w.Code, w.Body.String())
	}
	if row, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "u9"); row["bot_token"] == "" || !reg.HasKey("slack:u9") {
		t.Fatalf("blank save dropped the token: %v", row)
	}
	if w := channelSave(t, member, "slack", "bot_token", url.Values{"value": {""}, "clear": {"1"}}); w.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
	if row, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "u9"); row["bot_token"] != "" || reg.HasKey("slack:u9") {
		t.Fatalf("after clear: row=%v running=%v", row, reg.HasKey("slack:u9"))
	}
}

// Handler level: the Telegram switch stops its instance too.
func TestChannelsSaveTelegramSwitchOff(t *testing.T) {
	withAgentSlackWorld(t)
	reg := agentchannels.NewRegistry()
	prev := globalChannels
	globalChannels = reg
	t.Cleanup(func() { globalChannels = prev })
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "telegram", "u9", map[string]string{"bot_token": "123:abc"}); err != nil {
		t.Fatal(err)
	}
	reg.AddKeyed("telegram:u9", agenttelegram.NewWithOwner(agentconfig.TelegramChannelConfig{BotToken: "123:abc"}, "u9"), stubSource{})
	if w := channelSave(t, &entity.User{ID: "u9"}, "telegram", "enabled", url.Values{"value": {"false"}}); w.Code != http.StatusOK {
		t.Fatalf("switch off: %d %s", w.Code, w.Body.String())
	}
	if reg.HasKey("telegram:u9") {
		t.Fatal("the Telegram instance still runs after switching off")
	}
	if owners, _ := agentchannels.ListChannelOwners(globalDB, "telegram"); len(owners) != 0 {
		t.Fatalf("a switched-off Telegram row would boot: %v", owners)
	}
}

// fakeSocketChannel is a registered bot whose Socket Mode state the test
// sets — what waitAgentSlackOnline reads.
type fakeSocketChannel struct {
	state string
	at    time.Time
}

func (f *fakeSocketChannel) Name() string                     { return "slack" }
func (f *fakeSocketChannel) Start(context.Context) error      { return nil }
func (f *fakeSocketChannel) Stop()                            {}
func (f *fakeSocketChannel) IsConfigured() bool               { return true }
func (f *fakeSocketChannel) SocketState() (string, time.Time) { return f.state, f.at }

// The real wait: connected after since is online; a "connected" left from
// an earlier run, or no bot at all, is not; HTTP mode only needs the bot
// registered.
func TestWaitAgentSlackOnline(t *testing.T) {
	reg := agentchannels.NewRegistry()
	prevReg, prevTimeout := globalChannels, agentSlackOnlineTimeout
	globalChannels, agentSlackOnlineTimeout = reg, 300*time.Millisecond
	t.Cleanup(func() { globalChannels, agentSlackOnlineTimeout = prevReg, prevTimeout })
	p := entity.AgentPersona{ID: "a1"}
	since := time.Now()

	if err := waitAgentSlackOnline(p, "socket", since); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("no bot: %v", err)
	}
	ch := &fakeSocketChannel{state: "connected", at: since.Add(-time.Minute)}
	reg.AddKeyed(agentSlackInstanceKey(p.ID), ch, stubSource{})
	if err := waitAgentSlackOnline(p, "socket", since); err == nil || !strings.Contains(err.Error(), "no Socket Mode connection") {
		t.Fatalf("stale connected must time out: %v", err)
	}
	ch.at = since.Add(time.Second)
	if err := waitAgentSlackOnline(p, "socket", since); err != nil {
		t.Fatalf("fresh connected: %v", err)
	}
	ch.state = "connecting"
	if err := waitAgentSlackOnline(p, "http", since); err != nil {
		t.Fatalf("http mode, registered: %v", err)
	}
}

// Taking a bot over through the wizard in HTTP mode is refused: HTTP
// events would never reach the agent's bot.
func TestAgentSlackTakeOverRefusesHTTPMode(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	w := connectSlack(t, &entity.User{ID: "u1", IsOwner: true}, a.ID, map[string]any{
		"mode": "http", "bot_token": "xoxb-x-B7", "signing_secret": "sig", "take_over": true,
	})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Socket Mode") {
		t.Fatalf("http take over: %d %s", w.Code, w.Body.String())
	}
	if m, _ := agentchannels.AgentSlackConfig(globalDB, a.ID); m["bot_token"] != "" {
		t.Fatalf("a refused take over stored the connection: %v", m)
	}
}

// The B2 guard tells a dead token (Slack rejected it: 422, paste a valid
// one) from Slack being unreachable (503, try again).
func TestChannelsSaveGuardDeadTokenVsOutage(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	if err := agentchannels.SaveAgentSlack(globalDB, a.ID, "u1", map[string]string{agentSlackKeyBotID: "B1"}, true); err != nil {
		t.Fatal(err)
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"bot_token": "xoxb-revoked-B7", "enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	status, msg := slackEnableConflict("u9", "enabled", "true")
	if status != http.StatusUnprocessableEntity || !strings.Contains(msg, "token_revoked") {
		t.Fatalf("revoked token: %d %q", status, msg)
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"bot_token": "xoxb-down-B7"}); err != nil {
		t.Fatal(err)
	}
	if status, msg := slackEnableConflict("u9", "enabled", "true"); status != http.StatusServiceUnavailable || !strings.Contains(msg, "try again") {
		t.Fatalf("Slack down: %d %q", status, msg)
	}
}

// A failed take over of a rotating app token carries the pair the agent
// already rotated to back to the Channels row — the copied refresh token
// is spent — and the 502 says what was restored.
func TestConvertRollbackCarriesRotatedAppToken(t *testing.T) {
	withAgentSlackWorld(t)
	seedTeamProject(t, "p1", "u1")
	a := seedTeamAgent(t, "u1", "rekap", "p1")
	seedChannelsSlackSocket(t, "")
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "", map[string]string{
		"app_token": "xoxe.xapp-1-old", "app_refresh_token": "xoxe-1-r1", "client_id": "cid", "client_secret": "csec",
	}); err != nil {
		t.Fatal(err)
	}
	waitAgentSlackOnline = func(p entity.AgentPersona, _ string, _ time.Time) error {
		// The agent's bot rotated the token, then never connected.
		// The test DB is SQLite shared-cache: a write from the bot just
		// started in the background can hold the table for a moment.
		var err error
		for range 50 {
			if err = persistAgentSlackAppToken(p.ID)("xoxe.xapp-1-new", "xoxe-1-r2", time.Now().Add(12*time.Hour)); err == nil || !strings.Contains(err.Error(), "locked") {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Error(err)
		}
		return errors.New("socket refused")
	}
	code, body := convertSlack(t, &entity.User{ID: "u1", IsOwner: true}, a.ID, true)
	if code != http.StatusBadGateway || !strings.Contains(body, "switched back on") || !strings.Contains(body, "carried back") {
		t.Fatalf("convert: %d %s", code, body)
	}
	row, _ := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", "")
	if row["app_token"] != "xoxe.xapp-1-new" || row["app_refresh_token"] != "xoxe-1-r2" || row["app_token_expires_at"] == "" {
		t.Fatalf("Channels row after rollback = %v, want the rotated pair", row)
	}
}

// slackSigned is a Slack events request signed with secret.
func slackSigned(secret, body string) *http.Request {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":" + body))
	r := httptest.NewRequest(http.MethodPost, "/integrations/slack/events", strings.NewReader(body))
	r.Header.Set("X-Slack-Request-Timestamp", ts)
	r.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return r
}

// End to end with the handler mounted once (as at boot): an HTTP-mode
// Channels instance switched off drops its events with a 200, and
// switched back on — a new instance object — answers again, no restart.
func TestChannelsSlackHTTPOffOnServesWithoutRestart(t *testing.T) {
	withAgentSlackWorld(t)
	reg := agentchannels.NewRegistry()
	prev := globalChannels
	globalChannels = reg
	t.Cleanup(func() { globalChannels = prev; reg.StopAll() })
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{
		"bot_token": "xoxb-x-B7", "mode": "http", "signing_secret": "sig9",
	}); err != nil {
		t.Fatal(err)
	}
	syncChannelInstance(context.Background(), "slack", "u9")
	h := reg.HTTPHandlers()["POST /integrations/slack/events"]
	if h == nil {
		t.Fatal("events route not mounted")
	}
	challenge := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, slackSigned("sig9", `{"type":"url_verification","challenge":"c-123"}`))
		return w
	}
	if w := challenge(); !strings.Contains(w.Body.String(), "c-123") {
		t.Fatalf("running instance: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	for _, on := range []string{"false", "true"} {
		if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u9", map[string]string{"enabled": on}); err != nil {
			t.Fatal(err)
		}
		syncChannelInstance(context.Background(), "slack", "u9")
		w := challenge()
		if on == "false" && (w.Code != http.StatusOK || w.Header().Get("X-Wick-Ignored") == "" || strings.Contains(w.Body.String(), "c-123")) {
			t.Fatalf("switched off: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
		if on == "true" && !strings.Contains(w.Body.String(), "c-123") {
			t.Fatalf("switched back on, not answering: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
}

// Two HTTP-mode instances with their own signing secrets behind the one
// events route: real Slack HMAC sends each event to its own bot (the body
// is restored for the instance that is not first), and once one is
// switched off its events are acknowledged and dropped while the other
// keeps answering.
func TestChannelsSlackHTTPTwoInstancesRouteBySignature(t *testing.T) {
	withAgentSlackWorld(t)
	reg := agentchannels.NewRegistry()
	prev := globalChannels
	globalChannels = reg
	t.Cleanup(func() { globalChannels = prev; reg.StopAll() })
	for uid, sec := range map[string]string{"u8": "sig8", "u9": "sig9"} {
		if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", uid, map[string]string{
			"bot_token": "xoxb-x-B" + uid, "mode": "http", "signing_secret": sec,
		}); err != nil {
			t.Fatal(err)
		}
		syncChannelInstance(context.Background(), "slack", uid)
	}
	h := reg.HTTPHandlers()["POST /integrations/slack/events"]
	challenge := func(secret, c string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, slackSigned(secret, `{"type":"url_verification","challenge":"`+c+`"}`))
		return w
	}
	for _, c := range []struct{ sec, ch string }{{"sig8", "c-8"}, {"sig9", "c-9"}} {
		if w := challenge(c.sec, c.ch); !strings.Contains(w.Body.String(), c.ch) {
			t.Fatalf("%s: %d %s", c.sec, w.Code, w.Body.String())
		}
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", "u8", map[string]string{"enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	syncChannelInstance(context.Background(), "slack", "u8")
	if w := challenge("sig8", "c-8"); w.Code != http.StatusOK || w.Header().Get("X-Wick-Ignored") == "" || strings.Contains(w.Body.String(), "c-8") {
		t.Fatalf("switched-off instance's event: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if w := challenge("sig9", "c-9"); !strings.Contains(w.Body.String(), "c-9") {
		t.Fatalf("the other instance stopped answering: %d %s", w.Code, w.Body.String())
	}
}

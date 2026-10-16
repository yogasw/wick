package channels

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&entity.AgentChannel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestEnsureChannelForUser(t *testing.T) {
	db := testDB(t)

	if err := EnsureChannelForUser(db, "slack", ""); err != nil {
		t.Fatalf("EnsureChannelForUser owner: %v", err)
	}
	if err := EnsureChannelForUser(db, "slack", "user-a"); err != nil {
		t.Fatalf("EnsureChannelForUser user-a: %v", err)
	}
	if err := EnsureChannelForUser(db, "slack", "user-a"); err != nil {
		t.Fatalf("EnsureChannelForUser idempotent: %v", err)
	}

	var count int64
	db.Model(&entity.AgentChannel{}).Where("type = ?", "slack").Count(&count)
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}
}

func TestSetAndGetChannelConfigKeyForUser(t *testing.T) {
	db := testDB(t)

	if err := SetChannelConfigKeyForUser(db, "slack", "user-a", "bot_token", "xoxb-test-123"); err != nil {
		t.Fatalf("set config: %v", err)
	}

	m, err := GetChannelConfigMapForUser(db, "slack", "user-a")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if m["bot_token"] != "xoxb-test-123" {
		t.Fatalf("expected bot_token=xoxb-test-123, got %q", m["bot_token"])
	}
}

func TestListChannelOwners(t *testing.T) {
	db := testDB(t)

	owners, err := ListChannelOwners(db, "slack")
	if err != nil {
		t.Fatalf("list owners: %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("expected 0, got %d", len(owners))
	}

	if err := SetChannelConfigKeyForUser(db, "slack", "user-a", "bot_token", "xoxb-abc"); err != nil {
		t.Fatalf("set: %v", err)
	}

	owners, err = ListChannelOwners(db, "slack")
	if err != nil {
		t.Fatalf("list owners 2: %v", err)
	}
	if len(owners) != 1 {
		t.Fatalf("expected 1 enabled owner, got %d", len(owners))
	}
	if owners[0] == nil || *owners[0] != "user-a" {
		t.Fatalf("expected user-a, got %v", owners[0])
	}
}

func TestOwnerRowIsolation(t *testing.T) {
	db := testDB(t)

	if err := SetChannelConfigKeyForUser(db, "slack", "", "bot_token", "xoxb-owner"); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	if err := SetChannelConfigKeyForUser(db, "slack", "user-a", "bot_token", "xoxb-user-a"); err != nil {
		t.Fatalf("set user-a: %v", err)
	}

	ownerMap, _ := GetChannelConfigMapForUser(db, "slack", "")
	userMap, _ := GetChannelConfigMapForUser(db, "slack", "user-a")

	if ownerMap["bot_token"] != "xoxb-owner" {
		t.Fatalf("owner isolation broken: got %q", ownerMap["bot_token"])
	}
	if userMap["bot_token"] != "xoxb-user-a" {
		t.Fatalf("user-a isolation broken: got %q", userMap["bot_token"])
	}
}

// A Slack connection created off must read back off; the enabled column
// defaults to true.
func TestSaveAgentSlackCreateKeepsDisabled(t *testing.T) {
	db := testDB(t)
	if err := SaveAgentSlack(db, "a1", "u1", map[string]string{"k": "v"}, false); err != nil {
		t.Fatal(err)
	}
	row, ok, err := AgentSlackRow(db, "a1")
	if err != nil || !ok || row.Enabled {
		t.Fatalf("row = %+v ok=%v err=%v, want disabled", row, ok, err)
	}
	if err := SaveAgentSlack(db, "a2", "u1", map[string]string{}, true); err != nil {
		t.Fatal(err)
	}
	if row, _, _ := AgentSlackRow(db, "a2"); !row.Enabled {
		t.Fatalf("enabled row = %+v", row)
	}
}

// An agent's Telegram row lives beside its Slack row, never mixed up.
func TestAgentTelegramRow(t *testing.T) {
	db := testDB(t)
	if err := SaveAgentSlack(db, "a1", "u1", map[string]string{"bot_token": "s"}, true); err != nil {
		t.Fatal(err)
	}
	if err := SaveAgentTelegram(db, "a1", "u1", map[string]string{"bot_token": "t", "project_id": "p1"}, true); err != nil {
		t.Fatal(err)
	}
	m, err := AgentTelegramConfig(db, "a1")
	if err != nil || m["bot_token"] != "t" {
		t.Fatalf("telegram config = %v %v", m, err)
	}
	cfg, err := DBStore{db: db}.LoadTelegramForAgent("a1")
	if err != nil || cfg.BotToken != "t" || cfg.ProjectID != "p1" {
		t.Fatalf("load = %+v %v", cfg, err)
	}
	if rows, _ := ListAgentTelegram(db); len(rows) != 1 || rows[0].Type != AgentTelegramType {
		t.Fatalf("list = %+v", rows)
	}
	if err := DeleteAgentTelegram(db, "a1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := AgentTelegramRow(db, "a1"); ok {
		t.Fatal("delete kept the telegram row")
	}
	if _, ok, _ := AgentSlackRow(db, "a1"); !ok {
		t.Fatal("telegram delete dropped the slack row")
	}
}

// The Enabled switch wins over the token: saving any key (the token
// included) after switching off keeps the row off, and only the switch
// turns it back on.
func TestChannelEnabledSwitchSurvivesSaves(t *testing.T) {
	db := testDB(t)
	enabled := func() bool {
		var ch entity.AgentChannel
		if err := db.Where("type = ? AND user_id = ?", "slack", "u1").First(&ch).Error; err != nil {
			t.Fatal(err)
		}
		return ch.Enabled
	}
	if err := SetChannelConfigKeyForUser(db, "slack", "u1", "bot_token", "xoxb-1"); err != nil {
		t.Fatal(err)
	}
	if !enabled() {
		t.Fatal("a row with a token and no switch must be on")
	}
	if err := SetChannelConfigKeyForUser(db, "slack", "u1", "enabled", "false"); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"bot_token", "xoxb-2"}, {"users_mode", "all"}, {"app_token", "xapp-1"}} {
		if err := SetChannelConfigKeyForUser(db, "slack", "u1", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
		if enabled() {
			t.Fatalf("saving %s switched the row back on", kv[0])
		}
	}
	if owners, _ := ListChannelOwners(db, "slack"); len(owners) != 0 {
		t.Fatalf("a switched-off row must not boot: %v", owners)
	}
	if err := SetChannelConfigKeyForUser(db, "slack", "u1", "enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if !enabled() {
		t.Fatal("switching on must turn the row on")
	}
	// Clearing the token stops it whatever the switch says.
	if err := SetChannelConfigKeyForUser(db, "slack", "u1", "bot_token", ""); err != nil {
		t.Fatal(err)
	}
	if enabled() {
		t.Fatal("a cleared token must leave the row off")
	}
}

func TestChannelRowEnabled(t *testing.T) {
	cases := []struct {
		m    map[string]string
		want bool
	}{
		{map[string]string{}, false},
		{map[string]string{"bot_token": "x"}, true},
		{map[string]string{"bot_token": "x", "enabled": "true"}, true},
		{map[string]string{"bot_token": "x", "enabled": "false"}, false},
		{map[string]string{"bot_token": "", "enabled": "true"}, false},
		{map[string]string{"enabled": "true"}, true}, // REST: no token key
		{map[string]string{"enabled": "false"}, false},
	}
	for _, c := range cases {
		if got := ChannelRowEnabled(c.m); got != c.want {
			t.Errorf("ChannelRowEnabled(%v) = %v, want %v", c.m, got, c.want)
		}
	}
}

package slack

import (
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func TestFormatAttachmentsEmpty(t *testing.T) {
	if got := formatAttachments(nil); got != "" {
		t.Errorf("nil files should yield empty string, got %q", got)
	}
	if got := formatAttachments([]slackgo.File{}); got != "" {
		t.Errorf("empty files should yield empty string, got %q", got)
	}
}

func TestFormatAttachmentsRendersMetadataAndLink(t *testing.T) {
	files := []slackgo.File{
		{Title: "order.png", PrettyType: "PNG", Size: 2048, Permalink: "https://acme.slack.com/files/U1/F1/order.png"},
	}
	got := formatAttachments(files)
	for _, want := range []string{
		"[Attached files",
		"order.png",
		"(PNG)",
		"2.0KB",
		"https://acme.slack.com/files/U1/F1/order.png",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatAttachmentsFallbacks(t *testing.T) {
	// No Title → Name; no Permalink → URLPrivate.
	files := []slackgo.File{{Name: "log.txt", URLPrivate: "https://files.slack.com/log.txt"}}
	got := formatAttachments(files)
	if !strings.Contains(got, "log.txt") {
		t.Errorf("name fallback missing: %s", got)
	}
	if !strings.Contains(got, "https://files.slack.com/log.txt") {
		t.Errorf("url_private fallback missing: %s", got)
	}
}

func TestFormatSenderLabel(t *testing.T) {
	tests := []struct {
		name, userID, handle, real, want string
	}{
		{"full", "U1", "yoga", "Yoga Setiawan", "Yoga Setiawan (@yoga, U1)"},
		{"handle only", "U1", "yoga", "", "@yoga (U1)"},
		{"real only", "U1", "", "Yoga Setiawan", "Yoga Setiawan (U1)"},
		{"id only", "U1", "", "", "U1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatSenderLabel(tc.userID, tc.handle, tc.real); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeUserText(t *testing.T) {
	if got := normalizeUserText("hello"); got != "hello" {
		t.Errorf("non-empty text changed: %q", got)
	}
	if got := normalizeUserText("   "); got != pingFallbackText {
		t.Errorf("blank text not normalized: %q", got)
	}
	if got := normalizeUserText(""); got != pingFallbackText {
		t.Errorf("empty text not normalized: %q", got)
	}
}

func TestChunkText(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		max    int
		chunks int
	}{
		{"short", "hello", 3800, 1},
		{"exact", strings.Repeat("a", 3800), 3800, 1},
		{"one over", strings.Repeat("a", 3801), 3800, 2},
		{"double", strings.Repeat("a", 7600), 3800, 2},
		{"empty", "", 3800, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := chunkText(tc.input, tc.max)
			if len(got) != tc.chunks {
				t.Errorf("got %d chunks, want %d", len(got), tc.chunks)
			}
			// Verify no data is lost.
			var joined string
			for _, c := range got {
				joined += c
			}
			if joined != tc.input {
				t.Errorf("chunks do not reassemble to original input")
			}
		})
	}
}

func TestChunkTextBreaksOnNewline(t *testing.T) {
	// Build a string that has a newline near the boundary so the chunker
	// should prefer to break there.
	near := strings.Repeat("a", 3750) + "\n" + strings.Repeat("b", 100)
	chunks := chunkText(near, 3800)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if strings.Contains(chunks[0], "b") {
		t.Error("first chunk should not contain 'b' content after newline break")
	}
}

func TestAllowed(t *testing.T) {
	s := &Channel{}
	// allowed wraps allowedCfg, discarding the deny-reason for the boolean
	// assertions below (reason is covered by TestAllowedReason).
	allowed := func(userID string, groups []string, channelID string) bool {
		ok, _ := s.allowedCfg(s.cfg, userID, groups, channelID)
		return ok
	}

	// default — all modes "all", any user / group / channel passes
	s.cfg.UsersMode = "all"
	s.cfg.GroupsMode = "all"
	s.cfg.ChannelsMode = "all"
	if !allowed("U123", nil, "C123") {
		t.Error("all mode: should allow any user")
	}

	// users whitelist
	s.cfg.UsersMode = "whitelist"
	s.cfg.AllowedUsers = `[{"id":"U001","name":"a"},{"id":"U002","name":"b"}]`
	if !allowed("U001", nil, "C1") {
		t.Error("users whitelist: U001 should be allowed")
	}
	if allowed("U999", nil, "C1") {
		t.Error("users whitelist: U999 should be denied")
	}

	// groups whitelist (users back to all)
	s.cfg.UsersMode = "all"
	s.cfg.GroupsMode = "whitelist"
	s.cfg.AllowedGroups = `[{"id":"G001","name":"g1"},{"id":"G002","name":"g2"}]`
	if !allowed("Uany", []string{"G001"}, "C1") {
		t.Error("groups whitelist: member of G001 should be allowed")
	}
	if allowed("Uany", []string{"G999"}, "C1") {
		t.Error("groups whitelist: member of G999 should be denied")
	}
	if allowed("Uany", nil, "C1") {
		t.Error("groups whitelist: no groups should be denied")
	}

	// users + groups whitelist (OR): pass via users
	s.cfg.UsersMode = "whitelist"
	s.cfg.AllowedUsers = `[{"id":"U001","name":"a"}]`
	s.cfg.GroupsMode = "whitelist"
	s.cfg.AllowedGroups = `[{"id":"G001","name":"g1"}]`
	if !allowed("U001", nil, "C1") {
		t.Error("OR semantic: U001 in users whitelist should pass even with no group")
	}
	// pass via groups
	if !allowed("U999", []string{"G001"}, "C1") {
		t.Error("OR semantic: member of G001 should pass even when not in users whitelist")
	}
	// neither matches
	if allowed("U999", []string{"G999"}, "C1") {
		t.Error("OR semantic: no match in users or groups should be denied")
	}

	// channels whitelist
	s.cfg.UsersMode = "all"
	s.cfg.GroupsMode = "all"
	s.cfg.ChannelsMode = "whitelist"
	s.cfg.AllowedChannels = `[{"id":"CABC","name":"#general"}]`
	if !allowed("U1", nil, "CABC") {
		t.Error("channels whitelist: CABC should be allowed")
	}
	if allowed("U1", nil, "CXYZ") {
		t.Error("channels whitelist: CXYZ should be denied")
	}
}

// TestAllowedReason verifies the deny-reason returned alongside the boolean,
// which drives the access-denied DM wording.
func TestAllowedReason(t *testing.T) {
	s := &Channel{}

	// identity deny: user not in the users whitelist.
	s.cfg.UsersMode = "whitelist"
	s.cfg.AllowedUsers = `[{"id":"U001","name":"a"}]`
	s.cfg.GroupsMode = "all"
	s.cfg.ChannelsMode = "all"
	if ok, reason := s.allowedCfg(s.cfg, "U999", nil, "C1"); ok || reason != "identity" {
		t.Errorf("identity deny: got ok=%v reason=%q, want false/identity", ok, reason)
	}

	// channels deny: identity passes, channel not whitelisted.
	s.cfg.UsersMode = "all"
	s.cfg.ChannelsMode = "whitelist"
	s.cfg.AllowedChannels = `[{"id":"CABC","name":"#general"}]`
	if ok, reason := s.allowedCfg(s.cfg, "U1", nil, "CXYZ"); ok || reason != "channels" {
		t.Errorf("channels deny: got ok=%v reason=%q, want false/channels", ok, reason)
	}

	// allowed: empty reason.
	s.cfg.ChannelsMode = "all"
	if ok, reason := s.allowedCfg(s.cfg, "U1", nil, "C1"); !ok || reason != "" {
		t.Errorf("allowed: got ok=%v reason=%q, want true/empty", ok, reason)
	}
}

func TestAutoReplySwitchState(t *testing.T) {
	s := &Channel{autoReply: map[string]bool{}}

	if s.autoReplyOn("slack:__owner__:111.222") {
		t.Fatal("fresh channel: switch should be OFF")
	}
	s.setAutoReply("slack:__owner__:111.222", true)
	if !s.autoReplyOn("slack:__owner__:111.222") {
		t.Error("after setAutoReply(true): switch should be ON")
	}
	// Another thread is independent.
	if s.autoReplyOn("slack:__owner__:333.444") {
		t.Error("unrelated thread should stay OFF")
	}
	s.setAutoReply("slack:__owner__:111.222", false)
	if s.autoReplyOn("slack:__owner__:111.222") {
		t.Error("after setAutoReply(false): switch should be OFF")
	}
	// Removing an absent key is a harmless no-op.
	s.setAutoReply("slack:__owner__:never", false)
}

func TestIsBotUser(t *testing.T) {
	s := &Channel{botUserID: "UBOT"}
	if !s.isBotUser("UBOT") {
		t.Error("the bot's own user id should be recognised")
	}
	if s.isBotUser("UHUMAN") {
		t.Error("a human user id is not the bot")
	}
	// Empty ids never match — guards against an unresolved bot id treating
	// every empty-author event as a self-reaction.
	if s.isBotUser("") {
		t.Error("empty user id should not match")
	}
	s.botUserID = ""
	if s.isBotUser("") {
		t.Error("empty bot id should not match empty user id")
	}
}

func TestMentionsBot(t *testing.T) {
	s := &Channel{botUserID: "UBOT"}
	if !s.mentionsBot("<@UBOT> hai") {
		t.Error("leading mention should be detected")
	}
	if !s.mentionsBot("hey <@UBOT> tolong cek ini") {
		t.Error("inline mention should be detected")
	}
	if s.mentionsBot("<@UHUMAN> bukan bot") {
		t.Error("a mention of someone else is not the bot")
	}
	if s.mentionsBot("no mention here") {
		t.Error("plain text has no mention")
	}
	// Unresolved bot id → fail open (false) so a real reply is never dropped
	// during the brief post-connect window.
	s.botUserID = ""
	if s.mentionsBot("<@UBOT> hai") {
		t.Error("empty bot id should report no mention (fail open)")
	}
}

// TestReactionAddedFastGuards covers the cheap branches of handleReactionAdded
// that short-circuit before any Slack API call: wrong emoji, feature
// disabled, the bot's own reaction, and a channel outside ReactionChannels.
// None of these should arm the switch. (The API-dependent guards — parent
// check, session-exists, access control — are exercised by integration tests
// where a live client is available.)
func TestReactionAddedFastGuards(t *testing.T) {
	react := func(s *Channel, emoji, user, channel string) {
		s.handleReactionAdded(nil, &slackevents.ReactionAddedEvent{
			Reaction: emoji,
			User:     user,
			Item:     slackevents.Item{Channel: channel, Timestamp: "111.222"},
		})
	}

	base := func() *Channel {
		s := &Channel{autoReply: map[string]bool{}, botUserID: "UBOT"}
		s.cfg.ReactionTriggerEnabled = true
		s.cfg.ReactionChannelsMode = "whitelist"
		s.cfg.ReactionChannels = `[{"id":"C1","name":"#ops"}]`
		return s
	}

	// wrong emoji
	s := base()
	react(s, "thumbsup", "U1", "C1")
	if len(s.autoReply) != 0 {
		t.Error("non-trigger emoji must not arm the switch")
	}

	// feature disabled
	s = base()
	s.cfg.ReactionTriggerEnabled = false
	react(s, reactionTrigger, "U1", "C1")
	if len(s.autoReply) != 0 {
		t.Error("disabled feature must not arm the switch")
	}

	// bot's own reaction
	s = base()
	react(s, reactionTrigger, "UBOT", "C1")
	if len(s.autoReply) != 0 {
		t.Error("the bot's own reaction must not arm the switch")
	}

	// channel not in ReactionChannels
	s = base()
	react(s, reactionTrigger, "U1", "C2")
	if len(s.autoReply) != 0 {
		t.Error("a channel outside ReactionChannels must not arm the switch")
	}
}

// TestReactionChannelAllowed covers the mode gate: "all" accepts any channel,
// "whitelist" (and any unknown/empty mode, which fails closed) accepts only
// the listed channels.
func TestReactionChannelAllowed(t *testing.T) {
	cfg := agentconfig.SlackChannelConfig{
		ReactionChannels: `[{"id":"C1","name":"#ops"}]`,
	}

	cfg.ReactionChannelsMode = "all"
	if !reactionChannelAllowed(cfg, "Cany") {
		t.Error("all mode: any channel should be allowed")
	}

	cfg.ReactionChannelsMode = "whitelist"
	if !reactionChannelAllowed(cfg, "C1") {
		t.Error("whitelist: listed channel C1 should be allowed")
	}
	if reactionChannelAllowed(cfg, "C2") {
		t.Error("whitelist: unlisted channel C2 should be denied")
	}

	// Empty/unknown mode fails closed (whitelist semantics).
	cfg.ReactionChannelsMode = ""
	if reactionChannelAllowed(cfg, "C2") {
		t.Error("empty mode should fail closed (deny unlisted)")
	}
	if !reactionChannelAllowed(cfg, "C1") {
		t.Error("empty mode should still allow a listed channel")
	}
}

// TestReactionRemovedGuards verifies the OFF path: wrong emoji and the bot's
// own reaction are ignored, and removing the trigger clears an armed switch.
func TestReactionRemovedGuards(t *testing.T) {
	s := &Channel{autoReply: map[string]bool{}, botUserID: "UBOT"}
	key := s.sessionKey("111.222") // no prefix set → bare ts
	s.setAutoReply(key, true)

	// wrong emoji leaves it armed
	s.handleReactionRemoved(&slackevents.ReactionRemovedEvent{
		Reaction: "thumbsup", User: "U1",
		Item: slackevents.Item{Channel: "C1", Timestamp: "111.222"},
	})
	if !s.autoReplyOn(key) {
		t.Error("non-trigger emoji removal must not disarm the switch")
	}

	// bot's own removal leaves it armed
	s.handleReactionRemoved(&slackevents.ReactionRemovedEvent{
		Reaction: reactionTrigger, User: "UBOT",
		Item: slackevents.Item{Channel: "C1", Timestamp: "111.222"},
	})
	if !s.autoReplyOn(key) {
		t.Error("the bot's own removal must not disarm the switch")
	}

	// a genuine trigger removal disarms it
	s.handleReactionRemoved(&slackevents.ReactionRemovedEvent{
		Reaction: reactionTrigger, User: "U1",
		Item: slackevents.Item{Channel: "C1", Timestamp: "111.222"},
	})
	if s.autoReplyOn(key) {
		t.Error("trigger removal by an allowed reactor must disarm the switch")
	}
}

func TestDashboardURL(t *testing.T) {
	s := &Channel{pubURL: "https://wick.example.com"}
	got := s.dashboardURL("1715167891.234567")
	want := "https://wick.example.com/tools/agents/sessions/1715167891.234567"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}

	// Empty pubURL
	s.pubURL = ""
	got = s.dashboardURL("T123")
	if !strings.Contains(got, "not configured") {
		t.Errorf("expected 'not configured' message, got %q", got)
	}
}

package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/credreq"
)

// Session secrets from Slack: a person hands values (a staging login, an API
// key) to a thread's agent session through a modal, never through the chat.
//
// Two ways in, both mapped by the bot's Shortcuts setting (key → action):
//   - a MESSAGE shortcut (⋯ on any message in the thread): Slack sends the
//     thread, so the modal goes straight to that thread's session;
//   - a SLASH command: Slack sends no thread, so the modal asks for the
//     thread's link.
//
// Nothing here is typed by the agent: there is no MCP tool to ask, so an
// agent cannot invent a request. The values are stored encrypted by credreq
// and the session is woken with tokens (secret rows) or the value itself
// (rows the person marked not secret).

// SessionSecrets stores the values. Wired by the server; nil = off.
var SessionSecrets interface {
	Save(ctx context.Context, in credreq.SaveInput) (credreq.View, error)
}

// ShortcutActionSessionSecret is the action that opens the session-secret
// modal.
const ShortcutActionSessionSecret = "session_secret"

const (
	secretCallback   = "session_secret_save"
	secretRows       = 5
	secretThreadBlk  = "ss_thread"
	secretTitleBlk   = "ss_title"
	secretTTLBlk     = "ss_ttl"
	secretKeyBlkPfx  = "ss_k"
	secretValBlkPfx  = "ss_v"
	secretPlainBlk   = "ss_plain"
	secretPlainValue = "plain"
)

// shortcutRow is one row of SlackChannelConfig.Shortcuts.
type shortcutRow struct {
	Key    string `json:"key"`
	Action string `json:"action"`
}

// shortcutAction maps a message-shortcut callback_id or a slash command to
// its action. An empty setting keeps the default key session_secret.
func shortcutAction(cfg agentconfig.SlackChannelConfig, key string) string {
	key = strings.TrimPrefix(strings.TrimSpace(key), "/")
	if key == "" {
		return ""
	}
	var rows []shortcutRow
	if strings.TrimSpace(cfg.Shortcuts) != "" {
		_ = json.Unmarshal([]byte(cfg.Shortcuts), &rows)
	} else {
		rows = []shortcutRow{{Key: ShortcutActionSessionSecret, Action: ShortcutActionSessionSecret}}
	}
	for _, r := range rows {
		if strings.TrimPrefix(strings.TrimSpace(r.Key), "/") == key {
			return strings.TrimSpace(r.Action)
		}
	}
	return ""
}

// handleSecretShortcut opens the modal for a message shortcut or slash
// command mapped to session_secret. Returns false when the key is not ours,
// so the caller's workflow handling continues untouched.
func (s *Channel) handleSecretShortcut(ctx context.Context, key, triggerID, userID, channelID, threadTS string) bool {
	if shortcutAction(s.snapshot(), key) != ShortcutActionSessionSecret {
		return false
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return true
	}
	var view map[string]any
	if threadTS != "" {
		sessionID := s.sessionKey(threadTS)
		if !s.sessionOnDisk(sessionID) {
			s.accessEphemeral(channelID, threadTS, userID, "No agent session in this thread yet. Mention the agent here first, then save the secrets.")
			return true
		}
		view = secretModal(sessionID+"|"+channelID+"|"+threadTS, false)
	} else {
		view = secretModal("|"+channelID+"|", true)
	}
	req, err := toModalRequest(view)
	if err != nil {
		return true
	}
	if _, err := api.OpenViewContext(ctx, triggerID, req); err != nil {
		log.Warn().Str("channel", "slack").Err(err).Msg("session secret: open modal failed")
	}
	return true
}

// secretModal is the form: an optional thread link (slash command), a title,
// up to five key/value rows, which rows are NOT secret, and how long the
// values may sit unused.
func secretModal(meta string, askThread bool) map[string]any {
	blocks := []map[string]any{
		contextBlock("Values go to this thread's agent session, encrypted. The agent gets a token for each secret value, never the value; a script reads it when it runs. Nothing is posted in the thread."),
	}
	if askThread {
		blocks = append(blocks, inputBlock(secretThreadBlk, "Thread link", "text", plainInput("text", "Paste the link of the thread (Copy link on any reply)", false), false))
	}
	blocks = append(blocks, inputBlock(secretTitleBlk, "What is it for", "text", plainInput("text", "e.g. OC staging login", false), true))
	for i := 1; i <= secretRows; i++ {
		opt := i > 1
		blocks = append(blocks,
			inputBlock(fmt.Sprintf("%s%d", secretKeyBlkPfx, i), fmt.Sprintf("Key %d", i), "text", plainInput("text", "e.g. password", false), opt),
			inputBlock(fmt.Sprintf("%s%d", secretValBlkPfx, i), fmt.Sprintf("Value %d", i), "text", plainInput("text", "", false), opt),
		)
	}
	plainOpts := make([]map[string]any, 0, secretRows)
	for i := 1; i <= secretRows; i++ {
		plainOpts = append(plainOpts, map[string]any{"text": plain(fmt.Sprintf("Value %d", i)), "value": fmt.Sprintf("%s%d", secretPlainValue, i)})
	}
	blocks = append(blocks, inputBlock(secretPlainBlk, "Not secret (the agent may read these values)", "rows",
		map[string]any{"type": "checkboxes", "action_id": "rows", "options": plainOpts}, true))
	ttlOpts := make([]map[string]any, 0, len(credreq.TTLs))
	var initial map[string]any
	for _, t := range credreq.TTLs {
		o := map[string]any{"text": plain(credreq.TTLLabel(t)), "value": t}
		if t == credreq.TTL24h {
			initial = o
		}
		ttlOpts = append(ttlOpts, o)
	}
	blocks = append(blocks, inputBlock(secretTTLBlk, "Keep them", "ttl",
		map[string]any{"type": "static_select", "action_id": "ttl", "options": ttlOpts, "initial_option": initial}, false))
	return modalBase("Session secret", secretCallback, meta, blocks, "Save")
}

func inputBlock(blockID, label, _ string, element map[string]any, optional bool) map[string]any {
	return map[string]any{"type": "input", "block_id": blockID, "label": plain(label), "element": element, "optional": optional}
}

func plainInput(actionID, placeholder string, multiline bool) map[string]any {
	el := map[string]any{"type": "plain_text_input", "action_id": actionID, "multiline": multiline}
	if placeholder != "" {
		el["placeholder"] = plain(placeholder)
	}
	return el
}

// stateChecked lists the selected values of a checkboxes element.
func stateChecked(values map[string]any, blockID, actionID string) map[string]bool {
	out := map[string]bool{}
	opts, _ := stateField(values, blockID, actionID)["selected_options"].([]any)
	for _, o := range opts {
		if m, ok := o.(map[string]any); ok {
			if v, ok := m["value"].(string); ok {
				out[v] = true
			}
		}
	}
	return out
}

// handleSecretSubmit saves a submitted modal and tells the person, only them,
// what happened. The agent hears about it through the session wake-up.
func (s *Channel) handleSecretSubmit(ctx context.Context, cb slackgo.InteractionCallback) {
	parts := strings.SplitN(cb.View.PrivateMetadata, "|", 3)
	if len(parts) != 3 {
		return
	}
	sessionID, chatID, threadTS := parts[0], parts[1], parts[2]
	values := viewStateValues(cb.View)
	if sessionID == "" {
		c, t := linkedThread(stateText(values, secretThreadBlk, "text"))
		if c == "" || t == "" {
			s.accessEphemeral(chatID, "", cb.User.ID, "That is not a Slack thread link, so nothing was saved. Use Copy link on a reply in the thread.")
			return
		}
		chatID, threadTS = c, t
		sessionID = s.sessionKey(threadTS)
		if !s.sessionOnDisk(sessionID) {
			s.accessEphemeral(chatID, "", cb.User.ID, "That thread has no agent session of this bot, so nothing was saved.")
			return
		}
	}
	if SessionSecrets == nil {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Session secrets are not available on this wick.")
		return
	}
	viewer := s.accessViewerID(ctx, cb.User.ID)
	if viewer == "" {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Your Slack account is not linked to an approved wick user, so nothing was saved.")
		return
	}
	cfg := s.snapshot()
	groups, _ := s.resolveUserGroups(cb.User.ID)
	if ok, _ := s.allowedCfg(cfg, cb.User.ID, groups, chatID); !ok {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "You may not talk to this agent here, so nothing was saved.")
		return
	}
	notSecret := stateChecked(values, secretPlainBlk, "rows")
	entries := make([]credreq.Entry, 0, secretRows)
	for i := 1; i <= secretRows; i++ {
		entries = append(entries, credreq.Entry{
			Key:    stateText(values, fmt.Sprintf("%s%d", secretKeyBlkPfx, i), "text"),
			Value:  stateText(values, fmt.Sprintf("%s%d", secretValBlkPfx, i), "text"),
			Secret: !notSecret[fmt.Sprintf("%s%d", secretPlainValue, i)],
		})
	}
	name := cb.User.Name
	if name == "" {
		name = cb.User.ID
	}
	v, err := SessionSecrets.Save(ctx, credreq.SaveInput{
		SessionID: sessionID, UserID: viewer, UserName: name,
		Title: stateText(values, secretTitleBlk, "text"), Entries: entries,
		TTL: stateSelected(values, secretTTLBlk, "ttl"),
	})
	if err != nil {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Nothing was saved: "+err.Error())
		return
	}
	keys := make([]string, 0, len(v.Fields))
	for _, f := range v.Fields {
		keys = append(keys, "`"+f.Key+"`")
	}
	s.accessEphemeral(chatID, threadTS, cb.User.ID, fmt.Sprintf("🔐 Saved %s for the agent (%s). Only you see this message.", strings.Join(keys, ", "), credreq.TTLLabel(v.TTL)))
}

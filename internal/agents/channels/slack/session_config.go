package slack

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
)

// Session connector setup from Slack. When an agent in a Slack thread needs
// a person to fill a session connector's config (wick_session_workspace
// configure, or add with prompt:true), the web modal cannot reach them, so
// a "Fill in" card goes into the thread instead. The button opens a Slack
// modal with the connector's fields; the values are saved through the same
// validation and encryption as the web form and the session is woken.

// ConfigField is one field of a session connector's fill form.
type ConfigField struct {
	Key         string
	Help        string
	Type        string // text | secret | dropdown
	Value       string
	Placeholder string
	Options     []string
}

// Hooks wired by the server. nil leaves the card unavailable (the agent
// falls back to the web modal / set_config).
var (
	SessionConfigForm func(sessionID, instID string, keys []string) (label string, fields []ConfigField, err error)
	SessionConfigSave func(sessionID, instID string, values map[string]string) ([]string, error)
	WakeSession       func(ctx context.Context, sessionID, text string) error
)

const (
	cfgCardBlockID  = "sw_cfg"
	cfgFillAction   = "sw_fill"
	cfgCancelAction = "sw_cancel"
	cfgSaveCallback = "sw_config_save"
	cfgFieldBlkPfx  = "sw_f:"
)

// CfgCardTTL is how long a Fill in card stays usable. Past it the card
// closes itself, so a request nobody answered cannot be filled hours later
// by whoever scrolls by. A var so tests can shorten it.
var CfgCardTTL = 30 * time.Minute

// PostSessionConfigCard posts the Fill in card in the session's thread.
// handled=false when this channel does not own the session.
func (s *Channel) PostSessionConfigCard(ctx context.Context, sessionID, instID, label, reason string, keys []string) (bool, error) {
	if SessionConfigForm == nil || SessionConfigSave == nil {
		return false, nil
	}
	chatID, threadTS, ok := s.accessBinding(sessionID)
	if !ok {
		return false, nil
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return true, fmt.Errorf("slack bot is not connected")
	}
	text := fmt.Sprintf("🔧 *%s* needs a few values before the agent can use it.", label)
	if reason != "" {
		text += "\n_Agent:_ " + reason
	}
	expires := time.Now().Add(CfgCardTTL)
	value := sessionID + "|" + instID + "|" + strings.Join(keys, ",") + "|" + strconv.FormatInt(expires.Unix(), 10)
	blocks, err := blocksFromJSON([]map[string]any{
		section(text),
		{"type": "actions", "block_id": cfgCardBlockID, "elements": []any{
			map[string]any{"type": "button", "action_id": cfgFillAction, "value": clipText(value, 2000), "style": "primary", "text": plain("Fill in")},
			map[string]any{"type": "button", "action_id": cfgCancelAction, "value": clipText(value, 2000), "text": plain("Cancel")},
		}},
		contextBlock(fmt.Sprintf("Values go straight to the session, encrypted. Nothing is posted here. This card closes in %d minutes if nobody fills it.", int(CfgCardTTL/time.Minute))),
	})
	if err != nil {
		return true, err
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionText("🔧 "+label+" needs values", false), slackgo.MsgOptionBlocks(blocks...)}
	if threadTS != "" {
		opts = append(opts, slackgo.MsgOptionTS(threadTS))
	}
	opts = append(opts, s.personaOpts(threadTS)...)
	_, ts, err := api.PostMessageContext(ctx, chatID, opts...)
	if err != nil {
		return true, fmt.Errorf("post fill card: %w", err)
	}
	s.cfgMu.Lock()
	if s.cfgCards == nil {
		s.cfgCards = map[string]cfgCardRef{}
	}
	s.cfgCards[sessionID+"|"+instID] = cfgCardRef{chatID: chatID, ts: ts, label: label}
	s.cfgMu.Unlock()
	time.AfterFunc(CfgCardTTL, func() { s.expireConfigCard(sessionID, instID, ts) })
	return true, nil
}

// expireConfigCard closes a card nobody filled in time. The ts check keeps
// an old timer from closing a newer card posted for the same instance.
func (s *Channel) expireConfigCard(sessionID, instID, ts string) {
	s.cfgMu.Lock()
	ref, ok := s.cfgCards[sessionID+"|"+instID]
	s.cfgMu.Unlock()
	if !ok || ref.ts != ts {
		return
	}
	s.closeConfigCard(context.Background(), sessionID, instID, ref.chatID, ref.ts, ref.label,
		"⌛ *%s* was not filled within "+strconv.Itoa(int(CfgCardTTL/time.Minute))+" minutes. The card is closed.",
		fmt.Sprintf("[session connector] nobody filled %s within %d minutes and its Fill in card expired. Do not wait for it: ask again (action=configure) only if you still need it.", instID, int(CfgCardTTL/time.Minute)))
}

// cardExpired reads the expiry stamp a card carries in its button value
// (4th field). Cards posted before the stamp existed never expire here.
func cardExpired(parts []string) bool {
	if len(parts) < 4 {
		return false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	return err == nil && time.Now().Unix() > exp
}

// cfgCardRef locates a posted Fill in card so it can be closed once the
// values are saved, whichever form saved them.
type cfgCardRef struct {
	chatID, ts, label string
}

// SessionConfigFilled closes the Fill in card this channel posted for the
// instance and wakes the session. It is the web form's door: a person who
// fills the values from the session's Config tab instead of the card must
// not leave the card open and the agent asleep. Reports false when this
// channel posted no card for it (a plain Config-tab edit wakes nobody).
func (s *Channel) SessionConfigFilled(ctx context.Context, sessionID, instID, who string, keys []string) bool {
	s.cfgMu.Lock()
	ref, ok := s.cfgCards[sessionID+"|"+instID]
	s.cfgMu.Unlock()
	if !ok {
		return false
	}
	s.finishConfigCard(ctx, sessionID, instID, ref.chatID, ref.ts, ref.label, who, keys)
	return true
}

// finishConfigCard closes the card as filled and wakes the session with
// what was filled.
func (s *Channel) finishConfigCard(ctx context.Context, sessionID, instID, chatID, ts, label, who string, keys []string) {
	s.closeConfigCard(ctx, sessionID, instID, chatID, ts, label,
		"✅ *%s* filled by "+who+" ("+strings.Join(keys, ", ")+"). The agent continues.",
		fmt.Sprintf("[session connector] %s filled %s on %s. Secret values are stored encrypted in its config; run the connector (action=test first if unsure) instead of asking for them.", who, strings.Join(keys, ", "), instID))
}

// closeConfigCard swaps the card's buttons for one line (doneFmt gets the
// label), forgets the card and wakes the session with wakeText.
func (s *Channel) closeConfigCard(ctx context.Context, sessionID, instID, chatID, ts, label, doneFmt, wakeText string) {
	s.cfgMu.Lock()
	api := s.api
	if ref, ok := s.cfgCards[sessionID+"|"+instID]; ok {
		if label == "" {
			label = ref.label
		}
		if ts == "" {
			chatID, ts = ref.chatID, ref.ts
		}
		delete(s.cfgCards, sessionID+"|"+instID)
	}
	if ts != "" {
		if s.cfgClosed == nil {
			s.cfgClosed = map[string]bool{}
		}
		s.cfgClosed[ts] = true
	}
	s.cfgMu.Unlock()
	if label == "" {
		label = instID
	}
	if api != nil && chatID != "" && ts != "" {
		text := fmt.Sprintf(doneFmt, label)
		if blocks, err := blocksFromJSON([]map[string]any{section(text)}); err == nil {
			if _, _, _, err := api.UpdateMessageContext(ctx, chatID, ts, slackgo.MsgOptionText(text, false), slackgo.MsgOptionBlocks(blocks...)); err != nil {
				log.Warn().Str("channel", "slack").Str("session", sessionID).Err(err).Msg("session config: close fill card failed")
			}
		}
	}
	if WakeSession == nil {
		log.Warn().Str("channel", "slack").Str("session", sessionID).Msg("session config: no waker wired, session not woken")
		return
	}
	if err := WakeSession(context.WithoutCancel(ctx), sessionID, wakeText); err != nil {
		log.Warn().Str("channel", "slack").Str("session", sessionID).Err(err).Msg("session config: wake failed")
	}
}

// cardLabel recovers the connector label for a card this process no longer
// remembers (posted before a restart): the first bold run of its text.
func cardLabel(cb slackgo.InteractionCallback) string {
	t := cb.Message.Text
	if i := strings.Index(t, "🔧 "); i >= 0 {
		t = strings.TrimSuffix(strings.TrimSpace(t[i+len("🔧 "):]), " needs values")
		return t
	}
	return ""
}

// mayActOnCard runs the same who-may-answer checks as the modal save.
func (s *Channel) mayActOnCard(ctx context.Context, cb slackgo.InteractionCallback, chatID, threadTS string) bool {
	if s.accessViewerID(ctx, cb.User.ID) == "" {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Your Slack account is not linked to an approved wick user.")
		return false
	}
	groups, _ := s.resolveUserGroups(cb.User.ID)
	if ok, _ := s.allowedCfg(s.snapshot(), cb.User.ID, groups, chatID); !ok {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "You may not talk to this agent here.")
		return false
	}
	return true
}

func cardTS(cb slackgo.InteractionCallback) string {
	if cb.Container.MessageTs != "" {
		return cb.Container.MessageTs
	}
	return cb.Message.Timestamp
}

func slackName(cb slackgo.InteractionCallback) string {
	if cb.User.Name != "" {
		return cb.User.Name
	}
	return "<@" + cb.User.ID + ">"
}

// expireClicked answers a click on a card past its expiry (its timer was
// lost to a restart): close it as expired and tell the clicker.
func (s *Channel) expireClicked(ctx context.Context, cb slackgo.InteractionCallback, sessionID, instID string) {
	s.accessEphemeral(cb.Channel.ID, threadOf(cb), cb.User.ID, "This Fill in card expired. Ask the agent for a new one.")
	s.closeConfigCard(ctx, sessionID, instID, cb.Channel.ID, cardTS(cb), cardLabel(cb),
		"⌛ *%s* expired before anyone filled it. The card is closed.",
		fmt.Sprintf("[session connector] the Fill in card for %s expired unfilled. Do not wait for it: ask again (action=configure) only if you still need it.", instID))
}

// handleConfigCancel closes the card without saving anything and tells the
// agent to stop waiting.
func (s *Channel) handleConfigCancel(ctx context.Context, cb slackgo.InteractionCallback, action *slackgo.BlockAction) {
	parts := strings.Split(action.Value, "|")
	if len(parts) < 2 {
		return
	}
	sessionID, instID := parts[0], parts[1]
	if !s.mayActOnCard(ctx, cb, cb.Channel.ID, threadOf(cb)) {
		return
	}
	who := slackName(cb)
	s.closeConfigCard(ctx, sessionID, instID, cb.Channel.ID, cardTS(cb), cardLabel(cb),
		"✖️ *%s* cancelled by "+who+". Nothing was saved.",
		fmt.Sprintf("[session connector] %s cancelled the Fill in card for %s; nothing was saved. Do not wait for it and do not post the card again unless they ask.", who, instID))
}

// handleConfigFill opens the fill modal for the card's button.
func (s *Channel) handleConfigFill(ctx context.Context, cb slackgo.InteractionCallback, action *slackgo.BlockAction) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || SessionConfigForm == nil {
		return
	}
	parts := strings.Split(action.Value, "|")
	if len(parts) < 2 {
		return
	}
	sessionID, instID := parts[0], parts[1]
	if cardExpired(parts) {
		s.expireClicked(ctx, cb, sessionID, instID)
		return
	}
	s.cfgMu.Lock()
	closed := s.cfgClosed[cardTS(cb)]
	s.cfgMu.Unlock()
	if closed {
		s.accessEphemeral(cb.Channel.ID, threadOf(cb), cb.User.ID, "This Fill in card is already closed.")
		return
	}
	var keys []string
	if len(parts) >= 3 && parts[2] != "" {
		keys = strings.Split(parts[2], ",")
	}
	label, fields, err := SessionConfigForm(sessionID, instID, keys)
	if err != nil {
		s.accessEphemeral(cb.Channel.ID, threadOf(cb), cb.User.ID, "Cannot open the form: "+err.Error())
		return
	}
	exp := ""
	if len(parts) >= 4 {
		exp = parts[3]
	}
	meta := sessionID + "|" + instID + "|" + cb.Channel.ID + "|" + threadOf(cb) + "|" + cardTS(cb) + "|" + exp
	req, err := toModalRequest(configModal(label, meta, fields))
	if err != nil {
		return
	}
	if _, err := api.OpenViewContext(ctx, cb.TriggerID, req); err != nil {
		log.Warn().Str("channel", "slack").Err(err).Msg("session config: open modal failed")
	}
}

// configModal is one input per field; secret fields start empty (empty =
// keep the current value), the rest show the current value.
func configModal(label, meta string, fields []ConfigField) map[string]any {
	blocks := []map[string]any{contextBlock("*" + label + "*: empty fields keep their current value. Secret values are encrypted and never shown to the agent.")}
	for _, f := range fields {
		var el map[string]any
		switch f.Type {
		case "dropdown":
			opts := make([]map[string]any, 0, len(f.Options))
			var initial map[string]any
			for _, o := range f.Options {
				opt := map[string]any{"text": plain(o), "value": o}
				if o == f.Value {
					initial = opt
				}
				opts = append(opts, opt)
			}
			el = map[string]any{"type": "static_select", "action_id": "v", "options": opts}
			if initial != nil {
				el["initial_option"] = initial
			}
		default:
			el = plainInput("v", f.Placeholder, false)
			if f.Type != "secret" && f.Value != "" {
				el["initial_value"] = f.Value
			}
		}
		b := inputBlock(cfgFieldBlkPfx+f.Key, f.Key, "", el, true)
		if f.Help != "" {
			b["hint"] = plain(clipText(f.Help, 150))
		}
		blocks = append(blocks, b)
		if len(blocks) >= 95 {
			break
		}
	}
	return modalBase("Connector setup", cfgSaveCallback, meta, blocks, "Save")
}

// handleConfigSubmit saves the modal and wakes the session.
func (s *Channel) handleConfigSubmit(ctx context.Context, cb slackgo.InteractionCallback) {
	parts := strings.Split(cb.View.PrivateMetadata, "|")
	if len(parts) < 4 || SessionConfigSave == nil {
		return
	}
	sessionID, instID, chatID, threadTS := parts[0], parts[1], parts[2], parts[3]
	msgTS := ""
	if len(parts) >= 5 {
		msgTS = parts[4]
	}
	if len(parts) >= 6 && cardExpired([]string{"", "", "", parts[5]}) {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "This Fill in card expired before you saved, so nothing was saved. Ask the agent for a new one.")
		return
	}
	s.cfgMu.Lock()
	closed := msgTS != "" && s.cfgClosed[msgTS]
	s.cfgMu.Unlock()
	if closed {
		// Cancelled, expired or filled by someone else while this modal
		// was open.
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "This Fill in card was closed before you saved, so nothing was saved.")
		return
	}
	if !s.mayActOnCard(ctx, cb, chatID, threadTS) {
		return
	}
	values := viewStateValues(cb.View)
	got := map[string]string{}
	for blockID := range values {
		key, ok := strings.CutPrefix(blockID, cfgFieldBlkPfx)
		if !ok {
			continue
		}
		v := stateText(values, blockID, "v")
		if v == "" {
			v = stateSelected(values, blockID, "v")
		}
		if v != "" {
			got[key] = v
		}
	}
	keys, err := SessionConfigSave(sessionID, instID, got)
	if err != nil {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Nothing was saved: "+err.Error())
		return
	}
	if len(keys) == 0 {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Nothing changed (every field was empty).")
		return
	}
	s.accessEphemeral(chatID, threadTS, cb.User.ID, "🔧 Saved "+strings.Join(keys, ", ")+". Only you see this message.")
	s.finishConfigCard(ctx, sessionID, instID, chatID, msgTS, "", slackName(cb), keys)
}

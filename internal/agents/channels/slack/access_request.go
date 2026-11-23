package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/accessreq"
)

// Access request cards: the Slack drawing of an accessreq.View. Nothing
// here decides access. A click names the request, the clicker is mapped
// to their wick user, and the access-request service computes what they
// may do; this file only draws the answer.

// AccessRequestService is what the Slack side needs from accessreq.
type AccessRequestService interface {
	ViewFor(ctx context.Context, id, viewerID string) (accessreq.View, error)
	Decide(ctx context.Context, id, viewerID string, ds []accessreq.Decision) (accessreq.View, []accessreq.DecideResult, error)
	DenyAll(ctx context.Context, id, viewerID, note string) (accessreq.View, []accessreq.DecideResult, error)
}

// AccessRequests is the access-request service, wired by the server. nil
// leaves the cards' buttons answering that approvals are unavailable.
var AccessRequests AccessRequestService

const (
	accessBlockID        = "access_req"
	accessReviewAction   = "access_review"
	accessDenyAction     = "access_deny"
	accessApproveAll     = "access_approve_all"
	accessOpenWebAction  = "access_open_web"
	accessDecideCallback = "access_req_decide"
	accessDenyCallback   = "access_req_deny"
	accessSavingCallback = "access_req_saving"
	accessNoteBlock      = "access_note"
	accessItemBlockPfx   = "access_item:"
	maxSectionText       = 2900
)

// accessBinding is where the session's card goes: its Slack thread, DMs
// included (unlike listen mode, a DM is a fine place to ask).
func (s *Channel) accessBinding(sessionID string) (chatID, threadTS string, ok bool) {
	if s.sessions == nil || !s.OwnsSession(sessionID) {
		return "", "", false
	}
	b, found := s.sessions.ThreadBinding(sessionID)
	if !found || b.Channel != "slack" || b.ChatID == "" {
		return "", "", false
	}
	return b.ChatID, b.ThreadID, true
}

// PublishAccessRequest posts the card for v in the session's thread, or
// updates the one already posted (origin slack_ts).
func (s *Channel) PublishAccessRequest(ctx context.Context, v accessreq.View) (map[string]string, error) {
	chatID, threadTS, ok := s.accessBinding(v.SessionID)
	if !ok {
		return nil, nil
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return nil, fmt.Errorf("slack bot is not connected")
	}
	blocks, err := blocksFromJSON(accessCardBlocks(v))
	if err != nil {
		return nil, err
	}
	fallback := accessCardText(v)
	if ts := v.Origin["slack_ts"]; ts != "" && v.Origin["slack_channel"] != "" {
		uerr := s.withBackoffErr(func() error {
			_, _, _, err := api.UpdateMessageContext(ctx, v.Origin["slack_channel"], ts,
				slackgo.MsgOptionText(fallback, false), slackgo.MsgOptionBlocks(blocks...))
			return err
		})
		return nil, uerr
	}
	// Post as the thread's agent (its name and avatar), like its replies;
	// without chat:write.customize fall back to the bot. Resolved from the
	// session's own thread, also when the card goes to the main thread.
	extra := s.personaOpts(threadTS)
	if mainChat, mainThread, ok := s.mainThreadOf(ctx, api, chatID, threadTS); ok {
		chatID, threadTS = mainChat, mainThread
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionText(fallback, false), slackgo.MsgOptionBlocks(blocks...)}
	if threadTS != "" {
		opts = append(opts, slackgo.MsgOptionTS(threadTS))
	}
	_, ts, err := api.PostMessageContext(ctx, chatID, append(opts, extra...)...)
	if err != nil && len(extra) > 0 && isCustomizeDenied(err) {
		s.customizeDenied.Store(true)
		_, ts, err = api.PostMessageContext(ctx, chatID, opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("post access request card: %w", err)
	}
	return map[string]string{"slack_channel": chatID, "slack_ts": ts, "slack_thread": threadTS}, nil
}

// threadLinkRe finds a Slack message link: channel, message ts and the
// thread_ts query a reply's link carries.
var threadLinkRe = regexp.MustCompile(`https://[a-zA-Z0-9.-]*slack\.com/archives/([CG][A-Z0-9]+)/p(\d{10})(\d{6})(?:\?[^|>\s"]*?thread_ts=(\d+\.\d+))?`)

// linkedThread reads the thread a message links to ("" when none).
func linkedThread(text string) (chatID, threadTS string) {
	m := threadLinkRe.FindStringSubmatch(text)
	if m == nil {
		return "", ""
	}
	if m[4] != "" {
		return m[1], m[4]
	}
	return m[1], m[2] + "." + m[3]
}

// mainThreadOf is where a session's access card goes when its thread is a
// mirror: a thread this bot itself opened with a link to another thread
// (a first-check card on an inquiry). The approvers watch the inquiry
// thread, so the card goes there and the case moves on sooner. A thread a
// person opened stays where it is, link or not. Looked up once per
// thread.
func (s *Channel) mainThreadOf(ctx context.Context, api *slackgo.Client, chatID, threadTS string) (string, string, bool) {
	if chatID == "" || threadTS == "" {
		return "", "", false
	}
	key := chatID + "/" + threadTS
	if v, ok := s.mainThreads.Load(key); ok {
		t := v.([2]string)
		return t[0], t[1], t[0] != ""
	}
	msgs, _, _, err := api.GetConversationRepliesContext(ctx, &slackgo.GetConversationRepliesParameters{
		ChannelID: chatID, Timestamp: threadTS, Limit: 1, Inclusive: true, Latest: threadTS,
	})
	if err != nil {
		log.Debug().Str("channel", "slack").Err(err).Msg("access request: thread starter not readable; card stays in the session thread")
		return "", "", false // not remembered: the next card tries again
	}
	var found [2]string
	if len(msgs) > 0 {
		found = s.mirrorTarget(msgs[0].Msg, chatID, threadTS)
	}
	s.mainThreads.Store(key, found)
	return found[0], found[1], found[0] != ""
}

// mirrorTarget is the thread a mirror's starter links to: the starter is
// this bot's own message and links to a different thread.
func (s *Channel) mirrorTarget(m slackgo.Msg, chatID, threadTS string) [2]string {
	s.cfgMu.Lock()
	botID := s.botUserID
	s.cfgMu.Unlock()
	if botID == "" || m.User != botID || m.Timestamp != threadTS {
		return [2]string{}
	}
	text := m.Text
	if raw, err := json.Marshal(m.Blocks); err == nil {
		text += " " + string(raw)
	}
	c, t := linkedThread(text)
	if c == "" || (c == chatID && t == threadTS) {
		return [2]string{}
	}
	return [2]string{c, t}
}

func blocksFromJSON(raw []map[string]any) ([]slackgo.Block, error) {
	b, err := json.Marshal(map[string]any{"blocks": raw})
	if err != nil {
		return nil, err
	}
	var msg struct {
		Blocks slackgo.Blocks `json:"blocks"`
	}
	if err := json.Unmarshal(b, &msg); err != nil {
		return nil, err
	}
	return msg.Blocks.BlockSet, nil
}

func mrkdwn(text string) map[string]any {
	return map[string]any{"type": "mrkdwn", "text": clipText(text, maxSectionText)}
}

func plain(text string) map[string]any {
	return map[string]any{"type": "plain_text", "text": clipText(text, 150), "emoji": true}
}

func section(text string) map[string]any {
	return map[string]any{"type": "section", "text": mrkdwn(text)}
}

func contextBlock(text string) map[string]any {
	return map[string]any{"type": "context", "elements": []any{mrkdwn(text)}}
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// accessStatusIcon is where an action ended up. It deliberately avoids
// ✅/❌: those already mean a tool call passed or failed elsewhere in the
// thread, and an approver read them as "approved"/"declined". The words
// in itemStatusLine carry the decision; the icon carries the outcome.
func accessStatusIcon(status string) string {
	switch status {
	case accessreq.ItemOK:
		return "🟢"
	case accessreq.ItemError:
		return "🔴"
	case accessreq.ItemDenied:
		return "🚫"
	case accessreq.ItemRunning:
		return "🔄"
	}
	return "⏳"
}

func agentLabel(v accessreq.View) string {
	if v.AgentName != "" {
		return v.AgentName
	}
	return "The agent"
}

func accessCardText(v accessreq.View) string {
	return fmt.Sprintf("🔐 %s asks to run %d action(s) — %d waiting for a decision", agentLabel(v), len(v.Items), v.Pending)
}

func approverList(ps []accessreq.Person) string {
	if len(ps) == 0 {
		return "nobody yet"
	}
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

func itemStatusLine(it accessreq.ItemView) string {
	switch it.Status {
	case accessreq.ItemPending:
		who := approverList(it.Approvers)
		if it.ApproversText != "" {
			who = it.ApproversText
		}
		line := "_Waiting · who can approve: " + escapeMrkdwn(who) + "_"
		if it.Note != "" {
			line += "\nℹ️ _" + escapeMrkdwn(clipText(it.Note, 300)) + "_"
		}
		return line
	case accessreq.ItemRunning:
		return approvedBy(it) + " · Run: _running…_"
	case accessreq.ItemOK:
		return approvedBy(it) + " · Run: *succeeded*"
	case accessreq.ItemError:
		return approvedBy(it) + " · Run: *failed* `" + escapeMrkdwn(clipText(it.Error, 300)) + "`"
	case accessreq.ItemDenied:
		line := "Decision: *Denied* by " + escapeMrkdwn(it.DecidedBy) + " · not run"
		if it.Note != "" {
			line += ": _" + escapeMrkdwn(clipText(it.Note, 300)) + "_"
		}
		return line
	}
	return it.Status
}

// approvedBy is the decision half of a decided action's line, kept apart
// from how the run went.
func approvedBy(it accessreq.ItemView) string {
	return fmt.Sprintf("Decision: *Approved* %s by %s", strings.ToLower(accessreq.ModeLabel(it.Mode)), escapeMrkdwn(it.DecidedBy))
}

func itemHeading(it accessreq.ItemView) string {
	label := it.ConnectorLabel
	if label == "" {
		label = it.ConnectorKey
	}
	return fmt.Sprintf("*%d. `%s.%s`* · %s (%s)", it.Idx+1, it.ConnectorKey, it.OpKey, escapeMrkdwn(label), it.Kind)
}

// accessCardBlocks is the short card in the thread: one line per action
// with its status, and the buttons while anything waits.
func accessCardBlocks(v accessreq.View) []map[string]any {
	title := fmt.Sprintf("🔐 *%s* asks to run %d action(s)", escapeMrkdwn(agentLabel(v)), len(v.Items))
	if v.Status == accessreq.StatusCancelled {
		title = fmt.Sprintf("🔐 ~%s asked to run %d action(s)~ · *cancelled*", escapeMrkdwn(agentLabel(v)), len(v.Items))
	}
	blocks := []map[string]any{section(title)}
	if v.Reason != "" {
		blocks = append(blocks, section(whyLine(v.Reason)))
	}
	waiting := v.Status == accessreq.StatusPending && v.Pending > 0
	for _, it := range v.Items {
		text := accessStatusIcon(it.Status) + " " + itemHeading(it)
		if it.OpName != "" && it.OpName != it.OpKey {
			text += "\n_" + escapeMrkdwn(it.OpName) + "_"
		}
		if it.Target != "" {
			text += "\n" + escapeMrkdwn(it.Target)
		}
		if it.Status == accessreq.ItemPending && v.Status != accessreq.StatusPending {
			// A request that ended (cancelled, expired) leaves its undecided
			// items pending: say so instead of a "who can approve" line,
			// which no rule labels any more and would list people by name.
			text += "\n_Not decided · request " + escapeMrkdwn(v.Status) + "_"
		} else {
			text += "\n" + itemStatusLine(it)
		}
		blocks = append(blocks, section(text))
		// While it waits, the payload itself: approvers check it here
		// without opening the web page.
		if it.Status == accessreq.ItemPending && waiting {
			blocks = append(blocks, section("```"+escapeMrkdwn(payloadPreview(it, cardPayloadChars(len(v.Items))))+"```"))
		}
	}
	if waiting {
		approve := "Approve once"
		confirm := "Run this action once, now, with your own access?"
		if v.Pending > 1 {
			approve = fmt.Sprintf("Approve all (%d)", v.Pending)
			confirm = fmt.Sprintf("Run all %d waiting actions once, now, with your own access? Actions you may not approve stay waiting.", v.Pending)
		}
		elems := []any{
			map[string]any{"type": "button", "action_id": accessApproveAll, "value": v.ID, "style": "primary", "text": plain(approve),
				"confirm": map[string]any{
					"title":   plain("Approve"),
					"text":    map[string]any{"type": "plain_text", "text": confirm},
					"confirm": plain("Approve"),
					"deny":    plain("Cancel"),
				}},
			map[string]any{"type": "button", "action_id": accessReviewAction, "value": v.ID, "text": plain("Review & decide")},
			map[string]any{"type": "button", "action_id": accessDenyAction, "value": v.ID, "style": "danger", "text": plain("Deny")},
		}
		blocks = append(blocks, map[string]any{"type": "actions", "block_id": accessBlockID, "elements": elems})
	}
	footer := fmt.Sprintf("Request `%s` · approved actions run with the approver's own access", shortID(v.ID))
	if !waiting {
		// Decided or ended: the "approved actions run…" hint read as if
		// something was approved on a card that was denied.
		footer = fmt.Sprintf("Request `%s` · closed", shortID(v.ID))
	}
	blocks = append(blocks, contextBlock(footer))
	return blocks
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// whyLine is the agent's explanation as the card and the modal show it.
func whyLine(reason string) string {
	return "*Why:* " + escapeMrkdwn(clipText(reason, 500)) + " _(agent's words, unverified)_"
}

// cardPayloadChars is how much of each action's payload the thread card
// shows: room for one action's draft, less each when there are many, so
// the card stays readable and within Slack's limits.
func cardPayloadChars(items int) int {
	switch {
	case items <= 1:
		return 1800
	case items <= 3:
		return 900
	}
	return 400
}

// modalPayloadChars fits one Slack section (3000) with room to spare.
const modalPayloadChars = 2700

// payloadPreview is an action's parameters as readable JSON (masked by
// the service): a JSON document passed as a string (a bot draft, a
// config) is unfolded so it reads as structure, not as one escaped line.
// Cut at max with a note saying where the rest is.
func payloadPreview(it accessreq.ItemView, max int) string {
	var params map[string]any
	if it.ParamsJSON != "" {
		_ = json.Unmarshal([]byte(it.ParamsJSON), &params)
	}
	if params == nil && len(it.Params) > 0 {
		params = map[string]any{}
		for _, p := range it.Params {
			params[p.Name] = p.Value
		}
	}
	if len(params) == 0 {
		return "(no parameters)"
	}
	for k, v := range params {
		params[k] = unfoldJSON(v)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(params); err != nil {
		return "(parameters not readable)"
	}
	text := strings.ReplaceAll(strings.TrimRight(buf.String(), "\n"), "```", "'''")
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max]) + fmt.Sprintf("\n… cut: %d more characters, the full payload is on the web page", len(r)-max)
}

// unfoldJSON parses a string holding a JSON object or array, all the way
// down; anything else is returned as it is.
func unfoldJSON(v any) any {
	switch x := v.(type) {
	case string:
		t := strings.TrimSpace(x)
		if len(t) > 1 && (t[0] == '{' || t[0] == '[') {
			var inner any
			if json.Unmarshal([]byte(t), &inner) == nil {
				return unfoldJSON(inner)
			}
		}
		return x
	case map[string]any:
		for k, vv := range x {
			x[k] = unfoldJSON(vv)
		}
		return x
	case []any:
		for i, vv := range x {
			x[i] = unfoldJSON(vv)
		}
		return x
	}
	return v
}

func modalBase(title, callback, meta string, blocks []map[string]any, submit string) map[string]any {
	m := map[string]any{
		"type":             "modal",
		"callback_id":      callback,
		"private_metadata": meta,
		"title":            plain(clipText(title, 24)),
		"close":            plain("Close"),
		"blocks":           blocks,
	}
	if submit != "" {
		m["submit"] = plain(submit)
		m["close"] = plain("Cancel")
	}
	return m
}

func toModalRequest(m map[string]any) (slackgo.ModalViewRequest, error) {
	var v slackgo.ModalViewRequest
	b, err := json.Marshal(m)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(b, &v)
}

func loadingModal() map[string]any {
	return modalBase("Access request", "access_req_loading", "", []map[string]any{section("Loading the request…")}, "")
}

// reviewModal lists every action with its real parameters and, for the
// ones the viewer may decide, a mode picker. Rows they cannot decide stay
// visible with the reason and who can.
func reviewModal(v accessreq.View, meta string) map[string]any {
	blocks := []map[string]any{
		section(fmt.Sprintf("*%s* asks to run %d action(s)", escapeMrkdwn(agentLabel(v)), len(v.Items))),
	}
	if v.AskedBy != nil {
		who := v.AskedBy.Name
		if who == "" {
			who = v.AskedBy.ID
		}
		blocks = append(blocks, contextBlock("Asked while answering "+escapeMrkdwn(who)))
	}
	if v.Requester != "" {
		blocks = append(blocks, contextBlock("Asked in a conversation of "+escapeMrkdwn(v.Requester)))
	}
	if v.Reason != "" {
		blocks = append(blocks, section(whyLine(v.Reason)))
	}
	if !v.ViewerCanDecide {
		blocks = append(blocks, section("⛔ *"+escapeMrkdwn(v.ViewerReason)+"*"))
	}
	for _, it := range v.Items {
		blocks = append(blocks, map[string]any{"type": "divider"})
		text := accessStatusIcon(it.Status) + " " + itemHeading(it)
		if it.Target != "" {
			text += "\n*Target:* " + escapeMrkdwn(it.Target)
		}
		if it.CanDecide {
			text += "\n*Runs as:* your own account"
		}
		blocks = append(blocks, section(text))
		blocks = append(blocks, section("```"+escapeMrkdwn(payloadPreview(it, modalPayloadChars))+"```"))
		if it.Note != "" && it.Status == accessreq.ItemPending {
			blocks = append(blocks, contextBlock("ℹ️ "+escapeMrkdwn(it.Note)))
		}
		if it.CanDecide {
			var opts []any
			for _, m := range append(append([]string{}, it.Modes...), accessreq.ModeDeny) {
				opts = append(opts, map[string]any{"text": plain(accessreq.ModeLabel(m)), "value": m})
			}
			hint := ""
			switch it.Kind {
			case accessreq.KindSend:
				hint = ""
			case accessreq.KindDelete:
				hint = "Deletes: only \"Once\"."
			}
			input := map[string]any{
				"type":     "input",
				"block_id": accessItemBlockPfx + it.ID,
				"optional": true,
				"label":    plain(fmt.Sprintf("Decision for action %d", it.Idx+1)),
				"element":  map[string]any{"type": "radio_buttons", "action_id": "mode", "options": opts},
			}
			if hint != "" {
				input["hint"] = plain(hint)
			}
			blocks = append(blocks, input)
			continue
		}
		if it.Status == accessreq.ItemPending {
			blocks = append(blocks, contextBlock("⛔ "+escapeMrkdwn(it.LockedReason)))
		} else {
			blocks = append(blocks, contextBlock(itemStatusLine(it)))
		}
	}
	submit := ""
	if v.ViewerCanDecide {
		blocks = append(blocks, map[string]any{"type": "divider"})
		blocks = append(blocks, map[string]any{
			"type": "input", "block_id": accessNoteBlock, "optional": true,
			"label":   plain("Note for the agent (sent with a deny)"),
			"element": map[string]any{"type": "plain_text_input", "action_id": "text", "multiline": true},
		})
		blocks = append(blocks, contextBlock("Leave an action unselected to keep it waiting. Approved actions run right away with your access. \"This session\" covers the same operation on the same target only; standing access is set with rules."))
		submit = "Save decisions"
	}
	title := "Access request"
	if !v.ViewerCanDecide && v.ViewerReasonCode != accessreq.ReasonDecided {
		title = "No access"
	}
	return modalBase(title, accessDecideCallback, meta, capBlocks(blocks), submit)
}

// denyModal confirms denying every action the viewer may decide.
func denyModal(v accessreq.View, meta string) map[string]any {
	if !v.ViewerCanDecide {
		return reviewModal(v, meta)
	}
	blocks := []map[string]any{section(fmt.Sprintf("Deny these actions *%s* asked for?", escapeMrkdwn(agentLabel(v))))}
	others := 0
	for _, it := range v.Items {
		if !it.CanDecide {
			if it.Status == accessreq.ItemPending {
				others++
			}
			continue
		}
		line := "• " + itemHeading(it)
		if it.Target != "" {
			line += "\n   " + escapeMrkdwn(it.Target)
		}
		blocks = append(blocks, section(line))
	}
	if others > 0 {
		blocks = append(blocks, contextBlock(fmt.Sprintf("%d action(s) outside your access stay waiting for another approver.", others)))
	}
	blocks = append(blocks, map[string]any{
		"type": "input", "block_id": accessNoteBlock, "optional": true,
		"label":   plain("Reason (sent to the agent)"),
		"element": map[string]any{"type": "plain_text_input", "action_id": "text", "multiline": true},
	})
	return modalBase("Deny actions", accessDenyCallback, meta, blocks, "Deny")
}

func capBlocks(b []map[string]any) []map[string]any {
	if len(b) <= 99 {
		return b
	}
	out := append([]map[string]any{}, b[:98]...)
	return append(out, contextBlock("Too many details for Slack: open the request on the web to see the rest."))
}

// accessViewerID maps a Slack user to the wick user approving as them:
// the linked channel identity first, then a connected Slack account, then
// the Slack profile email. Bots never approve.
func (s *Channel) accessViewerID(ctx context.Context, slackUserID string) string {
	if s.isBotUser(slackUserID) {
		return ""
	}
	s.cfgMu.Lock()
	users := s.users
	byOAuth := s.wickUserIDFn
	api := s.api
	instanceKey := s.sessionPrefix
	s.cfgMu.Unlock()
	if users != nil {
		if id, ok := users.FindByChannelIdentity(ctx, "slack", instanceKey, slackUserID); ok && id != "" {
			return id
		}
	}
	if byOAuth != nil {
		if id, ok := byOAuth(ctx, slackUserID); ok && id != "" {
			return id
		}
	}
	if users != nil && api != nil {
		if u, err := api.GetUserInfoContext(ctx, slackUserID); err == nil && !u.IsBot && u.Profile.Email != "" {
			if id, ok := users.FindByEmail(ctx, u.Profile.Email); ok {
				return id
			}
		}
	}
	return ""
}

// handleAccessClick opens the review or deny modal for a card button.
// The modal opens at once with a loading view (a trigger id dies after 3
// seconds) and is then replaced with the viewer's own picture.
func (s *Channel) handleAccessClick(ctx context.Context, cb slackgo.InteractionCallback, action *slackgo.BlockAction) {
	if action.ActionID == accessOpenWebAction {
		return // a URL button: Slack opens the page itself
	}
	if action.ActionID == accessApproveAll {
		s.handleAccessApproveAll(ctx, cb, action.Value)
		return
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}
	loading, err := toModalRequest(loadingModal())
	if err != nil {
		return
	}
	resp, err := api.OpenViewContext(ctx, cb.TriggerID, loading)
	if err != nil {
		log.Warn().Str("channel", "slack").Err(err).Msg("access request: open modal failed")
		return
	}
	reqID := action.Value
	meta := reqID + "|" + cb.Channel.ID + "|" + threadOf(cb)
	var view map[string]any
	if AccessRequests == nil {
		view = modalBase("Access request", "access_req_off", meta, []map[string]any{section("Approvals are not available on this wick.")}, "")
	} else {
		viewer := s.accessViewerID(ctx, cb.User.ID)
		v, verr := AccessRequests.ViewFor(ctx, reqID, viewer)
		switch {
		case verr != nil:
			view = modalBase("Access request", "access_req_gone", meta, []map[string]any{section("This request could not be found: " + verr.Error())}, "")
		case viewer == "":
			v.ViewerCanDecide = false
			v.ViewerReason = "Your Slack account is not linked to an approved wick user, so you cannot approve this. Sign in to wick once from Slack, or ask an admin to link your account."
			v.ViewerReasonCode = accessreq.ReasonNotLinked
			for i := range v.Items {
				v.Items[i].CanDecide = false
				if v.Items[i].LockedReason == "" {
					who := approverList(v.Items[i].Approvers)
					if v.Items[i].ApproversText != "" {
						who = v.Items[i].ApproversText
					}
					v.Items[i].LockedReason = "Not linked to wick. Who can: " + who + "."
				}
			}
			view = reviewModal(v, meta)
		case action.ActionID == accessDenyAction:
			view = denyModal(v, meta)
		default:
			view = reviewModal(v, meta)
		}
	}
	if name, icon := s.accessSender(ctx, api, threadOf(cb)); name != "" {
		view = withSenderHeader(view, name, icon)
	}
	req, err := toModalRequest(view)
	if err != nil {
		log.Warn().Str("channel", "slack").Err(err).Msg("access request: build modal failed")
		return
	}
	if _, err := api.UpdateViewContext(ctx, req, "", resp.View.Hash, resp.View.ID); err != nil {
		log.Warn().Str("channel", "slack").Err(err).Msg("access request: update modal failed")
	}
}

// handleAccessApproveAll approves, once, every waiting action the clicker
// may decide (Slack already asked them to confirm). What they may not
// decide stays waiting; they are told why, only them.
func (s *Channel) handleAccessApproveAll(ctx context.Context, cb slackgo.InteractionCallback, reqID string) {
	chatID, threadTS := cb.Channel.ID, threadOf(cb)
	if AccessRequests == nil {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Approvals are not available on this wick.")
		return
	}
	viewer := s.accessViewerID(ctx, cb.User.ID)
	if viewer == "" {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Your Slack account is not linked to an approved wick user, so nothing was approved.")
		return
	}
	v, err := AccessRequests.ViewFor(ctx, reqID, viewer)
	if err != nil {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "This request could not be found: "+err.Error())
		return
	}
	ds, locked := approveAllDecisions(v)
	if len(ds) == 0 {
		msg := "Nothing was approved."
		if len(locked) > 0 {
			msg += "\n• " + strings.Join(locked, "\n• ")
		} else if v.ViewerReason != "" {
			msg += " " + v.ViewerReason
		}
		s.accessEphemeral(chatID, threadTS, cb.User.ID, msg)
		return
	}
	_, results, err := AccessRequests.Decide(ctx, reqID, viewer, ds)
	if err != nil {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Could not save your approval: "+err.Error())
		return
	}
	for _, r := range results {
		if !r.OK {
			locked = append(locked, r.Error)
		}
	}
	if len(locked) > 0 {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Some actions were not approved:\n• "+strings.Join(locked, "\n• "))
	}
}

// approveAllDecisions is "once" for every waiting action the viewer may
// decide, plus why each waiting one they may not decide stays.
func approveAllDecisions(v accessreq.View) (ds []accessreq.Decision, locked []string) {
	for _, it := range v.Items {
		if it.Status != accessreq.ItemPending {
			continue
		}
		if !it.CanDecide {
			if it.LockedReason != "" {
				locked = append(locked, fmt.Sprintf("Action %d: %s", it.Idx+1, it.LockedReason))
			}
			continue
		}
		ds = append(ds, accessreq.Decision{ItemID: it.ID, Mode: accessreq.ModeOnce})
	}
	return ds, locked
}

func threadOf(cb slackgo.InteractionCallback) string {
	if cb.Message.ThreadTimestamp != "" {
		return cb.Message.ThreadTimestamp
	}
	return cb.Message.Timestamp
}

// accessPickMsg is the error shown on a review modal submitted without
// any decision picked (a note alone decides nothing).
const accessPickMsg = "Pick a decision for at least one action. A note is only sent together with a Deny."

// accessSubmitAck is the socket ack for an interaction: nil (empty ack)
// for everything but an access-request modal submit. A review submitted
// with nothing picked keeps the modal open with an error under each
// decision; otherwise the modal turns into a "saving" view at once and
// handleAccessSubmit replaces it with the result.
func accessSubmitAck(cb slackgo.InteractionCallback) any {
	if cb.Type != slackgo.InteractionTypeViewSubmission ||
		(cb.View.CallbackID != accessDecideCallback && cb.View.CallbackID != accessDenyCallback) {
		return nil
	}
	if cb.View.CallbackID == accessDecideCallback {
		values := viewStateValues(cb.View)
		errs := map[string]string{}
		for blockID := range values {
			if !strings.HasPrefix(blockID, accessItemBlockPfx) {
				continue
			}
			if stateSelected(values, blockID, "mode") != "" {
				errs = nil
				break
			}
			errs[blockID] = accessPickMsg
		}
		if len(errs) > 0 {
			return map[string]any{"response_action": "errors", "errors": errs}
		}
	}
	return map[string]any{"response_action": "update", "view": savingModal(cb.View.PrivateMetadata)}
}

func savingModal(meta string) map[string]any {
	return modalBase("Access request", accessSavingCallback, meta, []map[string]any{
		section("⏳ *Saving your decision…*"),
		contextBlock("This view shows the result when it is done. You can close it: the card in the thread shows the same."),
	}, "")
}

// resultModal is what replaces the saving view: every action with its
// decision and how its run went, plus what did not go through.
func resultModal(v accessreq.View, meta string, failed []string) map[string]any {
	head := "✔️ *Decision saved.*"
	if len(failed) > 0 {
		head = "⚠️ *Some decisions were not applied.*"
	}
	blocks := []map[string]any{section(head)}
	if len(failed) > 0 {
		blocks = append(blocks, section("• "+escapeMrkdwn(strings.Join(failed, "\n• "))))
	}
	running := false
	for _, it := range v.Items {
		blocks = append(blocks, map[string]any{"type": "divider"})
		text := accessStatusIcon(it.Status) + " " + itemHeading(it)
		if it.Target != "" {
			text += "\n" + escapeMrkdwn(it.Target)
		}
		text += "\n" + itemStatusLine(it)
		blocks = append(blocks, section(text))
		running = running || it.Status == accessreq.ItemRunning
	}
	if running {
		blocks = append(blocks, contextBlock("Still running: the card in the thread shows the outcome when it finishes."))
	}
	return modalBase("Access request", accessSavingCallback, meta, capBlocks(blocks), "")
}

// handleAccessSubmit applies a submitted review or deny modal, then
// replaces the modal (already showing "saving…") with the outcome.
func (s *Channel) handleAccessSubmit(ctx context.Context, cb slackgo.InteractionCallback) {
	if AccessRequests == nil {
		return
	}
	parts := strings.SplitN(cb.View.PrivateMetadata, "|", 3)
	if len(parts) != 3 || parts[0] == "" {
		return
	}
	reqID, chatID, threadTS := parts[0], parts[1], parts[2]
	viewer := s.accessViewerID(ctx, cb.User.ID)
	if viewer == "" {
		s.accessSubmitMessage(ctx, cb, chatID, threadTS, "⛔ Your Slack account is not linked to an approved wick user, so nothing was decided.")
		return
	}
	values := viewStateValues(cb.View)
	note := stateText(values, accessNoteBlock, "text")
	var results []accessreq.DecideResult
	var err error
	if cb.View.CallbackID == accessDenyCallback {
		_, results, err = AccessRequests.DenyAll(ctx, reqID, viewer, note)
	} else {
		var ds []accessreq.Decision
		for blockID := range values {
			itemID, ok := strings.CutPrefix(blockID, accessItemBlockPfx)
			if !ok {
				continue
			}
			mode := stateSelected(values, blockID, "mode")
			if mode == "" {
				continue
			}
			d := accessreq.Decision{ItemID: itemID, Mode: mode}
			if mode == accessreq.ModeDeny {
				d.Note = note
			}
			ds = append(ds, d)
		}
		if len(ds) == 0 {
			return
		}
		_, results, err = AccessRequests.Decide(ctx, reqID, viewer, ds)
	}
	if err != nil {
		s.accessSubmitMessage(ctx, cb, chatID, threadTS, "⚠️ Could not save your decisions: "+err.Error())
		return
	}
	var failed []string
	for _, r := range results {
		if !r.OK {
			failed = append(failed, r.Error)
		}
	}
	if v, verr := AccessRequests.ViewFor(ctx, reqID, viewer); verr == nil && s.accessUpdateModal(ctx, cb, resultModal(v, cb.View.PrivateMetadata, failed)) {
		return
	}
	if len(failed) > 0 {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Some decisions were not applied:\n• "+strings.Join(failed, "\n• "))
	}
}

// accessSubmitMessage puts a one-line outcome in the submitted modal, or,
// when the modal is gone, in an ephemeral message to the clicker.
func (s *Channel) accessSubmitMessage(ctx context.Context, cb slackgo.InteractionCallback, chatID, threadTS, text string) {
	m := modalBase("Access request", accessSavingCallback, cb.View.PrivateMetadata, []map[string]any{section(escapeMrkdwn(text))}, "")
	if !s.accessUpdateModal(ctx, cb, m) {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, text)
	}
}

// accessUpdateModal replaces the submitted modal (by view id; the ack
// already swapped it for the saving view). False when Slack refused,
// e.g. the approver closed it.
func (s *Channel) accessUpdateModal(ctx context.Context, cb slackgo.InteractionCallback, view map[string]any) bool {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || cb.View.ID == "" {
		return false
	}
	req, err := toModalRequest(view)
	if err != nil {
		return false
	}
	if _, err := api.UpdateViewContext(ctx, req, "", "", cb.View.ID); err != nil {
		log.Debug().Str("channel", "slack").Err(err).Msg("access request: result modal update failed")
		return false
	}
	return true
}

func (s *Channel) accessEphemeral(chatID, threadTS, userID, text string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || chatID == "" {
		return
	}
	opts := []slackgo.MsgOption{slackgo.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slackgo.MsgOptionTS(threadTS))
	}
	if _, err := api.PostEphemeral(chatID, userID, opts...); err != nil {
		log.Debug().Str("channel", "slack").Err(err).Msg("access request: ephemeral failed")
	}
}

func stateField(values map[string]any, blockID, actionID string) map[string]any {
	b, _ := values[blockID].(map[string]any)
	a, _ := b[actionID].(map[string]any)
	return a
}

func stateText(values map[string]any, blockID, actionID string) string {
	v, _ := stateField(values, blockID, actionID)["value"].(string)
	return strings.TrimSpace(v)
}

func stateSelected(values map[string]any, blockID, actionID string) string {
	opt, _ := stateField(values, blockID, actionID)["selected_option"].(map[string]any)
	v, _ := opt["value"].(string)
	return v
}

// accessSender is who the card was posted as in this thread: the thread's
// agent persona when it has one, else this bot's own profile. Slack draws a
// modal's header with the APP's icon and no API changes that, so the modal
// repeats the sender at the top to match the card and the replies.
func (s *Channel) accessSender(ctx context.Context, api *slackgo.Client, threadTS string) (name, icon string) {
	if !s.customizeDenied.Load() && threadTS != "" {
		if p, ok := s.personaFor(s.sessionKey(threadTS)); ok && p.Username != "" {
			return p.Username, p.IconURL
		}
	}
	s.cfgMu.Lock()
	botID, botName := s.botUserID, s.botUserName
	s.cfgMu.Unlock()
	if botID == "" {
		return "", ""
	}
	if v, ok := s.botAvatar.Load(botID); ok {
		a := v.([2]string)
		return a[0], a[1]
	}
	name, icon = botName, ""
	if u, err := api.GetUserInfoContext(ctx, botID); err == nil && u != nil {
		if u.Profile.DisplayName != "" {
			name = u.Profile.DisplayName
		} else if u.RealName != "" {
			name = u.RealName
		}
		icon = u.Profile.Image72
	}
	s.botAvatar.Store(botID, [2]string{name, icon})
	return name, icon
}

// withSenderHeader puts "<avatar> name" above a modal's blocks.
func withSenderHeader(view map[string]any, name, icon string) map[string]any {
	blocks, _ := view["blocks"].([]map[string]any)
	els := []any{}
	if icon != "" {
		els = append(els, map[string]any{"type": "image", "image_url": icon, "alt_text": name})
	}
	els = append(els, mrkdwn("*"+escapeMrkdwn(name)+"*"))
	view["blocks"] = append([]map[string]any{{"type": "context", "elements": els}}, blocks...)
	return view
}

package slack

import (
	"context"
	"encoding/json"
	"fmt"
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
	accessOpenWebAction  = "access_open_web"
	accessDecideCallback = "access_req_decide"
	accessDenyCallback   = "access_req_deny"
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
	opts := []slackgo.MsgOption{slackgo.MsgOptionText(fallback, false), slackgo.MsgOptionBlocks(blocks...)}
	if threadTS != "" {
		opts = append(opts, slackgo.MsgOptionTS(threadTS))
	}
	_, ts, err := api.PostMessageContext(ctx, chatID, opts...)
	if err != nil {
		return nil, fmt.Errorf("post access request card: %w", err)
	}
	return map[string]string{"slack_channel": chatID, "slack_ts": ts, "slack_thread": threadTS}, nil
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

func accessStatusIcon(status string) string {
	switch status {
	case accessreq.ItemOK:
		return "✅"
	case accessreq.ItemError:
		return "❌"
	case accessreq.ItemDenied:
		return "⛔"
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
		return "_Waiting · who can approve: " + escapeMrkdwn(approverList(it.Approvers)) + "_"
	case accessreq.ItemRunning:
		return fmt.Sprintf("_Approved %s by %s · running…_", strings.ToLower(accessreq.ModeLabel(it.Mode)), escapeMrkdwn(it.DecidedBy))
	case accessreq.ItemOK:
		return fmt.Sprintf("Approved %s by %s · *OK*", strings.ToLower(accessreq.ModeLabel(it.Mode)), escapeMrkdwn(it.DecidedBy))
	case accessreq.ItemError:
		return fmt.Sprintf("Approved %s by %s · *error:* `%s`", strings.ToLower(accessreq.ModeLabel(it.Mode)), escapeMrkdwn(it.DecidedBy), escapeMrkdwn(clipText(it.Error, 300)))
	case accessreq.ItemDenied:
		line := "Denied by " + escapeMrkdwn(it.DecidedBy)
		if it.Note != "" {
			line += ": _" + escapeMrkdwn(clipText(it.Note, 300)) + "_"
		}
		return line
	}
	return it.Status
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
		blocks = append(blocks, contextBlock("_Agent's claim (unverified):_ "+escapeMrkdwn(clipText(v.Reason, 500))))
	}
	for _, it := range v.Items {
		text := accessStatusIcon(it.Status) + " " + itemHeading(it)
		if it.Target != "" {
			text += "\n" + escapeMrkdwn(it.Target)
		}
		text += "\n" + itemStatusLine(it)
		blocks = append(blocks, section(text))
	}
	if v.Status == accessreq.StatusPending && v.Pending > 0 {
		elems := []any{
			map[string]any{"type": "button", "action_id": accessReviewAction, "value": v.ID, "style": "primary", "text": plain("Review & decide")},
			map[string]any{"type": "button", "action_id": accessDenyAction, "value": v.ID, "style": "danger", "text": plain("Deny")},
		}
		if v.URL != "" {
			elems = append(elems, map[string]any{"type": "button", "action_id": accessOpenWebAction, "value": v.ID, "url": v.URL, "text": plain("Open in web")})
		}
		blocks = append(blocks, map[string]any{"type": "actions", "block_id": accessBlockID, "elements": elems})
	}
	blocks = append(blocks, contextBlock(fmt.Sprintf("Request `%s` · approved actions run with the approver's own access", shortID(v.ID))))
	return blocks
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// paramsText is the parameter list for the modal, masked by the service.
func paramsText(it accessreq.ItemView) string {
	var b strings.Builder
	for _, p := range it.Params {
		v := p.Value
		if p.Long {
			v = clipText(v, 280) + " (cut, full text on the web page)"
		}
		fmt.Fprintf(&b, "%s: %s\n", p.Name, strings.ReplaceAll(v, "```", "'''"))
	}
	if b.Len() == 0 {
		return "(no parameters)"
	}
	return clipText(b.String(), 1500)
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
	if v.Requester != "" {
		blocks = append(blocks, contextBlock("Asked in a conversation of "+escapeMrkdwn(v.Requester)))
	}
	if v.Reason != "" {
		blocks = append(blocks, contextBlock("_Agent's claim (unverified):_ "+escapeMrkdwn(clipText(v.Reason, 500))))
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
			text += "\n*Runs as:* your own account ✅"
		}
		blocks = append(blocks, section(text))
		blocks = append(blocks, section("```"+escapeMrkdwn(paramsText(it))+"```"))
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
					v.Items[i].LockedReason = "Not linked to wick. Who can: " + approverList(v.Items[i].Approvers) + "."
				}
			}
			view = reviewModal(v, meta)
		case action.ActionID == accessDenyAction:
			view = denyModal(v, meta)
		default:
			view = reviewModal(v, meta)
		}
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

func threadOf(cb slackgo.InteractionCallback) string {
	if cb.Message.ThreadTimestamp != "" {
		return cb.Message.ThreadTimestamp
	}
	return cb.Message.Timestamp
}

// handleAccessSubmit applies a submitted review or deny modal and tells
// the clicker (only them) what did not go through.
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
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Your Slack account is not linked to an approved wick user, so nothing was decided.")
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
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Could not save your decisions: "+err.Error())
		return
	}
	var failed []string
	for _, r := range results {
		if !r.OK {
			failed = append(failed, r.Error)
		}
	}
	if len(failed) > 0 {
		s.accessEphemeral(chatID, threadTS, cb.User.ID, "Some decisions were not applied:\n• "+strings.Join(failed, "\n• "))
	}
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

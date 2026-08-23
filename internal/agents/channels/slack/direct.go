package slack

import (
	"context"
	"errors"
	"strings"

	slackgo "github.com/slack-go/slack"
)

// direct.go sends an unsolicited DM to one Slack user — used for account
// notices (a registration awaiting approval, an approval granted), not for
// conversation replies.
//
// Separate from the sendHandler DM path because that one serves an agent tool
// call over HTTP and answers with a JSON envelope the model reads. This is an
// internal, fire-and-forget notice with no model in the loop.

// ErrNotConfigured is returned when the channel has no Slack client yet, so a
// caller can tell "not set up" from "Slack refused".
var ErrNotConfigured = errors.New("slack channel is not configured")

// SendDirect opens (or reuses) the bot's DM with externalUserID and posts text.
//
// Uses the BOT token deliberately. A user token would make the notice appear to
// come from whichever human authorised it, which is wrong for a system message
// — and account notices must keep working when no user token is connected.
//
// Slack refuses conversations.open against bot users, and a workspace can lack
// the im:write scope entirely; both surface as an error here rather than being
// swallowed, so the caller can log a real cause.
func (s *Channel) SendDirect(ctx context.Context, externalUserID, text string) error {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()

	if api == nil {
		return ErrNotConfigured
	}
	externalUserID = strings.TrimSpace(externalUserID)
	if externalUserID == "" || strings.TrimSpace(text) == "" {
		return errors.New("slack: missing target user or text")
	}

	ch, _, _, err := api.OpenConversationContext(ctx, &slackgo.OpenConversationParameters{
		Users:    []string{externalUserID},
		ReturnIM: true,
	})
	if err != nil {
		return annotateDMError(err)
	}
	if ch == nil || ch.ID == "" {
		return errors.New("slack: conversations.open returned no channel")
	}
	_, _, err = api.PostMessageContext(ctx, ch.ID,
		slackgo.MsgOptionText(text, false),
		slackgo.MsgOptionDisableLinkUnfurl(),
	)
	return err
}

// annotateDMError adds the actionable part to Slack's terse codes, so a log
// line says what to fix rather than just that something failed.
func annotateDMError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "missing_scope"):
		return errors.New(msg + " (the Slack app is missing the im:write scope — " +
			"add it under OAuth & Permissions, then reinstall)")
	case strings.Contains(msg, "cannot_dm_bot"):
		return errors.New(msg + " (target is a bot user; Slack does not allow DMs to bots)")
	default:
		return err
	}
}

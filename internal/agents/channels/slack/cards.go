package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/actioncard"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// cardBlockID marks the actions block of an actioncard so a click is told
// apart from the gate's approval buttons and from workflow interactions.
const cardBlockID = "actioncard"

// Slack limits the blocks below respect.
const (
	maxHeaderRunes = 150
	maxFieldRunes  = 2000
	maxFields      = 10
	maxButtonRunes = 75
	maxValueBytes  = 2000
)

// cardPointer replaces a card fence in the reply text: the card itself is
// posted as its own Block Kit message right after.
func cardPointer(c actioncard.Card) string {
	return "_" + strings.TrimSpace(c.Title) + " — see the card below._"
}

// cardValue packs what a click needs into the button value.
func cardValue(sessionID, cardID, value string) string {
	return sessionID + "|" + cardID + "|" + value
}

// parseCardValue is the reverse of cardValue. The value itself may hold a
// "|", so it is everything after the second one.
func parseCardValue(v string) (sessionID, cardID, value string, ok bool) {
	parts := strings.SplitN(v, "|", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// cardBlocks renders c as Block Kit: header, subtitle/status, the rows as
// fields, and one button per action whose value carries the postback. A
// final card has no buttons.
func cardBlocks(sessionID string, c actioncard.Card) []slackgo.Block {
	blocks := []slackgo.Block{
		slackgo.NewHeaderBlock(slackgo.NewTextBlockObject(slackgo.PlainTextType, clip(strings.TrimSpace(c.Title), maxHeaderRunes), false, false)),
	}
	var sub []string
	if c.Subtitle != "" {
		sub = append(sub, c.Subtitle)
	}
	if c.Status != "" {
		sub = append(sub, "*Status:* "+c.Status)
	}
	if len(sub) > 0 {
		blocks = append(blocks, slackgo.NewContextBlock("", slackgo.NewTextBlockObject(slackgo.MarkdownType, clip(strings.Join(sub, "  ·  "), maxFieldRunes), false, false)))
	}
	var fields []*slackgo.TextBlockObject
	for _, r := range c.Rows {
		if len(r) == 0 || len(fields) == maxFields {
			continue
		}
		f := "*" + r[0] + "*"
		if len(r) > 1 {
			f += "\n" + r[1]
		}
		fields = append(fields, slackgo.NewTextBlockObject(slackgo.MarkdownType, clip(f, maxFieldRunes), false, false))
	}
	if len(fields) > 0 {
		blocks = append(blocks, slackgo.NewSectionBlock(nil, fields, nil))
	}
	if c.Final || len(c.Actions) == 0 {
		return blocks
	}
	var btns []slackgo.BlockElement
	for i, a := range c.Actions {
		v := cardValue(sessionID, c.ID, a.Value)
		if len(v) > maxValueBytes || i == 25 {
			continue
		}
		b := slackgo.NewButtonBlockElement(fmt.Sprintf("actioncard:%d", i), v,
			slackgo.NewTextBlockObject(slackgo.PlainTextType, clip(a.Label, maxButtonRunes), false, false))
		if a.Style == "primary" {
			b.Style = slackgo.StylePrimary
		}
		btns = append(btns, b)
	}
	if len(btns) > 0 {
		blocks = append(blocks, slackgo.NewActionBlock(cardBlockID, btns...))
	}
	return blocks
}

// decidedBlocks is a clicked card's blocks with the buttons swapped for a
// "✓ label · by @user" line, so the thread shows the choice and the card
// cannot be clicked again.
func decidedBlocks(blocks []slackgo.Block, label, userID string) []slackgo.Block {
	out := make([]slackgo.Block, 0, len(blocks)+1)
	for _, b := range blocks {
		if ab, ok := b.(*slackgo.ActionBlock); ok && ab.BlockID == cardBlockID {
			continue
		}
		out = append(out, b)
	}
	note := "✓ " + label
	if userID != "" {
		note += " · by <@" + userID + ">"
	}
	return append(out, slackgo.NewContextBlock("", slackgo.NewTextBlockObject(slackgo.MarkdownType, note, false, false)))
}

// postCards posts each card as its own message in the thread, after the
// reply text that pointed at it.
func (s *Channel) postCards(channelID, threadTS, sessionID string, cards []actioncard.Card) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}
	for _, c := range cards {
		c := c
		s.withBackoff(func() error {
			_, _, err := api.PostMessage(channelID,
				slackgo.MsgOptionText(actioncard.PlainText(c), false),
				slackgo.MsgOptionBlocks(cardBlocks(sessionID, c)...),
				slackgo.MsgOptionTS(threadTS),
			)
			return err
		})
	}
}

// handleCardClick sends an actioncard button click to the agent as a
// postback and locks the Slack card on the choice. A refused click (card
// already decided, superseded) is told to the clicker only.
func (s *Channel) handleCardClick(ctx context.Context, cb slackgo.InteractionCallback, action *slackgo.BlockAction) {
	sessionID, cardID, value, ok := parseCardValue(action.Value)
	fn := agentchannels.CardPostback
	if !ok || fn == nil {
		return
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	ephemeral := func(msg string) {
		if api == nil {
			return
		}
		threadTS := cb.Message.ThreadTimestamp
		if threadTS == "" {
			threadTS = cb.Message.Timestamp
		}
		if _, err := api.PostEphemeral(cb.Channel.ID, cb.User.ID, slackgo.MsgOptionText(msg, false), slackgo.MsgOptionTS(threadTS)); err != nil {
			log.Debug().Str("channel", "slack").Err(err).Msg("post card ephemeral failed")
		}
	}
	if !s.approverAllowed(s.snapshot(), cb.User.ID) {
		ephemeral("Not authorized to answer this card.")
		return
	}
	label, err := fn(ctx, sessionID, "slack", cardID, value)
	if err != nil {
		ephemeral("Could not send your choice: " + err.Error())
		return
	}
	if api == nil {
		return
	}
	blocks := decidedBlocks(cb.Message.Blocks.BlockSet, label, cb.User.ID)
	s.withBackoff(func() error {
		_, _, _, err := api.UpdateMessage(cb.Channel.ID, cb.Message.Timestamp,
			slackgo.MsgOptionText("✓ "+label, false), slackgo.MsgOptionBlocks(blocks...))
		return err
	})
}

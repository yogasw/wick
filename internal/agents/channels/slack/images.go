package slack

import (
	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/imagecard"
)

// maxImageBlocks keeps one image message well under Slack's 50-block cap.
const maxImageBlocks = 20

// clipRunes cuts s to n runes; Slack refuses image alt text and titles
// over 2000 characters.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// imageBlocks renders images as Slack image blocks. Slack fetches each URL
// itself, so a GIF shows animated without being uploaded.
func imageBlocks(imgs []imagecard.Image) []slackgo.Block {
	out := make([]slackgo.Block, 0, len(imgs))
	for _, img := range imgs {
		alt := img.Caption
		if alt == "" {
			alt = "image"
		}
		var title *slackgo.TextBlockObject
		if img.Caption != "" {
			title = slackgo.NewTextBlockObject(slackgo.PlainTextType, clipRunes(img.Caption, 2000), false, false)
		}
		out = append(out, slackgo.NewImageBlock(img.URL, clipRunes(alt, 2000), "", title))
	}
	return out
}

// postImages posts the images of a reply in the thread, after its text,
// at most maxImageBlocks per message.
func (s *Channel) postImages(channelID, threadTS string, imgs []imagecard.Image) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}
	for start := 0; start < len(imgs); start += maxImageBlocks {
		end := start + maxImageBlocks
		if end > len(imgs) {
			end = len(imgs)
		}
		batch := imgs[start:end]
		s.withBackoff(func() error {
			_, _, err := api.PostMessage(channelID,
				slackgo.MsgOptionText(imagecard.PlainText(batch), false),
				slackgo.MsgOptionBlocks(imageBlocks(batch)...),
				slackgo.MsgOptionTS(threadTS),
				slackgo.MsgOptionDisableLinkUnfurl(),
				slackgo.MsgOptionDisableMediaUnfurl(),
			)
			return err
		})
	}
}

// deleteMessage removes one message, best effort.
func (s *Channel) deleteMessage(channelID, ts string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || ts == "" {
		return
	}
	s.withBackoff(func() error {
		_, _, err := api.DeleteMessage(channelID, ts)
		return err
	})
}

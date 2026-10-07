// Package slack — markdown.go: outbound reply shaping for markdown text.
//
// Purpose: agent replies are written in markdown, which Slack's mrkdwn
// garbles — a pipe table arrives as a jumble of pipes. When a reply carries
// markdown mrkdwn cannot render, it is posted as a Block Kit `markdown`
// block (Slack then renders real tables) with a short plain `text` fallback
// for notifications. Plain replies keep the old text-only path untouched.
//
// Caller: postReply, flushLiveMessage, finalizeReply, reconcilePlan,
// postChunked and sendHandler.
// Dependencies: slackgo, internal/pkg/slackmd.
// Main Functions:
//   - replyChunks()     — split a reply under the limit of the path it takes
//   - replyMsgOptions() — text or markdown-block options for one chunk
package slack

import (
	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/pkg/slackmd"
)

// replyChunks splits a reply into postable chunks. Markdown replies use the
// markdown block limit and never cut a table row or fenced block in half;
// plain replies keep the mrkdwn chunker and its smaller limit.
func replyChunks(text string) []string {
	if slackmd.NeedsBlock(text) {
		return slackmd.Chunks(text, slackmd.MaxBlockChars)
	}
	return chunkText(text, maxSlackChunk)
}

// replyMsgOptions returns the message options that post or edit one reply
// chunk. Markdown text becomes a single `markdown` block plus a plain
// fallback `text`. forceBlock keeps the block form for an edit of a message
// that already went out as a block, so the old block is replaced rather
// than left behind next to the new text.
func replyMsgOptions(text string, forceBlock bool) []slackgo.MsgOption {
	if !forceBlock && !slackmd.NeedsBlock(text) {
		return []slackgo.MsgOption{slackgo.MsgOptionText(text, false)}
	}
	fallback := slackmd.Fallback(text)
	if fallback == "" {
		fallback = text
	}
	return []slackgo.MsgOption{
		slackgo.MsgOptionText(fallback, false),
		slackgo.MsgOptionBlocks(slackgo.NewMarkdownBlock("", text)),
	}
}

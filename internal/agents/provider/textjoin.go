// Package provider — textjoin.go: paragraph breaks between assistant messages.
//
// Purpose: one turn is not one message. A provider emits text, runs a tool,
//
//	then emits text again — two separate assistant messages, each starting at
//	its own column 0 and carrying no newline of its own. Every consumer appends
//	the pieces into one body, so without a separator the two run together:
//	"…mengikuti bentuk intent produksi." + "Sudah jadi. Ada di …" arrives as
//	"produksi.Sudah jadi." — glued in the web chat, in Slack, in Telegram and
//	in conversation.jsonl alike.
//
// Caller:   the read loop in agent.go, on the single event path every
//
//	provider's parser feeds and every channel reads from.
//
// Dependencies: stdlib only.
// Main Functions:
//   - textJoiner.toolRan()     — record a tool boundary
//   - textJoiner.turnEnded()   — reset at Done/Error
//   - textJoiner.breakBefore() — the newlines to prepend to the next message
//
// Side Effects: none — per-run counters, nothing global.
package provider

import "strings"

// textJoiner tracks, within one turn, whether a tool ran since the last piece
// of assistant text and how that piece ended. Driven by the read loop, which
// is single-goroutine, so it needs no locking.
//
// The zero value is ready to use and means "no text yet this turn".
type textJoiner struct {
	sawText   bool // some assistant text has been emitted this turn
	breakNext bool // a tool ran since; the next text is a NEW message
	tailNL    int  // trailing newlines of the last emitted text
}

// toolRan records a tool boundary. Only a boundary that FOLLOWS text starts a
// new message — a turn that opens with a tool call has nothing to separate.
func (j *textJoiner) toolRan() {
	if j.sawText {
		j.breakNext = true
	}
}

// turnEnded resets the joiner so the next turn starts clean.
func (j *textJoiner) turnEnded() { *j = textJoiner{} }

// breakBefore returns the newlines to prepend to next so it reads as its own
// paragraph, and records next as the latest text. Returns "" when no break is
// needed: the first text of a turn, a continuation of the same message, or
// text already separated by a blank line from either side.
func (j *textJoiner) breakBefore(next string) string {
	if next == "" {
		return ""
	}
	sep := ""
	if j.breakNext {
		// One blank line between the two messages — count what each side
		// already contributes so a model that ended on "\n" does not get
		// three newlines and an empty paragraph.
		if have := j.tailNL + leadingNewlines(next); have < 2 {
			sep = strings.Repeat("\n", 2-have)
		}
		j.breakNext = false
	}
	j.sawText = true
	j.tailNL = trailingNewlines(next)
	return sep
}

func leadingNewlines(s string) int {
	n := 0
	for n < len(s) && s[n] == '\n' {
		n++
	}
	return n
}

func trailingNewlines(s string) int {
	n := 0
	for n < len(s) && s[len(s)-1-n] == '\n' {
		n++
	}
	return n
}

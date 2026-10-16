package channels

import (
	"regexp"
	"strings"
)

// MaxOptions and MaxOptionLen cap the choices SplitOptions keeps: the
// UI shows them as pills, so a few short ones.
const (
	MaxOptions   = 4
	MaxOptionLen = 40
)

// optionsLineRe is the machine line a teammate ends a question with when
// it offers choices: "[options: A | B | C]".
var optionsLineRe = regexp.MustCompile(`(?i)^[ \t]*\[options:([^\]\n]*)\][ \t]*$`)

// SplitOptions takes the "[options: A | B | C]" line off the end of a
// teammate's question and returns the question without it plus the
// choices, trimmed, at most MaxOptions. An empty choice, or one longer
// than MaxOptionLen characters, is dropped: cut short it would answer
// with a text the teammate never offered. A text whose last non-blank
// line is not such a line comes back as it was, with nil choices.
func SplitOptions(text string) (string, []string) {
	body := strings.TrimRight(text, " \t\r\n")
	i := strings.LastIndexByte(body, '\n')
	m := optionsLineRe.FindStringSubmatch(strings.TrimRight(body[i+1:], "\r"))
	if m == nil {
		return text, nil
	}
	var opts []string
	for _, o := range strings.Split(m[1], "|") {
		o = strings.TrimSpace(o)
		if o == "" || len([]rune(o)) > MaxOptionLen {
			continue
		}
		if opts = append(opts, o); len(opts) == MaxOptions {
			break
		}
	}
	if i < 0 {
		return "", opts
	}
	return strings.TrimRight(body[:i], " \t\r\n"), opts
}

// openOptionsLineRe is an options line still streaming in at the end of
// a live reply: "[options: prod | sta", or any prefix of "[options:".
var openOptionsLineRe = regexp.MustCompile(`(?i)(?:^|\n)[ \t]*\[(?:o(?:p(?:t(?:i(?:o(?:n(?:s(?::[^\]\n]*)?)?)?)?)?)?)?)?$`)

// TrimOpenOptionsLine drops an options line that has not finished
// streaming from the end of a live reply, so the raw "[options: a | b"
// never flashes in the thread before RewriteOptionsLine can read it.
func TrimOpenOptionsLine(text string) string {
	loc := openOptionsLineRe.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return strings.TrimRight(text[:loc[0]], " \t\r\n")
}

// RewriteOptionsLine turns the options line of a text going out to a chat
// (Slack, Telegram) into plain text, "Options: A / B / C", or drops it
// when it names no choice: like a source tag, the raw line never reaches
// the chat.
func RewriteOptionsLine(text string) string {
	if !strings.Contains(strings.ToLower(text), "[options:") {
		return text
	}
	q, opts := SplitOptions(text)
	if q == text {
		return text
	}
	if len(opts) == 0 {
		return q
	}
	line := "Options: " + strings.Join(opts, " / ")
	if q == "" {
		return line
	}
	return q + "\n\n" + line
}

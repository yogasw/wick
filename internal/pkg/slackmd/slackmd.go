// Package slackmd decides when outbound Slack text should travel as a Block
// Kit `markdown` block instead of plain mrkdwn, and prepares it for that.
//
// Slack's mrkdwn cannot render pipe tables, `#` headings, `**bold**`,
// `[label](url)` links, nested lists or fenced code the way markdown does.
// The `markdown` block can — Slack renders real tables (with copy/download)
// from it. Text that uses none of those constructs keeps the plain path, so
// ordinary replies look exactly as before.
//
// Main functions:
//   - NeedsBlock — true when text carries markdown mrkdwn would garble
//   - Chunks     — split text under the block limit without cutting a table
//     row or a fenced block in half
//   - Fallback   — short plain version for the message's top-level `text`
//     (notifications, clients that cannot render blocks)
//
// Pure and dependency-free so both the Slack channel and the Slack connector
// can share it.
package slackmd

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxBlockChars is Slack's cumulative limit for markdown block text in one
// message. Counted in bytes here, which is never more than the character
// count Slack enforces, so a chunk that fits by bytes always fits.
const MaxBlockChars = 12000

// maxFallbackRunes caps the plain `text` fallback — it only feeds
// notifications and previews, so the first sentence or two is enough.
const maxFallbackRunes = 300

var (
	reHeading    = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+\S`)
	reBold       = regexp.MustCompile(`\*\*[^*\n]+\*\*`)
	reLink       = regexp.MustCompile(`\[[^\]\n]+\]\((?:https?://|mailto:)[^)\s]+\)`)
	reNestedList = regexp.MustCompile(`(?m)^(?: {2,}|\t+)(?:[-*+]|\d+[.)])\s+\S`)
	reFence      = regexp.MustCompile("(?m)^\\s*```")
	reTableSep   = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(?:\|\s*:?-{2,}:?\s*)+\|?\s*$`)
	reLinkRepl   = regexp.MustCompile(`\[([^\]\n]+)\]\((?:https?://|mailto:)[^)\s]+\)`)
	reHeadRepl   = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	reSpaces     = regexp.MustCompile(`\s+`)
)

// NeedsBlock reports whether text uses markdown that Slack mrkdwn cannot
// render: a pipe table, a heading, **bold**, a [label](url) link, a nested
// list or a fenced code block.
func NeedsBlock(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	return hasTable(text) ||
		reHeading.MatchString(text) ||
		reBold.MatchString(text) ||
		reLink.MatchString(text) ||
		reNestedList.MatchString(text) ||
		reFence.MatchString(text)
}

// hasTable is true when a line containing `|` is followed by a separator
// row like `|---|:--:|` — the shape that makes a markdown table.
func hasTable(text string) bool {
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		if reTableSep.MatchString(lines[i]) && strings.Contains(lines[i-1], "|") {
			return true
		}
	}
	return false
}

// segment is one contiguous piece of the text: a paragraph, a table or a
// fenced block, carrying any blank lines that follow it so joining the
// segments back reproduces the input byte for byte.
type segment struct {
	text   string
	table  bool
	fence  bool
	header string // table: header + separator rows, repeated when split
}

// Chunks splits text into pieces of at most max bytes. Cuts fall between
// paragraphs, tables and fenced blocks first. A single table larger than max
// is split between rows with its header repeated, a fenced block is closed
// and reopened, and only a single line longer than max is cut mid-line.
func Chunks(text string, max int) []string {
	if max <= 0 || len(text) <= max {
		return []string{text}
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if c := strings.TrimRight(cur.String(), " \n"); c != "" {
			out = append(out, c)
		}
		cur.Reset()
	}
	for _, seg := range segments(text) {
		if cur.Len()+len(seg.text) <= max {
			cur.WriteString(seg.text)
			continue
		}
		flush()
		if len(seg.text) <= max {
			cur.WriteString(seg.text)
			continue
		}
		pieces := splitSegment(seg, max)
		for i, p := range pieces {
			if i == len(pieces)-1 {
				cur.WriteString(p)
				break
			}
			if c := strings.TrimRight(p, " \n"); c != "" {
				out = append(out, c)
			}
		}
	}
	flush()
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

// segments walks text line by line and groups it into paragraphs, tables
// and fenced blocks. Blank lines attach to the segment before them.
func segments(text string) []segment {
	lines := strings.SplitAfter(text, "\n")
	var segs []segment
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		var seg segment
		start := i
		switch {
		case strings.HasPrefix(trimmed, "```"):
			seg.fence = true
			i++
			for i < len(lines) {
				closing := strings.HasPrefix(strings.TrimSpace(lines[i]), "```")
				i++
				if closing {
					break
				}
			}
		case strings.Contains(line, "|") && i+1 < len(lines) && reTableSep.MatchString(strings.TrimRight(lines[i+1], "\n")):
			seg.table = true
			seg.header = line + lines[i+1]
			i += 2
			for i < len(lines) && strings.Contains(lines[i], "|") && strings.TrimSpace(lines[i]) != "" {
				i++
			}
		case trimmed == "":
			i++
		default:
			i++
			for i < len(lines) {
				t := strings.TrimSpace(lines[i])
				if t == "" || strings.HasPrefix(t, "```") {
					break
				}
				if strings.Contains(lines[i], "|") && i+1 < len(lines) && reTableSep.MatchString(strings.TrimRight(lines[i+1], "\n")) {
					break
				}
				i++
			}
		}
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		seg.text = strings.Join(lines[start:i], "")
		segs = append(segs, seg)
	}
	return segs
}

// splitSegment breaks one oversized segment into pieces of at most max bytes
// between lines. Table pieces after the first start with the header again so
// each renders as its own table, fence pieces are closed and reopened. Only a
// single line longer than the limit is cut mid-line.
func splitSegment(seg segment, max int) []string {
	lines := strings.SplitAfter(seg.text, "\n")
	// head starts every piece, tail closes every piece but the last.
	var head, tail string
	body := lines
	switch {
	case seg.table:
		head, body = seg.header, lines[2:]
	case seg.fence:
		head, body, tail = lines[0], lines[1:], "```\n"
		if !strings.HasSuffix(head, "\n") {
			head += "\n"
		}
	}
	// A header or fence line too big to repeat makes repetition pointless.
	if len(head)+len(tail) >= max/2 {
		head, tail, body = "", "", lines
	}

	var out []string
	cur := head
	emit := func() {
		if tail != "" && !strings.HasSuffix(cur, "\n") {
			cur += "\n"
		}
		out = append(out, cur+tail)
		cur = head
	}
	room := max - len(head) - len(tail)
	for _, line := range body {
		for len(line) > room {
			if len(cur) > len(head) {
				emit()
			}
			cut := cutLine(line, room)
			cur += line[:cut]
			emit()
			line = line[cut:]
		}
		if len(cur)+len(line)+len(tail) > max && len(cur) > len(head) {
			emit()
		}
		cur += line
	}
	if len(cur) > len(head) {
		out = append(out, cur)
	}
	return out
}

// cutLine returns where to cut an over-long line so the head fits in max:
// at the last space when there is one, else at the last full rune.
func cutLine(line string, max int) int {
	if max <= 0 {
		max = 1
	}
	if max >= len(line) {
		return len(line)
	}
	if idx := strings.LastIndex(line[:max], " "); idx > max/2 {
		return idx + 1
	}
	for max > 0 && !utf8.RuneStart(line[max]) {
		max--
	}
	if max == 0 {
		return len(line)
	}
	return max
}

// Fallback returns a short plain version of markdown text for the message's
// top-level `text` field: table separators and fences dropped, heading and
// bold markers removed, links reduced to their label, whitespace collapsed
// and the result clipped to a preview length.
func Fallback(text string) string {
	var parts []string
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") || reTableSep.MatchString(t) {
			continue
		}
		t = reHeadRepl.ReplaceAllString(t, "")
		t = strings.ReplaceAll(t, "**", "")
		t = reLinkRepl.ReplaceAllString(t, "$1")
		if strings.HasPrefix(t, "|") {
			cells := strings.Split(strings.Trim(t, "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			t = strings.Join(cells, " · ")
		}
		parts = append(parts, t)
	}
	out := reSpaces.ReplaceAllString(strings.Join(parts, " "), " ")
	if utf8.RuneCountInString(out) > maxFallbackRunes {
		r := []rune(out)
		out = strings.TrimSpace(string(r[:maxFallbackRunes])) + "…"
	}
	return out
}

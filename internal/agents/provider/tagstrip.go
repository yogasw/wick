package provider

import (
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/agents/event"
)

// DefaultStripTags are the harness context tags always removed from
// assistant text. A provider that streams token deltas splits a tag over
// many events, so the text is filtered as a stream (tagStripper), never one
// delta at a time.
var DefaultStripTags = []string{"system-reminder"}

// StripTagsConfig is the per-instance setting that adds tags to the list
// without a rebuild.
type StripTagsConfig struct {
	StripTags string `wick:"key=strip_tags;textarea;desc=Extra context tags to hide from replies, one per line: a name like system-reminder or <system-reminder>. Everything from <name> to </name> is removed from the reply before it is saved or sent to any channel. system-reminder is always removed. Takes effect on the next session spawn."`
}

var stripTagName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]*$`)

// StripTagList turns the strip_tags setting into tag names: the defaults
// plus every valid line, without duplicates. "<name>" and "</name>" are
// accepted for name; anything else is skipped.
func StripTagList(raw string) []string {
	out := append([]string{}, DefaultStripTags...)
	seen := map[string]bool{}
	for _, t := range out {
		seen[t] = true
	}
	for _, line := range strings.Split(raw, "\n") {
		name := strings.TrimSpace(line)
		name = strings.TrimPrefix(strings.TrimPrefix(name, "<"), "/")
		name = strings.TrimSpace(strings.TrimSuffix(name, ">"))
		if !stripTagName.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// maxHeldTag bounds how much text an unclosed tag may hold back. Past it the
// text is let through as is: a lost closing tag must not swallow the reply.
const maxHeldTag = 64 << 10

// tagStripper removes whole <tag>…</tag> blocks from text that arrives in
// pieces. Text that may be the start of a tag is held until it is known.
type tagStripper struct {
	tags    []string
	pending string
	// skipWS drops the whitespace right after a removed block: the harness
	// pads its tags with blank lines, which would otherwise stay behind as
	// a run of empty lines in the reply.
	skipWS bool
}

func newTagStripper(tags []string) *tagStripper {
	if len(tags) == 0 {
		tags = DefaultStripTags
	}
	return &tagStripper{tags: tags}
}

// feed adds one piece and returns the text that is safe to show now.
func (t *tagStripper) feed(s string) string {
	t.pending += s
	var out strings.Builder
	for t.pending != "" {
		if t.skipWS {
			t.pending = strings.TrimLeft(t.pending, " \t\r\n")
			if t.pending == "" {
				break
			}
			t.skipWS = false
		}
		i := strings.IndexByte(t.pending, '<')
		if i < 0 {
			out.WriteString(t.pending)
			t.pending = ""
			break
		}
		out.WriteString(t.pending[:i])
		rest := t.pending[i:]
		open, partial := t.matchOpen(rest)
		switch {
		case open != "":
			closeTag := "</" + open + ">"
			j := strings.Index(rest, closeTag)
			if j < 0 {
				if len(rest) > maxHeldTag {
					out.WriteString(rest)
					t.pending = ""
				} else {
					t.pending = rest
				}
				return out.String()
			}
			t.pending = rest[j+len(closeTag):]
			t.skipWS = true
		case partial:
			t.pending = rest
			return out.String()
		default:
			out.WriteByte('<')
			t.pending = rest[1:]
		}
	}
	return out.String()
}

// flush returns whatever is still held, as is.
func (t *tagStripper) flush() string {
	s := t.pending
	t.pending, t.skipWS = "", false
	return s
}

// matchOpen reports the tag s opens with, or whether s is still a prefix of
// an opening tag (more text needed to tell).
func (t *tagStripper) matchOpen(s string) (string, bool) {
	partial := false
	for _, name := range t.tags {
		open := "<" + name + ">"
		if strings.HasPrefix(s, open) {
			return name, false
		}
		if len(s) < len(open) && strings.HasPrefix(open, s) {
			partial = true
		}
	}
	return "", partial
}

// stripStream runs one line's events through the stripper: streamed text is
// filtered, an empty result drops the event, and held text is released as
// its own text event before any non-text event (a tool call, the turn end),
// so nothing is lost or reordered.
func (t *tagStripper) stripStream(evs []event.AgentEvent) []event.AgentEvent {
	out := evs[:0:0]
	for _, ev := range evs {
		if ev.Type == event.TextDelta && !ev.Synthetic {
			ev.Text = t.feed(ev.Text)
			if ev.Text == "" {
				continue
			}
			out = append(out, ev)
			continue
		}
		switch ev.Type {
		case event.ToolUse, event.ToolResult, event.Done, event.Error:
			if held := t.flush(); held != "" {
				out = append(out, event.AgentEvent{Type: event.TextDelta, Text: held})
			}
		}
		out = append(out, ev)
	}
	return out
}

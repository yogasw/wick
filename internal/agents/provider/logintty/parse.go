package logintty

import (
	"regexp"
	"strconv"
	"strings"
)

// EventType classifies what the output parser recognized in the PTY
// stream.
type EventType string

const (
	// EventLink is an https URL found in the output — the OAuth login
	// link the user must open. Value is the URL.
	EventLink EventType = "link"
	// EventSuccess is a "logged in" style confirmation line. Value is
	// the matched line (cleaned).
	EventSuccess EventType = "success"
	// EventFailure is a login-error line (invalid code, oauth error).
	// Value is the matched line (cleaned).
	EventFailure EventType = "failure"
)

// Event is one parser recognition.
type Event struct {
	Type  EventType
	Value string
}

var (
	// Escape-sequence classes for the matching copy of the stream (the
	// browser terminal renders the raw bytes; this is only for regexes).
	csiRe = regexp.MustCompile(`\x1b\[([0-9;?]*)[ -/]*([@-~])`)
	oscRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	escRe = regexp.MustCompile(`\x1b[@-_]`)
	urlRe = regexp.MustCompile(`https://[^\s"'<>\x60\x1b]+`)

	successNeedles = []string{
		"logged in as",
		"login successful",
		"successfully logged in",
		"successfully authenticated",
	}

	failureNeedles = []string{
		"login failed",
		"authentication failed",
		"invalid authorization code",
		"invalid code",
		"oauth error",
		"unable to authenticate",
	}
)

// LinkParser incrementally scans raw PTY output (ANSI escapes and all)
// for login URLs and success confirmations. Feed may be called with
// arbitrary chunk boundaries — URLs split across chunks are still
// found. Each distinct URL / success line is reported once.
type LinkParser struct {
	// Cols is the current terminal width. When > 0, cleaned lines of
	// exactly this length are joined with their successor before URL
	// matching — ConPTY injects literal \r\n at the wrap column, which
	// would otherwise split long OAuth URLs. 0 = no joining.
	Cols int

	buf  []byte
	seen map[string]bool
}

// keep bounds how much cleaned tail survives between Feed calls so a
// URL (or success phrase) broken across chunk boundaries still matches
// without the buffer growing with the whole session.
const keep = 8 << 10

// Feed consumes one raw output chunk and returns newly recognized
// events, in stream order.
func (p *LinkParser) Feed(chunk []byte) []Event {
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	p.buf = append(p.buf, chunk...)
	if len(p.buf) > keep {
		p.buf = append([]byte{}, p.buf[len(p.buf)-keep:]...)
	}
	clean := stripANSI(string(p.buf))
	clean = joinWrapped(clean, p.Cols)
	clean = joinURLContinuations(clean)

	var evs []Event
	for _, loc := range urlRe.FindAllStringIndex(clean, -1) {
		if loc[1] == len(clean) {
			// Match runs to the end of the buffer — the URL may still be
			// streaming in. Hold it; the next chunk re-matches the full
			// URL (login CLIs always follow the link with a newline).
			continue
		}
		u := strings.TrimRight(clean[loc[0]:loc[1]], ".,;:!)]}>'\"")
		if u == "" || p.seen["l:"+u] {
			continue
		}
		p.seen["l:"+u] = true
		evs = append(evs, Event{Type: EventLink, Value: u})
	}

	lower := strings.ToLower(clean)
	for _, needle := range successNeedles {
		if !strings.Contains(lower, needle) || p.seen["s:"+needle] {
			continue
		}
		p.seen["s:"+needle] = true
		evs = append(evs, Event{Type: EventSuccess, Value: lineAround(clean, lower, needle)})
	}
	for _, needle := range failureNeedles {
		if !strings.Contains(lower, needle) || p.seen["f:"+needle] {
			continue
		}
		p.seen["f:"+needle] = true
		evs = append(evs, Event{Type: EventFailure, Value: lineAround(clean, lower, needle)})
	}
	return evs
}

// stripANSI produces the matching copy of raw terminal output. Ink
// positions text with cursor moves instead of literal whitespace, so
// dropping every sequence glues adjacent words ("Paste code here" →
// "Pastecodehere") and defeats the URL-wrap heuristics. Cursor-forward
// becomes spaces, vertical/absolute cursor moves become a line break,
// everything else is removed.
func stripANSI(s string) string {
	s = oscRe.ReplaceAllString(s, "")
	s = csiRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := csiRe.FindStringSubmatch(m)
		params, final := sub[1], sub[2]
		switch final {
		case "C": // cursor forward = horizontal gap
			n := 1
			if v, err := strconv.Atoi(params); err == nil && v > 0 {
				n = v
			}
			if n > 8 {
				n = 8 // it's a word gap marker, not layout fidelity
			}
			return strings.Repeat(" ", n)
		case "B", "E", "F", "H", "f", "d": // vertical / absolute move = new line
			return "\n"
		default:
			return ""
		}
	})
	return escRe.ReplaceAllString(s, "")
}

// joinWrapped merges lines that are exactly cols wide with their
// successor. ConPTY hard-wraps at the terminal width by injecting
// literal \r\n into the output stream, so a long URL arrives as
// consecutive full-width lines. Lines shorter than cols keep their
// breaks. cols <= 0 disables joining.
func joinWrapped(clean string, cols int) string {
	if cols <= 0 {
		return clean
	}
	lines := strings.Split(strings.ReplaceAll(clean, "\r\n", "\n"), "\n")
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(line)
		if i == len(lines)-1 {
			break
		}
		if len([]rune(line)) != cols {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// joinURLContinuations removes a line break that lands INSIDE a URL:
// whenever a URL match runs to the end of a line and the next line
// starts immediately with more URL-looking text (no blank line, no
// leading space), the break is dropped and the scan repeats. This is
// how ink-style TUIs wrap long OAuth links — the exact wrap width is
// not knowable from the stream, so the terminator (a blank line or a
// space-indented line) is the signal instead.
func joinURLContinuations(clean string) string {
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	for i := 0; i < 32; i++ {
		joined := false
		for _, loc := range urlRe.FindAllStringIndex(clean, -1) {
			end := loc[1]
			if end >= len(clean) || clean[end] != '\n' {
				continue
			}
			next := end + 1
			if next >= len(clean) {
				continue
			}
			c := clean[next]
			if c <= ' ' || c == '\n' {
				continue // blank/indented next line = URL block ended
			}
			nextLine := clean[next:]
			if j := strings.IndexByte(nextLine, '\n'); j >= 0 {
				nextLine = nextLine[:j]
			}
			// A wrap continuation is a bare URL fragment: no spaces (a
			// sentence has them) and not a fresh URL of its own (a TUI
			// re-render repeats the whole link on the next line).
			if strings.ContainsAny(nextLine, " \t") || strings.HasPrefix(nextLine, "https://") {
				continue
			}
			clean = clean[:end] + clean[next:]
			joined = true
			break
		}
		if !joined {
			break
		}
	}
	return clean
}

// lineAround extracts the (cleaned) line containing the first match of
// needle, for display in the UI.
func lineAround(clean, lower, needle string) string {
	i := strings.Index(lower, needle)
	if i < 0 {
		return needle
	}
	start := strings.LastIndexAny(clean[:i], "\r\n") + 1
	end := len(clean)
	if j := strings.IndexAny(clean[i:], "\r\n"); j >= 0 {
		end = i + j
	}
	return strings.TrimSpace(clean[start:end])
}

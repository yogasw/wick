package usagereport

import (
	"regexp"
	"strings"
)

// TextMatch narrows to conversations whose title or session id matches a
// pattern. Chips answer "which account, which model"; this answers "which
// use case" — every conversation one workflow opens shares a title shape
// ("daily-2026-10-…", "[HANDOVER] …"), so a pattern over the title is how
// a reader follows one use case across days and sees it grow or shrink.
//
// The zero value matches everything.
type TextMatch struct {
	Pattern string
	// In is where to look: "title", "session" (the id) or "any" (either).
	In string
	// Mode is how: "contains", "exact" or "regex". Case never matters —
	// a title typed by a person is not something to match case-exactly.
	Mode string
	// Err is set when the regex does not compile. Such a match keeps
	// nothing: falling back to "everything" would print the unfiltered
	// totals under a filter the reader believes is applied.
	Err string

	re *regexp.Regexp
}

// Match fields and modes, as the page sends them.
const (
	MatchInTitle   = "title"
	MatchInSession = "session"
	MatchInAny     = "any"

	MatchContains = "contains"
	MatchExact    = "exact"
	MatchRegex    = "regex"
)

// maxPattern bounds what a query string can make the server compile.
const maxPattern = 500

// ParseTextMatch builds a TextMatch from the query values. Unknown
// fields and modes fall back to the defaults (title, contains) rather
// than to an error: a stale link should still open.
func ParseTextMatch(pattern, in, mode string) TextMatch {
	t := TextMatch{Pattern: strings.TrimSpace(pattern), In: in, Mode: mode}
	if t.Pattern == "" {
		return TextMatch{}
	}
	if len(t.Pattern) > maxPattern {
		t.Pattern = t.Pattern[:maxPattern]
	}
	switch t.In {
	case MatchInTitle, MatchInSession, MatchInAny:
	default:
		t.In = MatchInTitle
	}
	switch t.Mode {
	case MatchContains, MatchExact, MatchRegex:
	default:
		t.Mode = MatchContains
	}
	if t.Mode == MatchRegex {
		re, err := regexp.Compile("(?i)" + t.Pattern)
		if err != nil {
			t.Err = err.Error()
		} else {
			t.re = re
		}
	}
	return t
}

// Empty reports whether this match narrows anything.
func (t TextMatch) Empty() bool { return t.Pattern == "" }

// Keep reports whether a session with this id and title passes.
func (t TextMatch) Keep(id, title string) bool {
	if t.Empty() {
		return true
	}
	if t.Err != "" {
		return false
	}
	switch t.In {
	case MatchInSession:
		return t.hit(id)
	case MatchInAny:
		return t.hit(title) || t.hit(id)
	default:
		return t.hit(title)
	}
}

func (t TextMatch) hit(s string) bool {
	switch t.Mode {
	case MatchExact:
		return strings.EqualFold(strings.TrimSpace(s), t.Pattern)
	case MatchRegex:
		return t.re != nil && t.re.MatchString(s)
	default:
		return strings.Contains(strings.ToLower(s), strings.ToLower(t.Pattern))
	}
}

// key identifies the match in the ledger cache.
func (t TextMatch) key() string {
	if t.Empty() {
		return ""
	}
	return "|tx:" + t.In + ":" + t.Mode + ":" + t.Pattern
}

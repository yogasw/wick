package usagereport

import (
	"regexp"
	"strings"
)

// TextMatch is one condition of the page's condition builder: a field of
// the conversation (its title, id, model, channel…), an operator and a
// value. Chips answer "which account, which model"; conditions answer
// "which use case" — every conversation one workflow opens shares a title
// shape ("daily-2026-10-…"), so "title contains X AND model is Y" is how a
// reader follows one use case across days and sees it grow or shrink.
//
// The zero value matches everything.
type TextMatch struct {
	Pattern string
	// In is the field to read: one of the MatchIn* constants. "any" is
	// the title or the session id.
	In string
	// Mode is how: "contains", "exact" or "regex". Case never matters —
	// a title typed by a person is not something to match case-exactly.
	Mode string
	// Not inverts the rule: "not contains", "not exact", "not regex".
	// Sent as the mode with a "not_" prefix.
	Not bool
	// Err is set when the regex does not compile. Such a match keeps
	// nothing: falling back to "everything" would print the unfiltered
	// totals under a filter the reader believes is applied.
	Err string

	re *regexp.Regexp
}

// Condition fields and modes, as the page sends them.
const (
	MatchInTitle    = "title"
	MatchInSession  = "session"
	MatchInAny      = "any"
	MatchInModel    = "model"
	MatchInProvider = "provider"
	MatchInPerson   = "person"
	MatchInChannel  = "channel"
	MatchInProject  = "project"
	MatchInAgent    = "agent"

	MatchContains = "contains"
	MatchExact    = "exact"
	MatchRegex    = "regex"
)

// notPrefix turns a mode into its negation on the wire: "not_contains".
const notPrefix = "not_"

// maxPattern bounds what a query string can make the server compile.
const maxPattern = 500

// Subject is what a condition can read off one conversation. The list
// fields hold every value the conversation had — a session that switched
// model ran on both — and a condition hits when ANY of them does, so
// "model is X" keeps it and "model is not X" drops it.
type Subject struct {
	ID, Title, Channel, Project, Agent string
	Models, Providers, People          []string
}

// ParseTextMatch builds a TextMatch from the query values. Unknown
// fields and modes fall back to the defaults (title, contains) rather
// than to an error: a stale link should still open.
func ParseTextMatch(pattern, in, mode string) TextMatch {
	t := TextMatch{Pattern: strings.TrimSpace(pattern), In: in, Mode: mode}
	if rest, ok := strings.CutPrefix(t.Mode, notPrefix); ok {
		t.Not, t.Mode = true, rest
	}
	if t.Pattern == "" {
		return TextMatch{}
	}
	if len(t.Pattern) > maxPattern {
		t.Pattern = t.Pattern[:maxPattern]
	}
	if !knownIn(t.In) {
		t.In = MatchInTitle
	}
	if !knownMode(t.Mode) {
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

// Keep reports whether a session with this id and title passes. The
// short form for a rule over the title or the id.
func (t TextMatch) Keep(id, title string) bool {
	return t.keep(Subject{ID: id, Title: title}, nil)
}

func (t TextMatch) keep(s Subject, alias func(field, key string) []string) bool {
	if t.Empty() {
		return true
	}
	if t.Err != "" {
		return false
	}
	hit := false
	for _, v := range t.values(s, alias) {
		if t.hit(v) {
			hit = true
			break
		}
	}
	return hit != t.Not
}

// values is what the rule's field holds for one conversation. People and
// projects are stored by id; alias adds the names a reader would type.
// An empty list reads as the bucket the chips use for it, so "model is
// (none)" means the same thing in both places.
func (t TextMatch) values(s Subject, alias func(field, key string) []string) []string {
	var keys []string
	switch t.In {
	case MatchInSession:
		return []string{s.ID}
	case MatchInAny:
		return []string{s.Title, s.ID}
	case MatchInChannel:
		return []string{s.Channel}
	case MatchInAgent:
		return []string{s.Agent}
	case MatchInModel:
		return orNone(s.Models, NoProvider)
	case MatchInProvider:
		return orNone(s.Providers, NoProvider)
	case MatchInPerson:
		keys = orNone(s.People, NoPerson)
	case MatchInProject:
		keys = []string{s.Project}
	default:
		return []string{s.Title}
	}
	if alias == nil {
		return keys
	}
	out := keys
	for _, k := range keys {
		out = append(out, alias(t.In, k)...)
	}
	return out
}

func orNone(list []string, none string) []string {
	if len(list) == 0 {
		return []string{none}
	}
	return list
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

// key identifies one rule in the ledger cache.
func (t TextMatch) key() string {
	mode := t.Mode
	if t.Not {
		mode = notPrefix + mode
	}
	return t.In + ":" + mode + ":" + t.Pattern
}

// TextMatches is several conditions joined by AND (every one must hold)
// or OR (any one is enough) — "title contains daily-2026-10 AND title
// contains shift-1 AND model is not haiku" without writing a regex.
// The zero value matches everything.
type TextMatches struct {
	Rules []TextMatch
	// Any joins the rules with OR; false joins them with AND.
	Any bool
	// Alias, when set, names an id-valued field ("person", "project") so
	// a condition can be written with the name a reader sees on the page.
	// Not part of the cache key: it only resolves, never narrows.
	Alias func(field, key string) []string
}

// maxRules bounds how many rules a query string can stack.
const maxRules = 10

// ParseTextMatches reads the rules off the query. Each "match" value is
// "<field>:<mode>:<pattern>"; a value without that prefix is the older
// single-rule form, read with match_in / match_mode as before, so a link
// shared before rules could be stacked still opens the same view.
// matchOp "or" joins with OR, anything else with AND.
func ParseTextMatches(values []string, legacyIn, legacyMode, matchOp string) TextMatches {
	out := TextMatches{Any: strings.EqualFold(strings.TrimSpace(matchOp), "or")}
	for _, raw := range values {
		if len(out.Rules) == maxRules {
			break
		}
		in, mode, pattern := legacyIn, legacyMode, raw
		if a, rest, ok := strings.Cut(raw, ":"); ok && knownIn(a) {
			if b, p, ok := strings.Cut(rest, ":"); ok && knownMode(b) {
				in, mode, pattern = a, b, p
			}
		}
		if t := ParseTextMatch(pattern, in, mode); !t.Empty() {
			out.Rules = append(out.Rules, t)
		}
	}
	return out
}

func knownIn(s string) bool {
	switch s {
	case MatchInTitle, MatchInSession, MatchInAny, MatchInModel, MatchInProvider,
		MatchInPerson, MatchInChannel, MatchInProject, MatchInAgent:
		return true
	}
	return false
}

func knownMode(s string) bool {
	s = strings.TrimPrefix(s, notPrefix)
	return s == MatchContains || s == MatchExact || s == MatchRegex
}

// Empty reports whether any rule narrows anything.
func (m TextMatches) Empty() bool { return len(m.Rules) == 0 }

// Err is the first rule whose regex did not compile, "" when none. Such
// a set keeps nothing, whichever way it is joined: an OR that quietly
// dropped the broken rule would answer a narrower question than the one
// on screen.
func (m TextMatches) Err() string {
	for _, r := range m.Rules {
		if r.Err != "" {
			return r.Err
		}
	}
	return ""
}

// Keep reports whether a session with this id and title passes.
func (m TextMatches) Keep(id, title string) bool {
	return m.KeepSubject(Subject{ID: id, Title: title})
}

// KeepSubject reports whether one conversation passes every condition
// (AND) or any of them (OR).
func (m TextMatches) KeepSubject(s Subject) bool {
	if m.Empty() {
		return true
	}
	if m.Err() != "" {
		return false
	}
	for _, r := range m.Rules {
		hit := r.keep(s, m.Alias)
		if m.Any && hit {
			return true
		}
		if !m.Any && !hit {
			return false
		}
	}
	return !m.Any
}

// Values is the rules in their query form, for echoing back.
func (m TextMatches) Values() []string {
	out := make([]string, 0, len(m.Rules))
	for _, r := range m.Rules {
		out = append(out, r.key())
	}
	return out
}

// Op is "and" or "or", as the page sends it.
func (m TextMatches) Op() string {
	if m.Any {
		return "or"
	}
	return "and"
}

// key identifies the set in the ledger cache.
func (m TextMatches) key() string {
	if m.Empty() {
		return ""
	}
	return "|tx:" + m.Op() + ":" + strings.Join(m.Values(), "\x00")
}

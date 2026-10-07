package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Idle-compact triggers: what the threshold counts.
const (
	// IdleCompactPercent: the threshold is a percentage of the window.
	IdleCompactPercent = "percent"
	// IdleCompactTokens: the threshold is tokens in use.
	IdleCompactTokens = "tokens"
)

// Idle-compact defaults, used when the instance leaves a field empty.
const (
	DefaultIdleCompactSeconds = 1800
	DefaultIdleCompactPercent = 40
	DefaultIdleCompactTokens  = 100_000
)

// Idle-compact scopes: which sessions the match list picks.
const (
	// IdleCompactScopeAll compacts every session; the list is ignored.
	IdleCompactScopeAll = "all"
	// IdleCompactScopeAllow compacts only sessions the list matches.
	IdleCompactScopeAllow = "whitelist"
	// IdleCompactScopeSkip compacts every session the list does not match.
	IdleCompactScopeSkip = "skip"
)

// DefaultIdleCompactSkip is the skip list used when the scope is skip and
// the list is empty: one-way REST calls and one-off workflow runs never get
// a next turn, so summarising them only spends tokens.
const DefaultIdleCompactSkip = "id:rest-\nid:wf_adhoc_"

// IdleCompactConfig is the per-instance "compact when idle" section: once a
// session on this instance has sat idle for Seconds and its context is past
// the threshold, wick sends /compact so the next turn starts short.
type IdleCompactConfig struct {
	IdleCompact          bool   `wick:"bool;key=idle_compact;desc=Compact when idle: once a session on this instance has been idle for the seconds below and its context is past the threshold, wick runs /compact so the next message starts on a short context. Once per idle stretch, a running turn is never interrupted."`
	IdleCompactSeconds   int    `wick:"key=idle_compact_seconds;desc=Seconds a session must be idle before it is compacted, e.g. 1800 = 30 minutes. Empty or 0 = 1800."`
	IdleCompactTrigger   string `wick:"key=idle_compact_trigger;dropdown=percent|tokens;desc=What the threshold counts.\npercent — percentage of the model's context window in use.\ntokens — tokens in use, whatever the window size."`
	IdleCompactThreshold string `wick:"key=idle_compact_threshold;desc=Compact only when the context is at least this much. Percent: 40 or 40%. Tokens: the count in use, e.g. 100000 (100k also works). Empty or 0 = 40% / 100000 tokens."`
	IdleCompactScope     string `wick:"key=idle_compact_scope;dropdown=skip|whitelist|all;desc=Which sessions are compacted.\nskip — every session except those the list below matches (empty list = id:rest- and id:wf_adhoc_, one-way REST calls and one-off workflow runs).\nwhitelist — only sessions the list matches.\nall — every session, the list is ignored."`
	IdleCompactMatch     string `wick:"key=idle_compact_match;textarea;desc=Session patterns, one per line, for the scope above. A line can mix methods: plain text = contains (case-insensitive), /pattern/ = regular expression. Prefix project:, project_id:, title: or id: to check only that field (project: is the project NAME, so one list works for everyone sharing this instance). Without a prefix the line is checked against all three. Join conditions with & to require all of them and put ! in front of one to negate it, e.g. project:ygsw & !title:abc."`
}

// IdleCompactPolicy is an instance's idle-compact settings with the
// defaults filled in.
type IdleCompactPolicy struct {
	Enabled   bool
	Idle      time.Duration
	Trigger   string
	Threshold int
	// Scope is IdleCompactScopeAll, IdleCompactScopeAllow or
	// IdleCompactScopeSkip; Match is the list it applies.
	Scope string
	Match []SessionPattern
}

// SessionPattern is one idle_compact_match line: one or more conditions
// joined with &, all of which must hold. A line without & is a single
// condition, so existing lists keep their meaning.
type SessionPattern struct {
	Conds []SessionCond
}

// SessionCond is one condition of a line: a case-insensitive substring, or
// a regular expression when written as /re/, checked against one session
// field (Field) or all of them (Field ""). Neg (a leading !) inverts it.
type SessionCond struct {
	Field string
	Text  string
	Re    *regexp.Regexp
	Neg   bool
}

// SessionRef is what a session pattern can be checked against.
type SessionRef struct {
	ID    string
	Title string
	// Project is the project NAME, ProjectID its id; both "" without one.
	Project   string
	ProjectID string
}

// sessionPatternFields are the prefixes a condition may name. project_id
// comes before project so the longer prefix wins.
var sessionPatternFields = []string{"project_id", "project", "title", "id"}

// Match reports whether every condition of the line holds for a session.
func (p SessionPattern) Match(s SessionRef) bool {
	for _, c := range p.Conds {
		if c.Match(s) == c.Neg {
			return false
		}
	}
	return len(p.Conds) > 0
}

// Match reports whether the condition's text or expression is found,
// before Neg is applied. Without a field it checks id, title and project
// name; a project id is only checked when named.
func (c SessionCond) Match(s SessionRef) bool {
	switch c.Field {
	case "id":
		return c.match(s.ID)
	case "title":
		return c.match(s.Title)
	case "project":
		return c.match(s.Project)
	case "project_id":
		return c.match(s.ProjectID)
	}
	return c.match(s.ID) || c.match(s.Title) || c.match(s.Project)
}

func (c SessionCond) match(s string) bool {
	if s == "" {
		return false
	}
	if c.Re != nil {
		return c.Re.MatchString(s)
	}
	return strings.Contains(strings.ToLower(s), c.Text)
}

// splitConds splits a line on & that sit outside a /regular expression/.
func splitConds(line string) []string {
	var out []string
	inRe, start := false, 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			// An escaped character inside an expression (\/ or \&) never
			// closes it or splits the line.
			if inRe {
				i++
			}
		case '/':
			// A / opens an expression only at the start of a condition
			// (after optional !, field: and spaces); inside one it closes it.
			if inRe {
				inRe = false
			} else if strings.TrimLeft(strings.TrimSpace(condBody(line[start:i])), "!") == "" {
				inRe = true
			}
		case '&':
			if !inRe {
				out = append(out, line[start:i])
				start = i + 1
			}
		}
	}
	return append(out, line[start:])
}

// condBody strips a leading field: prefix (after an optional !).
func condBody(s string) string {
	t := strings.TrimSpace(s)
	neg := strings.HasPrefix(t, "!")
	t = strings.TrimSpace(strings.TrimPrefix(t, "!"))
	for _, f := range sessionPatternFields {
		if rest, ok := strings.CutPrefix(strings.ToLower(t), f+":"); ok {
			t = t[len(t)-len(rest):]
			break
		}
	}
	if neg {
		return "!" + t
	}
	return t
}

// parseCond reads one condition: [!][project:|project_id:|title:|id:]text-or-/re/.
func parseCond(s string) (SessionCond, bool, error) {
	var c SessionCond
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "!"); ok {
		c.Neg = true
		s = strings.TrimSpace(rest)
	}
	for _, f := range sessionPatternFields {
		if rest, ok := strings.CutPrefix(strings.ToLower(s), f+":"); ok {
			c.Field = f
			s = strings.TrimSpace(s[len(s)-len(rest):])
			break
		}
	}
	if s == "" {
		return c, false, nil
	}
	if len(s) > 2 && strings.HasPrefix(s, "/") && strings.HasSuffix(s, "/") {
		re, err := regexp.Compile(s[1 : len(s)-1])
		if err != nil {
			return c, false, fmt.Errorf("idle compact match: bad regular expression %s: %v", s, err)
		}
		c.Re = re
	} else {
		c.Text = strings.ToLower(s)
	}
	return c, true, nil
}

// ParseSessionPatterns reads idle_compact_match: one pattern per line (or
// comma); a pattern is conditions joined with &, each optionally negated
// with ! and prefixed with project:, project_id:, title: or id:.
func ParseSessionPatterns(raw string) ([]SessionPattern, error) {
	var out []SessionPattern
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
		var sp SessionPattern
		for _, part := range splitConds(line) {
			c, ok, err := parseCond(part)
			if err != nil {
				return nil, err
			}
			if ok {
				sp.Conds = append(sp.Conds, c)
			}
		}
		if len(sp.Conds) > 0 {
			out = append(out, sp)
		}
	}
	return out, nil
}

// Skips reports whether the session is left alone by the scope.
func (p IdleCompactPolicy) Skips(s SessionRef) bool {
	if p.Scope == IdleCompactScopeAll {
		return false
	}
	matched := p.MatchedRule(s) >= 0
	if p.Scope == IdleCompactScopeAllow {
		return !matched
	}
	return matched
}

// MatchedRule returns the index of the first pattern line that matches the
// session, -1 when none does.
func (p IdleCompactPolicy) MatchedRule(s SessionRef) int {
	for i, sp := range p.Match {
		if sp.Match(s) {
			return i
		}
	}
	return -1
}

// IdleCompactMatchLines splits an idle_compact_match value into its
// non-empty pattern lines, in the order ParseSessionPatterns reads them.
func IdleCompactMatchLines(raw string) []string {
	var out []string
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ',' }) {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// IdleCompactPolicyOf resolves ins's idle-compact settings.
func IdleCompactPolicyOf(ins Instance) IdleCompactPolicy {
	// A provider that cannot act on /compact is never sent one: the
	// model would only say it compacted.
	p := IdleCompactPolicy{Enabled: ins.IdleCompact && CanCompact(ins.Type), Trigger: ins.IdleCompactTrigger, Threshold: ins.IdleCompactThreshold}
	p.Idle = time.Duration(IdleCompactSecondsOf(ins)) * time.Second
	// A bad pattern was refused on save, so an error here means a
	// hand-edited config: skip nothing extra rather than stop compacting.
	p.Scope = ins.IdleCompactScope
	if p.Scope != IdleCompactScopeAll && p.Scope != IdleCompactScopeAllow {
		p.Scope = IdleCompactScopeSkip
	}
	raw := ins.IdleCompactMatch
	if p.Scope == IdleCompactScopeSkip && strings.TrimSpace(raw) == "" {
		raw = DefaultIdleCompactSkip
	}
	p.Match, _ = ParseSessionPatterns(raw)
	if p.Trigger != IdleCompactTokens {
		p.Trigger = IdleCompactPercent
	}
	if p.Trigger == IdleCompactTokens && p.Threshold > 0 && p.Threshold < 1000 {
		// Saved before the threshold took plain tokens, when it counted
		// thousands: 100 meant 100k.
		p.Threshold *= 1000
	}
	if p.Threshold <= 0 {
		if p.Trigger == IdleCompactTokens {
			p.Threshold = DefaultIdleCompactTokens
		} else {
			p.Threshold = DefaultIdleCompactPercent
		}
	}
	return p
}

// Due reports whether a session idle for idleFor, with used of window
// tokens in its context, should be compacted now. A percent trigger needs
// a known window; with none it never fires rather than guessing.
func (p IdleCompactPolicy) Due(used, window int, idleFor time.Duration) bool {
	if !p.Enabled || used <= 0 || idleFor < p.Idle {
		return false
	}
	if p.Trigger == IdleCompactTokens {
		return used >= p.Threshold
	}
	if window <= 0 {
		return false
	}
	return float64(used)*100 >= float64(p.Threshold)*float64(window)
}

// IdleCompactSecondsOf is how long ins's sessions must sit idle, in
// seconds: the seconds setting, else the older minutes one, else the default.
func IdleCompactSecondsOf(ins Instance) int {
	if ins.IdleCompactSeconds > 0 {
		return ins.IdleCompactSeconds
	}
	if ins.IdleCompactMinutes > 0 {
		return ins.IdleCompactMinutes * 60
	}
	return DefaultIdleCompactSeconds
}

// ParseIdleCompactThreshold reads a threshold as typed: a whole number,
// optionally ending in % (percent) or k (thousands of tokens), so 40,
// 40%, 100000 and 100k all work. Empty is 0, the default.
func ParseIdleCompactThreshold(value string) (int, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, nil
	}
	mult := 1
	if rest, ok := strings.CutSuffix(v, "%"); ok {
		v = rest
	} else if rest, ok := strings.CutSuffix(strings.ToLower(v), "k"); ok {
		v, mult = rest, 1000
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("idle compact threshold must be a number like 40, 40%%, 100000 or 100k, got %q", value)
	}
	return n * mult, nil
}

// FormatIdleCompactThreshold shows a saved threshold the way it is typed:
// 40% for percent, the plain token count for tokens, "" when unset.
func FormatIdleCompactThreshold(trigger string, n int) string {
	if n <= 0 {
		return ""
	}
	if trigger != IdleCompactTokens {
		return strconv.Itoa(n) + "%"
	}
	if n < 1000 {
		n *= 1000
	}
	return strconv.Itoa(n)
}

// IsIdleCompactKey reports whether key is one of the idle-compact settings.
func IsIdleCompactKey(key string) bool {
	return key == "idle_compact" || strings.HasPrefix(key, "idle_compact_")
}

func validateIdleCompactKey(key, value string) error {
	v := strings.TrimSpace(value)
	switch key {
	case "idle_compact_seconds":
		if v == "" {
			return nil
		}
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			return fmt.Errorf("%s must be a whole number, got %q", key, v)
		}
	case "idle_compact_threshold":
		_, err := ParseIdleCompactThreshold(v)
		return err
	case "idle_compact_trigger":
		if v != "" && v != IdleCompactPercent && v != IdleCompactTokens {
			return fmt.Errorf("idle compact trigger must be percent or tokens, got %q", v)
		}
	case "idle_compact_scope":
		if v != "" && v != IdleCompactScopeAll && v != IdleCompactScopeAllow && v != IdleCompactScopeSkip {
			return fmt.Errorf("idle compact scope must be skip, whitelist or all, got %q", v)
		}
	case "idle_compact_match":
		_, err := ParseSessionPatterns(v)
		return err
	}
	return nil
}

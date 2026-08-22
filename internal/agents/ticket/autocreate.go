package ticket

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// Match prefixes an AutoCreateRule.Match can carry.
const (
	matchContains = "contains:"
	matchRegex    = "regex:"
)

// titleMaxRunes caps an auto-generated ticket title. Titles are read on a
// board card, so one long first message must not stretch a column.
const titleMaxRunes = 60

// AutoCreateInput is what a rule is judged against: where the session came
// from and what it opened with.
type AutoCreateInput struct {
	// Origin is the session's origin ("ui", "slack", "telegram", "rest", …).
	Origin string
	// ChannelKind is "dm", "channel", or "thread" for channel origins;
	// empty elsewhere.
	ChannelKind string
	// FirstMessage is the text the session opened with. Empty when it is
	// not known yet, in which case a rule carrying a text condition cannot
	// fire — it waits rather than guessing.
	FirstMessage string
	// IsSubAgent marks a delegated child's session. Those are working
	// contexts, not somebody's piece of work, so they never get a ticket.
	IsSubAgent bool
	// AlreadyTicketed short-circuits everything: a session on a ticket is
	// left alone.
	AlreadyTicketed bool
}

// ValidateAutoCreate checks a rule list before it is stored. A rule that
// cannot fire (an origin nobody sets, a regex that will not compile) is
// refused here rather than sitting in the config looking active.
func ValidateAutoCreate(rules []project.AutoCreateRule) error {
	for i, r := range rules {
		where := fmt.Sprintf("rule %d", i+1)
		if strings.TrimSpace(r.Origin) == "" {
			return fmt.Errorf("%s: origin is required (use \"*\" for any)", where)
		}
		switch r.ChannelKind {
		case project.ChannelKindAny, project.ChannelKindDM,
			project.ChannelKindChannel, project.ChannelKindThread:
		default:
			return fmt.Errorf("%s: channel_kind %q is not one of dm, channel, thread", where, r.ChannelKind)
		}
		m := strings.TrimSpace(r.Match)
		switch {
		case m == "":
		case strings.HasPrefix(m, matchContains):
			if strings.TrimSpace(strings.TrimPrefix(m, matchContains)) == "" {
				return fmt.Errorf("%s: match \"contains:\" needs some text after the colon", where)
			}
		case strings.HasPrefix(m, matchRegex):
			expr := strings.TrimPrefix(m, matchRegex)
			if strings.TrimSpace(expr) == "" {
				return fmt.Errorf("%s: match \"regex:\" needs an expression after the colon", where)
			}
			if _, err := regexp.Compile(expr); err != nil {
				return fmt.Errorf("%s: match regex does not compile: %w", where, err)
			}
		default:
			return fmt.Errorf("%s: match must start with %q or %q", where, matchContains, matchRegex)
		}
	}
	return nil
}

// ShouldAutoCreate reports whether a session should be given a ticket, and
// which rule decided it. Rules are tried in order and the FIRST match wins,
// so a narrow exception placed above a broad rule carves a hole in it —
// that is how "everything from Slack except DMs" is expressed.
//
// A rule whose Match cannot be judged yet (a text condition with no message
// to test) does not fire. Waiting is the safe direction: a ticket created
// from the wrong session is worse than one created a turn later.
func ShouldAutoCreate(rules []project.AutoCreateRule, in AutoCreateInput) (bool, project.AutoCreateRule) {
	if in.AlreadyTicketed || in.IsSubAgent {
		return false, project.AutoCreateRule{}
	}
	for _, r := range rules {
		if !matchesRule(r, in) {
			continue
		}
		// A matched-but-disabled rule still consumes the decision, which
		// is what makes it usable as an exception above a broader rule.
		if !r.Enabled {
			return false, project.AutoCreateRule{}
		}
		return true, r
	}
	return false, project.AutoCreateRule{}
}

// matchesRule reports whether a rule's conditions describe this session,
// ignoring whether the rule is enabled.
func matchesRule(r project.AutoCreateRule, in AutoCreateInput) bool {
	origin := strings.TrimSpace(r.Origin)
	if origin != "*" && !strings.EqualFold(origin, in.Origin) {
		return false
	}
	if r.ChannelKind != project.ChannelKindAny && !strings.EqualFold(r.ChannelKind, in.ChannelKind) {
		return false
	}
	m := strings.TrimSpace(r.Match)
	if m == "" {
		return true
	}
	if in.FirstMessage == "" {
		return false // nothing to test yet
	}
	switch {
	case strings.HasPrefix(m, matchContains):
		needle := strings.TrimPrefix(m, matchContains)
		return strings.Contains(strings.ToLower(in.FirstMessage), strings.ToLower(needle))
	case strings.HasPrefix(m, matchRegex):
		re, err := regexp.Compile(strings.TrimPrefix(m, matchRegex))
		if err != nil {
			return false // refused at save time; treat as inert here
		}
		return re.MatchString(in.FirstMessage)
	}
	return false
}

// AutoCreateTitle renders the title for an auto-created ticket. The rule's
// template wins when set; otherwise the first message is the title, because
// that is what a person scanning the board would have written anyway.
func AutoCreateTitle(r project.AutoCreateRule, in AutoCreateInput) string {
	msg := strings.TrimSpace(strings.ReplaceAll(in.FirstMessage, "\n", " "))
	tpl := strings.TrimSpace(r.Title)
	var out string
	switch {
	case tpl != "":
		out = strings.ReplaceAll(tpl, "{message}", msg)
		out = strings.ReplaceAll(out, "{origin}", in.Origin)
	case msg != "":
		out = msg
	default:
		// No message yet: name it after where it came from rather than
		// leaving a blank card nobody can identify.
		origin := in.Origin
		if origin == "" {
			origin = "wick"
		}
		out = "New " + origin + " session"
	}
	out = strings.TrimSpace(out)
	if r := []rune(out); len(r) > titleMaxRunes {
		out = strings.TrimSpace(string(r[:titleMaxRunes-1])) + "…"
	}
	return out
}

// AutoCreateDeps are what EvaluateAutoCreate needs from outside: the
// project's rules and a way to record the new ticket on the session.
type AutoCreateDeps struct {
	Layout config.Layout
	// LoadRules returns a project's auto-create rules.
	LoadRules func(projectID string) ([]project.AutoCreateRule, error)
	// WriteBackPointer keeps session.Meta.TicketID in step. Optional.
	WriteBackPointer func(sessionID, ticketID string)
}

// EvaluateAutoCreate gives a session a ticket when the project's rules say
// so, and returns the ticket it created (or the zero value when nothing
// matched). Safe to call on every first message: it is a no-op for projects
// with no rules, and a session already on a ticket is left alone.
//
// The session is attached to the new ticket, which also carries any notes it
// already accumulated onto it.
func EvaluateAutoCreate(d AutoCreateDeps, projectID, sessionID string, in AutoCreateInput) (Ticket, bool) {
	if projectID == "" || sessionID == "" || d.LoadRules == nil {
		return Ticket{}, false
	}
	if _, already := FindBySession(d.Layout, projectID, sessionID); already {
		return Ticket{}, false
	}
	rules, err := d.LoadRules(projectID)
	if err != nil || len(rules) == 0 {
		return Ticket{}, false
	}
	ok, matched := ShouldAutoCreate(rules, in)
	if !ok {
		return Ticket{}, false
	}
	tk, err := Create(d.Layout, CreateOptions{
		ProjectID: projectID,
		Title:     AutoCreateTitle(matched, in),
	})
	if err != nil {
		return Ticket{}, false
	}
	// Attach rather than seeding Sessions, so the notes this session may
	// already hold travel onto the ticket through the usual path.
	if aerr := AttachSession(d.Layout, projectID, tk.ID, sessionID); aerr != nil {
		return Ticket{}, false
	}
	if d.WriteBackPointer != nil {
		d.WriteBackPointer(sessionID, tk.ID)
	}
	tk.Sessions = []string{sessionID}
	return tk, true
}

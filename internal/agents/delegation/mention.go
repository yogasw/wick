package delegation

import "strings"

// Mention is one @handle directive found in an agent's text.
type Mention struct {
	Handle string
	Body   string
}

// ParseMentions finds mentions in an agent's final text.
//
// Deliberately strict: line-leading only, the handle must already exist
// in the tree's roster, and fenced code is skipped.
//
// Agent output is full of @ tokens that are not mentions — @media,
// @ts-ignore, @scope/pkg, decorators, email addresses. The asymmetry
// decides the rule: a missed mention costs one clarifying turn, while a
// false one spawns an agent and spends tokens because the model happened
// to write an email address. When a candidate is not certain, it stays
// plain text.
func ParseMentions(text string, roster []string) []Mention {
	known := make(map[string]bool, len(roster))
	for _, h := range roster {
		known[h] = true
	}
	var out []Mention
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "@") {
			continue
		}
		rest := trimmed[1:]
		cut := strings.IndexAny(rest, " \t")
		if cut <= 0 {
			continue
		}
		handle, body := rest[:cut], strings.TrimSpace(rest[cut:])
		if body == "" || !known[handle] {
			continue
		}
		out = append(out, Mention{Handle: handle, Body: body})
	}
	return out
}

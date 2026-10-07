// Package actioncard parses the ```actioncard fences agents write and
// renders them for chat surfaces without the web UI's card component:
// Block Kit for Slack (see channels/slack), a numbered list for plain
// text channels. Kept free of the tools and channel packages so both can
// import it.
package actioncard

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Card is the JSON inside an ```actioncard fence. A later card with the
// same ID replaces the earlier one in the thread.
type Card struct {
	ID       string     `json:"id"`
	Icon     string     `json:"icon,omitempty"`
	Title    string     `json:"title"`
	Subtitle string     `json:"subtitle,omitempty"`
	Status   string     `json:"status,omitempty"`
	Rows     [][]string `json:"rows,omitempty"`
	Actions  []Button   `json:"actions,omitempty"`
	// Final locks the card's buttons: the decision it asked for is made.
	Final bool `json:"final,omitempty"`
}

// Button is one button of a card. Clicking it sends Value back to the
// agent as a postback.
type Button struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Style string `json:"style,omitempty"`
}

// Parse returns every well-formed card fence in text, in order. A fence
// whose JSON does not parse, that has no id, or that is never closed is
// not a card.
func Parse(text string) []Card {
	_, cards := Split(text, nil)
	return cards
}

// Split returns text with each card fence replaced by render(card) (nil
// keeps the fence as written) and the cards, in order. A fence that is
// not a card is left alone.
func Split(text string, render func(Card) string) (string, []Card) {
	var out []Card
	lines := strings.Split(text, "\n")
	var kept []string
	for i := 0; i < len(lines); i++ {
		open := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(open, "```actioncard") || strings.TrimSpace(strings.TrimPrefix(open, "```actioncard")) != "" {
			kept = append(kept, lines[i])
			continue
		}
		var body []string
		j := i + 1
		for ; j < len(lines) && strings.TrimSpace(lines[j]) != "```"; j++ {
			body = append(body, lines[j])
		}
		if j == len(lines) {
			kept = append(kept, lines[i:]...) // unterminated: not a card
			break
		}
		var c Card
		if json.Unmarshal([]byte(strings.Join(body, "\n")), &c) != nil || strings.TrimSpace(c.ID) == "" {
			kept = append(kept, lines[i:j+1]...)
			i = j
			continue
		}
		out = append(out, c)
		if render == nil {
			kept = append(kept, lines[i:j+1]...)
		} else if r := render(c); r != "" {
			kept = append(kept, r)
		}
		i = j
	}
	return strings.Join(kept, "\n"), out
}

// PlainText renders c for a channel without buttons: title, subtitle,
// rows and the actions as a numbered list the user answers with the
// number. A final card lists no actions.
func PlainText(c Card) string {
	var b strings.Builder
	b.WriteString("*" + strings.TrimSpace(c.Title) + "*")
	if c.Status != "" {
		b.WriteString(" [" + c.Status + "]")
	}
	if c.Subtitle != "" {
		b.WriteString("\n" + c.Subtitle)
	}
	for _, r := range c.Rows {
		if len(r) >= 2 {
			b.WriteString("\n• " + r[0] + ": " + strings.ReplaceAll(r[1], "\n", "; "))
		} else if len(r) == 1 {
			b.WriteString("\n• " + r[0])
		}
	}
	if !c.Final && len(c.Actions) > 0 {
		b.WriteString("\n")
		for i, a := range c.Actions {
			fmt.Fprintf(&b, "\n%d. %s", i+1, a.Label)
		}
		b.WriteString("\nReply with the number.")
	}
	return b.String()
}


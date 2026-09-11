// Package todoprompt puts a session's unfinished todo list in front of the
// agent at spawn time.
//
// The list is written by the todo tool and shown to the human in the rail,
// but a new subprocess starts with none of that: the previous spawn's
// reasoning is gone, and a checklist left half-ticked looks, from the inside,
// exactly like no checklist at all. The agent then either starts a fresh list
// (so the panel jumps to something unrelated) or simply drops the work it had
// planned — which the person watching reads as the work being abandoned.
//
// So the open list travels with the spawn. Nothing is injected when there is
// nothing outstanding: a finished list is history, and a prompt that repeats
// it every turn spends context on work already done.
package todoprompt

import (
	"fmt"
	"strings"

	config "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// maxItems caps how much of a long list is quoted. A checklist is a plan, not
// a document; past a dozen lines it stops being a reminder and starts being a
// second prompt.
const maxItems = 12

// Pointer renders the session's unfinished checklist as a prompt block, or ""
// when there is nothing to say.
func Pointer(layout config.Layout, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	rec, err := session.LoadTodos(layout, sessionID)
	if err != nil || rec == nil || rec.Active == nil {
		return ""
	}
	list := rec.Active
	if list.Done || len(list.Items) == 0 {
		return ""
	}

	done := 0
	for _, it := range list.Items {
		if it.Status == "completed" {
			done++
		}
	}
	// Everything ticked but not flagged done is still nothing to chase.
	if done == len(list.Items) {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Your todo list (unfinished)\n\n")
	fmt.Fprintf(&b, "You wrote this in an earlier turn of THIS session and %d of %d items are done. "+
		"It is on screen for the person you are working with, so a list that stops moving reads as work that stopped.\n\n",
		done, len(list.Items))

	shown := 0
	for _, it := range list.Items {
		if shown == maxItems {
			fmt.Fprintf(&b, "… and %d more\n", len(list.Items)-shown)
			break
		}
		fmt.Fprintf(&b, "%s %s\n", mark(it.Status), it.Label())
		for _, sub := range it.Substeps {
			fmt.Fprintf(&b, "    %s %s\n", mark(sub.Status), sub.Step)
		}
		shown++
	}

	b.WriteString("\nCarry it on with the todo tool rather than starting a new list: send the FULL list " +
		"each time with the SAME titles, moving statuses as you go. A call that shares no item with this one " +
		"is treated as a new checklist and files this one away as abandoned.")
	return b.String()
}

func mark(status string) string {
	switch status {
	case "completed":
		return "[x]"
	case "in_progress":
		return "[~]"
	default:
		return "[ ]"
	}
}

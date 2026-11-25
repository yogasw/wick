package accessreq

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// turnNoteWindow is how long a finished request stays in the turn note.
const turnNoteWindow = 24 * time.Hour

// turnNoteMax caps the requests listed in the turn note.
const turnNoteMax = 8

// TurnNote is the short block a user turn of the session opens with: the
// session's access requests that are open or finished recently, one line
// each (key · id · version · status · outcome). It keeps the agent aware
// of what it already asked for even after its context was compacted, so
// it replaces a step (same key) instead of filing a second card. "" when
// there is nothing to say, so a session that never asked pays nothing.
func (s *Service) TurnNote(ctx context.Context, sessionID string) string {
	if sessionID == "" || !s.Enabled(sessionID) {
		return ""
	}
	reqs, err := s.store.sessionRequests(ctx, sessionID, 30)
	if err != nil || len(reqs) == 0 {
		return ""
	}
	cutoff := s.now().Add(-turnNoteWindow)
	var lines []string
	for i := range reqs {
		r := reqs[i]
		if r.Status != StatusPending && r.UpdatedAt.Before(cutoff) {
			continue
		}
		if r.Status == StatusSuperseded {
			continue // its replacement is listed
		}
		v, err := s.ViewFor(ctx, r.ID, "")
		if err != nil {
			continue
		}
		lines = append(lines, turnNoteLine(v))
		if len(lines) == turnNoteMax {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "[access requests] This session's access requests (newest first):\n" + strings.Join(lines, "\n") +
		"\nTo change a step, file wick_request_access again with the same key (an untouched card is replaced); to order steps, pass after. action=list shows details."
}

func turnNoteLine(v View) string {
	name := "(no key)"
	if v.Key != "" {
		name = v.Key
	}
	line := fmt.Sprintf("- %s · %s · v%d · %s", name, short(v.ID), v.Version, v.Status)
	if c := countsLine(v.Counts); c != "" {
		line += " · " + c
	}
	if v.AfterRef != nil && v.Status == StatusPending {
		line += fmt.Sprintf(" · after %s (%s)", v.AfterRef.Name(), v.AfterState)
	}
	for _, it := range v.Items {
		if it.Status == ItemError {
			line += fmt.Sprintf(" · stopped at action %d: %s", it.Idx+1, truncate(it.Error, 120))
			break
		}
	}
	return line
}

package agents

import (
	"fmt"
	"net/http"
	"time"

	"github.com/yogasw/wick/pkg/tool"
)

// sessionsLifecycleSSE handles GET /stream/sessions — the lifecycle of
// every conversation the caller may see, so the shell sidebar can show
// which session is working without polling or a page reload, plus a
// small `activity` event per turn step (thinking / the running tool /
// a failed tool / waiting on a person) that the Team roster follows
// instead of polling (see sessionActivity).
//
// Deliberately NOT the global /stream. That one carries pool_stats, which
// lists every active session across all users, and is therefore
// admin-only; the sidebar is for everyone. This endpoint emits nothing
// but {session_id, lifecycle} for sessions that pass the caller's normal
// project access check, so opening it to any logged-in user leaks
// nothing they could not already see in their own session list.
//
// A sub-agent's own session is never a row here, but its liveness is
// re-attributed to the conversation that owns it (see
// projectSidebarEvent) — otherwise a row goes dark the moment its leader
// idles, while the work it delegated is still running.
func sessionsLifecycleSSE(c *tool.Ctx) {
	if notReady(c) || globalBcast == nil {
		c.Error(http.StatusServiceUnavailable, "broadcaster not ready")
		return
	}
	access := callerProjectAccess(c)

	w := c.W
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	// Clear the server's default write timeout: an SSE connection lives
	// until the client goes away.
	_ = rc.SetWriteDeadline(time.Time{})
	flush := func() { _ = rc.Flush() }

	ch, unsub := globalBcast.Subscribe("")
	defer unsub()

	// visible gates on the ROOT conversation, since that is the row an
	// event ends up addressing. A child inherits its parent's visibility:
	// it lives in the same project and belongs to the same user, so no
	// access decision is skipped by resolving it first.
	visible := func(sessionID string) bool {
		sess, ok := globalMgr.Registry().Session(sessionID)
		if !ok || sess.Meta.ParentSessionID != "" {
			return false
		}
		return access.allowSession(sess.Meta.ProjectID, sess.Meta.UserID, sess.Meta.Participants)
	}
	acts := newActivityTracker(sessionParentOf, visible, sessionNeedsAttention)

	fmt.Fprintf(w, ": connected\n\n")
	// Replay what is running right now, so a freshly loaded page paints
	// its spinners immediately instead of waiting for the next
	// transition — which for a long-running turn may be minutes away.
	if globalPool != nil {
		active := globalPool.ActiveSnapshot()
		for _, e := range active {
			ev, ok := projectSidebarEvent(e.SessionID, e.Lifecycle, sessionParentOf)
			if !ok || !visible(ev.SessionID) {
				continue
			}
			// A replayed child that is not working carries no information:
			// the row starts clean, so an empty marker would only overwrite
			// a sibling's live one during the replay loop.
			if ev.Lifecycle == "" && ev.SubAgent == "" {
				continue
			}
			fmt.Fprintf(w, "event: session\ndata: %s\n\n", ev.JSON())
		}
		// The same for the turn's step, so the Team roster paints the
		// running tool at once rather than "thinking" until the next one.
		for _, a := range acts.replay(active) {
			fmt.Fprintf(w, "event: activity\ndata: %s\n\n", a.JSON())
		}
	}
	flush()

	ctx := c.R.Context()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case ev, open := <-ch:
			if !open {
				return
			}
			if ev.SessionID == "" {
				continue
			}
			// Activity goes first: on a turn's end the roster clears the
			// tool before the session event re-reads it.
			wrote := false
			if a, ok := acts.apply(ev); ok {
				fmt.Fprintf(w, "event: activity\ndata: %s\n\n", a.JSON())
				wrote = true
			}
			if ev.Type != "lifecycle" {
				if wrote {
					flush()
				}
				continue
			}
			// Re-projected rather than forwarded: the source event also
			// carries PID, substate and agent name, none of which the
			// sidebar renders, and a stream should not ship fields whose
			// only effect is to widen what a subscriber can observe.
			// Projection also re-addresses a sub-agent's transition to the
			// conversation that owns it.
			out, ok := projectSidebarEvent(ev.SessionID, ev.Lifecycle, sessionParentOf)
			if !ok || !visible(out.SessionID) {
				if wrote {
					flush()
				}
				continue
			}
			fmt.Fprintf(w, "event: session\ndata: %s\n\n", out.JSON())
			flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flush()
		case <-ctx.Done():
			return
		}
	}
}

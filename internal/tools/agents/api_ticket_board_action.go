package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// maxBoardActionTickets caps how many rows one board-action delivery
// carries. A board button is a click, not an export: the receiver gets the
// filter and the honest match count either way, and the list is there so
// the common case (a person's couple of dozen tickets) needs no callback.
const maxBoardActionTickets = 500

// boardActionRequest is the filter the toolbar was showing when the button
// was clicked. Same vocabulary as the board's own query params, so a client
// sends back exactly what it asked the board for.
type boardActionRequest struct {
	// Assignee is "" (everyone), "me", or a user id.
	Assignee string `json:"assignee"`
	// Statuses are the selected columns; empty means all of them.
	Statuses []string `json:"statuses"`
}

// apiBoardAction handles
// POST /api/projects/{id}/board-actions/{buttonID} — a custom button in the
// TICKET LIST's toolbar was clicked.
//
// Where apiTicketAction sends one ticket, this sends the LIST: who it was
// filtered to, which columns, and the matching cards. That is the whole
// point of the placement — the receiver is being asked to do something to a
// set ("pull every ticket assigned to me from Notion"), and without the
// filter it would have to guess which set.
//
// Synchronous, like the per-ticket button: the clicker is watching. A
// receiver whose work outlives one HTTP call should answer immediately and
// say so — its reply is shown to the clicker verbatim.
func apiBoardAction(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	projectID := c.PathValue("id")
	if !callerProjectAccess(c).allowProject(projectID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "you don't have access to this project"})
		return
	}
	if !requireTicketAPI(c, projectID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "the REST API is disabled for this project"})
		return
	}
	p, ok := globalMgr.Registry().Project(projectID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	cfg := p.Meta.Ticket

	btn, found := cfg.ButtonByID(c.PathValue("buttonID"))
	if !found || !btn.On(project.ButtonOnBoard) {
		// A ticket-page button reached through this route is not a board
		// button that happens to be misconfigured — it is the wrong URL for
		// it, and answering 404 keeps the two placements from quietly
		// standing in for each other.
		c.JSON(http.StatusNotFound, map[string]string{"error": "button not found"})
		return
	}
	if ticketDispatcher == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "webhook dispatcher not wired"})
		return
	}

	var req boardActionRequest
	if c.R.Body != nil {
		// An absent or empty body is the unfiltered board, not an error:
		// the toolbar may have no chips selected and no assignee chosen.
		_ = json.NewDecoder(c.R.Body).Decode(&req)
	}

	board := buildBoardContext(c, cfg, projectID, req)
	ev := ticket.Event{
		Event:     ticket.EventBoardAction,
		ProjectID: projectID,
		Action:    btn.ID,
		Actor:     callerActor(c),
		Board:     &board,
	}
	rec := ticketDispatcher.Deliver(
		// Same delivery machinery as every other button: one URL, no
		// secret, the SSRF guard and retry schedule for free.
		project.TicketWebhook{ID: "btn:" + btn.ID, URL: btn.URL, Enabled: true},
		ev,
	)
	out := map[string]any{
		"ok":       rec.OK,
		"status":   rec.Status,
		"error":    rec.Err,
		"attempts": rec.Attempts,
		"message":  rec.Message,
		"tickets":  board.MatchCount,
	}
	// The receiver's own JSON, passed through. A bulk job cannot finish
	// inside one delivery attempt, so what it CAN do is hand back where it
	// is — counters, a run id, a URL to watch, its own HTML — and the
	// client renders that instead of a single line of text.
	if obj := replyObject(rec.Reply); obj != nil {
		out["result"] = obj
	}
	c.JSON(http.StatusOK, out)
}

// replyObject decodes a receiver's body as a JSON object, or nil.
func replyObject(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil
	}
	return obj
}

// apiBoardActionPoll handles
// POST /api/projects/{id}/board-actions/{buttonID}/poll — fetch the URL a
// board action handed back, so the panel can follow a run that is still
// going.
//
// The URL is not trusted because a receiver named it. It must live on the
// SAME origin as the button's own URL: a receiver that could redirect this
// to anywhere would turn a ticket button into an SSRF primitive with the
// server's network position. Scheme, host and port must all match, and the
// dispatcher's private-address guard still applies on top.
func apiBoardActionPoll(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	projectID := c.PathValue("id")
	if !callerProjectAccess(c).allowProject(projectID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "you don't have access to this project"})
		return
	}
	if !requireTicketAPI(c, projectID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "the REST API is disabled for this project"})
		return
	}
	p, ok := globalMgr.Registry().Project(projectID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	btn, found := p.Meta.Ticket.ButtonByID(c.PathValue("buttonID"))
	if !found || !btn.On(project.ButtonOnBoard) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "button not found"})
		return
	}
	if ticketDispatcher == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "webhook dispatcher not wired"})
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if c.R.Body != nil {
		_ = json.NewDecoder(c.R.Body).Decode(&req)
	}
	if err := sameOrigin(btn.URL, req.URL); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	rec := ticketDispatcher.Get(strings.TrimSpace(req.URL))
	out := map[string]any{
		"ok":      rec.OK,
		"status":  rec.Status,
		"error":   rec.Err,
		"message": rec.Message,
	}
	if obj := replyObject(rec.Reply); obj != nil {
		out["result"] = obj
	}
	c.JSON(http.StatusOK, out)
}

// sameOrigin reports whether raw may be polled on behalf of a button whose
// endpoint is buttonURL.
func sameOrigin(buttonURL, raw string) error {
	want, err := url.Parse(strings.TrimSpace(buttonURL))
	if err != nil {
		return fmt.Errorf("button url is not parseable")
	}
	got, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || got.Host == "" {
		return fmt.Errorf("poll url must be a full http(s) URL")
	}
	if got.Scheme != want.Scheme || !strings.EqualFold(got.Host, want.Host) {
		return fmt.Errorf("poll url must be on the same origin as the button (%s://%s)", want.Scheme, want.Host)
	}
	return nil
}

// buildBoardContext resolves the toolbar's filter and collects the tickets
// it selects.
//
// "me" is resolved HERE rather than left for the receiver: the clicker is
// the only party who knows who "me" is, and a receiver that guessed would
// act on the wrong person's work.
func buildBoardContext(
	c *tool.Ctx,
	cfg project.TicketConfig,
	projectID string,
	req boardActionRequest,
) ticket.BoardContext {
	out := ticket.BoardContext{Assignee: strings.TrimSpace(req.Assignee)}
	out.AssigneeID = out.Assignee
	if out.Assignee == "me" {
		out.AssigneeID = ""
		if u := login.GetUser(c.Context()); u != nil {
			out.AssigneeID = u.ID
		}
	}
	if out.AssigneeID != "" {
		// Name AND email: a receiver matching people by hand-kept ids needs
		// a table nobody remembers to update, and an email is the
		// identifier both systems usually already have.
		if globalAuth != nil {
			if u, err := globalAuth.GetUserByID(c.Context(), out.AssigneeID); err == nil && u != nil {
				out.AssigneeName, out.AssigneeEmail = u.Name, u.Email
			}
		}
		if out.AssigneeName == "" {
			if names := userNames(c, map[string]bool{out.AssigneeID: true}); names[out.AssigneeID] != "" {
				out.AssigneeName = names[out.AssigneeID]
			}
		}
	}

	want := map[string]bool{}
	for _, s := range req.Statuses {
		if s = strings.TrimSpace(s); s != "" {
			want[s] = true
			out.Statuses = append(out.Statuses, s)
		}
	}

	tickets, err := ticket.List(globalLayout, projectID)
	if err != nil {
		return out
	}
	out.Tickets, out.MatchCount, out.Truncated = selectBoardTickets(tickets, want, out.AssigneeID)
	return out
}

// selectBoardTickets applies the toolbar's filter to a project's tickets
// and renders the matches, newest first.
//
// The count is of MATCHES, not of rows returned: a truncated list that also
// under-reported the total would have a receiver believing it had seen
// everything.
func selectBoardTickets(
	tickets []ticket.Ticket,
	wantStatus map[string]bool,
	assigneeID string,
) (rows []ticket.BoardTicket, matched int, truncated bool) {
	rows = make([]ticket.BoardTicket, 0, len(tickets))
	for _, t := range tickets {
		if len(wantStatus) > 0 && !wantStatus[t.Status] {
			continue
		}
		if assigneeID != "" && t.Assignee != assigneeID {
			continue
		}
		matched++
		rows = append(rows, ticket.BoardTicket{
			ID:       t.ID,
			Title:    t.Title,
			Status:   t.Status,
			Assignee: t.Assignee,
			// The FULL field map, not the card's subset: a receiver's join
			// key (a Notion page id, an external ticket number) is exactly
			// the kind of field nobody marks show_on_card.
			Fields:    t.Fields,
			UpdatedAt: t.UpdatedAt.UTC(),
		})
	}
	// Most recently touched first, so a truncated list keeps the rows most
	// likely to matter.
	sort.Slice(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	if len(rows) > maxBoardActionTickets {
		rows = rows[:maxBoardActionTickets]
		truncated = true
	}
	return rows, matched, truncated
}

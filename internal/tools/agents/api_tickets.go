package agents

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"

	"github.com/yogasw/wick/internal/agents/notes"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── payload caps ────────────────────────────────────────────────────────── */

// The board is polled, so its size is a running cost, not a one-off. These
// caps keep a project with hundreds of chats as cheap to draw as a small
// one: the client asks for what it will actually render, and the server
// sends no more. A count always travels even when the rows do not, so the
// UI can say "3 of 41" without holding 41 of anything.
const (
	// defaultRowsPerCard is how many session rows a card carries. Three
	// fits the card without scrolling; the rest live in the ticket's page.
	defaultRowsPerCard = 3
	maxRowsPerCard     = 25

	// defaultUntrackedLimit is one page of the untracked rail.
	defaultUntrackedLimit = 25
	maxUntrackedLimit     = 200
)

// queryInt reads a bounded integer query param, falling back to def when it
// is absent or unparseable. Out-of-range values are clamped rather than
// refused: a board that renders slightly differently beats one that 400s.
func queryInt(c *tool.Ctx, key string, def, min, max int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// isTrueish reads an opt-in flag from a query param. Anything a client might
// reasonably send for "yes" counts; absent and anything else are no, so the
// expensive thing behind the flag stays off unless it was actually asked for.
func isTrueish(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// queryCSV reads a comma-separated set query param. A MISSING param and an
// EMPTY one mean different things here: absent is "no opinion, send the
// default", while `?statuses=` is an explicit empty set — the caller drew no
// columns and wants no cards. Returning (set, present) keeps the two apart.
func queryCSV(c *tool.Ctx, key string) (map[string]bool, bool) {
	q := c.R.URL.Query()
	if !q.Has(key) {
		return nil, false
	}
	set := map[string]bool{}
	for _, part := range strings.Split(q.Get(key), ",") {
		if v := strings.TrimSpace(part); v != "" {
			set[v] = true
		}
	}
	return set, true
}

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// TicketCard is one card on the project board. A card is a TICKET, not a
// session: a ticket can hold several sessions, and the board shows the work
// rather than each conversation about it.
type TicketCard struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Assignee is the first of Assignees. Both travel: a card has room for
	// one avatar, and the older clients that only read this field keep
	// working against a ticket that now has three people on it.
	Assignee  string            `json:"assignee,omitempty"`
	Assignees []string          `json:"assignees,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	// SessionRows are the ticket's sessions, listed on the card so one can
	// be dragged to another ticket without opening anything.
	SessionRows []ticketSessionRow `json:"session_rows,omitempty"`
	Sessions    int                `json:"sessions"`
	Notes       int                `json:"notes"`
	OpenTasks   int                `json:"open_tasks"`
	UpdatedAt   string             `json:"updated_at"`
	CreatedAt   string             `json:"created_at"`
	Stale       bool               `json:"stale"`
}

// ticketBoardResponse is the envelope for GET /api/projects/{id}/tickets.
type ticketBoardResponse struct {
	Config  project.TicketConfig `json:"config"`
	Tickets []TicketCard         `json:"tickets"`
	// Untracked are this project's chats that belong to no ticket — the
	// board's left rail, and the drag source for attaching one. Sent one
	// page at a time (see the caps above), or not at all when the client
	// has the rail collapsed.
	Untracked []ticketSessionRow `json:"untracked"`
	// UntrackedTotal is how many exist, however few were sent, so the rail
	// can say "25 of 142" instead of implying it has them all.
	UntrackedTotal int `json:"untracked_total"`
	// UntrackedNext is the cursor for the page after these rows
	// (?untracked_after=), empty when nothing follows them. Opaque: the
	// client hands it back as-is and never builds one.
	UntrackedNext string                 `json:"untracked_next,omitempty"`
	Statuses      []project.TicketStatus `json:"statuses"`
	Users         map[string]string      `json:"users,omitempty"`
	Me            string                 `json:"me,omitempty"`
}

// cardFields picks what a card carries: the schema's show_on_card subset
// for a board somebody is looking at, or everything for a caller that said
// it needs everything.
func cardFields(cfg project.TicketConfig, fields map[string]string, all bool) map[string]string {
	if !all {
		return cfg.CardFields(fields)
	}
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}

// ticketSessionRow is one session inside a ticket's detail view.
type ticketSessionRow struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Lifecycle  string `json:"lifecycle,omitempty"`
	LastActive string `json:"last_active"`
}

// ticketDetailResponse is the envelope for GET /api/tickets/{ticketID}.
type ticketDetailResponse struct {
	Ticket   ticket.Ticket          `json:"ticket"`
	Config   project.TicketConfig   `json:"config"`
	Sessions []ticketSessionRow     `json:"sessions"`
	Notes    []notes.Note           `json:"notes"`
	Statuses []project.TicketStatus `json:"statuses"`
	Users    map[string]string      `json:"users,omitempty"`
	Me       string                 `json:"me,omitempty"`
}

/* ── helpers ─────────────────────────────────────────────────────────────── */

// resolveTicketProject finds which project owns a ticket. Ticket ids are
// unique per project, and the URL carries only the ticket, so this scans the
// projects the caller may see — which also enforces access without a second
// check.
func resolveTicketProject(c *tool.Ctx, ticketID string) (string, bool) {
	access := callerProjectAccess(c)
	for id := range globalMgr.Registry().Projects() {
		if !access.allowProject(id) {
			continue
		}
		if ticket.Exists(globalLayout, id, ticketID) {
			// A token may only address tickets in a project that opted into
			// the REST surface. The caller answers "ticket not found"; the
			// server log (requireTicketAPI) records that the real reason
			// was the project's API toggle.
			if !requireTicketAPI(c, id) {
				return "", false
			}
			return id, true
		}
	}
	return "", false
}

// userNames resolves display names for a set of user ids, best effort.
func userNames(c *tool.Ctx, ids map[string]bool) map[string]string {
	out := map[string]string{}
	if globalAuth == nil {
		return out
	}
	for id := range ids {
		if id == "" {
			continue
		}
		if u, err := globalAuth.GetUserByID(c.Context(), id); err == nil && u != nil {
			out[id] = u.Name
		}
	}
	return out
}

// sessionRow renders one session for the board or a ticket's detail, and
// records its owner so names can be resolved in one pass.
func sessionRow(
	sid string,
	live map[string]session.Session,
	lc map[string]view.SessionLifecycleVM,
	ids map[string]bool,
) ticketSessionRow {
	row := ticketSessionRow{ID: sid, Label: loadFirstUserMessage(globalLayout, sid, 60), Lifecycle: lc[sid].Lifecycle}
	if s, ok := live[sid]; ok {
		// The status the ORDER uses, so a row pinned to the top because its
		// sub-agent is working also says so, as the sidebar's dot does.
		row.Lifecycle = view.SidebarRowStatus(s, lc[sid])
		row.Status = string(s.Meta.Status)
		// The same clock the sidebar prints, so a chat reads "2m" in both
		// places rather than "2m" there and "1h" here mid-turn.
		if ms := view.SidebarLastActiveMs(s, lc[sid]); ms > 0 {
			row.LastActive = time.UnixMilli(ms).UTC().Format(time.RFC3339)
		}
		if ids != nil {
			ids[s.Meta.UserID] = true
		}
	}
	return row
}

/* ── board + ticket CRUD ─────────────────────────────────────────────────── */

// apiProjectTickets handles GET /api/projects/{id}/tickets — the board.
// Access enforced by projectAccessMW.
func apiProjectTickets(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	if !requireTicketAPI(c, id) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "the REST API is disabled for this project"})
		return
	}
	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	cfg := p.Meta.Ticket

	tickets, err := ticket.List(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now()
	ids := map[string]bool{}
	lc := sidebarLifecycles()
	live := globalMgr.Registry().Sessions()
	ticketed := map[string]bool{}

	// The board pays only for what it draws. A project with hundreds of
	// chats would otherwise send every one of them on every poll, and the
	// client would throw most away — so what to send is decided HERE, from
	// what the caller says it will render, not by a filter in the UI.
	//
	//   ?fields=all      the full field map per card, not just show_on_card
	//   ?rows=N          session rows per card (0 = none, just the count)
	//   ?statuses=a,b    only these columns; absent = all, `?statuses=` = none
	//   ?assignee=ID|me  only this person's tickets; absent/empty = everyone
	//   ?untracked=1     ask for the untracked list at all (default: no)
	//   ?untracked_limit=N
	//   ?untracked_after=C    the rail's next page: rows strictly after the
	//                    row cursor C points at (a previous response's
	//                    untracked_next). The poll asks for the first page
	//                    only; older pages are fetched once, on scroll.
	//   ?untracked_owner=me   only the caller's loose chats; absent = everyone's.
	//                    Applied to the COUNT too, so the rail's number and its
	//                    rows always describe the same set.
	rowsPerCard := queryInt(c, "rows", defaultRowsPerCard, 0, maxRowsPerCard)
	// A MACHINE reading this board needs the fields the board itself does
	// not draw: an external id, a mirror's page reference — exactly the
	// keys nobody marks show_on_card. Without this a sync cannot recognise
	// the tickets it created and re-creates them on every run. Opt-in, so
	// the UI keeps paying only for what it renders.
	allFields := isTrueish(c.Query("fields")) || strings.EqualFold(strings.TrimSpace(c.Query("fields")), "all")
	// The untracked list is the board's most expensive part and the one
	// least often looked at, so it is opt-in: a caller that never asks
	// never pays. "0" stays honoured for callers written against the old
	// opt-out spelling.
	wantUntracked := isTrueish(c.Query("untracked"))
	untrackedLimit := queryInt(c, "untracked_limit", defaultUntrackedLimit, 1, maxUntrackedLimit)
	// A cursor, not an offset — see railKey for why a row count skips chats.
	// Unlike the clamped ints above, a bad one is refused: paging from a
	// guessed place would silently draw the wrong rows.
	var untrackedAfter *railKey
	if raw := strings.TrimSpace(c.Query("untracked_after")); raw != "" {
		k, err := decodeRailCursor(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		untrackedAfter = &k
	}
	// "me" resolves against the caller here, same as ?assignee=me. An
	// untracked chat has no ticket, so ownership is the only "mine" there is.
	untrackedOwner := ""
	if strings.EqualFold(strings.TrimSpace(c.Query("untracked_owner")), "me") {
		if u := login.GetUser(c.Context()); u != nil {
			untrackedOwner = u.ID
		}
	}

	wantStatus, statusFiltered := queryCSV(c, "statuses")
	// "me" resolves against the caller, so the client can save a filter that
	// keeps meaning the right person.
	wantAssignee := strings.TrimSpace(c.Query("assignee"))
	if wantAssignee == "me" {
		if u := login.GetUser(c.Context()); u != nil {
			wantAssignee = u.ID
		} else {
			wantAssignee = ""
		}
	}

	cards := make([]TicketCard, 0, len(tickets))
	for _, t := range tickets {
		// Every session is marked as ticketed even when its card is not
		// sent: the untracked list is "has no ticket", and neither
		// truncating the rows nor hiding a whole column may make a tracked
		// chat look loose. So this runs BEFORE the filters below.
		for _, sid := range t.Sessions {
			ticketed[sid] = true
		}
		// Cards the caller will not draw are not built. The assignees are
		// still collected for name resolution, so the filter dropdown can
		// list people whose tickets are currently filtered out.
		for _, a := range t.AssigneeList() {
			ids[a] = true
		}
		if statusFiltered && !wantStatus[t.Status] {
			continue
		}
		// Anyone on the ticket counts: filtering to a person must not hide
		// the work they share with somebody else.
		if wantAssignee != "" && !t.HasAssignee(wantAssignee) {
			continue
		}
		count, _ := notes.Counts(globalLayout, notes.Scope{ProjectID: id, TicketID: t.ID})
		// Same order as the sidebar before the cap, so the rows a card DOES
		// show are the chats in use, not the first ones ever attached.
		shown := orderSidebarIDs(t.Sessions, live, lc)
		if rowsPerCard < len(shown) {
			shown = shown[:rowsPerCard]
		}
		rows := make([]ticketSessionRow, 0, len(shown))
		for _, sid := range shown {
			rows = append(rows, sessionRow(sid, live, lc, ids))
		}
		cards = append(cards, TicketCard{
			ID:        t.ID,
			Title:     t.Title,
			Status:    t.Status,
			Assignee:  t.Assignee,
			Assignees: t.AssigneeList(),
			// A card carries only the schema fields marked show_on_card.
			// Everything else — unmarked fields, values written outside the
			// schema via the REST surface — lives on the ticket's own page,
			// and comes back here only for a caller that asked for all of
			// them (?fields=all).
			Fields:      cardFields(cfg, t.Fields, allFields),
			SessionRows: rows,
			Sessions:    len(t.Sessions),
			Notes:       count.Visible,
			OpenTasks:   count.OpenTasks,
			UpdatedAt:   t.UpdatedAt.Format(time.RFC3339),
			CreatedAt:   t.CreatedAt.Format(time.RFC3339),
			Stale:       ticket.NeedsFollowup(cfg, t, now),
		})
	}

	// Untracked: this project's chats with no ticket. Sub-agent sessions
	// are working contexts, not chats, so they never appear.
	//
	// Counted in full but sent in part: the header needs the total ("142
	// untracked") while the rail only draws the first page.
	// The COUNT is always computed and the rows never are unless asked: the
	// number is one walk over sessions already in memory, and only the
	// requested page is ever ordered and built. That split is what lets the
	// board offer "Untracked (89)" as something to switch on without having
	// paid to draw it.
	untracked := []ticketSessionRow{}
	page, untrackedNext, untrackedTotal := pageLooseSessions(
		globalMgr.Registry().SessionIDs(), live, lc,
		func(sid string, s session.Session) bool {
			return s.Meta.ProjectID == id && s.Meta.ParentSessionID == "" && !ticketed[sid] &&
				(untrackedOwner == "" || s.Meta.UserID == untrackedOwner)
		},
		untrackedAfter, untrackedLimit, wantUntracked,
	)
	for _, sid := range page {
		untracked = append(untracked, sessionRow(sid, live, lc, ids))
	}

	resp := ticketBoardResponse{
		// Redacted: the config carries webhook secrets, and this response
		// reaches every board viewer.
		Config:         redactTicketConfig(cfg),
		Tickets:        cards,
		Untracked:      untracked,
		UntrackedTotal: untrackedTotal,
		UntrackedNext:  untrackedNext,
		// Board columns come from the project: a team names its own stages.
		Statuses: cfg.StatusList(),
	}
	if u := login.GetUser(c.Context()); u != nil {
		resp.Me = u.ID
		ids[u.ID] = true
	}
	resp.Users = userNames(c, ids)
	c.JSON(http.StatusOK, resp)
}

// apiTicketCreate handles POST /api/projects/{id}/tickets.
func apiTicketCreate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	projectID := c.PathValue("id")
	if !requireTicketAPI(c, projectID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "the REST API is disabled for this project"})
		return
	}
	var req struct {
		Title string `json:"title"`
		// ID adopts an external identifier (a Notion page id) as the ticket
		// id, so the source system can address the ticket it just created
		// without keeping a mapping. Omitted means wick generates one.
		ID     string `json:"id"`
		Status string `json:"status"`
		// Body is the markdown description, optional.
		Body string `json:"body"`
		// Assignee is a pointer so "not sent" stays distinct from "sent
		// empty": omitting it means "whoever is creating this", while an
		// explicit "" is a deliberate no-assignee.
		Assignee *string `json:"assignee"`
		// Assignees puts several people on it at once. Merged with Assignee
		// above, so a caller may send either.
		//
		// A pointer for the same reason Assignee is one: `[]` is a caller
		// saying "nobody", and a plain slice cannot tell that from a field
		// nobody sent — which turned an explicitly unassigned ticket into
		// one assigned to whoever created it.
		Assignees *[]string         `json:"assignees"`
		Fields    map[string]string `json:"fields"`
		// SessionID optionally attaches an existing conversation, which is
		// how "turn this chat into a ticket" works.
		SessionID string `json:"session_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Statuses are the project's own, so a board that renamed its stages
	// accepts its own keys and rejects the built-in ones.
	cfg := project.TicketConfig{}
	if p, pok := globalMgr.Registry().Project(projectID); pok {
		cfg = p.Meta.Ticket
	}
	if req.Status != "" && !ticket.ValidStatus(cfg, req.Status) {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid status " + req.Status + " (want " + strings.Join(cfg.StatusKeys(), ", ") + ")",
		})
		return
	}
	var seed []string
	if s := strings.TrimSpace(req.SessionID); s != "" {
		seed = []string{s}
	}
	// Whoever creates a ticket is presumed to be taking it on — dragging a
	// chat onto a column is someone saying "I am working on this", and
	// landing an "unassigned" card in front of them says the opposite.
	// An explicit empty assignee still means unassigned.
	assignee := ""
	var assignees []string
	if req.Assignees != nil {
		assignees = *req.Assignees
	}
	if req.Assignee != nil {
		assignee = strings.TrimSpace(*req.Assignee)
	} else if req.Assignees == nil {
		if u := login.GetUser(c.Context()); u != nil {
			assignee = u.ID
		}
	}
	tk, err := ticket.Create(globalLayout, ticket.CreateOptions{
		ProjectID: projectID,
		ID:        req.ID,
		Title:     req.Title,
		Body:      req.Body,
		Status:    req.Status,
		Assignee:  assignee,
		Assignees: assignees,
		Fields:    req.Fields,
		Sessions:  seed,
		Actor:     callerActor(c),
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	for _, sid := range seed {
		writeSessionTicketPointer(sid, tk.ID)
	}
	c.JSON(http.StatusOK, tk)
}

// apiTicketDetail handles GET /api/tickets/{ticketID}.
func apiTicketDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ticketID := c.PathValue("ticketID")
	projectID, ok := resolveTicketProject(c, ticketID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	tk, err := ticket.Load(globalLayout, projectID, ticketID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	resp := ticketDetailResponse{Ticket: tk}
	if p, pok := globalMgr.Registry().Project(projectID); pok {
		// Redacted: the config carries webhook secrets.
		resp.Config = redactTicketConfig(p.Meta.Ticket)
	}
	resp.Statuses = resp.Config.StatusList()

	lc := sidebarLifecycles()
	live := globalMgr.Registry().Sessions()
	ids := map[string]bool{}
	for _, a := range tk.AssigneeList() {
		ids[a] = true
	}
	// MOST RECENTLY ACTIVE FIRST, by the sidebar's rule. tk.Sessions is
	// attach order, so the chat someone was just in sat wherever it
	// happened to be added — a ticket with a long history buried it. Rows
	// with no live session carry no timestamp and sink to the bottom.
	resp.Sessions = make([]ticketSessionRow, 0, len(tk.Sessions))
	for _, sid := range orderSidebarIDs(tk.Sessions, live, lc) {
		resp.Sessions = append(resp.Sessions, sessionRow(sid, live, lc, ids))
	}

	// The UI view includes hidden notes (rendered blurred); only the MCP
	// surface filters them out.
	list, _ := notes.List(globalLayout, notes.Scope{ProjectID: projectID, TicketID: ticketID})
	resp.Notes = list
	for _, n := range list {
		ids[n.Author] = true
	}
	if u := login.GetUser(c.Context()); u != nil {
		resp.Me = u.ID
		ids[u.ID] = true
	}
	resp.Users = userNames(c, ids)
	c.JSON(http.StatusOK, resp)
}

// apiTicketUpdate handles PATCH /api/tickets/{ticketID} — partial update.
// Any edit bumps UpdatedAt, which is what the stale and auto-resolve timers
// key on.
func apiTicketUpdate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ticketID := c.PathValue("ticketID")
	projectID, ok := resolveTicketProject(c, ticketID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	var req struct {
		Title *string `json:"title"`
		// Body is a pointer so "not sent" stays distinct from "clear it":
		// an explicit "" deliberately empties the description.
		Body     *string `json:"body"`
		Status   *string `json:"status"`
		Assignee *string `json:"assignee"`
		// Assignees replaces the whole list; an explicit [] unassigns
		// everyone. It wins over Assignee when both are sent, since it is
		// the more specific statement of the same thing.
		Assignees *[]string         `json:"assignees"`
		Fields    map[string]string `json:"fields"`
		// UpdatedAt lets a MIRROR say when the change really happened:
		// an RFC3339 timestamp (the source system's edit time), or the
		// literal "keep" for a write that should not move the clock at all
		// (a sync stamping "I checked this and nothing had changed").
		// Absent — which is every edit made from the UI — means now.
		UpdatedAt string `json:"updated_at"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	tk, err := ticket.Load(globalLayout, projectID, ticketID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	if req.Status != nil {
		cfg := project.TicketConfig{}
		if p, pok := globalMgr.Registry().Project(projectID); pok {
			cfg = p.Meta.Ticket
		}
		if !ticket.ValidStatus(cfg, *req.Status) {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid status: " + *req.Status})
			return
		}
		tk.Status = *req.Status
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "title cannot be empty"})
			return
		}
		tk.Title = title
	}
	if req.Body != nil {
		tk.Body = strings.TrimSpace(*req.Body)
	}
	if req.Assignees != nil {
		tk.SetAssignees(*req.Assignees)
	} else if req.Assignee != nil {
		tk.SetAssignees([]string{*req.Assignee})
	}
	if req.Fields != nil {
		if tk.Fields == nil {
			tk.Fields = map[string]string{}
		}
		for k, v := range req.Fields {
			if strings.TrimSpace(v) == "" {
				delete(tk.Fields, k)
				continue
			}
			tk.Fields[k] = v
		}
	}
	saved, err := saveWithRequestedTime(tk, callerActor(c), req.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// The SAVED ticket, so the card that just moved shows the time it moved
	// and the board re-sorts on it without waiting for a reload.
	c.JSON(http.StatusOK, saved)
}

// saveWithRequestedTime applies the PATCH's updated_at choice.
//
//	""      -> now, the normal edit
//	"keep"  -> leave the stored timestamp alone (bookkeeping write)
//	RFC3339 -> that instant (the mirrored system's own edit time)
//
// A value that is neither is refused rather than silently treated as now:
// a mirror that mis-formats its timestamps would otherwise stamp every
// ticket with the sync's clock and nobody would notice until the board's
// order stopped meaning anything.
// It returns the SAVED ticket, not the one handed in: the save stamps
// updated_at, and answering with the caller's copy told the board a time that
// was already wrong.
func saveWithRequestedTime(tk ticket.Ticket, actor ticket.Actor, want string) (ticket.Ticket, error) {
	switch v := strings.TrimSpace(want); v {
	case "":
		return ticket.SaveAsAt(globalLayout, tk, actor, time.Time{})
	case "keep":
		return ticket.SaveAsKeeping(globalLayout, tk, actor)
	default:
		at, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return ticket.Ticket{}, fmt.Errorf("updated_at must be RFC3339 or \"keep\": %w", err)
		}
		return ticket.SaveAsAt(globalLayout, tk, actor, at)
	}
}

// apiTicketAction handles POST /api/tickets/{ticketID}/actions/{buttonID} —
// a custom ticket button was clicked. The ticket is POSTed to that button's
// URL as a ticket.action event, synchronously: the user is waiting to see
// whether their "Sync" landed, so the delivery outcome IS the response.
func apiTicketAction(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ticketID := c.PathValue("ticketID")
	projectID, ok := resolveTicketProject(c, ticketID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	cfg := project.TicketConfig{}
	if p, pok := globalMgr.Registry().Project(projectID); pok {
		cfg = p.Meta.Ticket
	}
	btn, found := cfg.ButtonByID(c.PathValue("buttonID"))
	if !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "button not found"})
		return
	}
	if ticketDispatcher == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "webhook dispatcher not wired"})
		return
	}
	tk, err := ticket.Load(globalLayout, projectID, ticketID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	rec := ticketDispatcher.Deliver(
		// The button rides the webhook delivery machinery (signing aside —
		// buttons carry no secret), so it inherits the SSRF guard and the
		// retry schedule without a second HTTP path.
		project.TicketWebhook{ID: "btn:" + btn.ID, URL: btn.URL, Enabled: true},
		ticket.Event{
			Event:  ticket.EventAction,
			Ticket: tk,
			Action: btn.ID,
			Actor:  callerActor(c),
		},
	)
	c.JSON(http.StatusOK, map[string]any{
		"ok":       rec.OK,
		"status":   rec.Status,
		"error":    rec.Err,
		"attempts": rec.Attempts,
	})
}

// apiTicketDelete handles DELETE /api/tickets/{ticketID}.
//
// Two shapes, and the difference is the whole point:
//
//	?sessions=keep    (default) the chats survive as untracked
//	?sessions=delete  the chats are deleted with the ticket
//
// The destructive shape has to be asked for by name. A ticket is cheap to
// recreate; the conversations under it are not, and deleting them takes the
// notes and working history with them. The client names the count in its
// confirmation, and the response reports what actually went.
func apiTicketDelete(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ticketID := c.PathValue("ticketID")
	projectID, ok := resolveTicketProject(c, ticketID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	mode := strings.TrimSpace(c.Query("sessions"))
	if mode != "" && mode != "keep" && mode != "delete" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": `sessions must be "keep" or "delete"`})
		return
	}
	cascade := mode == "delete"

	tk, err := ticket.Load(globalLayout, projectID, ticketID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}

	deleted := 0
	for _, sid := range tk.Sessions {
		if !cascade {
			// Kept: the chat lives on, just without a ticket.
			writeSessionTicketPointer(sid, "")
			continue
		}
		// Deleting the session takes its notes with it, so the ticket's
		// notes are not separately rescued — they described work that is
		// being thrown away on purpose.
		if derr := globalMgr.DeleteSession(c.Context(), sid); derr != nil {
			log.Ctx(c.Context()).Error().Str("session", sid).Err(derr).
				Msg("ticket delete: session delete failed")
			continue
		}
		deleted++
	}

	if err := ticket.Delete(globalLayout, projectID, ticketID); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"status":           "ok",
		"sessions_deleted": deleted,
		"sessions_kept":    len(tk.Sessions) - deleted,
	})
}

/* ── ticket ↔ session ────────────────────────────────────────────────────── */

// writeSessionTicketPointer keeps session.Meta.TicketID in step with the
// ticket's own list. Best effort by design: the ticket is the record, so a
// failure here degrades a shortcut, never correctness.
func writeSessionTicketPointer(sessionID, ticketID string) {
	sess, err := session.Load(globalLayout, sessionID)
	if err != nil {
		return
	}
	if sess.Meta.TicketID == ticketID {
		return
	}
	sess.Meta.TicketID = ticketID
	if err := session.SaveMeta(globalLayout, sessionID, sess.Meta); err != nil {
		return
	}
	_ = globalMgr.RefreshSession(sessionID)
}

// apiTicketAttachSession handles PUT /api/tickets/{ticketID}/sessions/{sid}.
func apiTicketAttachSession(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ticketID := c.PathValue("ticketID")
	sid := c.PathValue("sid")
	projectID, ok := resolveTicketProject(c, ticketID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	// Which ticket this chat is leaving, so the response can tell the board
	// that one just emptied — a ticket with no sessions has nothing left to
	// track, and the user is offered its removal rather than left with a
	// husk on the board.
	from, hadFrom := ticket.FindBySession(globalLayout, projectID, sid)

	if err := ticket.AttachSession(globalLayout, projectID, ticketID, sid); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeSessionTicketPointer(sid, ticketID)
	c.JSON(http.StatusOK, emptiedResponse(globalLayout, projectID, from, hadFrom))
}

// emptiedResponse reports whether the ticket a chat just left is now empty,
// so the client can offer to delete it. Reported, never acted on here:
// deleting something the user did not ask about is not the server's call.
func emptiedResponse(layout agentsconfig.Layout, projectID string, from ticket.Ticket, had bool) map[string]any {
	out := map[string]any{"status": "ok"}
	if !had {
		return out
	}
	now, err := ticket.Load(layout, projectID, from.ID)
	if err != nil || len(now.Sessions) > 0 {
		return out
	}
	out["emptied_ticket"] = map[string]string{"id": now.ID, "title": now.Title}
	return out
}

// apiTicketDetachSession handles DELETE /api/tickets/{ticketID}/sessions/{sid}.
func apiTicketDetachSession(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	ticketID := c.PathValue("ticketID")
	sid := c.PathValue("sid")
	projectID, ok := resolveTicketProject(c, ticketID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	from, hadFrom := ticket.Load(globalLayout, projectID, ticketID)
	if err := ticket.DetachSession(globalLayout, projectID, ticketID, sid); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeSessionTicketPointer(sid, "")
	c.JSON(http.StatusOK, emptiedResponse(globalLayout, projectID, from, hadFrom == nil))
}

/* ── notes ───────────────────────────────────────────────────────────────── */

// notesScopeFromQuery builds a scope from ?ticket_id= or ?session_id=. A
// session id resolves through notes.Resolve, so a session that belongs to a
// ticket reads the TICKET's notes — that sharing is what lets a fresh
// session pick up where a previous one left off.
func notesScopeFromQuery(c *tool.Ctx) (notes.Scope, bool) {
	if tid := strings.TrimSpace(c.Query("ticket_id")); tid != "" {
		projectID, ok := resolveTicketProject(c, tid)
		if !ok {
			return notes.Scope{}, false
		}
		return notes.Scope{ProjectID: projectID, TicketID: tid}, true
	}
	sid := strings.TrimSpace(c.Query("session_id"))
	if sid == "" {
		return notes.Scope{}, false
	}
	sess, ok := globalMgr.Registry().Session(sid)
	if !ok || !callerProjectAccess(c).allowSession(sess.Meta.ProjectID, sess.Meta.UserID, sess.Meta.Participants) {
		return notes.Scope{}, false
	}
	// A shared agent's chat lives in the owner's project; its notes are
	// the owner's, whoever else might reach the project.
	if _, shared := sharedChatAgent(c.Context(), sess); shared {
		return notes.Scope{}, false
	}
	sc, err := notes.Resolve(globalLayout, sid)
	if err != nil {
		return notes.Scope{}, false
	}
	return sc, true
}

// notesProjectID names the project a notes scope belongs to.
//
// A session that is NOT on a ticket resolves to its own scope, which carries
// no project id — but the session still belongs to one, and the rail needs
// that project to know whether tickets run here at all. Reading it off the
// scope alone left the field missing on every ticket-less session, and the
// rail reads a missing field as "older server, show the tab" — so a project
// with tickets switched off still got a Ticket tab and lost its Notes one.
func notesProjectID(sc notes.Scope) string {
	if sc.ProjectID != "" {
		return sc.ProjectID
	}
	if sc.SessionID == "" {
		return ""
	}
	if sess, ok := globalMgr.Registry().Session(sc.SessionID); ok {
		return sess.Meta.ProjectID
	}
	return ""
}

// apiNotesList handles GET /api/notes?ticket_id=…|session_id=…
// noteTicketSummary is the ticket as the conversation rail shows it: what it
// asks (body) and the values of the project's fields beside it. Fields is
// never nil, so the rail can read a value without first asking whether the
// map exists. Keys the project does not define (a mirror's bookkeeping, such
// as a linked page id) travel too — the rail shows only defined fields, and
// the ticket's own page already exposes the full set.
func noteTicketSummary(tk ticket.Ticket) map[string]any {
	fields := tk.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	return map[string]any{
		"id":     tk.ID,
		"title":  tk.Title,
		"status": tk.Status,
		"body":   tk.Body,
		"fields": fields,
	}
}

// ticketPanelButton is a custom button as the rail needs it: enough to draw
// and click it, nothing it could leak.
type ticketPanelButton struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ticketPanelButtons keeps the buttons placed on a TICKET — a ticket-list
// button acts on a board filter the rail does not have — and drops any that
// cannot be clicked (no id yet, or no label to show). Never nil, so the rail
// reads an empty list rather than null.
func ticketPanelButtons(all []project.TicketButton) []ticketPanelButton {
	out := []ticketPanelButton{}
	for _, b := range all {
		if !b.On(project.ButtonOnTicket) || b.ID == "" || strings.TrimSpace(b.Label) == "" {
			continue
		}
		out = append(out, ticketPanelButton{ID: b.ID, Label: strings.TrimSpace(b.Label)})
	}
	return out
}

func apiNotesList(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	sc, ok := notesScopeFromQuery(c)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "scope not found"})
		return
	}
	list, err := notes.List(globalLayout, sc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ids := map[string]bool{}
	for _, n := range list {
		ids[n.Author] = true
	}
	out := map[string]any{"notes": list, "users": userNames(c, ids)}
	if u := login.GetUser(c.Context()); u != nil {
		out["me"] = u.ID
	}
	// Whether this project runs tickets at all. The rail hides its Ticket
	// tab when it does not: offering to file a ticket on a board that was
	// never turned on produces one nobody will ever look at. Sent
	// unconditionally — a missing field means an older server, and the rail
	// falls back to showing the tab rather than hiding it on a guess.
	if p, pok := globalMgr.Registry().Project(notesProjectID(sc)); pok {
		out["ticket_enabled"] = p.Meta.Ticket.Enabled
	}
	// When the scope resolved to a ticket, name it: the conversation rail
	// shows which ticket the notes belong to, and without this it would
	// have to guess from the session or fetch again.
	if sc.TicketID != "" {
		if tk, err := ticket.Load(globalLayout, sc.ProjectID, sc.TicketID); err == nil {
			// Body travels with the rest: the Notes panel shows what the
			// ticket ASKED FOR above the running record of what was found,
			// and reading notes without the request they answer is half a
			// conversation.
			out["ticket"] = noteTicketSummary(tk)
			// The rail's status select offers the project's own columns, so
			// a board that renamed its stages does not present the built-in
			// four as if they were valid. The field definitions travel the
			// same way: the rail shows and edits the project's own fields,
			// and a value is only meaningful next to its label and type.
			if p, pok := globalMgr.Registry().Project(sc.ProjectID); pok {
				out["statuses"] = p.Meta.Ticket.StatusList()
				out["ticket_fields"] = p.Meta.Ticket.Fields
				// The ticket's custom buttons ("Sync from Notion"), so the rail
				// can offer them where the ticket is being worked on. Id and
				// label only: the click goes through /actions/{buttonID}, which
				// looks the URL up server-side, so the rail never needs it.
				out["ticket_buttons"] = ticketPanelButtons(p.Meta.Ticket.Integrations.Buttons)
			}
		}
	}
	c.JSON(http.StatusOK, out)
}

// apiNotesAdd handles POST /api/notes?ticket_id=…|session_id=…
func apiNotesAdd(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	sc, ok := notesScopeFromQuery(c)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "scope not found"})
		return
	}
	var req struct {
		Body      string `json:"body"`
		Checkable bool   `json:"checkable"`
		Audience  string `json:"audience"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	author := ""
	if u := login.GetUser(c.Context()); u != nil {
		author = u.ID
	}
	n, err := notes.Add(globalLayout, sc, notes.AddOptions{
		Body: req.Body, Checkable: req.Checkable, Audience: req.Audience, Author: author,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Only a ticket-scoped note is a ticket event. A note on a loose session
	// belongs to that chat, and no board is watching it.
	if sc.TicketID != "" {
		ticket.EmitNoteAdded(globalLayout, sc.ProjectID, sc.TicketID, req.Body, callerActor(c))
	}
	c.JSON(http.StatusOK, n)
}

// apiNotesUpdate handles PATCH /api/notes/{noteID}?ticket_id=…|session_id=…
// Hidden is part of this: hiding a note takes it out of the agent's reach
// without deleting it.
func apiNotesUpdate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	sc, ok := notesScopeFromQuery(c)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "scope not found"})
		return
	}
	var req struct {
		Body      *string `json:"body"`
		Checkable *bool   `json:"checkable"`
		Audience  *string `json:"audience"`
		Hidden    *bool   `json:"hidden"`
		Done      *bool   `json:"done"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	noteID := c.PathValue("noteID")
	if req.Done != nil {
		n, err := notes.Check(globalLayout, sc, noteID, *req.Done)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// A done-only request is the checkbox click; nothing else to apply.
		if req.Body == nil && req.Checkable == nil && req.Audience == nil && req.Hidden == nil {
			c.JSON(http.StatusOK, n)
			return
		}
	}
	n, err := notes.Update(globalLayout, sc, noteID, notes.UpdateOptions{
		Body: req.Body, Checkable: req.Checkable, Audience: req.Audience, Hidden: req.Hidden,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, n)
}

// apiNotesDelete handles DELETE /api/notes/{noteID}?ticket_id=…|session_id=…
func apiNotesDelete(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	sc, ok := notesScopeFromQuery(c)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "scope not found"})
		return
	}
	if err := notes.Delete(globalLayout, sc, c.PathValue("noteID")); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

/* ── project ticket config + saved board filter ──────────────────────────── */

// apiProjectTicketConfig handles PUT /api/projects/{id}/ticket-config.
func apiProjectTicketConfig(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req project.TicketConfig
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Statuses first: a board with no terminal stage would leave
	// auto-resolve nowhere to move finished work, and dropping a status
	// that still holds tickets would lose sight of them.
	if err := ticket.ValidateStatuses(req.Statuses); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if orphans := ticket.OrphanedStatuses(globalLayout, id, req.Statuses); len(orphans) > 0 {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "these statuses still hold tickets: " + strings.Join(orphans, ", ") +
				" — move or delete those tickets first",
		})
		return
	}
	for i, s := range req.Statuses {
		req.Statuses[i].Key = strings.TrimSpace(s.Key)
		if strings.TrimSpace(s.Label) == "" {
			req.Statuses[i].Label = req.Statuses[i].Key
		}
	}
	seen := map[string]bool{}
	for i, f := range req.Fields {
		key := strings.TrimSpace(f.Key)
		if key == "" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "field key is required"})
			return
		}
		if seen[key] {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "duplicate field key: " + key})
			return
		}
		seen[key] = true
		if f.Type != "text" && f.Type != "select" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "field " + key + ": type must be text or select"})
			return
		}
		if f.Type == "select" && len(f.Options) == 0 {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "field " + key + ": select needs at least one option"})
			return
		}
		req.Fields[i].Key = key
		if strings.TrimSpace(f.Label) == "" {
			req.Fields[i].Label = key
		}
	}
	if req.FollowupAfterSec < 0 || req.AutoResolveAfterSec < 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "durations must be >= 0"})
		return
	}
	// Auto-create rules carry a regex an operator typed. Refusing it here is
	// the only place it can be reported: by the time a rule is judged, the
	// session that would have been tracked is already past.
	if err := ticket.ValidateAutoCreate(req.AutoCreate); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// First enable with an empty schema gets the seed fields, so the board
	// is useful before anyone visits the field editor.
	if req.Enabled && len(req.Fields) == 0 {
		req.Fields = project.DefaultTicketFields()
	}

	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	// Webhooks carry a secret the client never sees, so the stored copy has
	// to be merged in before validation — otherwise saving an unrelated
	// setting would silently unsign every endpoint.
	if err := normaliseWebhooks(&req.Integrations, p.Meta.Ticket.Integrations); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	buttons, err := normaliseTicketButtons(req.Integrations.Buttons)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.Integrations.Buttons = buttons
	meta := p.Meta
	meta.Ticket = req
	if _, err := globalMgr.UpdateProject(c.Context(), id, meta); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// apiTicketPrefsGet handles GET /api/me/ticket-prefs — the standing answers
// this user has given to ticket prompts.
func apiTicketPrefsGet(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusOK, map[string]string{"auto_delete_empty": ""})
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"auto_delete_empty": u.Metadata.AutoDeleteEmptyTickets,
	})
}

// apiTicketPrefsSave handles PUT /api/me/ticket-prefs.
func apiTicketPrefsSave(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "login required"})
		return
	}
	var req struct {
		AutoDeleteEmpty string `json:"auto_delete_empty"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if globalAuth == nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "auth service unavailable"})
		return
	}
	if err := globalAuth.SetAutoDeleteEmptyTickets(c.Context(), u.ID, req.AutoDeleteEmpty); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// apiRailPrefsGet handles GET /api/me/rail — the caller's rail layout.
func apiRailPrefsGet(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusOK, entity.RailPrefs{})
		return
	}
	c.JSON(http.StatusOK, u.Metadata.Rail)
}

// apiRailPrefsSave handles PUT /api/me/rail.
func apiRailPrefsSave(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "login required"})
		return
	}
	var p entity.RailPrefs
	if err := c.BindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if globalAuth == nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "auth service unavailable"})
		return
	}
	if err := globalAuth.SetRailPrefs(c.Context(), u.ID, p); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// apiTicketFilterGet handles GET /api/me/ticket-filters/{projectID}.
func apiTicketFilterGet(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusOK, entity.TicketFilter{})
		return
	}
	c.JSON(http.StatusOK, u.Metadata.TicketFilters[c.PathValue("projectID")])
}

// apiTicketFilterSave handles PUT /api/me/ticket-filters/{projectID}.
func apiTicketFilterSave(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "login required"})
		return
	}
	var f entity.TicketFilter
	if err := c.BindJSON(&f); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if globalAuth == nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "auth service unavailable"})
		return
	}
	if err := globalAuth.SetTicketFilter(c.Context(), u.ID, c.PathValue("projectID"), f); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// pageLooseSessions picks one page of the untracked rail — the rows
// strictly after `after` (nil = from the top), in the sidebar's order
// (running first, then newest by the pool's clock or the saved one) —
// without sorting every chat the project ever had.
//
// orderedIDs is the registry's list, already sorted by saved LastActive.
// Only a session the pool knows about (or one queued) can rank anywhere
// other than where that list puts it, and those are a handful — so they
// are gathered whole, while the rest are taken in list order, skipping any
// at or before the cursor, and the walk stops collecting once it holds
// `limit` of them: anything later is older than every one already held and
// can never reach the page. It keeps collecting while the age still TIES
// the last one held, since the id decides those and the registry's order
// among equal ages is not the id's. The merge is then a sort of ~page-size
// items, not of thousands.
//
// next is the cursor of the page's last row, set only when more rows
// follow it, so "no cursor" is the rail's "that was all". total counts every
// matching chat either way, because the header shows "25/142" whether or
// not the rows were asked for.
func pageLooseSessions(
	orderedIDs []string,
	sessions map[string]session.Session,
	lc map[string]view.SessionLifecycleVM,
	keep func(sid string, s session.Session) bool,
	after *railKey,
	limit int,
	wantRows bool,
) (page []string, next string, total int) {
	hot := make([]string, 0, 8)
	cold := make([]string, 0, limit)
	var coldLast int64
	remaining := 0 // rows after the cursor, held or not
	for _, sid := range orderedIDs {
		s, ok := sessions[sid]
		if !ok || !keep(sid, s) {
			continue
		}
		total++
		if !wantRows {
			continue
		}
		k := sidebarKey(sid, s, lc[sid])
		if after != nil && !after.before(k) {
			continue
		}
		remaining++
		if _, live := lc[sid]; live || k.running {
			hot = append(hot, sid)
			continue
		}
		if len(cold) < limit || k.at == coldLast {
			cold = append(cold, sid)
			coldLast = k.at
		}
	}
	if !wantRows || remaining == 0 {
		return nil, "", total
	}
	merged := orderSidebarIDs(append(hot, cold...), sessions, lc)
	if len(merged) > limit {
		merged = merged[:limit]
	}
	if remaining > len(merged) {
		last := merged[len(merged)-1]
		next = sidebarKey(last, sessions[last], lc[last]).encode()
	}
	return merged, next, total
}

package agents

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/pkg/tool"
)

/* An ordinary ticket session (PLAN-team-tickets T12b): ONE home agent —
   the session's own agent, run as its owner — plus @mentions. It is not
   a group: nobody is added or removed. The home agent answers only its
   owner; a mention, or anyone else's message, reaches ONE agent of the
   sender's own (the one named, else their Captain), run as the sender in
   a backing session and posted into the thread under its owner. The
   room's rules are reused as they are: foreignMentions, the run slots,
   groupRun, roomPrompt. */

// isTicketSession reports whether sess is an ordinary ticket session:
// on a ticket, and neither a room, a group chat nor a backing session.
func isTicketSession(sess session.Session) bool {
	return team.IsTicketSession(sess.Meta)
}

// errTicketSessionFiles refuses files from anyone but a ticket session's
// owner: only the home agent takes them.
const errTicketSessionFiles = "files go to this session's own agent only — send the text alone to reach your agent"

// ticketSessionOwner reports whether the caller is ticket session sess's
// owner — the one person its home agent answers, and whose Stop reaches
// its run. An ownerless session has none.
func ticketSessionOwner(c *tool.Ctx, sess session.Session) bool {
	actor := actorID(c)
	return actor != "" && actor == sess.Meta.UserID
}

// ticketSessionRefusesFiles reports whether files sent to session id must
// be refused before they are saved: an ordinary ticket session, and the
// sender not its owner (anything they send reaches their own agent, which
// takes no files).
func ticketSessionRefusesFiles(c *tool.Ctx, id string) bool {
	sess, ok := globalMgr.Registry().Session(id)
	return ok && isTicketSession(sess) && !ticketSessionOwner(c, sess)
}

// ticketSessionPlan is who a message in a ticket session reaches.
type ticketSessionPlan struct {
	// Home: the session's own agent answers, through its own run.
	Home bool
	// Target answers instead (HasTarget), in a backing session run as the
	// sender. Captain marks the sender's own Captain answering a message
	// with no @ in someone else's session; Default the project default
	// standing in for a sender with no Captain.
	Target    teamlink.Peer
	HasTarget bool
	Captain   bool
	Default   bool
	// Foreign: someone else's agents named; Unknown: handles that are none
	// of the sender's agents; Refused: the sender's disabled agents named;
	// Extra: further agents named — one message reaches one agent.
	Foreign []foreignMention
	Unknown []string
	Refused []string
	Extra   []string
}

// planTicketSessionMessage routes text from sender in a ticket session
// owned by owner ("" = nobody: everyone is answered by their own). own is the sender's own Team agents; senderHandle their
// person handle, so "@yoga/research" from yoga is their own @research.
func planTicketSessionMessage(text, owner, sender, senderHandle string, own []teamlink.Peer) ticketSessionPlan {
	if owner != "" && sender == owner {
		// The owner talks to their own agent as in any session: its
		// @mentions — their own agents, sub-agent roles, anyone else's —
		// go through the existing Team hand-off (TeamMentionRouter).
		return ticketSessionPlan{Home: true}
	}
	var plan ticketSessionPlan
	hit, unknown := teamlink.LeadMentions(text, own, false)
	plan.Unknown = unknown
	for _, f := range foreignMentions(text) {
		if !f.Invalid && senderHandle != "" && f.Owner == senderHandle {
			found := false
			for _, p := range own {
				if teamlink.NormalizeHandle(p.Handle) == f.Handle {
					hit, found = append(hit, p), true
					break
				}
			}
			if !found {
				plan.Unknown = append(plan.Unknown, f.Handle)
			}
			continue
		}
		plan.Foreign = append(plan.Foreign, f)
	}
	seen := map[string]bool{}
	for _, p := range hit {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		switch {
		case p.Disabled:
			plan.Refused = append(plan.Refused, p.Handle)
		case plan.HasTarget:
			plan.Extra = append(plan.Extra, p.Handle)
		default:
			plan.Target, plan.HasTarget = p, true
		}
	}
	if plan.HasTarget {
		return plan
	}
	if len(hit) > 0 || len(plan.Foreign) > 0 || len(plan.Unknown) > 0 {
		return plan // only refusals: nobody runs
	}
	// Someone else (or anyone, in a session with no owner), no @: their
	// own Captain answers; nobody is left
	// without an answer, so with no Captain the project default does, run
	// as them.
	for _, p := range own {
		if p.IsCaptain && !p.Disabled {
			plan.Target, plan.HasTarget, plan.Captain = p, true, true
			return plan
		}
	}
	plan.HasTarget, plan.Default = true, true
	return plan
}

// spokeIn reports whether agentID, working for owner, already has a turn
// in turns.
func spokeIn(turns []store.ConversationTurn, agentID, owner string) bool {
	for _, t := range turns {
		if t.Speaker != nil && t.Speaker.AgentID == agentID && t.Speaker.OwnerID == owner {
			return true
		}
	}
	return false
}

// ticketSessionTurnOwner is whose a ticket session turn is: as in a room,
// and an agent turn with no owner on it is the home agent's — it works
// for the session's owner.
func ticketSessionTurnOwner(owner string) func(store.ConversationTurn) string {
	return func(t store.ConversationTurn) string {
		if o := roomTurnOwner(t); o != "" {
			return o
		}
		if t.Role == "assistant" {
			return owner
		}
		return ""
	}
}

// sendTicketSessionMessage is POST /sessions/{id}/send on an ordinary
// ticket session. It returns false when the session's own agent answers
// as usual (not a ticket session, or the owner with no @ for anyone
// else); otherwise it has answered the request.
func sendTicketSessionMessage(c *tool.Ctx, sess session.Session, text string, hasFiles bool) bool {
	if !isTicketSession(sess) || globalMgr == nil {
		return false
	}
	sender := actorID(c)
	if sender == "" {
		return false
	}
	_, own := ownTeamPeers(c.Context(), sender)
	plan := planTicketSessionMessage(text, sess.Meta.UserID, sender, personHandle(c.Context(), sender), own)
	if plan.Home {
		return false
	}
	if hasFiles {
		c.JSON(http.StatusBadRequest, map[string]string{"error": errTicketSessionFiles})
		return true
	}
	if len(text) > roomTextMax {
		c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("message too long (max %d KB)", roomTextMax>>10)})
		return true
	}
	tk, err := ticket.Load(globalLayout, sess.Meta.ProjectID, sess.Meta.TicketID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return true
	}
	if plan.Default {
		plan.Target = roomHomePeer(sess.Meta.ProjectID)
	}

	var runCtx context.Context
	var runStarted, runRelease func()
	if plan.HasTarget {
		var ok bool
		runCtx, runStarted, runRelease, ok = admitRoomRun(context.WithoutCancel(c.Context()), sess.ID, sender)
		if !ok {
			c.JSON(http.StatusTooManyRequests, map[string]string{"error": "your agents in this session already have messages waiting — wait for them, or Stop"})
			return true
		}
	}
	queued := false
	defer func() {
		if runRelease != nil && !queued {
			runRelease()
		}
	}()
	backing := map[string]string{}
	if plan.HasTarget {
		var id string
		if plan.Default {
			id, err = roomDefaultBacking(c, sess.ID, sess.Meta.ProjectID)
		} else {
			id, err = groupBacking(c, sess.ID, plan.Target.ID)
		}
		if errors.Is(err, errNoProjectAgent) {
			c.JSON(http.StatusForbidden, map[string]string{"error": err.Error()})
			return true
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		backing[plan.Target.ID] = id
		// The session may have been deleted meanwhile: a backing session
		// just made for it would be an orphan.
		if _, ok := globalMgr.Registry().Session(sess.ID); !ok {
			stopRoomSession(c.Context(), id, "its ticket session was deleted")
			_ = globalMgr.DeleteSession(c.Context(), id)
			c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
			return true
		}
	}

	history, _ := loadConversation(globalLayout, sess.ID)
	now := time.Now().UTC()
	turn := store.ConversationTurn{
		TurnID: fmt.Sprintf("%d", now.UnixNano()), Timestamp: now, Role: "user", Source: "ui", Text: text,
		Sender: &store.Sender{ID: sender, Name: actorName(c), Channel: "ui", WickUserID: sender},
	}
	if err := recordGroupTurn(sess.ID, turn); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return true
	}
	by := roomActorHandle(c)
	if plan.Captain && !spokeIn(history, plan.Target.ID, sender) {
		name := systemLineName(plan.Target.Name, plan.Target.Handle)
		emitSystemEvent(sess.ID, store.KindGroupMemberAdded, fmt.Sprintf("%s · %s joined · by %s", name, by, by),
			map[string]string{"agent_id": plan.Target.ID, "handle": plan.Target.Handle, "owner_id": sender})
	}
	emitTicketSessionRefusals(c, plan, own, history)
	if plan.HasTarget {
		run := newTicketSessionRun(c.Context(), sess, tk, sender, plan.Target, backing)
		run.started = runStarted
		queued = true
		go func() {
			defer runRelease()
			run.play(runCtx, sess.ID, []teamlink.Peer{plan.Target})
		}()
	}
	c.JSON(http.StatusOK, map[string]string{"status": "sent"})
	return true
}

// emitTicketSessionRefusals writes one system line per mention that runs
// nobody.
func emitTicketSessionRefusals(c *tool.Ctx, plan ticketSessionPlan, own []teamlink.Peer, history []store.ConversationTurn) {
	sid := c.PathValue("id")
	for _, f := range plan.Foreign {
		if f.Invalid {
			emitSystemEvent(sid, store.KindMentionRefused, "That agent isn't one of yours — mention one of your own agents",
				map[string]string{"from": "user", "reason": "another person's agent"})
			continue
		}
		emitSystemEvent(sid, store.KindMentionRefused, foreignRefusal(f, own, history),
			map[string]string{"from": "user", "to": f.Owner + "/" + f.Handle, "reason": "another person's agent"})
	}
	for _, h := range plan.Unknown {
		emitSystemEvent(sid, store.KindMentionRefused, fmt.Sprintf("@%s isn't one of your agents", h),
			map[string]string{"from": "user", "to": h, "reason": "not your agent"})
	}
	for _, h := range plan.Refused {
		emitSystemEvent(sid, store.KindMentionRefused, fmt.Sprintf("@%s is disabled", h),
			map[string]string{"from": "user", "to": h, "reason": "disabled"})
	}
	for _, h := range plan.Extra {
		emitSystemEvent(sid, store.KindMentionRefused, fmt.Sprintf("@%s was not asked — one message reaches one agent", h),
			map[string]string{"from": "user", "to": h, "reason": "one agent per message"})
	}
}

// foreignRefusal is the line for "@owner/handle" of someone else's agent:
// named as it already answered in the thread, else by its @handle (a
// handle names no agent until its owner's Team is looked up).
func foreignRefusal(f foreignMention, own []teamlink.Peer, history []store.ConversationTurn) string {
	name := ""
	for _, t := range history {
		if sp := t.Speaker; sp != nil && sp.OwnerHandle == f.Owner && teamlink.NormalizeHandle(sp.Handle) == f.Handle && sp.Name != "" {
			name = sp.Name
		}
	}
	return fmt.Sprintf("%s only takes instructions from @%s. Ask them, or %s.", foreignLabel(f, name), f.Owner, ownCaptainHint(own))
}

// newTicketSessionRun is the group runner for one message in a ticket
// session: the one target, run as sender, its turn posted into the thread
// under sender with the labels other viewers need.
func newTicketSessionRun(ctx context.Context, sess session.Session, tk ticket.Ticket, sender string, target teamlink.Peer, backing map[string]string) *groupRun {
	sid := sess.ID
	senderHandle := personHandle(ctx, sender)
	handles := map[string]string{sender: senderHandle}
	handleOf := func(uid string) string {
		if h, ok := handles[uid]; ok {
			return h
		}
		h := personHandle(ctx, uid)
		handles[uid] = h
		return h
	}
	peers := []teamlink.Peer{target}
	return &groupRun{
		group: session.AgentGroup{Name: tk.Title}, members: peers, limit: teamlink.GroupLimit(peers, 0), backing: backing,
		lockKey: roomRunKey(sid, sender),
		turn: func(ctx context.Context, p teamlink.Peer, prompt string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, roomTurnTimeout)
			defer cancel()
			reply, err := roomBackingTurn(ctx, backing[p.ID], prompt)
			if cerr := ctx.Err(); cerr != nil {
				stopRoomBacking(context.WithoutCancel(ctx), backing[p.ID], "stopped from its ticket session")
				if cerr == context.DeadlineExceeded {
					return "", fmt.Errorf("no answer after %s", roomTurnTimeout)
				}
				return "", cerr
			}
			return reply, err
		},
		history: func() []store.ConversationTurn {
			turns, _ := loadConversation(globalLayout, sid)
			return turns
		},
		record: func(t store.ConversationTurn) {
			if t.Speaker != nil {
				t.Speaker.OwnerID, t.Speaker.OwnerHandle = sender, senderHandle
				if t.Speaker.Name == "" {
					t.Speaker.Name = target.Name
				}
			}
			if t.Role == "system" {
				recordSystemTurn(globalLayout, globalBcast, sid, t)
				return
			}
			if err := recordGroupTurn(sid, t); err != nil {
				log.Warn().Err(err).Str("session", sid).Msg("ticket session: turn write failed")
			}
		},
		typing: func(teamlink.Peer, bool) {},
		prompt: func(self teamlink.Peer, from string, history []store.ConversationTurn) string {
			return roomPrompt(roomPromptIn{
				Ticket: tk, Self: self, From: from, Sender: sender, Peers: peers, History: history,
				OwnerOf: ticketSessionTurnOwner(sess.Meta.UserID), HandleOf: handleOf, Place: "session",
			})
		},
	}
}

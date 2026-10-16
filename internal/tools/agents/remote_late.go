package agents

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Late replies of remote agents. A reply that arrives after its turn
// timed out — pushed in the late window, or read back by "Check again" —
// takes the place of the timeout in the conversation and is handed, once,
// to the agent that asked. Everything needed is read from the
// conversation itself (the mention_handoff turn carries the caller's
// session), so it holds across a restart and past the Hub's task TTL.

// lateOutcome is what settling a late reply did.
type lateOutcome struct {
	// Replaced: the timed-out turn now shows the reply.
	Replaced bool `json:"replaced"`
	// ForwardedTo is the handle of the agent the reply was handed to, ""
	// when nobody asked or it was handed over before.
	ForwardedTo string `json:"forwarded_to"`
}

// deliverLate wakes the asking session with a late reply. Tests replace it.
var deliverLate = func(ctx context.Context, sessionID, text string) error {
	if globalTeamHub == nil {
		return errors.New("team link is not ready")
	}
	h := globalTeamHub()
	if h == nil || h.Notify == nil {
		return errors.New("team link is not ready")
	}
	return h.Notify.Deliver(ctx, sessionID, text)
}

// followUpLate puts a late reply on the person's own task (FollowUp),
// where they see it, without waking the asking chat. Tests replace it.
var followUpLate = func(ctx context.Context, sessionID, text string) bool {
	if globalTeamHub == nil {
		return false
	}
	h := globalTeamHub()
	return h != nil && h.FollowUp(ctx, sessionID, text)
}

// lateLocks serialises settling per session, so a click racing the late
// window cannot forward twice.
var lateLocks sync.Map

func lockLate(sessionID string) func() {
	m, _ := lateLocks.LoadOrStore(sessionID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// settleLateReply records text as the reply to sessionID's turn that timed
// out (or ended without its marker) and forwards it to the agent that
// asked, unless that turn was settled before. note is the replacement
// turn's note (remote.NoteRechecked); "" writes nothing, for the late
// window, whose reply the session's store writes itself. Nothing to settle
// — no such turn, or a message sent after it — is not an error.
func settleLateReply(ctx context.Context, sessionID, text, note string) (lateOutcome, error) {
	var out lateOutcome
	if sessionID == "" || strings.TrimSpace(text) == "" {
		return out, nil
	}
	defer lockLate(sessionID)()
	turns, err := loadConversation(globalLayout, sessionID)
	if err != nil {
		return out, err
	}
	i := lateTarget(turns)
	if i < 0 {
		return out, nil
	}
	target := turns[i]
	prev := replacementOf(turns, target.TurnID)
	if prev == nil {
		if c, ok := lateCaller(turns, i); ok && c.userTask {
			followUpLate(context.WithoutCancel(ctx), sessionID, text)
		} else if ok {
			msg := remote.LateForward(c.remote, c.taskID, text)
			if err := deliverLate(context.WithoutCancel(ctx), c.session, msg); err == nil {
				out.ForwardedTo = c.handle
			}
		}
	}
	if note == "" {
		return out, nil
	}
	out.Replaced = true
	if prev != nil && strings.TrimSpace(prev.Text) == strings.TrimSpace(text) {
		return out, nil
	}
	now := time.Now().UTC()
	turn := store.ConversationTurn{
		TurnID: fmt.Sprintf("%d", now.UnixNano()), Timestamp: now,
		Role: "assistant", Agent: firstNonBlank(target.Agent, "main"), Provider: target.Provider,
		Text: text, RemoteNote: note, Replaces: target.TurnID,
	}
	if out.ForwardedTo != "" {
		turn.Extras = map[string]string{"forwarded_to": out.ForwardedTo}
	}
	if err := storage.AppendJSONL(globalLayout.SessionConversation(sessionID), "wick-conv-v1", sessionID, turn); err != nil {
		return lateOutcome{ForwardedTo: out.ForwardedTo}, err
	}
	touchSessionActive(globalLayout, sessionID)
	return out, nil
}

// lateTarget is the index of the turn a late reply stands in for: the
// last reply turn, when it is a remote timeout or a reply that ended
// without its marker and no message came after it. -1 for none.
// Replacements and system events in between are looked past.
func lateTarget(turns []store.ConversationTurn) int {
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		switch {
		case t.Role == "user":
			return -1
		case t.Replaces != "":
			continue
		case t.Role == "system" && t.IsError:
			if _, ok := remote.IsTimeout(t.Text); ok {
				return i
			}
			return -1
		case t.Role == "assistant":
			if t.RemoteNote == remote.NoteNoMarker {
				return i
			}
			return -1
		}
	}
	return -1
}

// replacementOf is the latest turn standing in for turn id, nil for none.
func replacementOf(turns []store.ConversationTurn, id string) *store.ConversationTurn {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Replaces == id {
			return &turns[i]
		}
	}
	return nil
}

// lateAsker is the agent that asked a turn: its session and handle, the
// remote's handle and the task id, from the turn's mention_handoff.
type lateAsker struct {
	session, handle, remote, taskID string
	// userTask: the person sent the task with an @mention (origin user);
	// its reply goes on the task, never into the asking chat.
	userTask bool
}

// lateCaller finds who asked the turn ending at index i: the message
// that started it must have come from a teammate, and the handoff written
// just before that message names the caller. A person's own message
// forwards nowhere. A task the person sent (origin user, on any handoff
// of it) is reported with userTask and must not be forwarded.
func lateCaller(turns []store.ConversationTurn, i int) (lateAsker, bool) {
	j := i - 1
	for j >= 0 && turns[j].Role != "user" {
		j--
	}
	if j < 0 || turns[j].Source != sourceTeam {
		return lateAsker{}, false
	}
	for k := j - 1; k >= 0 && turns[k].Role != "user"; k-- {
		t := turns[k]
		if t.Kind != store.KindMentionHandoff {
			continue
		}
		c := lateAsker{session: t.Extras["from_session"], handle: t.Extras["from"], remote: t.Extras["to"], taskID: t.Extras["task_id"]}
		if c.session == "" || c.handle == "" || c.handle == "user" {
			return lateAsker{}, false
		}
		// A task the person sent with an @mention is theirs: its late
		// reply stays on the task, never waking the asking chat's agent.
		// Any handoff of the task says so — an answer turn's own may not.
		c.userTask = t.Extras["origin"] == teamlink.OriginUser
		for m := k - 1; m >= 0 && !c.userTask; m-- {
			if o := turns[m]; o.Kind == store.KindMentionHandoff && o.Extras["task_id"] == c.taskID && o.Extras["origin"] == teamlink.OriginUser {
				c.userTask = true
			}
		}
		return c, true
	}
	return lateAsker{}, false
}

// recheckReply is a "Check again" answer: the reply as it stands now,
// and what keeping it did.
type recheckReply struct {
	remote.Recheck
	lateOutcome
}

// remoteRechecker is the remote.Rechecker of remote agent p, nil when its
// kind cannot read a reply back.
func remoteRechecker(ctx context.Context, p entity.AgentPersona) (remote.Rechecker, error) {
	if !isSlackRemote(p) || slackRemoteStore() == nil {
		return nil, nil
	}
	cfg, found, err := slackRemoteStore().Load(p.ID)
	if err != nil || !found {
		return nil, err
	}
	src, err := slackRemoteSource(ctx, p.OwnerUserID, cfg)
	if err != nil {
		return nil, err
	}
	return src, nil
}

// apiTeamRemoteRecheck handles POST
// /api/team/agents/{id}/remote/recheck?session_id= (and the older
// /slack-remote/recheck): the "Check again" of a remote turn that timed
// out or ended without its marker. It reads the reply back — never
// posting to the remote — and, once the reply is settled, keeps it in
// place of the timed-out turn and forwards it to the agent that asked
// (settleLateReply), so calling it twice is harmless. A reply still in
// progress changes nothing. Owner of the agent only.
func apiTeamRemoteRecheck(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	sid := c.Query("session_id")
	s, found := globalMgr.Registry().Session(sid)
	if sid == "" || !found || s.Meta.AgentID != p.ID {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rc, err := remoteRechecker(c.Context(), p)
	switch {
	case err != nil:
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	case rc == nil:
		c.JSON(http.StatusNotFound, map[string]string{"error": "this agent cannot check its reply again"})
		return
	}
	res, err := rc.Recheck(c.Context(), globalLayout.SessionDir(sid))
	switch {
	case errors.Is(err, remote.ErrNoTurn):
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	case err != nil:
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	out := recheckReply{Recheck: res}
	if res.Settled() {
		if out.lateOutcome, err = settleLateReply(c.Context(), sid, res.Text, remote.NoteRechecked); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, out)
}

// apiTeamRemoteQueueCancel drops a message still queued behind a remote
// agent's running turn, so it is never sent.
func apiTeamRemoteQueueCancel(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	sid := c.Query("session_id")
	s, found := globalMgr.Registry().Session(sid)
	if sid == "" || !found || s.Meta.AgentID != p.ID {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if !globalPool.CancelQueued(sid, c.PathValue("queue_id")) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "message is no longer queued"})
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"cancelled": true})
}

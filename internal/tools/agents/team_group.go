package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/pkg/tool"
)

// Group chats of Team agents (PLAN §6.0c).
//
// Model: the group is ONE conversation — a session whose meta carries
// AgentGroup and that never spawns a provider. Each member answers in a
// backing session of its own (meta.agent_id = the member, meta.
// group_session_id = the group), so the turn runs with that agent's
// persona, access and run_as exactly as in its own chat; the group grants
// nothing. A backing turn receives the last groupContextTurns messages of
// the thread plus the message that triggered it, and its reply is copied
// into the group thread as an assistant turn with speaker.via = "group".
// Members' replies sit side by side in the thread, never nested.
//
// Routing (teamlink/group.go): a person's lines that start with @member go
// to those members only, in line order; with none, the default responder
// answers. A member's reply may @ another member, which hands that member
// a turn — counted against the group's cap (the smallest max_hops of the
// members, or the group's lower override) and reset by every human
// message.

const (
	// sourceGroup marks the turns wick posts into a backing session.
	sourceGroup = "group"
	// groupContextTurns is how many recent thread messages a member sees.
	groupContextTurns = 12
	// groupNameMax caps a group's name, in characters.
	groupNameMax = 64
	// evGroupTurn carries a user or member turn of a group thread.
	evGroupTurn = "group_turn"
	// evGroupTyping says a member started ("start") or finished ("stop").
	evGroupTyping = "group_typing"
)

/* ── DTOs ────────────────────────────────────────────────────────────── */

// TeamGroupMember is one member as the roster and header show it.
type TeamGroupMember struct {
	ID        string      `json:"id"`
	Handle    string      `json:"handle"`
	Name      string      `json:"name"`
	Avatar    team.Avatar `json:"avatar"`
	IsCaptain bool        `json:"is_captain"`
	Disabled  bool        `json:"disabled"`
	MaxHops   int         `json:"max_hops"`
}

// TeamGroupItem is one group chat.
type TeamGroupItem struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Members          []TeamGroupMember `json:"members"`
	DefaultResponder string            `json:"default_responder"`
	// Responder is the handle a message with no @ goes to now.
	Responder       string `json:"responder"`
	MaxHopsOverride int    `json:"max_hops_override"`
	// MembersMaxHops is the smallest cap of the members; MaxHops the cap
	// in force (override applied).
	MembersMaxHops int        `json:"members_max_hops"`
	MaxHops        int        `json:"max_hops"`
	LastActive     *time.Time `json:"last_active"`
	LastPreview    string     `json:"last_preview"`
	Unread         bool       `json:"unread"`
}

type teamGroupWriteReq struct {
	Name             *string   `json:"name"`
	Members          *[]string `json:"members"`
	DefaultResponder *string   `json:"default_responder"`
	MaxHopsOverride  *int      `json:"max_hops_override"`
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// ownerPeers is the owner's agents as teamlink peers, by id.
func ownerPeers(ctx context.Context, ownerID string) (map[string]teamlink.Peer, []teamlink.Peer, error) {
	all, err := teamDirectory{svc: globalTeam}.Peers(ctx, ownerID)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]teamlink.Peer, len(all))
	for _, p := range all {
		byID[p.ID] = p
	}
	return byID, all, nil
}

// groupPeers is g's members that still exist, in group order.
func groupPeers(g *session.AgentGroup, byID map[string]teamlink.Peer) []teamlink.Peer {
	out := make([]teamlink.Peer, 0, len(g.Members))
	for _, id := range g.Members {
		if p, ok := byID[id]; ok {
			out = append(out, p)
		}
	}
	return out
}

// ownGroup loads the caller's group {id}; false = a 404/503 was written.
func ownGroup(c *tool.Ctx) (session.Session, bool) {
	if notReady(c) {
		return session.Session{}, false
	}
	if globalTeam == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Team is not available"})
		return session.Session{}, false
	}
	sess, ok := globalMgr.Registry().Session(c.PathValue("id"))
	if !ok || sess.Meta.AgentGroup == nil || sess.Meta.UserID != actorID(c) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "group not found"})
		return session.Session{}, false
	}
	return sess, true
}

func groupItem(ctx context.Context, sess session.Session, byID map[string]teamlink.Peer) TeamGroupItem {
	g := sess.Meta.AgentGroup
	members := groupPeers(g, byID)
	it := TeamGroupItem{
		ID: sess.ID, Name: g.Name, DefaultResponder: teamlink.NormalizeResponder(g.DefaultResponder),
		MaxHopsOverride: g.MaxHopsOverride,
		MembersMaxHops:  teamlink.GroupLimit(members, 0),
		MaxHops:         teamlink.GroupLimit(members, g.MaxHopsOverride),
		LastActive:      timePtr(sess.Meta.LastActive),
		Members:         make([]TeamGroupMember, 0, len(members)),
	}
	for _, p := range members {
		m := TeamGroupMember{ID: p.ID, Handle: p.Handle, Name: p.Name, IsCaptain: p.IsCaptain, Disabled: p.Disabled, MaxHops: teamlink.EffectiveHops(p.MaxHops)}
		if a, err := globalTeam.Get(ctx, p.ID); err == nil {
			m.Avatar = team.DecodeAvatar(a.Avatar)
		}
		it.Members = append(it.Members, m)
	}
	if r, err := teamlink.DefaultResponderOf(members, g.DefaultResponder); err == nil {
		it.Responder = r.Handle
	}
	if turns, err := loadConversation(globalLayout, sess.ID); err == nil {
		for i := len(turns) - 1; i >= 0; i-- {
			if t := turns[i]; t.Role != "system" && strings.TrimSpace(t.Text) != "" {
				it.LastPreview = firstLineOf(t.Text, 120)
				if t.Speaker != nil {
					it.LastPreview = "@" + t.Speaker.Handle + ": " + it.LastPreview
				}
				break
			}
		}
		// Unread: something after the owner last looked, the owner's own
		// messages aside.
		for i := len(turns) - 1; i >= 0; i-- {
			t := turns[i]
			if g.LastReadAt != nil && !t.Timestamp.After(*g.LastReadAt) {
				break
			}
			if t.Role == "assistant" {
				it.Unread = true
				break
			}
		}
	}
	return it
}

func firstLineOf(s string, max int) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > max {
				return string(r[:max-1]) + "…"
			}
			return l
		}
	}
	return ""
}

// validGroupName trims a name and checks its length.
func validGroupName(v string) (string, error) {
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return "", errors.New("name is required")
	}
	if len([]rune(v)) > groupNameMax {
		return "", fmt.Errorf("name must be at most %d characters", groupNameMax)
	}
	return v, nil
}

// applyGroupWrite applies req to g against the owner's agents. Pure apart
// from its inputs, so the rules are tested without a server.
func applyGroupWrite(g *session.AgentGroup, req teamGroupWriteReq, own []teamlink.Peer) error {
	if req.Name != nil {
		n, err := validGroupName(*req.Name)
		if err != nil {
			return err
		}
		g.Name = n
	}
	if req.Members != nil {
		ids, err := teamlink.GroupMembers(*req.Members, own)
		if err != nil {
			return err
		}
		g.Members = ids
	}
	if req.DefaultResponder != nil {
		v := strings.TrimSpace(*req.DefaultResponder)
		if v != teamlink.ResponderCaptain && v != teamlink.ResponderFirst {
			return errors.New("default_responder must be captain or first")
		}
		g.DefaultResponder = v
	}
	if req.MaxHopsOverride != nil {
		g.MaxHopsOverride = *req.MaxHopsOverride
	}
	byID := make(map[string]teamlink.Peer, len(own))
	for _, p := range own {
		byID[p.ID] = p
	}
	members := groupPeers(g, byID)
	if !teamlink.ValidGroupOverride(members, g.MaxHopsOverride) {
		return fmt.Errorf("max_hops_override must be 0 (none) or 1–%d, the smallest cap of the members", teamlink.GroupLimit(members, 0))
	}
	return nil
}

// memberDiff lists ids in after but not before (added) and the reverse.
func memberDiff(before, after []string) (added, removed []string) {
	in := func(list []string, id string) bool {
		for _, x := range list {
			if x == id {
				return true
			}
		}
		return false
	}
	for _, id := range after {
		if !in(before, id) {
			added = append(added, id)
		}
	}
	for _, id := range before {
		if !in(after, id) {
			removed = append(removed, id)
		}
	}
	return added, removed
}

func saveGroupMeta(sessionID string, edit func(*session.Meta)) error {
	sess, err := session.Load(globalLayout, sessionID)
	if err != nil {
		return err
	}
	edit(&sess.Meta)
	if err := session.SaveMeta(globalLayout, sessionID, sess.Meta); err != nil {
		return err
	}
	return globalMgr.RefreshSession(sessionID)
}

/* ── HTTP ────────────────────────────────────────────────────────────── */

// apiTeamGroupList handles GET /api/team/groups: the caller's groups,
// most recent first.
func apiTeamGroupList(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalTeam == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Team is not available"})
		return
	}
	owner := actorID(c)
	byID, _, err := ownerPeers(c.Context(), owner)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := []TeamGroupItem{}
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.AgentGroup != nil && s.Meta.UserID == owner {
			out = append(out, groupItem(c.Context(), s, byID))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].LastActive, out[j].LastActive
		return a != nil && (b == nil || a.After(*b))
	})
	c.JSON(http.StatusOK, map[string]any{"groups": out})
}

// apiTeamGroupCreate handles POST /api/team/groups. Members must be the
// caller's own agents (keputusan 18), at least two.
func apiTeamGroupCreate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalTeam == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Team is not available"})
		return
	}
	var req teamGroupWriteReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Name == nil || req.Members == nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "name and members are required"})
		return
	}
	owner := actorID(c)
	byID, own, err := ownerPeers(c.Context(), owner)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	g := &session.AgentGroup{DefaultResponder: teamlink.ResponderCaptain}
	if err := applyGroupWrite(g, req, own); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id := uuid.New().String()
	if _, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{ID: id, Origin: session.OriginUI, UserID: owner}); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	g.LastReadAt = &now
	if err := saveGroupMeta(id, func(m *session.Meta) { m.AgentGroup = g }); err != nil {
		_ = globalMgr.DeleteSession(c.Context(), id)
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sess, _ := globalMgr.Registry().Session(id)
	c.JSON(http.StatusCreated, groupItem(c.Context(), sess, byID))
}

// apiTeamGroupUpdate handles PATCH /api/team/groups/{id}: rename,
// members, default responder, cap override. A member change is recorded
// in the thread as group_member_added / group_member_removed.
func apiTeamGroupUpdate(c *tool.Ctx) {
	sess, ok := ownGroup(c)
	if !ok {
		return
	}
	var req teamGroupWriteReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	byID, own, err := ownerPeers(c.Context(), sess.Meta.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	g := *sess.Meta.AgentGroup
	g.Members = append([]string(nil), g.Members...)
	before := g.Members
	if err := applyGroupWrite(&g, req, own); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := saveGroupMeta(sess.ID, func(m *session.Meta) { m.AgentGroup = &g }); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	added, removed := memberDiff(before, g.Members)
	by := actorName(c)
	for _, id := range added {
		p := byID[id]
		emitSystemEvent(sess.ID, store.KindGroupMemberAdded, fmt.Sprintf("@%s joined the group · by %s", p.Handle, by),
			map[string]string{"agent_id": id, "handle": p.Handle, "by": by})
	}
	for _, id := range removed {
		p := byID[id]
		emitSystemEvent(sess.ID, store.KindGroupMemberRemoved, fmt.Sprintf("@%s left the group · by %s", p.Handle, by),
			map[string]string{"agent_id": id, "handle": p.Handle, "by": by})
	}
	sess, _ = globalMgr.Registry().Session(sess.ID)
	c.JSON(http.StatusOK, groupItem(c.Context(), sess, byID))
}

// apiTeamGroupDelete handles DELETE /api/team/groups/{id}: the thread and
// the members' backing sessions go; the agents stay.
func apiTeamGroupDelete(c *tool.Ctx) {
	sess, ok := ownGroup(c)
	if !ok {
		return
	}
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.GroupSessionID == sess.ID {
			_ = globalMgr.DeleteSession(c.Context(), s.ID)
		}
	}
	if err := globalMgr.DeleteSession(c.Context(), sess.ID); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// apiTeamGroupRead handles POST /api/team/groups/{id}/read.
func apiTeamGroupRead(c *tool.Ctx) {
	sess, ok := ownGroup(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if err := saveGroupMeta(sess.ID, func(m *session.Meta) {
		if m.AgentGroup != nil {
			m.AgentGroup.LastReadAt = &now
		}
	}); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// sendGroupMessage is POST /sessions/{id}/send on a group: the person's
// message is written into the thread and the members it names (or the
// default responder) answer in the background.
func sendGroupMessage(c *tool.Ctx, sess session.Session, text string) {
	if sess.Meta.UserID != actorID(c) || globalTeam == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if strings.TrimSpace(text) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "text required"})
		return
	}
	g := *sess.Meta.AgentGroup
	byID, _, err := ownerPeers(c.Context(), sess.Meta.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	members := groupPeers(&g, byID)
	targets, refused, err := teamlink.HumanTargets(text, members, g.DefaultResponder)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	// Every enabled member gets its backing session now, on the request
	// that has the caller: a member pulled in later by another's @ needs
	// one too, and creating it resolves the caller's provider.
	backing := map[string]string{}
	for _, p := range members {
		if p.Disabled {
			continue
		}
		id, err := groupBacking(c, sess.ID, p.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		backing[p.ID] = id
	}
	now := time.Now().UTC()
	turn := store.ConversationTurn{
		TurnID: fmt.Sprintf("%d", now.UnixNano()), Timestamp: now, Role: "user", Source: "ui", Text: text,
		Sender: &store.Sender{ID: actorID(c), Name: actorName(c), Channel: "ui", WickUserID: actorID(c)},
	}
	if err := recordGroupTurn(sess.ID, turn); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, h := range refused {
		emitSystemEvent(sess.ID, store.KindMentionRefused, fmt.Sprintf("@%s isn't an enabled member of this group", h),
			map[string]string{"from": "user", "to": h, "reason": "not an enabled member"})
	}
	run := &groupRun{
		group: g, members: members, limit: teamlink.GroupLimit(members, g.MaxHopsOverride),
		turn: func(ctx context.Context, p teamlink.Peer, prompt string) (string, error) {
			return runBackingTurn(ctx, backing[p.ID], prompt)
		},
		history: func() []store.ConversationTurn {
			turns, _ := loadConversation(globalLayout, sess.ID)
			return turns
		},
		record: func(t store.ConversationTurn) {
			if t.Role == "system" {
				recordSystemTurn(globalLayout, globalBcast, sess.ID, t)
				return
			}
			if err := recordGroupTurn(sess.ID, t); err != nil {
				log.Warn().Err(err).Str("session", sess.ID).Msg("team: group turn write failed")
			}
		},
		typing: func(p teamlink.Peer, on bool) {
			state := "stop"
			if on {
				state = "start"
			}
			body, _ := json.Marshal(map[string]string{"agent_id": p.ID, "handle": p.Handle, "state": state})
			if globalBcast != nil {
				globalBcast.PublishRaw(sess.ID, "", evGroupTyping, string(body))
			}
		},
	}
	ctx := context.WithoutCancel(c.Context())
	go run.play(ctx, sess.ID, targets)
	c.JSON(http.StatusOK, map[string]string{"status": "sent"})
}

// recordGroupTurn appends a user or member turn to the group thread and
// pushes it to open viewers.
func recordGroupTurn(groupID string, t store.ConversationTurn) error {
	if err := storage.AppendJSONL(globalLayout.SessionConversation(groupID), "wick-conv-v1", groupID, t); err != nil {
		return err
	}
	if body, err := json.Marshal(t); err == nil && globalBcast != nil {
		globalBcast.PublishRaw(groupID, "", evGroupTurn, string(body))
	}
	touchSessionActive(globalLayout, groupID)
	return nil
}

// groupBacking returns agentID's backing session for group groupID,
// creating it on first use.
func groupBacking(c *tool.Ctx, groupID, agentID string) (string, error) {
	owner := actorID(c)
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.GroupSessionID == groupID && s.Meta.AgentID == agentID && s.Meta.UserID == owner {
			return s.ID, nil
		}
	}
	p, err := globalTeam.Get(c.Context(), agentID)
	if err != nil {
		return "", err
	}
	id, err := createTeamAgentSession(c, p, false)
	if err != nil {
		return "", err
	}
	return id, saveGroupMeta(id, func(m *session.Meta) { m.GroupSessionID = groupID })
}

// runBackingTurn runs one turn in a member's backing session and returns
// its final text.
func runBackingTurn(ctx context.Context, sessionID, prompt string) (string, error) {
	if sessionID == "" {
		return "", errors.New("no backing session")
	}
	ch, unsub := NewDelegationStream(globalBcast).SubscribeSession(sessionID)
	defer unsub()
	if err := globalPool.Send(ctx, sessionID, "", sourceGroup, "user", prompt); err != nil {
		return "", err
	}
	return collectTurn(ctx, ch), nil
}

/* ── the runner ──────────────────────────────────────────────────────── */

// groupLocks serialises the runs of one group, so two quick messages do
// not interleave their members' turns.
var groupLocks sync.Map // group id → *sync.Mutex

// groupRun plays one person's message out: its targets answer in order,
// and members they @ answer after them, within limit. The I/O is
// injected so the routing is tested without a pool.
type groupRun struct {
	group   session.AgentGroup
	members []teamlink.Peer
	limit   int
	turn    func(ctx context.Context, p teamlink.Peer, prompt string) (string, error)
	history func() []store.ConversationTurn
	record  func(store.ConversationTurn)
	typing  func(p teamlink.Peer, on bool)
	now     func() time.Time
}

type groupJob struct {
	to   teamlink.Peer
	from string // handle of the member that handed the turn; "" = a person
}

func (r *groupRun) clock() time.Time {
	if r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}

func (r *groupRun) play(ctx context.Context, groupID string, targets []teamlink.Peer) {
	mu, _ := groupLocks.LoadOrStore(groupID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	queue := make([]groupJob, 0, len(targets))
	for _, t := range targets {
		queue = append(queue, groupJob{to: t})
	}
	hops := 0
	for len(queue) > 0 {
		j := queue[0]
		queue = queue[1:]
		reply := r.answer(ctx, j)
		if reply == "" {
			continue
		}
		hit, _ := teamlink.LeadMentions(reply, r.members, true)
		for _, t := range hit {
			if t.ID == j.to.ID {
				continue
			}
			if t.Disabled || !t.AcceptsFrom(j.to) {
				r.record(systemTurn(store.KindMentionRefused, fmt.Sprintf("@%s doesn't take mentions", t.Handle),
					map[string]string{"from": j.to.Handle, "to": t.Handle, "reason": teamlink.ErrMentionsOff.Error()}, r.clock()))
				continue
			}
			if hops >= r.limit {
				r.record(hopLimitTurn(map[string]string{"from": j.to.Handle, "to": t.Handle, "reason": teamlink.ErrHopLimit.Error()}, r.limit, r.clock()))
				return
			}
			hops++
			queue = append(queue, groupJob{to: t, from: j.to.Handle})
		}
	}
}

// answer runs one member's turn and copies its reply into the thread.
func (r *groupRun) answer(ctx context.Context, j groupJob) string {
	if r.typing != nil {
		r.typing(j.to, true)
		defer r.typing(j.to, false)
	}
	prompt := groupPrompt(r.group, r.members, j.to, j.from, r.history())
	reply, err := r.turn(ctx, j.to, prompt)
	now := r.clock()
	if err != nil {
		t := systemTurn("", fmt.Sprintf("@%s couldn't answer: %v", j.to.Handle, err), nil, now)
		t.IsError = true
		r.record(t)
		return ""
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "" // [silent]
	}
	r.record(store.ConversationTurn{
		TurnID: fmt.Sprintf("%d", now.UnixNano()), Timestamp: now, Role: "assistant", Source: sourceGroup, Text: reply,
		Speaker: &store.Speaker{AgentID: j.to.ID, Handle: j.to.Handle, Via: store.ViaGroup},
	})
	return reply
}

// groupPrompt is what a member's backing session receives: who is in the
// group, the recent thread, and whose turn it is. The thread's last
// message is the one being answered.
func groupPrompt(g session.AgentGroup, members []teamlink.Peer, self teamlink.Peer, from string, history []store.ConversationTurn) string {
	var b strings.Builder
	handles := make([]string, 0, len(members))
	for _, p := range members {
		if p.ID != self.ID {
			handles = append(handles, "@"+p.Handle)
		}
	}
	fmt.Fprintf(&b, "[Group chat %q — you are @%s; also here: %s]\n", g.Name, self.Handle, strings.Join(handles, ", "))
	b.WriteString("Your reply is posted to the group as yours. Start a line with @handle and a message to hand that member a turn; only do so when you need them.\n\n")
	msgs := make([]store.ConversationTurn, 0, groupContextTurns)
	for _, t := range history {
		if t.Role == "user" || t.Role == "assistant" {
			msgs = append(msgs, t)
		}
	}
	if len(msgs) > groupContextTurns {
		msgs = msgs[len(msgs)-groupContextTurns:]
	}
	b.WriteString("Recent messages:\n")
	for _, t := range msgs {
		who := "User"
		if t.Sender != nil && t.Sender.Name != "" {
			who = t.Sender.Name
		}
		if t.Speaker != nil {
			who = "@" + t.Speaker.Handle
		}
		fmt.Fprintf(&b, "--- %s:\n%s\n", who, strings.TrimSpace(t.Text))
	}
	if from != "" {
		fmt.Fprintf(&b, "\n@%s handed you this turn. Answer the last message.", from)
	} else {
		b.WriteString("\nAnswer the last message.")
	}
	return b.String()
}

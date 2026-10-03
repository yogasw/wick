package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Sharing a Team agent with another wick user (chat only). The owner (or
// an admin) picks people by user id; a recipient sees the agent in their
// roster, chats with it in conversations of their own and may @mention
// it, while every setting stays the owner's. Turns run with the owner's
// access narrowed by the agent's checklist (team.SpawnIdentity).

// RoleViewer marks a roster item shared with the caller: the front-end
// locks every menu but chat and info.
const RoleViewer = "viewer"

// teamShareItem is one person an agent is shared with.
type teamShareItem struct {
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// shareBlockOf is team.ShareBlock with the remote usage check filled in.
func shareBlockOf(p entity.AgentPersona) string {
	return team.ShareBlock(p, remoteOwnerOnly(p))
}

// sharedAgentsFor is what is shared with userID right now: another
// owner's enabled agents that may still be shared, with their share rows.
func sharedAgentsFor(ctx context.Context, userID string) ([]entity.AgentPersona, []entity.AgentShare, error) {
	rows, shares, err := globalTeam.SharedWith(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	var agents []entity.AgentPersona
	var kept []entity.AgentShare
	for i, p := range rows {
		if shareBlockOf(p) != "" {
			continue
		}
		agents, kept = append(agents, p), append(kept, shares[i])
	}
	return agents, kept, nil
}

// sharedWithCaller reports whether p is shared with the caller right now.
func sharedWithCaller(c *tool.Ctx, p entity.AgentPersona) (entity.AgentShare, bool) {
	uid := actorID(c)
	if uid == "" || p.OwnerUserID == uid || p.Disabled || shareBlockOf(p) != "" {
		return entity.AgentShare{}, false
	}
	sh, err := globalTeam.ShareOf(c.Context(), p.ID, uid)
	return sh, err == nil
}

// loadChatTeamAgent is loadOwnTeamAgent for the routes a recipient may
// use too (chat, read, sessions). shared reports a recipient. Anyone else
// gets 404, as does a recipient once the agent is unshared or turned off.
func loadChatTeamAgent(c *tool.Ctx) (p entity.AgentPersona, shared, ok bool) {
	p, err := globalTeam.Get(c.Context(), c.PathValue("id"))
	if err != nil && !errors.Is(err, team.ErrNotFound) {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return p, false, false
	}
	if err == nil && p.OwnerUserID == actorID(c) {
		return p, false, true
	}
	if err == nil {
		if _, ok := sharedWithCaller(c, p); ok {
			return p, true, true
		}
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": "agent not found"})
	return p, false, false
}

// loadShareManagedAgent loads {id} for the share routes: its owner or an
// admin. Anyone else gets 404, a recipient 403 like any other edit.
func loadShareManagedAgent(c *tool.Ctx) (entity.AgentPersona, bool) {
	if u := login.GetUser(c.Context()); u != nil && u.IsAdmin() {
		p, err := globalTeam.Get(c.Context(), c.PathValue("id"))
		if err != nil {
			c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
			return p, false
		}
		return p, true
	}
	return loadOwnTeamAgent(c)
}

// shareUserNames maps user ids to display names (name, else the email's local
// part, else the id). Only approved users are named.
func shareUserNames(ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	if globalDB == nil || len(ids) == 0 {
		return out
	}
	var users []entity.User
	if err := globalDB.Select("id", "name", "email").Where("id IN ?", ids).Find(&users).Error; err != nil {
		log.Warn().Err(err).Msg("agents: loading share user names failed")
		return out
	}
	for _, u := range users {
		out[u.ID] = displayNameOf(u)
	}
	return out
}

// displayNameOf is how a person is named in the share list and picker.
func displayNameOf(u entity.User) string {
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	if at := strings.IndexByte(u.Email, '@'); at > 0 {
		return u.Email[:at]
	}
	return u.ID
}

// apiTeamAgentShares handles GET /api/team/agents/{id}/shares.
func apiTeamAgentShares(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadShareManagedAgent(c)
	if !ok {
		return
	}
	rows, err := globalTeam.ListShares(c.Context(), p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SharedWithUserID)
	}
	names := shareUserNames(ids)
	out := make([]teamShareItem, 0, len(rows))
	for _, r := range rows {
		name := names[r.SharedWithUserID]
		if name == "" {
			name = r.SharedWithUserID
		}
		out = append(out, teamShareItem{UserID: r.SharedWithUserID, Name: name, CreatedAt: r.CreatedAt})
	}
	block := shareBlockOf(p)
	c.JSON(http.StatusOK, map[string]any{"shares": out, "shareable": block == "", "reason": block})
}

// apiTeamAgentShareAdd handles POST /api/team/agents/{id}/shares
// {"user_id": "…"}: the person must be an approved wick user.
func apiTeamAgentShareAdd(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadShareManagedAgent(c)
	if !ok {
		return
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	uid := strings.TrimSpace(body.UserID)
	if block := shareBlockOf(p); block != "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": block})
		return
	}
	switch {
	case uid == "":
		c.JSON(http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	case uid == p.OwnerUserID:
		c.JSON(http.StatusBadRequest, map[string]string{"error": team.ErrShareSelf.Error()})
		return
	}
	if globalDB != nil {
		var n int64
		if err := globalDB.Model(&entity.User{}).Where("id = ? AND approved = ?", uid, true).Count(&n).Error; err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not check the user"})
			return
		}
		if n == 0 {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "no such wick user"})
			return
		}
	}
	if err := globalTeam.AddShare(c.Context(), p.ID, uid, actorID(c)); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// apiTeamAgentShareRemove handles DELETE /api/team/agents/{id}/shares/{uid}.
// The recipient's conversations stay theirs but cannot be opened again
// unless the agent is shared once more (ownsSession).
func apiTeamAgentShareRemove(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadShareManagedAgent(c)
	if !ok {
		return
	}
	if err := globalTeam.RemoveShare(c.Context(), p.ID, c.PathValue("uid")); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// apiTeamShareUsers handles GET /api/team/share-users: the approved wick
// users an agent may be shared with, the caller aside. Ids and names
// only — a picker is not a user directory.
func apiTeamShareUsers(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	out := []assigneeOption{}
	if globalDB == nil {
		c.JSON(http.StatusOK, map[string]any{"users": out})
		return
	}
	var users []entity.User
	if err := globalDB.Select("id", "name", "email", "approved").
		Where("approved = ?", true).Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load users"})
		return
	}
	me := actorID(c)
	for _, u := range users {
		if u.ID != me {
			out = append(out, assigneeOption{ID: u.ID, Name: displayNameOf(u)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i].Name, out[j].Name) {
			return out[i].ID < out[j].ID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	c.JSON(http.StatusOK, map[string]any{"users": out})
}

// sharedTeamAgentItem is a shared agent as its recipient's roster shows
// it: the persona to recognise it by, the recipient's own chat state, and
// none of the owner's settings (prompt, access, mention rules).
func sharedTeamAgentItem(p entity.AgentPersona, sh entity.AgentShare, viewer string, owners map[string]string, live teamLive) TeamAgentItem {
	it := teamAgentToItem(p, teamProjectUsers{}, live, nil)
	it.Role, it.SharedByID = RoleViewer, p.OwnerUserID
	it.SharedBy = owners[p.OwnerUserID]
	if it.SharedBy == "" {
		it.SharedBy = p.OwnerUserID
	}
	it.SystemPrompt, it.Preset = "", ""
	it.AllowedConnectors, it.IncludeNewConnectors = []team.ConnectorGrant{}, false
	it.AllowedNativeTools, it.BashRules, it.DisabledSkills = []string{}, []team.BashRule{}, []string{}
	it.MentionFrom, it.MentionAllow = "", []string{}
	it.Remote, it.SlackRemote = nil, nil
	it.ManageAgents, it.CaptainCan = false, team.CaptainCan{}
	it.SharedWith = 0
	fillChatState(&it, viewer, p.ID, sh.LastReadAt, live)
	return it
}

// fillChatState sets the main-chat fields of it from userID's main chat
// with agentID (empty when they have none yet).
func fillChatState(it *TeamAgentItem, userID, agentID string, lastRead *time.Time, live teamLive) {
	it.MainSessionID, it.LastActive, it.Unread = "", nil, false
	it.Status, it.CurrentAction = string(session.StatusIdle), ""
	it.AttentionPreview, it.NeedsAttention, it.LastPreview = "", false, ""
	s, ok := mainSessionOf(userID, agentID)
	if !ok {
		return
	}
	it.MainSessionID = s.ID
	it.LastActive = timePtr(s.Meta.LastActive)
	// Meta.Status stays "running" for as long as the process is warm,
	// turn or no turn; the pool lifecycle is what says a turn is on.
	it.Status = team.TurnStatus(string(s.Meta.Status), live.lifecycles[s.ID])
	it.Unread = team.Unread(s.Meta.LastActive, lastRead)
	if it.Status != string(session.StatusIdle) {
		it.CurrentAction = live.actions[s.ID]
	}
	// What the agent waits on outranks what it last said.
	if it.AttentionPreview = live.attention(s.ID); it.AttentionPreview != "" {
		it.NeedsAttention = true
		it.LastPreview = it.AttentionPreview
	} else {
		it.LastPreview = lastPreview(s.ID)
	}
}

// sharedRosterItems is the "Shared with me" half of the caller's roster.
// A failure leaves it out rather than failing the whole roster.
func sharedRosterItems(c *tool.Ctx, live teamLive) []TeamAgentItem {
	viewer := actorID(c)
	agents, shares, err := sharedAgentsFor(c.Context(), viewer)
	if err != nil {
		log.Ctx(c.Context()).Warn().Err(err).Msg("team: shared agents")
		return nil
	}
	owners := make([]string, 0, len(agents))
	for _, p := range agents {
		owners = append(owners, p.OwnerUserID)
	}
	names := shareUserNames(owners)
	out := make([]TeamAgentItem, 0, len(agents))
	for i, p := range agents {
		out = append(out, sharedTeamAgentItem(p, shares[i], viewer, names, live))
	}
	return out
}

// sharedChatAgent returns the agent of a shared agent's chat with its
// recipient (team.IsSharedChat), false for any other session.
func sharedChatAgent(ctx context.Context, sess session.Session) (entity.AgentPersona, bool) {
	if globalTeam == nil || sess.Meta.AgentID == "" || sess.Meta.Origin != session.OriginUI {
		return entity.AgentPersona{}, false
	}
	p, err := globalTeam.Get(ctx, sess.Meta.AgentID)
	if err != nil || !team.IsSharedChat(sess.Meta, &p) {
		return entity.AgentPersona{}, false
	}
	return p, true
}

// sharedChatAllowed decides a shared agent's chat: only its recipient may
// open it — never the agent's owner, whose project it lives in — and only
// while the agent is still shared with them and on.
func sharedChatAllowed(ctx context.Context, userID string, sess session.Session, p entity.AgentPersona) bool {
	if userID == "" || userID != sess.Meta.UserID || p.Disabled || shareBlockOf(p) != "" {
		return false
	}
	_, err := globalTeam.ShareOf(ctx, p.ID, userID)
	return err == nil
}

// errSharedRail answers a share recipient who reaches for the rail of a
// shared agent's chat. The chat lives in the owner's project, so its
// files, repos, processes and the rest are the owner's: sharing hands over
// the conversation, never the project.
const errSharedRail = "this agent is shared with you for chat only"

// sharedChatRailPrefixes are the session subtrees that read or change the
// project a session lives in — every rail tab, plus moving the session to
// another project. The chat itself (send, conversation, meta, answers,
// approvals, stop, uploads, turn traces) stays open to the recipient.
var sharedChatRailPrefixes = []string{
	"/sessions/{id}/files",
	"/sessions/{id}/processes",
	"/sessions/{id}/workspace",
	"/sessions/{id}/schedules",
	"/sessions/{id}/project",
	"/api/sessions/{id}/subagents",
	"/api/sessions/{id}/team-tasks",
	"/api/sessions/{id}/todos",
	"/api/sessions/{id}/git",
}

// registerSharedChatRailGuard puts sharedChatRailMW on every rail subtree.
func registerSharedChatRailGuard(r tool.Router) {
	for _, p := range sharedChatRailPrefixes {
		r.Use(p, sharedChatRailMW)
	}
}

// sharedChatRailMW answers 403 on a rail route of a shared agent's chat.
// sessionAccessMW runs first, so only the recipient gets this far.
func sharedChatRailMW(next tool.HandlerFunc) tool.HandlerFunc {
	return func(c *tool.Ctx) {
		if globalMgr != nil {
			if sess, ok := globalMgr.Registry().Session(c.PathValue("id")); ok {
				if _, shared := sharedChatAgent(c.Context(), sess); shared {
					c.JSON(http.StatusForbidden, map[string]string{"error": errSharedRail})
					return
				}
			}
		}
		next(c)
	}
}

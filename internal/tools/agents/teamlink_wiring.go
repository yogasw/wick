package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/upgrade"
	"github.com/yogasw/wick/pkg/tool"
)

// Wiring of the Team A2A link (internal/agents/teamlink) onto the pool.

// sourceTeam marks a turn wick posted on a teammate's behalf, so the
// front-end badges it instead of showing it as the person typing.
const sourceTeam = team.SourceTeam

// NewTeamLinkHub builds the Hub over the Team service and the pool.
// deliver wakes a session with a late reply (the sub-agent delivery path).
func NewTeamLinkHub(svc *team.Service, deliver func(ctx context.Context, sessionID, text string) error) *teamlink.Hub {
	hub := teamlink.NewHub(teamDirectory{svc: svc}, poolTurns{}, teamNotifier{deliver: deliver})
	// A question no turn of the sending chat is handling waits for the
	// user: the thread shows it as "Needs you".
	hub.CallerBusy = poolSessionBusy
	// A task a restart left working is settled as interrupted, unless the
	// draining previous process may still be running its turn.
	hub.PredecessorBusy = func(chat string) bool {
		if chat == "" {
			_, draining := upgrade.ReadDrainState(globalLayout.BaseDir)
			return draining
		}
		return upgrade.PredecessorHolds(globalLayout.BaseDir, chat)
	}
	// Tasks are kept on disk, so get_task, list_tasks and late replies
	// survive a restart.
	if globalLayout.BaseDir != "" {
		if err := hub.Persist(globalLayout.TeamTasksDir()); err != nil {
			log.Warn().Err(err).Msg("team: task store")
		}
	}
	// A remote agent's late reply goes to the agent that asked it too. The
	// reply to a timed-out turn is found from the conversation, so it still
	// arrives after a restart; it is sent once (settleLateReply).
	remote.OnFollowUp = func(sessionID, text, note string) {
		if note == remote.NoteLate {
			if _, err := settleLateReply(context.Background(), sessionID, text, ""); err != nil {
				log.Warn().Err(err).Str("session", sessionID).Msg("team: late reply")
			}
			return
		}
		hub.FollowUp(context.Background(), sessionID, text)
	}
	return hub
}

// TeamAgentOf returns the Team agent a session belongs to, "" for none.
func TeamAgentOf(ctx context.Context, sessionID string) string {
	if p := globalTeam.AgentFor(ctx, sessionID); p != nil {
		return p.ID
	}
	return ""
}

// globalTeamHub is the server's late-bound Hub, set by SetTeamHub; nil
// until wiring runs (and on installs without Team).
var globalTeamHub func() *teamlink.Hub

// SetTeamHub hands the Hub accessor to the HTTP handlers.
func SetTeamHub(f func() *teamlink.Hub) { globalTeamHub = f }

// sessionTeamTasks handles GET /api/sessions/{id}/team-tasks: the
// team_message / @mention tasks this session sent, for the Sub-agents
// panel's Team section. Read-only; the session must be the caller's.
func sessionTeamTasks(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	var tasks []teamlink.TaskView
	if globalTeamHub != nil {
		if h := globalTeamHub(); h != nil {
			tasks = h.SentFrom(id)
		}
	}
	if tasks == nil {
		tasks = []teamlink.TaskView{}
	}
	c.JSON(http.StatusOK, map[string]any{"tasks": tasks})
}

// teamTaskHub resolves the session and the Hub for a write on one of its
// team tasks; false when it already answered (404/503).
func teamTaskHub(c *tool.Ctx) (string, *teamlink.Hub, bool) {
	if notReady(c) {
		return "", nil, false
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return "", nil, false
	}
	var h *teamlink.Hub
	if globalTeamHub != nil {
		h = globalTeamHub()
	}
	if h == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "team messaging is not available"})
		return "", nil, false
	}
	return id, h, true
}

// teamTaskError maps a Hub error on a user's task action to a status.
func teamTaskError(c *tool.Ctx, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, teamlink.ErrUnknownTask):
		status = http.StatusNotFound
	case errors.Is(err, teamlink.ErrAlreadyAnswered), errors.Is(err, teamlink.ErrTaskNotWaiting), errors.Is(err, teamlink.ErrTaskSettled):
		status = http.StatusConflict
	}
	c.JSON(status, map[string]string{"error": err.Error()})
}

// sessionTeamTaskAnswer handles POST /api/sessions/{id}/team-tasks/{task}/answer
// {text}: the person answers a teammate's question in a task this chat
// sent, straight to that task. The first answer wins (409 after).
func sessionTeamTaskAnswer(c *tool.Ctx) {
	id, h, ok := teamTaskHub(c)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}
	res, err := h.AnswerFromUser(c.Context(), id, c.PathValue("task"), body.Text)
	if err != nil {
		teamTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"task": res})
}

// sessionTeamTaskCancel handles POST /api/sessions/{id}/team-tasks/{task}/cancel:
// the person cancels a task this chat sent (cancel_task on its behalf).
func sessionTeamTaskCancel(c *tool.Ctx) {
	id, h, ok := teamTaskHub(c)
	if !ok {
		return
	}
	res, err := h.CancelFromUser(c.Context(), id, c.PathValue("task"))
	if err != nil {
		teamTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"task": res})
}

type teamDirectory struct{ svc *team.Service }

func (d teamDirectory) peer(p entity.AgentPersona) teamlink.Peer {
	m := d.svc.MemberOf(p)
	return teamlink.Peer{
		ID: p.ID, OwnerID: p.OwnerUserID, Handle: p.Handle,
		Name: m.Name, Tagline: m.Tagline, Description: m.Description,
		IsCaptain: p.IsCaptain, Disabled: p.Disabled,
		MentionFrom: teamlink.NormalizeMentionFrom(p.MentionFrom), MentionAllow: decodeIDList(p.MentionAllow),
		MaxHops: p.MaxHops,
		Remote:  IsRemoteAgent(p), RemoteOwnerOnly: remoteOwnerOnly(p),
	}
}

// remoteOwnerOnly reports whether p is a remote agent its owner keeps to
// themselves by the old usage setting, not yet carried into its mention
// policy (migrateRemoteUsage). Missing or unreadable settings count as
// owner-only. Once carried over, MentionFrom alone decides.
func remoteOwnerOnly(p entity.AgentPersona) bool {
	if !IsRemoteAgent(p) {
		return false
	}
	usage, ok := remoteUsage(p)
	return !ok || usage != remoteUsageByMention
}

func (d teamDirectory) Peers(ctx context.Context, ownerID string) ([]teamlink.Peer, error) {
	all, err := d.svc.List(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]teamlink.Peer, 0, len(all))
	for _, p := range all {
		out = append(out, d.peer(p))
	}
	return out, nil
}

// SharedPeers is the agents other owners share with userID that may still
// be shared (teamlink.SharedDirectory).
func (d teamDirectory) SharedPeers(ctx context.Context, userID string) ([]teamlink.Peer, error) {
	rows, _, err := sharedAgentsFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]teamlink.Peer, 0, len(rows))
	for _, p := range rows {
		peer := d.peer(p)
		peer.ChatUser = userID
		out = append(out, peer)
	}
	return out, nil
}

func (d teamDirectory) Get(ctx context.Context, agentID string) (teamlink.Peer, error) {
	p, err := d.svc.Get(ctx, agentID)
	if err != nil {
		return teamlink.Peer{}, err
	}
	return d.peer(p), nil
}

// poolTurns runs a turn in the agent's main conversation: queued behind
// whatever it is doing, spawned as the agent (persona, scope and run_as
// are applied by the ordinary spawn path, which keys off the session).
type poolTurns struct{}

func (t poolTurns) Run(ctx context.Context, agent teamlink.Peer, text string) (string, string, error) {
	s, err := mainChatOf(agent)
	if err != nil {
		return "", "", err
	}
	return t.RunIn(ctx, agent, s.ID, text)
}

// mainChatOf is the agent's main chat, or the error a turn reports
// when it has none yet.
func mainChatOf(agent teamlink.Peer) (session.Session, error) {
	s, ok := mainSessionOf(chatUserOf(agent), agent.ID)
	if !ok {
		if agent.ChatUser != "" {
			return s, fmt.Errorf("@%s has no chat with you yet — open it once from your Team roster", agent.Handle)
		}
		return s, fmt.Errorf("@%s has no main chat yet — its owner has to open it once in the Agents app", agent.Handle)
	}
	return s, nil
}

// teamTurnGate lines up the team tasks each session answers, so a turn
// is known to be a task's and cancel_task stops only that one.
var teamTurnGate = teamlink.NewTurnGate(poolSessionBusy)

// poolSessionBusy reports whether sessionID runs a turn, is being spawned,
// holds messages for its next turn or waits for a pool slot: anything a
// team task's message would land behind.
func poolSessionBusy(sessionID string) bool {
	if globalPool == nil {
		return false
	}
	for _, e := range globalPool.ActiveSnapshot() {
		if e.SessionID == sessionID && (e.Lifecycle == "working" || e.Lifecycle == "spawning" || e.Queued > 0) {
			return true
		}
	}
	for _, q := range globalPool.QueueSnapshot() {
		if q.SessionID == sessionID {
			return true
		}
	}
	return false
}

// RunIn is Run in sessionID (teamlink.ChatOpener). A team task's turn
// waits until the session runs nothing else, so the turn it starts is
// its own (teamTurnGate).
func (poolTurns) RunIn(ctx context.Context, agent teamlink.Peer, sessionID, text string) (string, string, error) {
	release, err := teamTurnGate.Acquire(ctx, sessionID, teamlink.TaskIDFrom(ctx))
	if err != nil {
		return sessionID, "", err
	}
	defer release()
	// Subscribe BEFORE sending, or a fast turn ends unseen.
	ch, unsub := NewDelegationStream(globalBcast).SubscribeSession(sessionID)
	defer unsub()
	// WithoutCancel: the turn must outlive the call that carried it.
	if err := globalPool.Send(context.WithoutCancel(ctx), sessionID, "", sourceTeam, "user", text); err != nil {
		return sessionID, "", err
	}
	text, failed := collectTurnErr(ctx, ch)
	// The task's turn is over: let go of the session now, not after the
	// reply is read, so a cancel arriving later never takes the next turn
	// (someone else's) for the task's.
	release()
	// A remote that timed out is not an empty answer: the asker learns the
	// reply will follow (settleLateReply forwards it when it does).
	if waited, ok := remote.IsTimeout(failed); ok && strings.TrimSpace(text) == "" {
		text = remote.PendingNotice(agent.Handle, waited)
	}
	// An A2A remote that asks a question keeps its task open; the asker
	// sees input_required and answers with the same task_id.
	if agent.Remote && a2aremote.LoadState(globalLayout.SessionDir(sessionID)).InputRequired {
		return sessionID, "", &teamlink.TurnEnd{State: a2a.TaskStateInputRequired, Text: text}
	}
	return sessionID, text, nil
}

// StopTask cancels task id in sessionID (teamlink.TaskStopper): the
// session's turn is stopped only when it is that task's; a task still
// waiting for its turn just leaves the line.
//
// The pool stops a session, not a turn: when a message from anyone else
// is queued in the session (the pool may merge it into the running turn,
// or start it the moment the task's turn ends) the turn is left running
// (TaskStopShared) rather than cutting that message off with it.
//
// Only providers that queue (codex, omp, opencode: RespawnQueue) hold such
// a message apart. On an appending provider (claude) a message typed into
// the session while the task's turn runs is written straight into that
// turn, Queued stays 0, and the kill stops it along with the task: it stays
// in the transcript, unanswered. Known limitation; the UI asks before a
// cancel for that reason.
func (poolTurns) StopTask(_ context.Context, _ teamlink.Peer, sessionID string, id a2a.TaskID, by string) (teamlink.TaskStop, error) {
	out := teamTurnGate.Stop(sessionID, id)
	if out != teamlink.TaskStopRunning || globalPool == nil {
		return out, nil
	}
	for _, e := range globalPool.ActiveSnapshot() {
		if e.SessionID == sessionID && e.Queued > 0 {
			return teamlink.TaskStopShared, nil
		}
	}
	return out, globalPool.KillBy(sessionID, "", by, "team task canceled")
}

// NewChat opens a chat of agent beside its main one, set up like it —
// same owner, project, preset, provider and model — but not main
// (teamlink.ChatOpener). A remote agent's chat keeps its own remote
// state, so a Slack remote starts a new thread there.
func (poolTurns) NewChat(ctx context.Context, agent teamlink.Peer) (string, error) {
	main, err := mainChatOf(agent)
	if err != nil {
		return "", err
	}
	id := uuid.New().String()
	if _, err := globalMgr.CreateSession(ctx, session.CreateOptions{
		ID:        id,
		ProjectID: main.Meta.ProjectID,
		Origin:    session.OriginUI,
		Preset:    main.Meta.Preset,
		UserID:    main.Meta.UserID,
		AgentID:   agent.ID,
	}); err != nil {
		return "", err
	}
	for _, a := range main.Agents {
		if a.Name != "main" {
			continue
		}
		if err := globalMgr.AddAgent(id, "main", a.Provider); err != nil {
			return "", err
		}
		if a.ModelID != "" {
			if err := session.SetModelID(globalLayout, id, "main", a.ModelID); err != nil {
				log.Ctx(ctx).Warn().Msgf("team new chat set model id: %s", err.Error())
			}
		}
	}
	// Titled like the main chat, so it reads as the agent's in every list.
	if sess, ok := globalMgr.Registry().Session(id); ok {
		sess.Meta.Label = main.Meta.Label
		sess.Meta.TitleCustom = true
		if err := session.SaveMeta(globalLayout, id, sess.Meta); err == nil {
			_ = globalMgr.RefreshSession(id)
		}
	}
	return id, nil
}

// LinkedChat is the chat of agent paired with callerSession
// (teamlink.ChatLinker): one of agent's chats for its chat user whose
// LinkedFromSession is callerSession, else the chat callerSession was
// itself opened for when that is one of agent's — a reply back to the
// asker lands in the conversation that asked.
func (poolTurns) LinkedChat(_ context.Context, agent teamlink.Peer, callerSession string) string {
	if callerSession == "" {
		return ""
	}
	chats := agentSessions(chatUserOf(agent), agent.ID)
	for _, s := range chats {
		if s.Meta.LinkedFromSession == callerSession {
			return s.ID
		}
	}
	if caller, ok := globalMgr.Registry().Session(callerSession); ok && caller.Meta.LinkedFromSession != "" {
		for _, s := range chats {
			if s.ID == caller.Meta.LinkedFromSession {
				return s.ID
			}
		}
	}
	return ""
}

// Link pairs chat with callerSession on chat's meta, so it survives a
// restart, after unpairing any other chat of agent paired with it
// (teamlink.ChatLinker).
func (poolTurns) Link(_ context.Context, agent teamlink.Peer, chat, callerSession string) error {
	for _, s := range agentSessions(chatUserOf(agent), agent.ID) {
		want := s.ID == chat
		if want == (s.Meta.LinkedFromSession == callerSession) {
			continue
		}
		// Re-read from disk: the registry copy can lag a write the chat's
		// own turn just made (title, pending input), which a save of it
		// would undo.
		fresh, err := session.Load(globalLayout, s.ID)
		if err != nil {
			return err
		}
		if want {
			fresh.Meta.LinkedFromSession = callerSession
		} else {
			fresh.Meta.LinkedFromSession = ""
		}
		if err := session.SaveMeta(globalLayout, s.ID, fresh.Meta); err != nil {
			return err
		}
		_ = globalMgr.RefreshSession(s.ID)
	}
	return nil
}

// Chats is agent's chats for its chat user, newest first
// (teamlink.ChatLister): only that user's own sessions, so another
// person's chat with a shared agent never shows, whatever the share's
// history toggle says. A Slack remote's chat carries its thread link.
func (poolTurns) Chats(_ context.Context, agent teamlink.Peer) []teamlink.ChatInfo {
	user := chatUserOf(agent)
	var out []teamlink.ChatInfo
	for _, s := range agentSessions(user, agent.ID) {
		c := teamlink.ChatInfo{
			SessionID: s.ID, Title: s.Meta.Label, LastActive: s.Meta.LastActive, Main: s.Meta.AgentMain,
			UserID: s.Meta.UserID, LinkedFrom: s.Meta.LinkedFromSession,
		}
		if agent.Remote {
			c.SlackThread = slackThreadLink(slackremote.LoadState(globalLayout.SessionDir(s.ID)))
		}
		out = append(out, c)
	}
	return out
}

// PairedWith is every Team chat paired with callerSession, in one pass
// over the registry (teamlink.PairedLister): a spawn's "This session"
// block and list_agents cost one scan, not one per teammate.
func (poolTurns) PairedWith(_ context.Context, callerSession string) []teamlink.ChatInfo {
	if callerSession == "" {
		return nil
	}
	var out []teamlink.ChatInfo
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.LinkedFromSession != callerSession || s.Meta.AgentID == "" || s.Meta.GroupSessionID != "" {
			continue
		}
		out = append(out, teamlink.ChatInfo{
			SessionID: s.ID, Title: s.Meta.Label, LastActive: s.Meta.LastActive, Main: s.Meta.AgentMain,
			UserID: s.Meta.UserID, LinkedFrom: s.Meta.LinkedFromSession, AgentID: s.Meta.AgentID,
			// Only paired chats get here, so the state read stays rare; a
			// chat that is not a Slack remote has no state file.
			SlackThread: slackThreadLink(slackremote.LoadState(globalLayout.SessionDir(s.ID))),
		})
	}
	return out
}

// slackThreadLink is a link to st's Slack thread, "" for none. The
// slack.com/archives form redirects to the workspace, so no API call.
func slackThreadLink(st slackremote.State) string {
	if st.Channel == "" || st.ThreadTS == "" {
		return ""
	}
	return "https://slack.com/archives/" + st.Channel + "/p" + strings.ReplaceAll(st.ThreadTS, ".", "")
}

// TeamLinkedChats is, for the Team agent session sessionID belongs to, the
// chat paired with it at each teammate the session's person can reach
// (teamlink.Hub.LinkedChats); nil outside the Team or with none.
func TeamLinkedChats(ctx context.Context, sessionID string) []teamlink.TeamChat {
	if globalTeamHub == nil || globalTeam == nil || sessionID == "" {
		return nil
	}
	h := globalTeamHub()
	id := TeamAgentOf(ctx, sessionID)
	if h == nil || id == "" {
		return nil
	}
	out, _ := h.LinkedChats(ctx, id, sessionID, sessionUserOf(sessionID))
	return out
}

// TeamLinkedChatsPrompt is TeamLinkedChats as "This session" lines, ""
// for none.
func TeamLinkedChatsPrompt(sessionID string) string {
	return teamlink.FormatLinkedChats(TeamLinkedChats(context.Background(), sessionID))
}

// SessionUserOf is the wick user of sessionID, "" when unknown.
func SessionUserOf(sessionID string) string { return sessionUserOf(sessionID) }

// MainSession is the session a turn of agent runs in (SessionLocator).
func (poolTurns) MainSession(_ context.Context, agent teamlink.Peer) string {
	if s, ok := mainSessionOf(chatUserOf(agent), agent.ID); ok {
		return s.ID
	}
	return ""
}

// chatUserOf is whose main chat with agent a turn runs in: a share
// recipient's own, else the owner's.
func chatUserOf(agent teamlink.Peer) string {
	if agent.ChatUser != "" {
		return agent.ChatUser
	}
	return agent.OwnerID
}

// collectTurn joins the text of one turn, up to its Done.
func collectTurn(ctx context.Context, ch <-chan delegation.StreamEvent) string {
	text, _ := collectTurnErr(ctx, ch)
	return text
}

// collectTurnErr is collectTurn plus the error of a remote turn that
// timed out, which ends the turn too.
func collectTurnErr(ctx context.Context, ch <-chan delegation.StreamEvent) (string, string) {
	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return b.String(), ""
		case ev, ok := <-ch:
			if !ok {
				return b.String(), ""
			}
			switch ev.Type {
			case event.Error:
				if _, timedOut := remote.IsTimeout(ev.Text); timedOut {
					return b.String(), ev.Text
				}
			case event.TextDelta:
				b.WriteString(ev.Text)
			case event.TextReplace:
				b.Reset()
				b.WriteString(ev.Text)
			case event.Done:
				return b.String(), ""
			}
		}
	}
}

type teamNotifier struct {
	deliver func(ctx context.Context, sessionID, text string) error
}

func (n teamNotifier) Deliver(ctx context.Context, sessionID, text string) error {
	if n.deliver == nil {
		return nil
	}
	return n.deliver(ctx, sessionID, text)
}

// Audit logs one mention_handoff per side, records it in that thread as
// a kind:"mention_handoff" system turn and pushes the same turn to open
// viewers as a mention_handoff SSE event, so the row shows without a
// reload. A task is audited twice (working, then its final state); the
// front-end folds the turns of one task_id into one row.
func (teamNotifier) Audit(_ context.Context, sessionID string, h teamlink.Handoff) {
	log.Info().Str("event", "mention_handoff").Str("session", sessionID).
		Str("from", h.From).Str("to", h.To).Str("context_id", h.ContextID).
		Str("task_id", h.TaskID).Str("state", string(h.State)).Msg("team: handoff")
	recordSystemTurn(globalLayout, globalBcast, sessionID, handoffTurn(h, time.Now()))
}

// Refused records a message the Hub would not send in the caller's
// thread: hop_limit when the exchange's budget ran out, mention_refused
// when an @mention named nobody who takes one.
func (teamNotifier) Refused(_ context.Context, r teamlink.Refusal) {
	recordSystemTurn(globalLayout, globalBcast, r.Session, refusalTurn(r, time.Now()))
}

// refusalTurn is r as a conversation system turn.
func refusalTurn(r teamlink.Refusal, now time.Time) store.ConversationTurn {
	extras := map[string]string{"from": r.From, "to": r.To}
	if r.Err != nil {
		extras["reason"] = r.Err.Error()
	}
	if r.HopLimit {
		extras["context_id"] = r.ContextID
		limit := r.MaxTurns
		if limit <= 0 {
			limit = teamlink.MaxContextTurns
		}
		return hopLimitTurn(extras, limit, now)
	}
	if errors.Is(r.Err, teamlink.ErrRemoteOwnerOnly) {
		return systemTurn(store.KindMentionRefused, fmt.Sprintf("@%s takes messages from its owner only — switch its Settings › Mention to \"Any of my agents\" to let agents reach it", r.To), extras, now)
	}
	return systemTurn(store.KindMentionRefused, fmt.Sprintf("@%s doesn't take mentions", r.To), extras, now)
}

// hopLimitTurn is the hop_limit event for a budget of limit turns.
func hopLimitTurn(extras map[string]string, limit int, now time.Time) store.ConversationTurn {
	extras["max_turns"] = fmt.Sprintf("%d", limit)
	return systemTurn(store.KindHopLimit, fmt.Sprintf("Agent-to-agent limit of %d turns reached — reply to continue", limit), extras, now)
}

// decodeIDList reads a JSON array of ids; a malformed value reads as none.
func decodeIDList(raw string) []string {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// encodeIDList is ids as a JSON array, "[]" for none.
func encodeIDList(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

// handoffTurn is h as a conversation system turn.
func handoffTurn(h teamlink.Handoff, now time.Time) store.ConversationTurn {
	extras := map[string]string{
		"from": h.From, "to": h.To, "to_agent_id": h.ToID, "state": string(h.State),
		"task_id": h.TaskID, "context_id": h.ContextID,
	}
	// Where to hand a late reply, read back after a restart (lateCaller).
	if h.FromSession != "" {
		extras["from_session"] = h.FromSession
	}
	return systemTurn(store.KindMentionHandoff, fmt.Sprintf("@%s → @%s · %s", h.From, h.To, h.State), extras, now)
}

// publishHandoff pushes turn to sessionID's live viewers.
func publishHandoff(b *Broadcaster, sessionID string, turn store.ConversationTurn) {
	publishSystemTurnEvent(b, sessionID, turn)
}

// appendHandoff writes turn into sessionID's conversation.
func appendHandoff(layout agentconfig.Layout, sessionID string, turn store.ConversationTurn) error {
	if layout.BaseDir == "" || sessionID == "" {
		return nil
	}
	return storage.AppendJSONL(layout.SessionConversation(sessionID), "wick-conv-v1", sessionID, turn)
}

// TeamMentionRouter adapts the Hub to delegation.TeamRouter: an @handle
// line naming a teammate is sent over the same client team_message uses,
// without waiting.
type TeamMentionRouter struct{ Hub func() *teamlink.Hub }

// TeamHandles is every handle a mention in sessionID may reach: the
// agent's teammates and those shared with its owner, or — in a chat
// with an agent shared with the session's user — that user's own agents
// and those shared with them (teamlink.Hub.MentionTargets).
func (r TeamMentionRouter) TeamHandles(ctx context.Context, sessionID string) []string {
	h, id := r.Hub(), TeamAgentOf(ctx, sessionID)
	if h == nil || id == "" {
		return nil
	}
	peers, err := h.MentionTargets(ctx, id, sessionUserOf(sessionID))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(peers))
	for _, p := range peers {
		out = append(out, p.Handle)
	}
	return out
}

func (r TeamMentionRouter) SendTeam(ctx context.Context, sessionID, handle, body string, human bool) error {
	h := r.Hub()
	if h == nil {
		return nil
	}
	_, err := h.Send(context.WithoutCancel(ctx), teamlink.SendInput{
		CallerSession: sessionID, CallerAgentID: TeamAgentOf(ctx, sessionID),
		SessionUser: sessionUserOf(sessionID),
		To:          handle, Text: body, Wait: -1, Mention: true, Human: human,
	})
	return err
}

// sessionUserOf is the wick user of sessionID, "" when unknown.
func sessionUserOf(sessionID string) string {
	if sess, ok := globalMgr.Registry().Session(sessionID); ok {
		return sess.Meta.UserID
	}
	return ""
}

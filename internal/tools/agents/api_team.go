package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// globalTeam backs the Agents app (/team). nil = the app is off and
// every /api/team/agents route answers 503.
var globalTeam *team.Service

// SetTeam wires the Agents-app store.
func SetTeam(s *team.Service) {
	globalTeam = s
	if s != nil {
		startTeamJobs()
	}
}

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// TeamAgentItem is one agent as the Agents app renders it. The persona text
// (name … preset) is read from the agent's project at response time, never
// stored on the row, so it can never disagree with the project settings.
type TeamAgentItem struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	// Kind is "" for a local agent, "a2a-remote" for a remote one; Remote
	// carries the remote's settings then (never its secret).
	Kind   string           `json:"kind"`
	Remote *RemoteAgentInfo `json:"remote,omitempty"`
	// SlackRemote carries a Slack remote agent's settings (kind
	// "slack-remote"); never a token.
	SlackRemote          *SlackRemoteInfo      `json:"slack_remote,omitempty"`
	IsCaptain            bool                  `json:"is_captain"`
	ProjectID            string                `json:"project_id"`
	Name                 string                `json:"name"`
	Icon                 string                `json:"icon"`
	Tagline              string                `json:"tagline"`
	Description          string                `json:"description"`
	SystemPrompt         string                `json:"system_prompt"`
	Provider             string                `json:"provider"`
	Model                string                `json:"model"`
	Preset               string                `json:"preset"`
	Features             team.Features         `json:"features"`
	Avatar               team.Avatar           `json:"avatar"`
	AllowedConnectors    []team.ConnectorGrant `json:"allowed_connectors"`
	IncludeNewConnectors bool                  `json:"include_new_connectors"`
	// AllowedNativeTools, BashRules and DisabledSkills are the Tools &
	// features and Skills tabs (team/tools.go). NativeToolsEnforced is
	// false when the agent's provider cannot be held to them.
	AllowedNativeTools  []string        `json:"allowed_native_tools"`
	BashRules           []team.BashRule `json:"bash_rules"`
	DisabledSkills      []string        `json:"disabled_skills"`
	NativeToolsEnforced bool            `json:"native_tools_enforced"`
	// RunAs is team.RunAsCaller or team.RunAsOwner: whose access a turn
	// runs with (see team.SpawnIdentity).
	RunAs    string `json:"run_as"`
	Disabled bool   `json:"disabled"`
	// MentionFrom, MentionAllow and MaxHops are the Mention tab: who may
	// hand the agent a turn and its agent-to-agent turn cap (effective
	// value, default applied). See teamlink/policy.go.
	MentionFrom  string   `json:"mention_from"`
	MentionAllow []string `json:"mention_allow"`
	MaxHops      int      `json:"max_hops"`
	// UseGlobalPrompt: spawns carry the global system_prompt instead of
	// system_prompt_team (on for agents converted from a project).
	UseGlobalPrompt bool `json:"use_global_prompt"`
	// AllowProviderSwitch is the effective value (default applied).
	AllowProviderSwitch bool                   `json:"allow_provider_switch"`
	SuggestedPrompts    []team.SuggestedPrompt `json:"suggested_prompts"`
	// ManageAgents is the effective "Manage other agents" permission
	// (on for the Captain unless switched off); CaptainCan is what the
	// Captain may do to this agent. See team.ManagesAgents.
	ManageAgents  bool            `json:"manage_agents"`
	CaptainCan    team.CaptainCan `json:"captain_can"`
	MainSessionID string          `json:"main_session_id"`
	LastActive    *time.Time      `json:"last_active"`
	LastPreview   string          `json:"last_preview"`
	Status        string          `json:"status"`
	// Unread is true when the main session moved after the owner last
	// opened the chat (POST /api/team/agents/{id}/read).
	Unread bool `json:"unread"`
	// NeedsAttention is true while the main session waits on the owner:
	// an ask_user question or a tool approval.
	NeedsAttention bool `json:"needs_attention"`
	// CurrentAction names the tool the main session's running turn is
	// waiting on ("Bash", "query_range"); "" when idle, thinking or
	// writing.
	CurrentAction string `json:"current_action"`
	// AttentionPreview is the short line of what NeedsAttention waits on
	// ("Butuh input: …", "Bash — butuh approval"); also LastPreview then.
	AttentionPreview string `json:"attention_preview,omitempty"`
	// SharedWith counts everything else on the same project — other
	// agents of any owner and non-agent web/channel conversations — so
	// the editor can warn that a persona edit changes them too.
	SharedWith int `json:"shared_with"`
}

// TeamAgentSessionItem is one conversation of an agent.
type TeamAgentSessionItem struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	LastActive *time.Time `json:"last_active"`
	AgentMain  bool       `json:"agent_main"`
	Status     string     `json:"status"`
}

// teamAgentConnectorItem is one row of the access checklist. Names and op
// declarations only — never a config value.
type teamAgentConnectorItem struct {
	ID          string                 `json:"id"`
	Key         string                 `json:"key"`
	Label       string                 `json:"label"`
	Description string                 `json:"description"`
	Accounts    []teamAgentAccountItem `json:"accounts"`
	Ops         []teamAgentConnectorOp `json:"ops"`
	// Tier is the Access list the entry belongs to: "platform", "system"
	// or "" (Connectors). See team.TierOf.
	Tier string `json:"tier"`
	// Tool marks a wick MCP tool entry (id "tool:<name>"): on/off only.
	Tool bool `json:"tool,omitempty"`
}

type teamAgentAccountItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type teamAgentConnectorOp struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Destructive bool   `json:"destructive"`
}

// teamAgentWriteReq is the POST body and, with every field optional, the
// PATCH body. Pointers tell "absent" apart from "set to empty".
type teamAgentWriteReq struct {
	Handle       *string `json:"handle"`
	Name         *string `json:"name"`
	Icon         *string `json:"icon"`
	Tagline      *string `json:"tagline"`
	Description  *string `json:"description"`
	SystemPrompt *string `json:"system_prompt"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	// Preset is read on create only, for the new project's defaults (a
	// duplicated agent keeps its original's preset).
	Preset               *string                 `json:"preset"`
	ProjectID            *string                 `json:"project_id"`
	Avatar               *team.Avatar            `json:"avatar"`
	Features             *team.Features          `json:"features"`
	AllowedConnectors    *[]team.ConnectorGrant  `json:"allowed_connectors"`
	IncludeNewConnectors *bool                   `json:"include_new_connectors"`
	AllowedNativeTools   *[]string               `json:"allowed_native_tools"`
	BashRules            *[]team.BashRule        `json:"bash_rules"`
	SuggestedPrompts     *[]team.SuggestedPrompt `json:"suggested_prompts"`
	DisabledSkills       *[]string               `json:"disabled_skills"`
	RunAs                *string                 `json:"run_as"`
	Disabled             *bool                   `json:"disabled"`
	AllowProviderSwitch  *bool                   `json:"allow_provider_switch"`
	IsCaptain            *bool                   `json:"is_captain"`
	UseGlobalPrompt      *bool                   `json:"use_global_prompt"`
	MentionFrom          *string                 `json:"mention_from"`
	MentionAllow         *[]string               `json:"mention_allow"`
	MaxHops              *int                    `json:"max_hops"`
	ManageAgents         *bool                   `json:"manage_agents"`
	CaptainCan           *team.CaptainCan        `json:"captain_can"`
	// Convert (create only, with ProjectID) turns an ordinary project
	// into this agent's own: the project gets the Team tag, so it leaves
	// the sidebar and lives on in the Team app. See checkConvertProject.
	Convert bool `json:"convert"`
}

// captainSystemAddon is the starting persona of the auto-created Captain.
// Kept short: the owner is expected to rewrite it. It names no one: the
// agent's name and handle come from the "Who you are" block built at
// spawn (team.WhoYouAre), so a rename never leaves a stale name here.
const captainSystemAddon = "You are the owner's main agent. Help the owner run their Team — who handles what — " +
	"and handle yourself whatever doesn't fit another agent."

// defaultAgentSystemAddon is saved for a new agent created with an empty
// system prompt, so its persona is never blank.
const defaultAgentSystemAddon = "Help the user with tasks in your area. Be concise and say when something is outside your access."

/* ── helpers ─────────────────────────────────────────────────────────────── */

func teamReady(c *tool.Ctx) bool {
	if notReady(c) {
		return false
	}
	if globalTeam == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "agents app is not enabled"})
		return false
	}
	if actorID(c) == "" {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "sign in to use agents"})
		return false
	}
	return true
}

// loadOwnTeamAgent fetches {id} and confirms the caller owns it. Someone
// else's agent answers 404, not 403, so ids are not probeable.
func loadOwnTeamAgent(c *tool.Ctx) (entity.AgentPersona, bool) {
	p, err := globalTeam.Get(c.Context(), c.PathValue("id"))
	if err != nil || p.OwnerUserID != actorID(c) {
		if err != nil && !errors.Is(err, team.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return p, false
		}
		c.JSON(http.StatusNotFound, map[string]string{"error": "agent not found"})
		return p, false
	}
	return p, true
}

func decodeTeamAgentReq(c *tool.Ctx) (teamAgentWriteReq, bool) {
	var req teamAgentWriteReq
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return req, false
	}
	return req, true
}

// ownerCatalog is the caller's own connector reach — the same set wick_list
// shows them — with any agent scope stripped from ctx, since the checklist
// is what an agent is narrowed FROM. Every route here acts on the caller's
// own agents, so the caller is the owner.
func ownerCatalog(c *tool.Ctx) ([]connectors.CatalogEntry, error) {
	if globalConnectors == nil {
		return nil, nil
	}
	u := login.GetUser(c.Context())
	isAdmin := u != nil && u.IsAdmin()
	ctx := connectors.WithoutAgentScope(c.Context())
	return globalConnectors.AgentCatalog(ctx, actorID(c), login.GetUserTagIDs(c.Context()), isAdmin)
}

// ownerReach is ownerCatalog indexed for team.MigrateFeatures; nil when
// the catalog cannot be read, which leaves the old switches as they are.
func ownerReach(c *tool.Ctx) team.Reach {
	cat, err := ownerCatalog(c)
	if err != nil {
		return nil
	}
	return team.ReachOf(cat)
}

// migrateTeamAccess rewrites p's old Notes/Tickets/Source/Sub-agents/
// Schedule switches into off grants (team.MigrateFeatures). Reports
// whether p changed; the caller saves.
func migrateTeamAccess(p *entity.AgentPersona, reach team.Reach) bool {
	f, gs, changed := team.MigrateFeatures(team.DecodeFeatures(p.Features), team.DecodeGrants(p.AllowedConnectors), reach)
	if changed {
		p.Features = team.EncodeFeatures(f)
		p.AllowedConnectors = team.EncodeGrants(gs)
	}
	return changed
}

// teamToolLabels names the PlatformTools entries in the Access tab.
var teamToolLabels = map[string][2]string{
	"wick_schedule_message":  {"Schedule", "Schedule a message into this conversation later."},
	"todo":                   {"Todo list", "Keep a step-by-step plan for the current task."},
	"ask_user":               {"Ask the user", "Pause and ask the person a question."},
	"wick_set_title":         {"Session title", "Rename the conversation."},
	"wick_session_info":      {"Session info", "Read details of this conversation."},
	"wick_context":           {"Context usage", "See how full the context window is."},
	"wick_usage":             {"Usage", "Read token and cost usage."},
	"wick_compact":           {"Compact", "Summarise the conversation to free context."},
	"wick_cli_token":         {"CLI token", "Mint a token for the wick CLI."},
	"wick_session_workspace": {"Session workspace", "Add throwaway connectors for this conversation."},
}

// validateGrants rejects a checklist the owner could not have ticked: an
// entry this build cannot interpret, or a connector, account or picked op
// outside the owner's catalog. Writes the 400/500 itself; false = stop.
func validateGrants(c *tool.Ctx, gs []team.ConnectorGrant) bool {
	cat, err := ownerCatalog(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	if err := team.CheckGrants(gs, team.CatalogOf(cat)); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return false
	}
	return true
}

// validRunAs reads req.RunAs: the stored value (unchanged when absent),
// or a 400 for anything but caller / owner. false = stop.
func validRunAs(c *tool.Ctx, v *string, current string) (string, bool) {
	if v == nil {
		return team.NormalizeRunAs(current), true
	}
	switch *v {
	case team.RunAsCaller, team.RunAsOwner:
		return *v, true
	}
	c.JSON(http.StatusBadRequest, map[string]string{"error": "run_as must be caller or owner"})
	return "", false
}

// applyMentionSettings writes the Mention tab fields of req onto p. The
// allow list may only name other agents of p's owner. false = a 400 was
// written.
func applyMentionSettings(c *tool.Ctx, p *entity.AgentPersona, req teamAgentWriteReq) bool {
	if req.MentionFrom != nil {
		v := strings.ToLower(strings.TrimSpace(*req.MentionFrom))
		if !teamlink.ValidMentionFrom(v) {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "mention_from must be all, captain, list or off"})
			return false
		}
		p.MentionFrom = v
	}
	if req.MaxHops != nil {
		if n := *req.MaxHops; n < 1 || n > teamlink.MaxHopsCeiling {
			c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("max_hops must be 1–%d", teamlink.MaxHopsCeiling)})
			return false
		}
		p.MaxHops = *req.MaxHops
	}
	if req.MentionAllow != nil {
		own, err := globalTeam.List(c.Context(), p.OwnerUserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return false
		}
		ids, err := teamAllowList(*req.MentionAllow, own, p.ID)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return false
		}
		p.MentionAllow = encodeIDList(ids)
	}
	return true
}

// teamAllowList dedupes ids and checks each names an agent in own other
// than self.
func teamAllowList(ids []string, own []entity.AgentPersona, self string) ([]string, error) {
	known := make(map[string]bool, len(own))
	for _, a := range own {
		known[a.ID] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if id == self || !known[id] {
			return nil, fmt.Errorf("mention_allow: %q is not another agent of yours", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// nonNilIDs keeps a JSON list a list.
func nonNilIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// validTagline trims a tagline from the request; nil keeps current.
// Writes the 400; false = stop.
func validTagline(c *tool.Ctx, v *string, current string) (string, bool) {
	if v == nil {
		return current, true
	}
	t, err := team.NormalizeTagline(*v)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return "", false
	}
	return t, true
}

// teamAgentSaveStatus maps a row-level error to an HTTP status.
func teamAgentSaveStatus(err error) int {
	switch {
	case errors.Is(err, team.ErrHandleTaken):
		return http.StatusConflict
	case errors.Is(err, team.ErrNotFound):
		return http.StatusNotFound
	case strings.HasPrefix(err.Error(), "handle "):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// agentSessions returns the owner's sessions of one agent, newest first.
func agentSessions(ownerID, agentID string) []session.Session {
	var out []session.Session
	for _, s := range globalMgr.Registry().Sessions() {
		// A group's backing session answers for the group, not as one of
		// the agent's own chats.
		if s.Meta.AgentID == agentID && s.Meta.UserID == ownerID && s.Meta.GroupSessionID == "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.LastActive.After(out[j].Meta.LastActive) })
	return out
}

// mainSessionOf returns the agent's main conversation, if it has one.
func mainSessionOf(ownerID, agentID string) (session.Session, bool) {
	for _, s := range agentSessions(ownerID, agentID) {
		if s.Meta.AgentMain {
			return s, true
		}
	}
	return session.Session{}, false
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// teamLive is the in-memory turn state of every session, read once per
// response so a roster of N agents costs one pool snapshot rather than N.
type teamLive struct {
	actions    map[string]string // session id → CurrentAction
	lifecycles map[string]string // session id → pool lifecycle
	approvals  map[string]string // session id → tool of a pending approval
}

func teamLiveNow() teamLive {
	l := teamLive{actions: map[string]string{}, lifecycles: map[string]string{}, approvals: map[string]string{}}
	if globalPool != nil {
		for _, e := range globalPool.ActiveSnapshot() {
			l.lifecycles[e.SessionID] = e.Lifecycle
			if a := team.CurrentAction(e.InFlightEvents); a != "" {
				l.actions[e.SessionID] = a
			}
		}
	}
	if globalApprovals != nil {
		for _, r := range globalApprovals.PendingFor("") {
			if _, seen := l.approvals[r.SessionID]; !seen {
				l.approvals[r.SessionID] = r.Tool
			}
		}
	}
	return l
}

// attention returns the roster preview of what sessionID waits on a
// person for — an ask_user question first, then a tool approval — or ""
// when it waits on nobody.
func (l teamLive) attention(sessionID string) string {
	if globalAskUsers != nil {
		if asks := globalAskUsers.PendingFor(sessionID); len(asks) > 0 {
			return team.AskPreview(asks[0].Question)
		}
	}
	if tool, ok := l.approvals[sessionID]; ok {
		return team.ApprovalPreview(tool)
	}
	return ""
}

// teamPreviewCache keeps the last roster preview per conversation file,
// keyed by its size and mtime, so an unchanged chat costs one stat per
// roster load instead of a read.
var teamPreviewCache = struct {
	sync.Mutex
	m map[string]teamPreviewEntry
}{m: map[string]teamPreviewEntry{}}

type teamPreviewEntry struct {
	size int64
	mod  time.Time
	text string
}

// lastPreview returns the one-line preview of sessionID's newest message.
func lastPreview(sessionID string) string {
	path := globalLayout.SessionConversation(sessionID)
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	teamPreviewCache.Lock()
	e, ok := teamPreviewCache.m[path]
	teamPreviewCache.Unlock()
	if ok && e.size == st.Size() && e.mod.Equal(st.ModTime()) {
		return e.text
	}
	text := team.TailPreview(path)
	teamPreviewCache.Lock()
	teamPreviewCache.m[path] = teamPreviewEntry{size: st.Size(), mod: st.ModTime(), text: text}
	teamPreviewCache.Unlock()
	return text
}

// teamProjectUsers counts, per project, who else a persona edit reaches:
// every agent on it (any owner) and every non-agent conversation (web,
// channel) run in it. Built once per response.
type teamProjectUsers struct {
	agents   map[string]int // project id → agents on it
	sessions map[string]int // project id → non-agent top-level sessions
}

func teamProjectUsersFor(ctx context.Context, rows []entity.AgentPersona) teamProjectUsers {
	u := teamProjectUsers{agents: map[string]int{}, sessions: map[string]int{}}
	want := map[string]bool{}
	var ids []string
	for _, r := range rows {
		if r.ProjectID != "" && !want[r.ProjectID] {
			want[r.ProjectID] = true
			ids = append(ids, r.ProjectID)
		}
	}
	if len(ids) == 0 {
		return u
	}
	if all, err := globalTeam.ListByProjects(ctx, ids); err == nil {
		for _, a := range all {
			u.agents[a.ProjectID]++
		}
	}
	for _, s := range globalMgr.Registry().Sessions() {
		m := s.Meta
		// Sub-agent sessions belong to their parent's count; agent
		// sessions are already counted as their agent.
		if want[m.ProjectID] && m.AgentID == "" && m.ParentSessionID == "" {
			u.sessions[m.ProjectID]++
		}
	}
	return u
}

// others is how many agents and conversations besides agent p use its
// project.
func (u teamProjectUsers) others(p entity.AgentPersona) int {
	if p.ProjectID == "" {
		return 0
	}
	n := u.agents[p.ProjectID] - 1 // p itself
	if n < 0 {
		n = 0
	}
	return n + u.sessions[p.ProjectID]
}

// teamAgentToItem renders one row. users feeds SharedWith; the zero
// value counts nothing. reach (the owner's catalog, may be nil) turns
// Features into what the agent can actually use, so the chat rail hides a
// tab whose Access row is Off.
func teamAgentToItem(p entity.AgentPersona, users teamProjectUsers, live teamLive, reach team.Reach) TeamAgentItem {
	it := TeamAgentItem{
		ID: p.ID, Handle: p.Handle, IsCaptain: p.IsCaptain, ProjectID: p.ProjectID,
		Kind:                 p.Kind,
		Remote:               remoteInfoFor(p),
		SlackRemote:          slackRemoteInfoFor(p),
		Name:                 p.Handle,
		Tagline:              p.Tagline,
		Features:             team.EffectiveFeatures(p, reach),
		Avatar:               team.DecodeAvatar(p.Avatar),
		AllowedConnectors:    team.DecodeGrants(p.AllowedConnectors),
		IncludeNewConnectors: p.IncludeNewConnectors,
		AllowedNativeTools:   team.DecodeNativeTools(p.AllowedNativeTools),
		BashRules:            team.DecodeBashRules(p.BashRules),
		DisabledSkills:       team.DecodeSkillNames(p.DisabledSkills),
		RunAs:                team.NormalizeRunAs(p.RunAs),
		Disabled:             p.Disabled,
		MentionFrom:          teamlink.NormalizeMentionFrom(p.MentionFrom),
		MentionAllow:         nonNilIDs(decodeIDList(p.MentionAllow)),
		MaxHops:              teamlink.EffectiveHops(p.MaxHops),
		UseGlobalPrompt:      p.UseGlobalPrompt,
		AllowProviderSwitch:  team.AllowsProviderSwitch(p.AllowProviderSwitch, p.IsCaptain),
		SuggestedPrompts:     team.DecodeSuggestedPrompts(p.SuggestedPrompts),
		ManageAgents:         team.ManagesAgents(p),
		CaptainCan:           team.DecodeCaptainCan(p.CaptainCan),
		Status:               string(session.StatusIdle),
	}
	if p.ProjectID != "" {
		if proj, ok := globalMgr.Registry().Project(p.ProjectID); ok {
			m := proj.Meta
			if m.Name != "" {
				it.Name = m.Name
			}
			it.Icon, it.Description = m.Icon, m.Description
			it.SystemPrompt = m.Defaults.SystemAddon
			it.Provider, it.Model, it.Preset = m.Defaults.Provider, m.Defaults.Model, m.Defaults.Preset
		}
	}
	it.NativeToolsEnforced = team.NativeToolsEnforced(providerTypeOf(it.Provider))
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		it.MainSessionID = s.ID
		it.LastActive = timePtr(s.Meta.LastActive)
		// Meta.Status stays "running" for as long as the process is warm,
		// turn or no turn; the pool lifecycle is what says a turn is on.
		it.Status = team.TurnStatus(string(s.Meta.Status), live.lifecycles[s.ID])
		it.Unread = team.Unread(s.Meta.LastActive, p.LastReadAt)
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
	it.SharedWith = users.others(p)
	return it
}

// applyProjectFields writes the persona half of req into project meta.
// Returns whether anything changed so an untouched project is not
// rewritten (and its UpdatedAt not bumped) by an access-only PATCH.
func applyProjectFields(m *project.Meta, req teamAgentWriteReq) bool {
	changed := false
	set := func(dst *string, v *string, trim bool) {
		if v == nil {
			return
		}
		nv := *v
		if trim {
			nv = strings.TrimSpace(nv)
		}
		if *dst != nv {
			*dst = nv
			changed = true
		}
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		set(&m.Name, req.Name, true)
	}
	if req.Icon != nil && strings.TrimSpace(*req.Icon) != "" {
		set(&m.Icon, req.Icon, true)
	}
	set(&m.Description, req.Description, false)
	set(&m.Defaults.SystemAddon, req.SystemPrompt, false)
	if req.Provider != nil {
		set(&m.Defaults.Provider, req.Provider, true)
		// A model id is scoped to one instance; switching the provider
		// without naming a model drops the old pin.
		if req.Model == nil && m.Defaults.Model != "" {
			m.Defaults.Model = ""
			changed = true
		}
	}
	if req.Model != nil {
		nv := modelWithProvider(m.Defaults.Provider, *req.Model)
		if m.Defaults.Model != nv {
			m.Defaults.Model = nv
			changed = true
		}
	}
	return changed
}

// requireAgentProjectAccess re-checks that the caller (the agent's owner)
// still reaches the project the agent is linked to. Access can be revoked
// after the link was made, and the link alone must not keep the project
// open to them. A project that no longer exists passes: the agent then
// runs unscoped (see createTeamAgentSession). Writes the 403; false = stop.
func requireAgentProjectAccess(c *tool.Ctx, p entity.AgentPersona) bool {
	if p.ProjectID == "" {
		return true
	}
	if _, ok := globalMgr.Registry().Project(p.ProjectID); !ok {
		return true
	}
	if callerProjectAccess(c).allowProject(p.ProjectID) {
		return true
	}
	c.JSON(http.StatusForbidden, map[string]string{"error": "you no longer have access to this agent's project; link it to another project first"})
	return false
}

// requireUsableProject checks a project_id the caller named: it must exist
// and be one the caller may already open.
func requireUsableProject(c *tool.Ctx, id string) bool {
	if _, ok := globalMgr.Registry().Project(id); !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return false
	}
	if !callerProjectAccess(c).allowProject(id) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "no access to that project"})
		return false
	}
	return true
}

// checkConvertProject guards "Make this an agent…": only the project's
// owner may convert it, never a protected (default/personal) project, and
// never one that already is an agent's or that another agent points at.
// Writes the error; false = stop.
func checkConvertProject(c *tool.Ctx, pid string) bool {
	proj, _ := globalMgr.Registry().Project(pid)
	refuse := func(code int, msg string) bool {
		c.JSON(code, map[string]string{"error": msg})
		return false
	}
	switch {
	case proj.Meta.OwnerUserID == "" || proj.Meta.OwnerUserID != actorID(c):
		return refuse(http.StatusForbidden, "only the project's owner can make it an agent")
	case project.IsProtected(proj.Meta):
		return refuse(http.StatusBadRequest, "a default or personal project cannot become an agent")
	case project.IsAgentProject(proj.Meta):
		return refuse(http.StatusConflict, "this project is already an agent")
	}
	if rows, err := globalTeam.ListByProjects(c.Context(), []string{pid}); err != nil || len(rows) > 0 {
		return refuse(http.StatusConflict, "an agent already uses this project")
	}
	return true
}

// boolOr is *v, or def when v is nil.
func boolOr(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// stampProjectSessions makes every top-level session of a converted
// project the agent's: channel threads, schedule fires and web chats
// alike. Their bindings (channel → project, schedule → project) are
// untouched, so they keep running; from now on they run as the agent.
// Sessions created later get the same stamp from session.ProjectAgent.
// Returns how many sessions were stamped.
func stampProjectSessions(c *tool.Ctx, pid, agentID string) int {
	n := 0
	for _, sid := range globalMgr.ProjectSessionIDs(pid) {
		sess, ok := globalMgr.Registry().Session(sid)
		if !ok || sess.Meta.ParentSessionID != "" || sess.Meta.AgentID != "" {
			continue
		}
		sess.Meta.AgentID = agentID
		if err := session.SaveMeta(globalLayout, sid, sess.Meta); err != nil {
			log.Ctx(c.Context()).Warn().Err(err).Str("session", sid).Msg("team convert: stamp session")
			continue
		}
		_ = globalMgr.RefreshSession(sid)
		n++
	}
	return n
}

// createTeamAgentProject makes the project a new agent's persona lives in.
func createTeamAgentProject(c *tool.Ctx, name, icon, description, systemPrompt, provider, model, preset string) (string, error) {
	return createAgentProjectFor(c.Context(), actorID(c), name, icon, description, systemPrompt, provider, model, preset)
}

// createAgentProjectFor is createTeamAgentProject for an owner named
// outright — the Captain's agents.create has no HTTP request.
func createAgentProjectFor(ctx context.Context, owner, name, icon, description, systemPrompt, provider, model, preset string) (string, error) {
	opt := project.CreateOptions{
		ID:          uuid.New().String(),
		Name:        name,
		Icon:        strings.TrimSpace(icon),
		Description: description,
		OwnerUserID: owner,
		Tags:        []string{project.AgentTag},
		Defaults: project.Defaults{
			Provider:    strings.TrimSpace(provider),
			Model:       modelWithProvider(provider, model),
			Preset:      strings.TrimSpace(preset),
			SystemAddon: systemPrompt,
		},
	}
	if _, err := globalMgr.CreateProject(ctx, opt); err != nil {
		return "", err
	}
	if globalTagsSvc != nil {
		_ = globalTagsSvc.CreateResourceOwnerTag(ctx, opt.ID, owner)
	}
	return opt.ID, nil
}

// discardTeamAgentProject removes a project createTeamAgentProject just
// made for an agent row that was then not saved. Best effort: a failure
// only leaves an empty project behind, which is what used to happen.
func discardTeamAgentProject(c *tool.Ctx, pid string) {
	if err := globalMgr.DeleteProject(c.Context(), pid); err != nil {
		log.Ctx(c.Context()).Warn().Err(err).Str("project", pid).Msg("team: discard orphan agent project")
		return
	}
	if globalTagsSvc != nil {
		_ = globalTagsSvc.DeleteResourceOwnerTag(c.Context(), pid)
	}
}

// ensureCaptain creates the owner's Captain when they have no agent yet,
// so the app never opens on an empty roster.
func ensureCaptain(c *tool.Ctx) ([]entity.AgentPersona, error) {
	owner := actorID(c)
	rows, err := globalTeam.List(c.Context(), owner)
	if err != nil || len(rows) > 0 {
		return rows, err
	}
	pid, err := createTeamAgentProject(c, "Captain", "🧭", "Lead agent that helps organise the team.", captainSystemAddon, "", "", "")
	if err != nil {
		return nil, err
	}
	p := &entity.AgentPersona{
		OwnerUserID: owner, Handle: "captain", ProjectID: pid, IsCaptain: true,
		AllowedConnectors: "[]",
		Features:          team.EncodeFeatures(team.DefaultFeatures()),
		Avatar:            team.EncodeAvatar(team.Avatar{Shape: "squircle", Color: "#f59e0b"}),
	}
	if err := globalTeam.Create(c.Context(), p); err != nil {
		// Either way the project made above has no agent and nothing
		// else knows its fresh id, so it goes rather than lingering as
		// an orphan in the Projects list.
		discardTeamAgentProject(c, pid)
		if !errors.Is(err, team.ErrHandleTaken) {
			return nil, err
		}
		// ErrHandleTaken = a concurrent first load won the race; its
		// Captain is the one to show.
	}
	return globalTeam.List(c.Context(), owner)
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// apiTeamAgentList handles GET /api/team/agents.
func apiTeamAgentList(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	// ?ensure=0 is the read-only roster (the Overview card): it never
	// creates the Captain, which only the Team app itself should do.
	var rows []entity.AgentPersona
	var err error
	if c.R.URL.Query().Get("ensure") == "0" {
		rows, err = globalTeam.List(c.Context(), actorID(c))
	} else {
		rows, err = ensureCaptain(c)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]TeamAgentItem, 0, len(rows))
	captainID := ""
	live, users := teamLiveNow(), teamProjectUsersFor(c.Context(), rows)
	reach := ownerReach(c)
	for _, r := range rows {
		if migrateTeamAccess(&r, reach) {
			// Best effort: the next load retries a failed save.
			_ = globalTeam.Update(c.Context(), &r)
		}
		items = append(items, teamAgentToItem(r, users, live, reach))
		if r.IsCaptain {
			captainID = r.ID
		}
	}
	c.JSON(http.StatusOK, map[string]any{"agents": items, "captain_id": captainID})
}

// apiTeamAgentCreate handles POST /api/team/agents.
func apiTeamAgentCreate(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	req, ok := decodeTeamAgentReq(c)
	if !ok {
		return
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	handle := team.NormalizeHandle(str(req.Handle))
	if err := validateTeamHandle(c.Context(), handle); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(str(req.Name))
	if name == "" {
		name = handle
	}
	// Everything that can refuse the request is checked before a project
	// is made, so a 400 does not leave an orphan project behind.
	runAs, ok := validRunAs(c, req.RunAs, "")
	if !ok {
		return
	}
	tagline, ok := validTagline(c, req.Tagline, "")
	if !ok {
		return
	}
	grants := "[]"
	if req.AllowedConnectors != nil {
		if !validateGrants(c, *req.AllowedConnectors) {
			return
		}
		grants = team.EncodeGrants(*req.AllowedConnectors)
	}
	pid := strings.TrimSpace(str(req.ProjectID))
	existing := pid != ""
	if req.Convert && !existing {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "convert needs a project_id"})
		return
	}
	if existing {
		if !requireUsableProject(c, pid) {
			return
		}
		if req.Convert && !checkConvertProject(c, pid) {
			return
		}
	} else {
		// Handle clash checked before a project is made, so a refused
		// create does not leave an orphan project behind.
		if _, err := globalTeam.GetByHandle(c.Context(), actorID(c), handle); err == nil {
			c.JSON(http.StatusConflict, map[string]string{"error": team.ErrHandleTaken.Error()})
			return
		}
		var err error
		sys := str(req.SystemPrompt)
		if strings.TrimSpace(sys) == "" {
			sys = defaultAgentSystemAddon
		}
		pid, err = createTeamAgentProject(c, name, str(req.Icon), str(req.Description), sys, str(req.Provider), str(req.Model), str(req.Preset))
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	feats := team.DefaultFeatures()
	if req.Features != nil {
		feats = *req.Features
	}
	av := team.DefaultAvatarFor(handle)
	if req.Avatar != nil {
		av = *req.Avatar
	}
	p := &entity.AgentPersona{
		OwnerUserID: actorID(c), Handle: handle, ProjectID: pid, Tagline: tagline,
		// A converted project keeps its operator prompt unless told not to.
		UseGlobalPrompt:      boolOr(req.UseGlobalPrompt, req.Convert),
		AllowedConnectors:    grants,
		IncludeNewConnectors: req.IncludeNewConnectors != nil && *req.IncludeNewConnectors,
		RunAs:                runAs,
		Features:             team.EncodeFeatures(feats),
		Avatar:               team.EncodeAvatar(av),
	}
	if !applyToolSettings(c, p, req) {
		return
	}
	if err := globalTeam.Create(c.Context(), p); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	if existing {
		// "Pakai project yang ada": the wizard shows that project's persona
		// and sends back what is in the form, so a name typed there is the
		// agent's name rather than being dropped for the project's.
		if proj, ok := globalMgr.Registry().Project(pid); ok {
			meta := proj.Meta
			changed := applyProjectFields(&meta, req)
			if req.Convert && !project.IsAgentProject(meta) {
				meta.Tags = append(slices.Clone(meta.Tags), project.AgentTag)
				changed = true
			}
			if changed {
				if _, err := globalMgr.UpdateProject(c.Context(), pid, meta); err != nil {
					c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
			}
		}
		if req.Convert {
			stampProjectSessions(c, pid, p.ID)
		}
	}
	how := "wizard"
	if req.Convert {
		how = "convert"
	}
	announceAgentCreated(c, *p, name, how)
	users := teamProjectUsersFor(c.Context(), []entity.AgentPersona{*p})
	c.JSON(http.StatusOK, teamAgentToItem(*p, users, teamLiveNow(), ownerReach(c)))
}

// apiTeamAgentUpdate handles PATCH /api/team/agents/{id}.
func apiTeamAgentUpdate(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	req, ok := decodeTeamAgentReq(c)
	if !ok {
		return
	}
	if req.ProjectID != nil {
		pid := strings.TrimSpace(*req.ProjectID)
		if pid == "" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "project_id cannot be empty"})
			return
		}
		if pid != p.ProjectID && !requireUsableProject(c, pid) {
			return
		}
		p.ProjectID = pid
	}
	// Moving the agent to a project the caller reaches is the way out;
	// anything else on a project they lost is refused.
	if !requireAgentProjectAccess(c, p) {
		return
	}
	if req.Handle != nil {
		p.Handle = team.NormalizeHandle(*req.Handle)
		if err := validateTeamHandle(c.Context(), p.Handle); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	accessBefore := p
	if req.AllowedConnectors != nil {
		if !validateGrants(c, *req.AllowedConnectors) {
			return
		}
		p.AllowedConnectors = team.EncodeGrants(*req.AllowedConnectors)
	}
	if req.IncludeNewConnectors != nil {
		p.IncludeNewConnectors = *req.IncludeNewConnectors
	}
	if !applyToolSettings(c, &p, req) {
		return
	}
	if p.RunAs, ok = validRunAs(c, req.RunAs, p.RunAs); !ok {
		return
	}
	if p.Tagline, ok = validTagline(c, req.Tagline, p.Tagline); !ok {
		return
	}
	if req.Disabled != nil {
		p.Disabled = *req.Disabled
	}
	if !applyMentionSettings(c, &p, req) {
		return
	}
	if req.UseGlobalPrompt != nil {
		p.UseGlobalPrompt = *req.UseGlobalPrompt
	}
	if req.AllowProviderSwitch != nil {
		v := *req.AllowProviderSwitch
		p.AllowProviderSwitch = &v
	}
	if req.Features != nil {
		p.Features = team.EncodeFeatures(*req.Features)
	}
	if req.Avatar != nil {
		p.Avatar = team.EncodeAvatar(*req.Avatar)
	}
	if req.ManageAgents != nil {
		v := *req.ManageAgents
		p.ManageAgents = &v
	}
	if req.CaptainCan != nil {
		p.CaptainCan = team.EncodeCaptainCan(*req.CaptainCan)
	}
	if req.IsCaptain != nil {
		// Captaincy moves by promoting another agent; un-ticking the
		// current one would leave the owner with none.
		if !*req.IsCaptain && p.IsCaptain {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "make another agent Captain instead"})
			return
		}
		p.IsCaptain = *req.IsCaptain
	}

	// Persona text goes to the project (after any project switch above,
	// so it lands in the project the agent now points at).
	if p.ProjectID != "" {
		if proj, ok := globalMgr.Registry().Project(p.ProjectID); ok {
			meta := proj.Meta
			if applyProjectFields(&meta, req) {
				if _, err := globalMgr.UpdateProject(c.Context(), p.ProjectID, meta); err != nil {
					c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
			}
		}
	}
	reach := ownerReach(c)
	migrateTeamAccess(&p, reach)
	if err := globalTeam.Update(c.Context(), &p); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	announceAccessChanged(c, accessBefore, p)
	// Disabling holds the agent's schedules, enabling releases them.
	syncAgentSchedules(c.Context(), accessBefore, p)
	// Disabling takes the agent's Slack bot offline, enabling brings it back.
	syncAgentSlack(context.Background(), p)
	users := teamProjectUsersFor(c.Context(), []entity.AgentPersona{p})
	c.JSON(http.StatusOK, teamAgentToItem(p, users, teamLiveNow(), reach))
}

// apiTeamAgentDelete handles DELETE /api/team/agents/{id}?chats=delete|keep.
//
// chats=delete removes the agent's own project with it — conversations,
// managed folder and the provider memory of it (purgeProject). Anything
// else keeps them: the project loses its Team tag so it shows in the
// sidebar as an ordinary project instead of going invisible. A project the
// agent was merely pointed at (no Team tag) or that another agent still
// uses is never deleted.
func apiTeamAgentDelete(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	rows, err := globalTeam.List(c.Context(), actorID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if p.IsCaptain && len(rows) > 1 {
		c.JSON(http.StatusConflict, map[string]string{"error": "make another agent Captain before deleting this one"})
		return
	}
	if err := globalTeam.Delete(c.Context(), p.ID); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	removeAgentSlack(p.ID)
	removeAgentRemote(p)
	// Before the project goes: the scope is read from the live sessions.
	deleteAgentSchedules(c.Context(), p)
	if err := releaseTeamAgentProject(c.Context(), p, rows, c.Query("chats") == "delete"); err != nil {
		log.Ctx(c.Context()).Error().Err(err).Str("project", p.ProjectID).Msg("team: release agent project")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "agent deleted, but its project was not: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// releaseTeamAgentProject settles the project of a deleted agent: purge it
// or untag it (see apiTeamAgentDelete). rows is the owner's roster as it was
// before the delete.
func releaseTeamAgentProject(ctx context.Context, agent entity.AgentPersona, rows []entity.AgentPersona, purge bool) error {
	pr, ok := globalMgr.Registry().Project(agent.ProjectID)
	if agent.ProjectID == "" || !ok || !slices.Contains(pr.Meta.Tags, project.AgentTag) {
		return nil
	}
	for _, r := range rows {
		if r.ID != agent.ID && r.ProjectID == agent.ProjectID {
			return nil
		}
	}
	if purge {
		return purgeProject(ctx, pr, "user")
	}
	meta := pr.Meta
	meta.Tags = slices.DeleteFunc(slices.Clone(meta.Tags), func(t string) bool { return t == project.AgentTag })
	_, err := globalMgr.UpdateProject(ctx, pr.Meta.ID, meta)
	return err
}

// apiTeamAgentConnectors handles GET /api/team/agents/connectors: the checklist
// source, i.e. what the OWNER reaches — exactly the connectors, accounts and
// live ops wick_list shows them (see ownerCatalog).
func apiTeamAgentConnectors(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	cat, err := ownerCatalog(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]teamAgentConnectorItem, 0, len(cat))
	for _, e := range cat {
		row, mod := e.Row, e.Module
		it := teamAgentConnectorItem{
			ID: row.ID, Key: row.Key, Label: row.Label,
			Description: strings.TrimSpace(row.Description),
			Accounts:    []teamAgentAccountItem{},
			Ops:         []teamAgentConnectorOp{},
			Tier:        team.TierOf(mod.Meta.DefaultTags),
		}
		if it.Description == "" {
			it.Description = mod.Meta.Description
		}
		if mod.OAuth != nil {
			// The instance's own identity is one more pickable account —
			// wick_list's connector entry for the row.
			it.Accounts = append(it.Accounts, teamAgentAccountItem{ID: "", DisplayName: "Bot / instance"})
			for _, a := range e.Accounts {
				it.Accounts = append(it.Accounts, teamAgentAccountItem{ID: a.ID, DisplayName: a.DisplayName})
			}
		}
		for _, op := range e.Ops {
			it.Ops = append(it.Ops, teamAgentConnectorOp{Key: op.Key, Name: op.Name, Destructive: op.Destructive})
		}
		out = append(out, it)
	}
	for _, name := range team.PlatformTools {
		l := teamToolLabels[name]
		if l[0] == "" {
			l = [2]string{name, ""}
		}
		out = append(out, teamAgentConnectorItem{
			ID: team.ToolPrefix + name, Key: name, Label: l[0], Description: l[1],
			Accounts: []teamAgentAccountItem{}, Ops: []teamAgentConnectorOp{},
			Tier: team.TierPlatform, Tool: true,
		})
	}
	c.JSON(http.StatusOK, out)
}

// apiTeamAgentChat handles POST /api/team/agents/{id}/chat: returns the agent's
// main conversation (creating it on first use) or, with new=true, a fresh
// side conversation.
func apiTeamAgentChat(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var body struct {
		New bool `json:"new"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if !requireAgentProjectAccess(c, p) {
		return
	}
	if !body.New {
		if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
			c.JSON(http.StatusOK, map[string]string{"session_id": s.ID})
			return
		}
	}
	id, err := createTeamAgentSession(c, p, !body.New)
	if err != nil {
		log.Ctx(c.Context()).Error().Err(err).Str("agent", p.ID).Msg("team agent chat: create session")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"session_id": id})
}

// createTeamAgentSession mirrors startNewSession minus the first message:
// the Agents app opens the chat empty and the user types into it.
func createTeamAgentSession(c *tool.Ctx, p entity.AgentPersona, main bool) (string, error) {
	projectID := p.ProjectID
	if projectID != "" {
		if _, ok := globalMgr.Registry().Project(projectID); !ok {
			// The project was deleted under the agent: chat unscoped
			// rather than refuse — the persona text is gone either way.
			projectID = ""
		}
	}
	prov, modelID := resolveSessionTarget(c, "", "", projectID)
	if key, ok := remoteProviderKey(p); ok {
		prov, modelID = key, ""
	}
	presetName := "default"
	label := p.Handle
	if projectID != "" {
		if proj, ok := globalMgr.Registry().Project(projectID); ok {
			if proj.Meta.Defaults.Preset != "" {
				presetName = proj.Meta.Defaults.Preset
			}
			if proj.Meta.Name != "" {
				label = proj.Meta.Name
			}
		}
	}
	id := uuid.New().String()
	if _, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{
		ID:        id,
		ProjectID: projectID,
		Origin:    session.OriginUI,
		Preset:    presetName,
		UserID:    actorID(c),
		AgentID:   p.ID,
		AgentMain: main,
	}); err != nil {
		return "", err
	}
	if err := globalMgr.AddAgent(id, "main", prov); err != nil {
		return "", err
	}
	if modelID != "" {
		if err := session.SetModelID(globalLayout, id, "main", modelID); err != nil {
			log.Ctx(c.Context()).Warn().Msgf("team agent chat set model id: %s", err.Error())
		}
	}
	// A new main chat opens on the agent's arrival, so its first turn is
	// the agent_created chip.
	if main {
		extras := agentCreatedExtras(p, label, actorName(c), "", connectorLabeler(c), "chat")
		emitSystemEvent(id, store.KindAgentCreated, agentCreatedText(label, extras["created_by"], "", extras["grants_summary"]), extras)
	}
	// Titled after the agent so its conversations read as the agent's in
	// the ordinary session list too. Best-effort: an untitled session is
	// still a working one.
	if sess, ok := globalMgr.Registry().Session(id); ok {
		sess.Meta.Label = label
		sess.Meta.TitleCustom = true
		if err := session.SaveMeta(globalLayout, id, sess.Meta); err == nil {
			_ = globalMgr.RefreshSession(id)
		}
	}
	return id, nil
}

// apiTeamAgentRead handles POST /api/team/agents/{id}/read: the owner opened
// the agent's chat, so what it has said so far is read.
func apiTeamAgentRead(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if err := globalTeam.MarkRead(c.Context(), p.ID, now); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"status": "ok", "last_read_at": now})
}

// apiTeamAgentSessions handles GET /api/team/agents/{id}/sessions.
func apiTeamAgentSessions(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	out := make([]TeamAgentSessionItem, 0)
	for _, s := range agentSessions(p.OwnerUserID, p.ID) {
		out = append(out, TeamAgentSessionItem{
			ID: s.ID, Label: s.Meta.Label, LastActive: timePtr(s.Meta.LastActive),
			AgentMain: s.Meta.AgentMain, Status: string(s.Meta.Status),
		})
	}
	c.JSON(http.StatusOK, out)
}

// validateTeamHandle is team.ValidateHandle plus one rule the team package
// cannot see: a handle may not equal a sub-agent role key, or "@handle"
// would mean two different things to the mention router.
func validateTeamHandle(ctx context.Context, handle string) error {
	if err := team.ValidateHandle(handle); err != nil {
		return err
	}
	if globalDelegation != nil && globalDelegation.Repo != nil {
		if p, err := globalDelegation.Repo.GetProfile(ctx, handle); err == nil && p != nil {
			return fmt.Errorf("handle %q is already a sub-agent role; pick another", handle)
		}
	}
	return nil
}

// teamAccessHistoryItem is one row of Settings › Access › History.
type teamAccessHistoryItem struct {
	ID        string    `json:"id"`
	Actor     string    `json:"actor"`
	Status    string    `json:"status"`
	Diff      []string  `json:"diff"`
	DecidedBy string    `json:"decided_by,omitempty"`
	At        time.Time `json:"at"`
}

// apiTeamAgentAccessHistory handles GET /api/team/agents/{id}/access-history:
// the agent's latest access changes, newest first.
func apiTeamAgentAccessHistory(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	rows, err := globalTeam.AccessHistory(c.Context(), p.ID, team.HistoryLimit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]teamAccessHistoryItem, 0, len(rows))
	for _, r := range rows {
		var ch team.AccessChange
		_ = json.Unmarshal([]byte(r.Change), &ch)
		if ch.Diff == nil {
			ch.Diff = []string{}
		}
		out = append(out, teamAccessHistoryItem{ID: r.ID, Actor: r.Actor, Status: r.Status, Diff: ch.Diff, DecidedBy: r.DecidedBy, At: r.At})
	}
	c.JSON(http.StatusOK, map[string]any{"items": out})
}

// applyToolSettings copies the Tools & features and Skills fields of req
// onto p, answering 400 (and false) for an unknown tool or a Bash rule
// that could chain a second command.
func applyToolSettings(c *tool.Ctx, p *entity.AgentPersona, req teamAgentWriteReq) bool {
	if req.SuggestedPrompts != nil {
		prompts, err := team.NormalizeSuggestedPrompts(*req.SuggestedPrompts)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return false
		}
		p.SuggestedPrompts = team.EncodeSuggestedPrompts(prompts)
	}
	if req.AllowedNativeTools != nil {
		if err := team.ValidateNativeTools(*req.AllowedNativeTools); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return false
		}
		p.AllowedNativeTools = team.EncodeNativeTools(*req.AllowedNativeTools)
	}
	if req.BashRules != nil {
		rules := make([]team.BashRule, 0, len(*req.BashRules))
		for i, r := range *req.BashRules {
			if err := team.ValidateBashRule(r); err != nil {
				c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error(), "index": i})
				return false
			}
			rules = append(rules, team.BashRule{Pattern: strings.TrimSpace(r.Pattern), Scope: strings.TrimSpace(r.Scope)})
		}
		p.BashRules = team.EncodeBashRules(rules)
	}
	if req.DisabledSkills != nil {
		names := slices.DeleteFunc(slices.Clone(*req.DisabledSkills), skillsync.IsRequiredSkill)
		p.DisabledSkills = team.EncodeSkillNames(names)
	}
	return true
}

// apiTeamAgentSkills handles GET /api/team/agents/{id}/skills: the skills
// the agent's spawns see — its project's own, the global and the built-in
// ones — each with whether the agent has it switched off.
func apiTeamAgentSkills(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	localDir := ""
	if p.ProjectID != "" {
		if dir, err := project.ResolvePath(globalLayout, p.ProjectID); err == nil {
			localDir = skillsync.LocalSkillsDir(dir)
		}
	}
	type item struct {
		skillsync.EffectiveSkill
		Disabled bool `json:"disabled"`
	}
	off := team.DecodeSkillNames(p.DisabledSkills)
	out := []item{}
	for _, s := range skillsync.EffectiveSkills(localDir, skillsync.ListSkills()) {
		out = append(out, item{EffectiveSkill: s, Disabled: !s.Required && slices.Contains(off, s.Name)})
	}
	c.JSON(http.StatusOK, map[string]any{"items": out, "local_dir": localDir})
}

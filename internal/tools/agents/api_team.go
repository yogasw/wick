package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// globalTeam backs the Agents app (/team). nil = the app is off and
// every /api/team/agents route answers 503.
var globalTeam *team.Service

// SetTeam wires the Agents-app store.
func SetTeam(s *team.Service) { globalTeam = s }

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// TeamAgentItem is one agent as the Agents app renders it. The persona text
// (name … preset) is read from the agent's project at response time, never
// stored on the row, so it can never disagree with the project settings.
type TeamAgentItem struct {
	ID                   string                   `json:"id"`
	Handle               string                   `json:"handle"`
	IsCaptain            bool                     `json:"is_captain"`
	ProjectID            string                   `json:"project_id"`
	Name                 string                   `json:"name"`
	Icon                 string                   `json:"icon"`
	Description          string                   `json:"description"`
	SystemPrompt         string                   `json:"system_prompt"`
	Provider             string                   `json:"provider"`
	Model                string                   `json:"model"`
	Preset               string                   `json:"preset"`
	Features             team.Features         `json:"features"`
	Avatar               team.Avatar           `json:"avatar"`
	AllowedConnectors    []team.ConnectorGrant `json:"allowed_connectors"`
	IncludeNewConnectors bool                     `json:"include_new_connectors"`
	Disabled             bool                     `json:"disabled"`
	MainSessionID        string                   `json:"main_session_id"`
	LastActive           *time.Time               `json:"last_active"`
	LastPreview          string                   `json:"last_preview"`
	Status               string                   `json:"status"`
	// SharedWith counts the owner's OTHER agents on the same project, so
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
	ID          string               `json:"id"`
	Key         string               `json:"key"`
	Label       string               `json:"label"`
	Description string               `json:"description"`
	Accounts    []teamAgentAccountItem `json:"accounts"`
	Ops         []teamAgentConnectorOp `json:"ops"`
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
	Handle               *string                   `json:"handle"`
	Name                 *string                   `json:"name"`
	Icon                 *string                   `json:"icon"`
	Description          *string                   `json:"description"`
	SystemPrompt         *string                   `json:"system_prompt"`
	Provider             *string                   `json:"provider"`
	Model                *string                   `json:"model"`
	ProjectID            *string                   `json:"project_id"`
	Avatar               *team.Avatar           `json:"avatar"`
	Features             *team.Features         `json:"features"`
	AllowedConnectors    *[]team.ConnectorGrant `json:"allowed_connectors"`
	IncludeNewConnectors *bool                     `json:"include_new_connectors"`
	Disabled             *bool                     `json:"disabled"`
	IsCaptain            *bool                     `json:"is_captain"`
}

// captainSystemAddon is the starting persona of the auto-created Captain.
// Kept short: the owner is expected to rewrite it.
const captainSystemAddon = "Kamu Captain: agent utama pemilik tim ini. Bantu pemilik mengatur tim agent-nya — " +
	"siapa mengerjakan apa — dan kerjakan sendiri hal yang tidak cocok untuk agent lain."

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

// validateGrants rejects a checklist entry this build cannot interpret. A
// grant naming a connector the owner cannot see is harmless (the scope
// only narrows) and is kept, so a temporarily lost tag does not wipe it.
func validateGrants(gs []team.ConnectorGrant) error {
	for _, g := range gs {
		if strings.TrimSpace(g.ConnectorID) == "" {
			return errors.New("allowed_connectors: connector_id is required")
		}
		switch g.Level {
		case team.LevelAll, team.LevelRead, team.LevelPick:
		default:
			return errors.New("allowed_connectors: level must be all, read or pick")
		}
	}
	return nil
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
		if s.Meta.AgentID == agentID && s.Meta.UserID == ownerID {
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

// teamAgentToItem renders one row. siblings is the owner's full list, used
// for SharedWith; pass nil to skip the count.
func teamAgentToItem(p entity.AgentPersona, siblings []entity.AgentPersona) TeamAgentItem {
	it := TeamAgentItem{
		ID: p.ID, Handle: p.Handle, IsCaptain: p.IsCaptain, ProjectID: p.ProjectID,
		Name:                 p.Handle,
		Features:             team.DecodeFeatures(p.Features),
		Avatar:               team.DecodeAvatar(p.Avatar),
		AllowedConnectors:    team.DecodeGrants(p.AllowedConnectors),
		IncludeNewConnectors: p.IncludeNewConnectors,
		Disabled:             p.Disabled,
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
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		it.MainSessionID = s.ID
		it.LastActive = timePtr(s.Meta.LastActive)
		it.Status = string(s.Meta.Status)
		// The preview is left empty on purpose for now: the last message
		// lives in conversation.jsonl, and reading one file per agent on
		// every roster load is the cost the sidebar was built to avoid.
	}
	for _, o := range siblings {
		if o.ID != p.ID && o.ProjectID != "" && o.ProjectID == p.ProjectID {
			it.SharedWith++
		}
	}
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

// createTeamAgentProject makes the project a new agent's persona lives in.
func createTeamAgentProject(c *tool.Ctx, name, icon, description, systemPrompt, provider, model string) (string, error) {
	opt := project.CreateOptions{
		ID:          uuid.New().String(),
		Name:        name,
		Icon:        strings.TrimSpace(icon),
		Description: description,
		OwnerUserID: actorID(c),
		Defaults: project.Defaults{
			Provider:    strings.TrimSpace(provider),
			Model:       modelWithProvider(provider, model),
			SystemAddon: systemPrompt,
		},
	}
	if _, err := globalMgr.CreateProject(c.Context(), opt); err != nil {
		return "", err
	}
	if globalTagsSvc != nil {
		_ = globalTagsSvc.CreateResourceOwnerTag(c.Context(), opt.ID, actorID(c))
	}
	return opt.ID, nil
}

// ensureCaptain creates the owner's Captain when they have no agent yet,
// so the app never opens on an empty roster.
func ensureCaptain(c *tool.Ctx) ([]entity.AgentPersona, error) {
	owner := actorID(c)
	rows, err := globalTeam.List(c.Context(), owner)
	if err != nil || len(rows) > 0 {
		return rows, err
	}
	pid, err := createTeamAgentProject(c, "Captain", "🧭", "Agent utama yang membantu mengatur tim agent.", captainSystemAddon, "", "")
	if err != nil {
		return nil, err
	}
	p := &entity.AgentPersona{
		OwnerUserID: owner, Handle: "captain", ProjectID: pid, IsCaptain: true,
		AllowedConnectors: "[]",
		Features:          team.EncodeFeatures(team.DefaultFeatures()),
		Avatar:            team.EncodeAvatar(team.Avatar{Shape: "squircle", Color: "#f59e0b"}),
	}
	if err := globalTeam.Create(c.Context(), p); err != nil && !errors.Is(err, team.ErrHandleTaken) {
		// ErrHandleTaken = a concurrent first load won the race; its
		// Captain is the one to show. The project made here is left as
		// an ordinary empty project rather than deleted under a request
		// that may be using it.
		return nil, err
	}
	return globalTeam.List(c.Context(), owner)
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// apiTeamAgentList handles GET /api/team/agents.
func apiTeamAgentList(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	rows, err := ensureCaptain(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]TeamAgentItem, 0, len(rows))
	captainID := ""
	for _, r := range rows {
		items = append(items, teamAgentToItem(r, rows))
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
	if err := team.ValidateHandle(handle); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(str(req.Name))
	if name == "" {
		name = handle
	}
	pid := strings.TrimSpace(str(req.ProjectID))
	if pid != "" {
		if !requireUsableProject(c, pid) {
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
		pid, err = createTeamAgentProject(c, name, str(req.Icon), str(req.Description), str(req.SystemPrompt), str(req.Provider), str(req.Model))
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	feats := team.DefaultFeatures()
	if req.Features != nil {
		feats = *req.Features
	}
	av := team.DefaultAvatar()
	if req.Avatar != nil {
		av = *req.Avatar
	}
	p := &entity.AgentPersona{
		OwnerUserID: actorID(c), Handle: handle, ProjectID: pid,
		AllowedConnectors: "[]",
		Features:          team.EncodeFeatures(feats),
		Avatar:            team.EncodeAvatar(av),
	}
	if err := globalTeam.Create(c.Context(), p); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	rows, _ := globalTeam.List(c.Context(), actorID(c))
	c.JSON(http.StatusOK, teamAgentToItem(*p, rows))
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
	if req.Handle != nil {
		p.Handle = team.NormalizeHandle(*req.Handle)
		if err := team.ValidateHandle(p.Handle); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.AllowedConnectors != nil {
		if err := validateGrants(*req.AllowedConnectors); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		p.AllowedConnectors = team.EncodeGrants(*req.AllowedConnectors)
	}
	if req.IncludeNewConnectors != nil {
		p.IncludeNewConnectors = *req.IncludeNewConnectors
	}
	if req.Disabled != nil {
		p.Disabled = *req.Disabled
	}
	if req.Features != nil {
		p.Features = team.EncodeFeatures(*req.Features)
	}
	if req.Avatar != nil {
		p.Avatar = team.EncodeAvatar(*req.Avatar)
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
	if err := globalTeam.Update(c.Context(), &p); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	rows, _ := globalTeam.List(c.Context(), actorID(c))
	c.JSON(http.StatusOK, teamAgentToItem(p, rows))
}

// apiTeamAgentDelete handles DELETE /api/team/agents/{id}. Only the row goes;
// its project and conversations stay as the owner's ordinary work.
func apiTeamAgentDelete(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	if p.IsCaptain {
		rows, err := globalTeam.List(c.Context(), actorID(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if len(rows) > 1 {
			c.JSON(http.StatusConflict, map[string]string{"error": "make another agent Captain before deleting this one"})
			return
		}
	}
	if err := globalTeam.Delete(c.Context(), p.ID); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// apiTeamAgentConnectors handles GET /api/team/agents/connectors: the checklist
// source, i.e. what the OWNER reaches. The request context carries no agent
// scope (it is a browser call), so this is the unnarrowed list.
func apiTeamAgentConnectors(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	out := make([]teamAgentConnectorItem, 0)
	if globalConnectors == nil {
		c.JSON(http.StatusOK, out)
		return
	}
	u := login.GetUser(c.Context())
	isAdmin := u != nil && u.IsAdmin()
	tagIDs := login.GetUserTagIDs(c.Context())
	rows, err := globalConnectors.ListVisibleTo(c.Context(), actorID(c), tagIDs, isAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, row := range rows {
		mod, ok := globalConnectors.Module(row.Key)
		if !ok {
			continue
		}
		it := teamAgentConnectorItem{
			ID: row.ID, Key: row.Key, Label: row.Label,
			Description: strings.TrimSpace(row.Description),
			Accounts:    []teamAgentAccountItem{},
			Ops:         []teamAgentConnectorOp{},
		}
		if it.Description == "" {
			it.Description = mod.Meta.Description
		}
		if mod.OAuth != nil {
			// The instance's own identity is one more pickable account.
			it.Accounts = append(it.Accounts, teamAgentAccountItem{ID: "", DisplayName: "bot / instance"})
			caller := globalConnectors.AccountAccessFor(row, actorID(c), isAdmin, tagIDs)
			if accs, aerr := globalConnectors.ListAccountsVisibleTo(c.Context(), row, caller); aerr == nil {
				for _, a := range accs {
					it.Accounts = append(it.Accounts, teamAgentAccountItem{ID: a.ID, DisplayName: a.DisplayName})
				}
			} else {
				log.Ctx(c.Context()).Warn().Err(aerr).Str("connector", row.ID).Msg("team agent checklist: list accounts")
			}
		}
		for _, op := range mod.AllOps() {
			if op.ConfigOnly {
				continue
			}
			it.Ops = append(it.Ops, teamAgentConnectorOp{Key: op.Key, Name: op.Name, Destructive: op.Destructive})
		}
		out = append(out, it)
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

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/a2aserver"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// globalA2A is the A2A channel, for the connection test. Set by the server.
var globalA2A *a2aserver.Server

// SetA2AServer is called once by the server after it registers the channel.
func SetA2AServer(s *a2aserver.Server) { globalA2A = s }

// maxA2ACallers bounds allowed_callers; it is a short allowlist, not a
// directory.
const maxA2ACallers = 50

// A2ADirectory is the Team side of the A2A server: agents and their
// sessions.
func A2ADirectory() a2aserver.Directory { return a2aDirectory{} }

type a2aDirectory struct{}

// a2aSessionMu serialises session creation: two first messages of one
// contextId must not both try to create it.
var a2aSessionMu sync.Mutex

func (a2aDirectory) Agent(ctx context.Context, id string) (a2aserver.Agent, bool) {
	if globalTeam == nil || id == "" {
		return a2aserver.Agent{}, false
	}
	p, err := globalTeam.Get(ctx, id)
	if err != nil {
		return a2aserver.Agent{}, false
	}
	return a2aAgentOf(p), true
}

// a2aAgentOf is p as the A2A server sees it: named and described the way
// its Slack app manifest is.
func a2aAgentOf(p entity.AgentPersona) a2aserver.Agent {
	a := a2aserver.Agent{
		ID: p.ID, OwnerUserID: p.OwnerUserID, ProjectID: p.ProjectID, Name: p.Handle, Disabled: p.Disabled,
	}
	if p.ProjectID != "" && globalMgr != nil {
		if proj, ok := globalMgr.Registry().Project(p.ProjectID); ok {
			if proj.Meta.Name != "" {
				a.Name = proj.Meta.Name
			}
			a.Description = proj.Meta.Description
		}
	}
	if p.Tagline != "" {
		a.Description = strings.TrimSuffix(strings.TrimSpace(p.Tagline+" — "+a.Description), " —")
	}
	for _, sp := range team.DecodeSuggestedPrompts(p.SuggestedPrompts) {
		a.Prompts = append(a.Prompts, a2aserver.Prompt{Title: sp.Title, Message: sp.Message})
	}
	return a
}

// EnsureSession creates an A2A context's session the way the Agents app
// creates a side chat: owned by the agent's owner, bound to the agent, in
// its project — origin "a2a".
func (a2aDirectory) EnsureSession(ctx context.Context, a a2aserver.Agent, sessionID string) (bool, error) {
	if globalMgr == nil {
		return false, errors.New("agents manager not ready")
	}
	a2aSessionMu.Lock()
	defer a2aSessionMu.Unlock()
	if _, ok := globalMgr.Registry().Session(sessionID); ok {
		return false, nil
	}
	projectID, preset := a.ProjectID, "default"
	if projectID != "" {
		if proj, ok := globalMgr.Registry().Project(projectID); !ok {
			projectID = ""
		} else if proj.Meta.Defaults.Preset != "" {
			preset = proj.Meta.Defaults.Preset
		}
	}
	prov, modelID := a2aSessionTarget(ctx, projectID)
	// A remote agent exposed over A2A answers through its adapter, as its
	// own chat does: the call goes on to the remote, not to a local CLI.
	if globalTeam != nil {
		if p, err := globalTeam.Get(ctx, a.ID); err == nil {
			if key, ok := remoteProviderKey(p); ok {
				prov, modelID = key, ""
			}
		}
	}
	if _, err := globalMgr.CreateSession(ctx, session.CreateOptions{
		ID: sessionID, ProjectID: projectID, Origin: session.Origin(a2aserver.Source),
		Preset: preset, UserID: a.OwnerUserID, AgentID: a.ID,
	}); err != nil {
		return false, err
	}
	if err := globalMgr.AddAgent(sessionID, "main", prov); err != nil {
		return true, err
	}
	if modelID != "" {
		if err := session.SetModelID(globalLayout, sessionID, "main", modelID); err != nil {
			return true, fmt.Errorf("set model: %w", err)
		}
	}
	return true, nil
}

// a2aSessionTarget is resolveSessionTarget without a request: the
// project's default provider, else the first installed one.
func a2aSessionTarget(ctx context.Context, projectID string) (string, string) {
	if projectID != "" {
		if p, err := project.Load(globalLayout, projectID); err == nil {
			if prov := strings.TrimSpace(p.Meta.Defaults.Provider); prov != "" {
				return normalizeProviderKey(prov), strings.TrimSpace(p.Meta.Defaults.Model)
			}
		}
	}
	if ps := providerChoicesCached(ctx); len(ps) > 0 {
		return normalizeProviderKey(ps[0].Type + "/" + ps[0].Name), ""
	}
	return normalizeProviderKey("claude"), ""
}

// AgentA2AStatus is GET /api/team/agents/{id}/a2a. APIKey is set only in
// the response that minted the key; nothing else ever carries it.
type AgentA2AStatus struct {
	Enabled        bool       `json:"enabled"`
	PublicCard     bool       `json:"public_card"`
	AllowedCallers []string   `json:"allowed_callers"`
	KeySet         bool       `json:"key_set"`
	KeyHint        string     `json:"key_hint,omitempty"`
	RotatedAt      *time.Time `json:"rotated_at,omitempty"`
	EndpointURL    string     `json:"endpoint_url"`
	CardURL        string     `json:"card_url"`
	APIKey         string     `json:"api_key,omitempty"`
}

func a2aStore() *a2aserver.Store { return a2aserver.NewStore(globalDB) }

func a2aStatusOf(agentID string, c a2aserver.Connection) AgentA2AStatus {
	base := ""
	if globalConfigs != nil {
		base = strings.TrimRight(globalConfigs.AppURL(), "/")
	}
	callers := c.AllowedCallers
	if callers == nil {
		callers = []string{}
	}
	return AgentA2AStatus{
		Enabled: c.Enabled, PublicCard: c.PublicCard, AllowedCallers: callers,
		KeySet: c.HasKey(), KeyHint: c.KeyHint, RotatedAt: c.RotatedAt,
		EndpointURL: base + a2aserver.EndpointPath(agentID), CardURL: base + a2aserver.CardPath(agentID),
	}
}

// loadA2AConn returns p's connection, a fresh disabled one when it has none.
func loadA2AConn(c *tool.Ctx, p entity.AgentPersona) (a2aserver.Connection, bool) {
	if globalDB == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "database not ready"})
		return a2aserver.Connection{}, false
	}
	conn, found, err := a2aStore().Load(p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return a2aserver.Connection{}, false
	}
	if !found {
		conn = a2aserver.Connection{AgentID: p.ID, OwnerUserID: p.OwnerUserID}
	}
	return conn, true
}

// mintA2AKey puts a new key on conn and returns its plaintext.
func mintA2AKey(conn *a2aserver.Connection) (string, error) {
	plain, hash, hint, err := a2aserver.NewKey()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	conn.KeyHash, conn.KeyHint, conn.RotatedAt = hash, hint, &now
	return plain, nil
}

func saveA2A(c *tool.Ctx, p entity.AgentPersona, conn a2aserver.Connection, plain string) {
	if err := a2aStore().Save(conn); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	st := a2aStatusOf(p.ID, conn)
	st.APIKey = plain
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentA2AGet handles GET /api/team/agents/{id}/a2a.
func apiTeamAgentA2AGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	conn, ok := loadA2AConn(c, p)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, a2aStatusOf(p.ID, conn))
}

// apiTeamAgentA2APut handles PUT /api/team/agents/{id}/a2a: a partial
// update, one field per autosave. The first Enable mints the key and is
// the one response that shows it.
func apiTeamAgentA2APut(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var body struct {
		Enabled        *bool     `json:"enabled"`
		PublicCard     *bool     `json:"public_card"`
		AllowedCallers *[]string `json:"allowed_callers"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 64<<10)).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	conn, ok := loadA2AConn(c, p)
	if !ok {
		return
	}
	if body.Enabled != nil {
		conn.Enabled = *body.Enabled
	}
	if body.PublicCard != nil {
		conn.PublicCard = *body.PublicCard
	}
	if body.AllowedCallers != nil {
		callers, err := cleanA2ACallers(*body.AllowedCallers)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		conn.AllowedCallers = callers
	}
	plain := ""
	if conn.Enabled && !conn.HasKey() {
		var err error
		if plain, err = mintA2AKey(&conn); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	saveA2A(c, p, conn, plain)
}

// cleanA2ACallers trims and dedupes user ids.
func cleanA2ACallers(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > maxA2ACallers {
		return nil, fmt.Errorf("at most %d allowed callers", maxA2ACallers)
	}
	return out, nil
}

// apiTeamAgentA2ARotate handles POST /api/team/agents/{id}/a2a/rotate: a
// new key replaces the old one at once.
func apiTeamAgentA2ARotate(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	conn, ok := loadA2AConn(c, p)
	if !ok {
		return
	}
	plain, err := mintA2AKey(&conn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	saveA2A(c, p, conn, plain)
}

// apiTeamAgentA2ARevoke handles POST /api/team/agents/{id}/a2a/revoke: the
// key stops working; PATs of the owner and allowed callers still do.
func apiTeamAgentA2ARevoke(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	conn, ok := loadA2AConn(c, p)
	if !ok {
		return
	}
	conn.KeyHash, conn.KeyHint, conn.RotatedAt = "", "", nil
	saveA2A(c, p, conn, "")
}

// apiTeamAgentA2ATest handles POST /api/team/agents/{id}/a2a/test: the
// agent card plus a "ping" through an A2A client, served in-process.
func apiTeamAgentA2ATest(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	if globalA2A == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "A2A server not running"})
		return
	}
	c.JSON(http.StatusOK, globalA2A.Probe(c.Context(), p.ID))
}

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// slackRemoteAPIOverride stands in for the Slack Web API in tests; the
// token is passed so a test can assert which identity was resolved.
var slackRemoteAPIOverride func(token string) slackremote.API

func slackRemoteStore() *slackremote.Store {
	if globalDB == nil {
		return nil
	}
	return slackremote.NewStore(globalDB)
}

// isSlackRemote reports whether p is a Slack remote agent.
func isSlackRemote(p entity.AgentPersona) bool { return p.Kind == slackremote.Kind }

// SlackRemoteInfo is a Slack remote agent's settings as the UI reads them.
// It never carries a token.
type SlackRemoteInfo struct {
	slackremote.Config
	Marker     bool               `json:"marker"`
	Mention    bool               `json:"mention_target"`
	IdleSecEff int                `json:"idle_sec_effective"`
	MaxSecEff  int                `json:"max_sec_effective"`
	ListenEff  string             `json:"listen_effective"`
	UsageEff   string             `json:"usage_effective"`
	Warning    string             `json:"warning"`
	Session    *slackremote.State `json:"session,omitempty"`
}

func slackRemoteInfoOf(c slackremote.Config) *SlackRemoteInfo {
	d, _ := slackremote.NewSource(c, slackremote.Deps{}).Describe(context.Background())
	return &SlackRemoteInfo{
		Config: c, Marker: c.MarkerOn(), Mention: c.MentionOn(), IdleSecEff: int(c.Idle().Seconds()), MaxSecEff: int(c.Max().Seconds()),
		ListenEff: c.EffectiveListen(), UsageEff: c.EffectiveUsage(), Warning: d.Warning,
	}
}

func slackRemoteInfoFor(p entity.AgentPersona) *SlackRemoteInfo {
	if !isSlackRemote(p) || slackRemoteStore() == nil {
		return nil
	}
	c, ok, err := slackRemoteStore().Load(p.ID)
	if err != nil || !ok {
		return nil
	}
	return slackRemoteInfoOf(c)
}

// checkSlackRemoteAccess enforces who may use what: the connector must be
// visible to the owner, and identity user only with an account the owner
// connected themselves — refused here, server side, whatever the form sent.
func checkSlackRemoteAccess(ctx context.Context, owner string, isAdmin bool, cfg slackremote.Config) error {
	if globalConnectors == nil {
		return errors.New("connectors are not ready")
	}
	row, err := globalConnectors.Get(ctx, cfg.ConnectorID)
	if err != nil || row.Key != "slack" {
		return errors.New("Slack connector not found")
	}
	var tagIDs []string
	if globalAuth != nil {
		tagIDs = globalAuth.GetUserFilterTagIDs(ctx, owner)
	}
	if ok, err := globalConnectors.IsVisibleTo(ctx, cfg.ConnectorID, owner, tagIDs, isAdmin); err != nil || !ok {
		return errors.New("Slack connector not found")
	}
	if cfg.Identity == slackremote.IdentityUser {
		acct, err := globalConnectors.GetAccount(ctx, cfg.AccountID)
		if err != nil || acct.ConnectorID != cfg.ConnectorID {
			return slackremote.ErrNotOwnAccount
		}
		return slackremote.CheckAccountOwner(acct.WickUserID, owner)
	}
	return nil
}

// slackRemoteSource builds the adapter for cfg on behalf of owner,
// resolving the token of its identity.
func slackRemoteSource(ctx context.Context, owner string, cfg slackremote.Config) (*slackremote.Source, error) {
	if globalConnectors == nil {
		return nil, errors.New("connectors are not ready")
	}
	token, err := globalConnectors.ResolveToken(ctx, cfg.ConnectorID, cfg.AccountID, owner)
	if err != nil {
		return nil, err
	}
	var api slackremote.API = slackremote.HTTPAPI{Token: token}
	if slackRemoteAPIOverride != nil {
		api = slackRemoteAPIOverride(token)
	}
	return slackremote.NewSource(cfg, slackremote.Deps{API: api}), nil
}

// slackDirectory caches workspace listings for the target search.
var slackDirectory = slackremote.NewDirectory()

// slackDirectoryOverride replaces the Slack Web API in tests.
var slackDirectoryOverride func(token string) slackremote.DirectoryAPI

// apiTeamSlackRemoteDirectory handles GET /api/team/slack-remote/directory
// ?connector_id=&identity=&account_id=&kind=users|channels&q=: the users,
// bots or channels whose names contain q, for a person to pick a target
// from. Only the picked id is ever stored; nothing here matches a person
// by name. A token without the read scope answers 200 with error and
// missing_scope so the form falls back to typing the id.
func apiTeamSlackRemoteDirectory(c *tool.Ctx) {
	if !slackRemoteReady(c) {
		return
	}
	cfg := slackremote.Config{ConnectorID: c.Query("connector_id"), Identity: c.Query("identity"), AccountID: c.Query("account_id")}
	if cfg.Identity != slackremote.IdentityUser {
		cfg.Identity, cfg.AccountID = slackremote.IdentityBot, ""
	}
	owner := actorID(c)
	if err := checkSlackRemoteAccess(c.Context(), owner, isAdminCtx(c), cfg); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	token, err := globalConnectors.ResolveToken(c.Context(), cfg.ConnectorID, cfg.AccountID, owner)
	if err != nil {
		c.JSON(http.StatusOK, map[string]any{"entries": []slackremote.DirEntry{}, "error": err.Error()})
		return
	}
	var api slackremote.DirectoryAPI = slackremote.HTTPAPI{Token: token}
	if slackDirectoryOverride != nil {
		api = slackDirectoryOverride(token)
	}
	key := cfg.ConnectorID + "|" + cfg.Identity + "|" + cfg.AccountID
	entries, err := slackDirectory.Search(c.Context(), api, key, c.Query("kind"), c.Query("q"))
	if err != nil {
		var ms *slackremote.MissingScopeError
		if errors.As(err, &ms) {
			c.JSON(http.StatusOK, map[string]any{"entries": []slackremote.DirEntry{}, "error": err.Error(), "missing_scope": true})
			return
		}
		if c.Query("kind") != slackremote.DirUsers && c.Query("kind") != slackremote.DirChannels {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, map[string]any{"entries": []slackremote.DirEntry{}, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"entries": entries})
}

func isAdminCtx(c *tool.Ctx) bool {
	u := login.GetUser(c.Context())
	return u != nil && u.IsAdmin()
}

func slackRemoteReady(c *tool.Ctx) bool {
	if !teamReady(c) {
		return false
	}
	if slackRemoteStore() == nil || globalConnectors == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "storage is not ready"})
		return false
	}
	return true
}

// loadOwnSlackRemote is loadOwnTeamAgent plus the Slack settings.
func loadOwnSlackRemote(c *tool.Ctx) (entity.AgentPersona, slackremote.Config, bool) {
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return p, slackremote.Config{}, false
	}
	cfg, found, err := slackRemoteStore().Load(p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return p, cfg, false
	}
	if !isSlackRemote(p) || !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "not a Slack remote agent"})
		return p, cfg, false
	}
	return p, cfg, true
}

// slackTestReq tests either a stored agent or a config not saved yet.
type slackTestReq struct {
	AgentID string `json:"agent_id"`
	slackremote.Config
}

// apiTeamSlackRemoteTest handles POST /api/team/slack-remote/test: posts
// "ping" to the target and waits for the first reply. Always 200 with a
// remote.TestResult, except a refused config (400/404).
func apiTeamSlackRemoteTest(c *tool.Ctx) {
	if !slackRemoteReady(c) {
		return
	}
	var req slackTestReq
	if !decodeRemoteReq(c, &req) {
		return
	}
	owner := actorID(c)
	cfg := req.Config
	if req.AgentID != "" {
		p, err := globalTeam.Get(c.Context(), req.AgentID)
		if err != nil || p.OwnerUserID != owner || !isSlackRemote(p) {
			c.JSON(http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		stored, ok, err := slackRemoteStore().Load(p.ID)
		if err != nil || !ok {
			c.JSON(http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		cfg = stored
	}
	if err := cfg.Normalize(); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := checkSlackRemoteAccess(c.Context(), owner, isAdminCtx(c), cfg); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	src, err := slackRemoteSource(c.Context(), owner, cfg)
	if err != nil {
		c.JSON(http.StatusOK, remote.TestResult{State: "auth_failed", Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, src.Test(c.Context()))
}

type slackCreateReq struct {
	slackremote.Config
	Name    string       `json:"name"`
	Handle  string       `json:"handle"`
	Tagline *string      `json:"tagline"`
	Avatar  *team.Avatar `json:"avatar"`
}

// apiTeamSlackRemoteCreate handles POST /api/team/slack-remote.
func apiTeamSlackRemoteCreate(c *tool.Ctx) {
	if !slackRemoteReady(c) {
		return
	}
	var req slackCreateReq
	if !decodeRemoteReq(c, &req) {
		return
	}
	cfg := req.Config
	if err := cfg.Normalize(); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	owner := actorID(c)
	if err := checkSlackRemoteAccess(c.Context(), owner, isAdminCtx(c), cfg); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	tagline, ok := validTagline(c, req.Tagline, "")
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimLeft(firstNonBlank(cfg.TargetName, cfg.Channel, cfg.User), "#@")
	}
	var handle string
	var err error
	if typed := team.NormalizeHandle(req.Handle); typed != "" {
		if err := validateTeamHandle(c.Context(), typed); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if _, err := globalTeam.GetByHandle(c.Context(), owner, typed); err == nil {
			c.JSON(http.StatusConflict, map[string]string{"error": team.ErrHandleTaken.Error()})
			return
		}
		handle = typed
	} else if handle, err = freeHandle(c.Context(), owner, handleFromName(name)); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	pid, err := createAgentProjectFor(c.Context(), owner, name, "💬", "Slack remote agent.", "", slackremote.ProviderKey, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if proj, ok := globalMgr.Registry().Project(pid); ok {
		meta := proj.Meta
		meta.Tags = append(meta.Tags, slackremote.ProjectTag)
		if _, err := globalMgr.UpdateProject(c.Context(), pid, meta); err != nil {
			log.Ctx(c.Context()).Warn().Err(err).Str("project", pid).Msg("team: tag slack remote agent project")
		}
	}
	av := team.DefaultAvatarFor(handle)
	if req.Avatar != nil {
		av = *req.Avatar
	}
	p := &entity.AgentPersona{
		OwnerUserID: owner, Handle: handle, ProjectID: pid, Kind: slackremote.Kind, Tagline: tagline,
		MentionFrom:       remoteMentionDefault(cfg.Usage),
		AllowedConnectors: "[]", AllowedNativeTools: "[]",
		Features: team.EncodeFeatures(team.Features{}),
		Avatar:   team.EncodeAvatar(av),
	}
	if err := globalTeam.Create(c.Context(), p); err != nil {
		discardTeamAgentProject(c, pid)
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	cfg.AgentID, cfg.OwnerUserID = p.ID, owner
	cfg.Usage = slackremote.UsageByMention
	if err := slackRemoteStore().Save(cfg); err != nil {
		_ = globalTeam.Delete(c.Context(), p.ID)
		discardTeamAgentProject(c, pid)
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	announceAgentCreated(c, *p, name, "slack-remote")
	users := teamProjectUsersFor(c.Context(), []entity.AgentPersona{*p})
	c.JSON(http.StatusOK, teamAgentToItem(*p, users, teamLiveNow(), ownerReach(c)))
}

func firstNonBlank(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// apiTeamSlackRemoteGet handles GET /api/team/agents/{id}/slack-remote
// [?session_id=], adding that chat's Slack thread.
func apiTeamSlackRemoteGet(c *tool.Ctx) {
	if !slackRemoteReady(c) {
		return
	}
	p, cfg, ok := loadOwnSlackRemote(c)
	if !ok {
		return
	}
	info := slackRemoteInfoOf(cfg)
	if sid := c.Query("session_id"); sid != "" {
		s, found := globalMgr.Registry().Session(sid)
		if !found || s.Meta.AgentID != p.ID {
			c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		st := slackremote.LoadState(globalLayout.SessionDir(sid))
		info.Session = &st
	}
	c.JSON(http.StatusOK, info)
}

// apiTeamSlackRemoteUpdate handles PATCH /api/team/agents/{id}/slack-remote:
// the fields sent replace the stored ones, the rest stay; the result is
// checked as on create.
func apiTeamSlackRemoteUpdate(c *tool.Ctx) {
	if !slackRemoteReady(c) {
		return
	}
	p, cfg, ok := loadOwnSlackRemote(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.R.Body, 1<<16))
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	usage := cfg.Usage
	if len(body) > 0 {
		if err := json.Unmarshal(body, &cfg); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
	}
	// Who may use it lives in the mention policy now: an older client's
	// usage never takes the agent back from it.
	if usage == slackremote.UsageByMention {
		cfg.Usage = usage
	}
	cfg.AgentID, cfg.OwnerUserID = p.ID, p.OwnerUserID
	if err := cfg.Normalize(); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := checkSlackRemoteAccess(c.Context(), p.OwnerUserID, isAdminCtx(c), cfg); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := slackRemoteStore().Save(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, slackRemoteInfoOf(cfg))
}

// apiTeamSlackRemoteIdentities handles GET
// /api/team/slack-remote/identities?connector_id=: the bot, and the
// caller's OWN connected accounts — never anyone else's.
func apiTeamSlackRemoteIdentities(c *tool.Ctx) {
	if !slackRemoteReady(c) {
		return
	}
	owner := actorID(c)
	cfg := slackremote.Config{ConnectorID: strings.TrimSpace(c.Query("connector_id")), Identity: slackremote.IdentityBot}
	if err := checkSlackRemoteAccess(c.Context(), owner, isAdminCtx(c), cfg); err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	accs, err := globalConnectors.ListAccounts(c.Context(), cfg.ConnectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type acc struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	mine := []acc{}
	for _, a := range accs {
		if a.WickUserID == owner && owner != "" {
			mine = append(mine, acc{ID: a.ID, DisplayName: a.DisplayName})
		}
	}
	c.JSON(http.StatusOK, map[string]any{"bot": true, "accounts": mine})
}

// slackRemoteSpawner is RemoteSpawnerFor's answer for a Slack remote
// agent. The token is resolved per spawn and kept in the adapter only.
func slackRemoteSpawner(p entity.AgentPersona) provider.Spawner {
	if slackRemoteStore() == nil {
		return remoteFailSpawner{msg: "Storage is not ready."}
	}
	cfg, found, err := slackRemoteStore().Load(p.ID)
	if err != nil || !found {
		return remoteFailSpawner{msg: "This Slack remote agent has no settings; add it again."}
	}
	if cfg.Identity == slackremote.IdentityUser {
		acct, err := globalConnectors.GetAccount(context.Background(), cfg.AccountID)
		if err != nil || slackremote.CheckAccountOwner(acct.WickUserID, p.OwnerUserID) != nil {
			return remoteFailSpawner{msg: slackremote.ErrNotOwnAccount.Error()}
		}
	}
	src, err := slackRemoteSource(context.Background(), p.OwnerUserID, cfg)
	if err != nil {
		return remoteFailSpawner{msg: "Could not use the Slack connector: " + err.Error()}
	}
	return remote.Spawner{Source: src}
}

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// remoteCodecOverride and remoteAllowedHostsOverride stand in for the
// configs service in tests.
var (
	remoteCodecOverride        a2aremote.Codec
	remoteAllowedHostsOverride func() string
)

// remoteCodec encrypts remote auth secrets — the configs codec connector
// secrets use.
func remoteCodec() a2aremote.Codec {
	if remoteCodecOverride != nil {
		return remoteCodecOverride
	}
	if globalConfigs == nil {
		return nil
	}
	return globalConfigs
}

// remoteGuard is the host policy, read per call so an admin edit applies
// to the next request.
func remoteGuard() a2aremote.Guard {
	raw := ""
	if remoteAllowedHostsOverride != nil {
		raw = remoteAllowedHostsOverride()
	} else if globalConfigs != nil {
		raw = globalConfigs.GetOwned("agents", "a2a_remote_allowed_hosts")
	}
	return a2aremote.Guard{Allowed: a2aremote.ParseAllowlist(raw)}
}

func remoteStore() *a2aremote.Store {
	if globalDB == nil {
		return nil
	}
	return a2aremote.NewStore(globalDB)
}

// IsRemoteAgent reports whether p is a remote agent of any source (A2A,
// Slack): no local process, its brain lives elsewhere.
func IsRemoteAgent(p entity.AgentPersona) bool { return isA2ARemote(p) || isSlackRemote(p) }

// isA2ARemote reports whether p is an A2A remote agent.
func isA2ARemote(p entity.AgentPersona) bool { return p.Kind == a2aremote.Kind }

// remoteProviderKey is the provider a session of remote agent p is
// created with; false for a local agent. The pool runs such a session on
// RemoteSpawnerFor, never a local CLI.
func remoteProviderKey(p entity.AgentPersona) (string, bool) {
	switch {
	case isSlackRemote(p):
		return slackremote.ProviderKey, true
	case isA2ARemote(p):
		return a2aremote.ProviderKey, true
	}
	return "", false
}

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// remoteAuthReq is auth as a form sends it. The secret is write-only.
type remoteAuthReq struct {
	Type   string `json:"type"`
	Header string `json:"header"`
	Secret string `json:"secret"`
}

func (a *remoteAuthReq) plain() (a2aremote.PlainAuth, error) {
	if a == nil {
		return a2aremote.PlainAuth{Type: a2aremote.AuthNone}, nil
	}
	return a2aremote.NormalizeAuth(a2aremote.PlainAuth{Type: a.Type, Header: a.Header, Secret: a.Secret})
}

// RemoteAgentInfo is a remote agent's settings as the UI reads them. The
// secret itself is never in it: AuthSet says one is stored.
type RemoteAgentInfo struct {
	CardURL          string         `json:"card_url"`
	Host             string         `json:"host"`
	Card             a2aremote.Card `json:"card"`
	AuthType         string         `json:"auth_type"`
	AuthHeader       string         `json:"auth_header,omitempty"`
	AuthSet          bool           `json:"auth_set"`
	TimeoutSec       int            `json:"timeout_sec"`
	MaxResponseBytes int64          `json:"max_response_bytes"`
	Usage            string         `json:"usage"`
	RefreshedAt      time.Time      `json:"refreshed_at"`
	// Session is set only when GET names a session_id.
	Session *a2aremote.State `json:"session,omitempty"`
}

func remoteInfoOf(c a2aremote.Config) *RemoteAgentInfo {
	info := &RemoteAgentInfo{
		CardURL: c.CardURL, Host: hostOf(c.CardURL), Card: c.Card,
		AuthType: c.Auth.Type, AuthSet: c.Auth.Set(),
		TimeoutSec: int(c.Timeout() / time.Second), MaxResponseBytes: c.MaxBytes(),
		Usage: c.EffectiveUsage(), RefreshedAt: c.RefreshedAt,
	}
	if info.AuthType == "" {
		info.AuthType = a2aremote.AuthNone
	}
	if c.Auth.Type == a2aremote.AuthAPIKey {
		info.AuthHeader = c.Auth.Header
	}
	if info.Card.Skills == nil {
		info.Card.Skills = []a2aremote.Skill{}
	}
	return info
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

// remoteInfoFor loads p's remote settings for TeamAgentItem; nil when p is
// local or the row is missing.
func remoteInfoFor(p entity.AgentPersona) *RemoteAgentInfo {
	if !isA2ARemote(p) || remoteStore() == nil {
		return nil
	}
	c, ok, err := remoteStore().Load(p.ID)
	if err != nil || !ok {
		return nil
	}
	return remoteInfoOf(c)
}

/* ── helpers ─────────────────────────────────────────────────────────────── */

func decodeRemoteReq(c *tool.Ctx, v any) bool {
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return false
	}
	return true
}

func remoteReady(c *tool.Ctx) bool {
	if !teamReady(c) {
		return false
	}
	if remoteStore() == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "storage is not ready"})
		return false
	}
	return true
}

// loadOwnRemoteAgent is loadOwnTeamAgent plus the remote settings; a
// local agent answers 404 here.
func loadOwnRemoteAgent(c *tool.Ctx) (entity.AgentPersona, a2aremote.Config, bool) {
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return p, a2aremote.Config{}, false
	}
	cfg, found, err := remoteStore().Load(p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return p, cfg, false
	}
	if !isA2ARemote(p) || !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "not an A2A remote agent"})
		return p, cfg, false
	}
	return p, cfg, true
}

var nonHandleRe = regexp.MustCompile(`[^a-z0-9]+`)

// handleFromName makes a handle out of a card name: "Research Agent" →
// "research-agent".
func handleFromName(name string) string {
	h := strings.Trim(nonHandleRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(h) > 24 {
		h = strings.TrimRight(h[:24], "-")
	}
	if len(h) < 2 {
		h = "remote-agent"
	}
	if team.ValidateHandle(h) != nil {
		h = "remote-agent"
	}
	return h
}

// freeHandle is base, or base-2, base-3 … when the owner already uses it.
func freeHandle(ctx context.Context, owner, base string) (string, error) {
	for i := 1; i <= 50; i++ {
		h := base
		if i > 1 {
			suffix := fmt.Sprintf("-%d", i)
			if len(h)+len(suffix) > 31 {
				h = strings.TrimRight(h[:31-len(suffix)], "-")
			}
			h += suffix
		}
		if team.ValidateHandle(h) != nil {
			continue
		}
		if _, err := globalTeam.GetByHandle(ctx, owner, h); errors.Is(err, team.ErrNotFound) {
			return h, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", team.ErrHandleTaken
}

// resolveStatus maps a resolve failure to a status: a refused host is the
// caller's input, anything else the remote's fault.
func resolveStatus(err error) int {
	if errors.Is(err, a2aremote.ErrBlockedHost) || strings.Contains(err.Error(), "URL") {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// remoteResolveResp is the wizard's card preview.
type remoteResolveResp struct {
	CardURL         string         `json:"card_url"`
	Host            string         `json:"host"`
	Card            a2aremote.Card `json:"card"`
	SuggestedHandle string         `json:"suggested_handle"`
}

// apiTeamRemoteResolve handles POST /api/team/a2a-remote/resolve
// {url, auth?}: fetch and preview an agent card.
func apiTeamRemoteResolve(c *tool.Ctx) {
	if !remoteReady(c) {
		return
	}
	var req struct {
		URL  string         `json:"url"`
		Auth *remoteAuthReq `json:"auth"`
	}
	if !decodeRemoteReq(c, &req) {
		return
	}
	auth, err := req.Auth.plain()
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := a2aremote.Resolve(c.Context(), remoteGuard(), req.URL, auth)
	if err != nil {
		c.JSON(resolveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	snap := a2aremote.SnapshotOf(res.Card)
	handle, err := freeHandle(c.Context(), actorID(c), handleFromName(snap.Name))
	if err != nil {
		handle = ""
	}
	c.JSON(http.StatusOK, remoteResolveResp{CardURL: res.CardURL, Host: hostOf(res.CardURL), Card: snap, SuggestedHandle: handle})
}

// apiTeamRemoteTest handles POST /api/team/a2a-remote/test
// {url, auth?} or {agent_id, auth?}: send "ping" and report the reply and
// latency. With agent_id and no auth, the stored auth is used.
func apiTeamRemoteTest(c *tool.Ctx) {
	if !remoteReady(c) {
		return
	}
	var req struct {
		URL     string         `json:"url"`
		AgentID string         `json:"agent_id"`
		Auth    *remoteAuthReq `json:"auth"`
	}
	if !decodeRemoteReq(c, &req) {
		return
	}
	auth, err := req.Auth.plain()
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	target := req.URL
	if req.AgentID != "" {
		p, err := globalTeam.Get(c.Context(), req.AgentID)
		if err != nil || p.OwnerUserID != actorID(c) || !isA2ARemote(p) {
			c.JSON(http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		cfg, ok, err := remoteStore().Load(p.ID)
		if err != nil || !ok {
			c.JSON(http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		target = cfg.CardURL
		if req.Auth == nil {
			if auth, err = cfg.Auth.Plain(remoteCodec()); err != nil {
				c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
	}
	start := time.Now()
	res, err := a2aremote.Resolve(c.Context(), remoteGuard(), target, auth)
	cardMS := time.Since(start).Milliseconds()
	if err != nil {
		c.JSON(http.StatusOK, map[string]any{"ok": false, "state": "card_failed", "card_ms": cardMS, "error": err.Error()})
		return
	}
	out := a2aremote.Ping(c.Context(), remoteGuard(), res.Card, auth)
	c.JSON(http.StatusOK, map[string]any{
		"ok": out.OK, "state": out.State, "card_ms": cardMS, "latency_ms": out.LatencyMS,
		"reply": out.Reply, "error": out.Error,
	})
}

// remoteCreateReq is POST /api/team/a2a-remote.
type remoteCreateReq struct {
	URL              string         `json:"url"`
	Auth             *remoteAuthReq `json:"auth"`
	Handle           string         `json:"handle"`
	Tagline          *string        `json:"tagline"`
	Avatar           *team.Avatar   `json:"avatar"`
	TimeoutSec       int            `json:"timeout_sec"`
	MaxResponseBytes int64          `json:"max_response_bytes"`
	Usage            string         `json:"usage"`
}

// apiTeamRemoteCreate handles POST /api/team/a2a-remote: fetch the card
// again (the preview may be stale), then add the agent. The handle comes
// from the request or the card name; a clash gets -2, -3 … unless the
// handle was typed, which answers 409. The agent's sessions live in a
// project made for it that only holds transcripts (plan decision 36).
func apiTeamRemoteCreate(c *tool.Ctx) {
	if !remoteReady(c) {
		return
	}
	var req remoteCreateReq
	if !decodeRemoteReq(c, &req) {
		return
	}
	if err := a2aremote.ValidateLimits(req.TimeoutSec, req.MaxResponseBytes, req.Usage); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	auth, err := req.Auth.plain()
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	tagline, ok := validTagline(c, req.Tagline, "")
	if !ok {
		return
	}
	sealed, err := a2aremote.Seal(remoteCodec(), auth)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	res, err := a2aremote.Resolve(c.Context(), remoteGuard(), req.URL, auth)
	if err != nil {
		c.JSON(resolveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	snap := a2aremote.SnapshotOf(res.Card)
	owner := actorID(c)
	var handle string
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
	} else if handle, err = freeHandle(c.Context(), owner, handleFromName(snap.Name)); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	pid, err := createRemoteAgentProject(c.Context(), owner, snap)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	av := team.DefaultAvatarFor(handle)
	if req.Avatar != nil {
		av = *req.Avatar
	}
	p := &entity.AgentPersona{
		OwnerUserID: owner, Handle: handle, ProjectID: pid, Kind: a2aremote.Kind, Tagline: tagline,
		AllowedConnectors: "[]", AllowedNativeTools: "[]",
		// A remote agent has no local process: no rail panel applies.
		Features: team.EncodeFeatures(team.Features{}),
		Avatar:   team.EncodeAvatar(av),
	}
	if err := globalTeam.Create(c.Context(), p); err != nil {
		discardTeamAgentProject(c, pid)
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	cfg := a2aremote.Config{
		AgentID: p.ID, OwnerUserID: owner, CardURL: res.CardURL, Card: snap, CardJSON: res.JSON,
		Auth: sealed, TimeoutSec: req.TimeoutSec, MaxResponseBytes: req.MaxResponseBytes,
		Usage: req.Usage, RefreshedAt: time.Now().UTC(),
	}
	if err := remoteStore().Save(cfg); err != nil {
		_ = globalTeam.Delete(c.Context(), p.ID)
		discardTeamAgentProject(c, pid)
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	announceAgentCreated(c, *p, snap.Name, "a2a-remote")
	users := teamProjectUsersFor(c.Context(), []entity.AgentPersona{*p})
	c.JSON(http.StatusOK, teamAgentToItem(*p, users, teamLiveNow(), ownerReach(c)))
}

// createRemoteAgentProject makes the project a remote agent's sessions
// live in. It carries the card's name and description so the roster and
// chat headers read like any agent's, and the RemoteProjectTag that keeps
// it out of project pickers.
func createRemoteAgentProject(ctx context.Context, owner string, card a2aremote.Card) (string, error) {
	desc := card.Description
	if desc == "" {
		desc = "A2A remote agent."
	}
	pid, err := createAgentProjectFor(ctx, owner, card.Name, "🛰️", desc, "", a2aremote.ProviderKey, "", "")
	if err != nil {
		return "", err
	}
	if proj, ok := globalMgr.Registry().Project(pid); ok {
		meta := proj.Meta
		meta.Tags = append(meta.Tags, a2aremote.ProjectTag)
		if _, err := globalMgr.UpdateProject(ctx, pid, meta); err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("project", pid).Msg("team: tag remote agent project")
		}
	}
	return pid, nil
}

// apiTeamRemoteGet handles GET /api/team/agents/{id}/a2a-remote. With
// ?session_id= (one of the agent's sessions) it adds that chat's A2A
// state: its contextId and whether the remote waits for an answer
// (input_required), for the question card.
func apiTeamRemoteGet(c *tool.Ctx) {
	if !remoteReady(c) {
		return
	}
	p, cfg, ok := loadOwnRemoteAgent(c)
	if !ok {
		return
	}
	info := remoteInfoOf(cfg)
	if sid := c.Query("session_id"); sid != "" {
		s, found := globalMgr.Registry().Session(sid)
		if !found || s.Meta.AgentID != p.ID {
			c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		st := a2aremote.LoadState(globalLayout.SessionDir(sid))
		info.Session = &st
	}
	c.JSON(http.StatusOK, info)
}

// RemoteSpawnerFor is the pool factory's RemoteSpawnerLoader: a session
// bound to an A2A remote agent runs on that remote. The auth secret is
// decrypted here, per spawn, and kept only in the spawner's memory.
func RemoteSpawnerFor(sessionID string) (provider.Spawner, bool) {
	if globalMgr == nil || globalTeam == nil || remoteStore() == nil {
		return nil, false
	}
	s, ok := globalMgr.Registry().Session(sessionID)
	if !ok || s.Meta.AgentID == "" {
		return nil, false
	}
	p, err := globalTeam.Get(context.Background(), s.Meta.AgentID)
	if err != nil || !IsRemoteAgent(p) {
		return nil, false
	}
	if isSlackRemote(p) {
		return slackRemoteSpawner(p), true
	}
	cfg, found, err := remoteStore().Load(p.ID)
	if err != nil || !found {
		log.Warn().Err(err).Str("agent", p.ID).Msg("team: remote agent settings missing")
		return remoteFailSpawner{msg: "This A2A remote agent has no settings; add it again."}, true
	}
	auth, err := cfg.Auth.Plain(remoteCodec())
	if err != nil {
		return remoteFailSpawner{msg: "Could not read the remote agent's auth: " + err.Error()}, true
	}
	return a2aremote.Spawner{Runtime: a2aremote.Runtime{Config: cfg, Auth: auth, Guard: remoteGuard()}}, true
}

// remoteFailSpawner refuses a remote session that cannot run. It still
// answers true from the loader so the session never falls back to a
// local CLI under the remote agent's name.
type remoteFailSpawner struct{ msg string }

func (f remoteFailSpawner) Spawn(context.Context, provider.SpawnOptions) (provider.Process, error) {
	return nil, errors.New(f.msg)
}

// apiTeamRemoteUpdate handles PATCH /api/team/agents/{id}/a2a-remote
// {timeout_sec?, max_response_bytes?, usage?, auth?}. A sent auth replaces
// the stored one ({type:"none"} clears it); an absent one keeps it.
func apiTeamRemoteUpdate(c *tool.Ctx) {
	if !remoteReady(c) {
		return
	}
	_, cfg, ok := loadOwnRemoteAgent(c)
	if !ok {
		return
	}
	var req struct {
		TimeoutSec       *int           `json:"timeout_sec"`
		MaxResponseBytes *int64         `json:"max_response_bytes"`
		Usage            *string        `json:"usage"`
		Auth             *remoteAuthReq `json:"auth"`
	}
	if !decodeRemoteReq(c, &req) {
		return
	}
	if req.TimeoutSec != nil {
		cfg.TimeoutSec = *req.TimeoutSec
	}
	if req.MaxResponseBytes != nil {
		cfg.MaxResponseBytes = *req.MaxResponseBytes
	}
	if req.Usage != nil {
		cfg.Usage = *req.Usage
	}
	if err := a2aremote.ValidateLimits(cfg.TimeoutSec, cfg.MaxResponseBytes, cfg.Usage); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Auth != nil {
		auth, err := req.Auth.plain()
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if cfg.Auth, err = a2aremote.Seal(remoteCodec(), auth); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := remoteStore().Save(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, remoteInfoOf(cfg))
}

// apiTeamRemoteRefresh handles POST /api/team/agents/{id}/a2a-remote/refresh-card:
// fetch the card again with the stored auth. Skills, version, streaming
// and endpoint follow the card; the handle and avatar stay as they are.
func apiTeamRemoteRefresh(c *tool.Ctx) {
	if !remoteReady(c) {
		return
	}
	_, cfg, ok := loadOwnRemoteAgent(c)
	if !ok {
		return
	}
	auth, err := cfg.Auth.Plain(remoteCodec())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	res, err := a2aremote.Resolve(c.Context(), remoteGuard(), cfg.CardURL, auth)
	if err != nil {
		c.JSON(resolveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	cfg.Card, cfg.CardJSON, cfg.RefreshedAt = a2aremote.SnapshotOf(res.Card), res.JSON, time.Now().UTC()
	if err := remoteStore().Save(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, remoteInfoOf(cfg))
}

// removeAgentRemote drops a deleted agent's remote settings. Its sessions
// stay: the transcript is kept unless the delete asked for the chats too.
func removeAgentRemote(p entity.AgentPersona) {
	if isSlackRemote(p) && slackRemoteStore() != nil {
		if err := slackRemoteStore().Delete(p.ID); err != nil {
			log.Warn().Err(err).Str("agent", p.ID).Msg("team: drop slack remote agent settings")
		}
		return
	}
	if !isA2ARemote(p) || remoteStore() == nil {
		return
	}
	if err := remoteStore().Delete(p.ID); err != nil {
		log.Warn().Err(err).Str("agent", p.ID).Msg("team: drop remote agent settings")
	}
}

package agents

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Instant mode: a Team agent without a Slack app of its own rides one of
// wick's Slack instances (the shared app) and answers under its own name
// and icon. One agent_channels row per agent, type agentSlackInstantType,
// Name = agent id; Config is instantConfig as JSON. Routing itself lives in
// channels/slack/instant.go; this file stores the settings, validates them
// and answers the channel's lookups.

const agentSlackInstantType = "slack-instant"

// instantSharedPrefix is the instance-key prefix of the Slack instances an
// Instant agent may ride: the App Owner's ("slack:__owner__") and per-user
// ones ("slack:<user id>"). A Custom agent's own bot ("slack-agent:…") is
// never shared.
const instantSharedPrefix = "slack:"

const instantOwnerInstance = "slack:__owner__"

type instantConfig struct {
	SharedChannel string   `json:"shared_channel"`
	BoundChannels []string `json:"bound_channels"`
	PrefixEnabled bool     `json:"prefix_enabled"`
	// AvatarToken is the unguessable path segment of the public avatar.
	// It is a capability: never in an API response or a log line.
	AvatarToken string `json:"avatar_token"`
}

func instantRow(db *gorm.DB, agentID string) (entity.AgentChannel, instantConfig, bool, error) {
	var row entity.AgentChannel
	var cfg instantConfig
	err := db.Where("type = ? AND name = ?", agentSlackInstantType, agentID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, cfg, false, nil
	}
	if err != nil {
		return row, cfg, false, err
	}
	_ = json.Unmarshal([]byte(row.Config), &cfg)
	return row, cfg, true, nil
}

func listInstantRows(db *gorm.DB) ([]entity.AgentChannel, error) {
	var rows []entity.AgentChannel
	err := db.Where("type = ?", agentSlackInstantType).Find(&rows).Error
	return rows, err
}

func saveInstantRow(db *gorm.DB, p entity.AgentPersona, cfg instantConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	row, _, found, err := instantRow(db, p.ID)
	if err != nil {
		return err
	}
	if found {
		return db.Model(&row).Updates(map[string]any{"config": string(data), "updated_at": time.Now()}).Error
	}
	owner := p.OwnerUserID
	return db.Create(&entity.AgentChannel{
		ID: uuid.New().String(), Type: agentSlackInstantType, Name: p.ID, UserID: &owner,
		Enabled: true, Config: string(data), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}).Error
}

// removeAgentSlackInstant drops agentID's Instant settings, if any. Called
// on disable-instant and when the agent is deleted.
func removeAgentSlackInstant(agentID string) {
	if globalDB == nil {
		return
	}
	if err := globalDB.Where("type = ? AND name = ?", agentSlackInstantType, agentID).
		Delete(&entity.AgentChannel{}).Error; err != nil {
		log.Warn().Err(err).Str("agent", agentID).Msg("team: delete agent slack instant settings")
	}
	instantCache.invalidate()
}

func newAvatarToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// slackChannelIDRe is a Slack conversation id; slackArchiveRe pulls one out
// of a pasted link (…/archives/C0123ABC or …/client/T…/C0123ABC).
var (
	slackChannelIDRe = regexp.MustCompile(`^[CG][A-Z0-9]{6,}$`)
	slackArchiveRe   = regexp.MustCompile(`/(?:archives|client/[A-Z0-9]+)/([CG][A-Z0-9]{6,})`)
)

// normalizeSlackChannel accepts a channel id or a pasted channel link.
func normalizeSlackChannel(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if m := slackArchiveRe.FindStringSubmatch(v); m != nil {
		return m[1], true
	}
	v = strings.ToUpper(strings.TrimPrefix(v, "#"))
	// A real id always carries digits; this keeps "#general" out.
	return v, slackChannelIDRe.MatchString(v) && strings.ContainsAny(v, "0123456789")
}

// errChannelBound is the 409 for a channel another agent already holds.
type errChannelBound struct{ channel, owner string }

func (e errChannelBound) Error() string {
	return "channel " + e.channel + " is already bound to " + e.owner + " — one channel answers as one agent"
}

// checkInstantBindings refuses a channel bound to any other agent.
func checkInstantBindings(agentID string, channels []string) error {
	rows, err := listInstantRows(globalDB)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Name == agentID {
			continue
		}
		var cfg instantConfig
		_ = json.Unmarshal([]byte(r.Config), &cfg)
		for _, ch := range channels {
			if slices.Contains(cfg.BoundChannels, ch) {
				owner := "another agent"
				if other, err := globalTeam.Get(context.Background(), r.Name); err == nil {
					owner = "@" + other.Handle
				}
				return errChannelBound{channel: ch, owner: owner}
			}
		}
	}
	return nil
}

// instantSharedAllowed reports whether userID may ride the instance key:
// wick's shared (App Owner) app, the user's own Slack channel, or — for an
// admin — any user's.
func instantSharedAllowed(key, userID string, admin bool) bool {
	if !strings.HasPrefix(key, instantSharedPrefix) {
		return false
	}
	return key == instantOwnerInstance || key == instantSharedPrefix+userID || admin
}

// sharedSlackApp is one choice of GET /api/team/slack/instant/apps.
type sharedSlackApp struct {
	Key      string `json:"key"`
	BotName  string `json:"bot_name,omitempty"`
	TeamName string `json:"team_name,omitempty"`
	Online   bool   `json:"online"`
	Shared   bool   `json:"shared"`
}

func sharedSlackInstance(key string) *agentslack.Channel {
	if globalChannels == nil || key == "" {
		return nil
	}
	ch, _ := globalChannels.ChannelByKey(key).(*agentslack.Channel)
	return ch
}

// apiTeamSlackInstantApps handles GET /api/team/slack/instant/apps: the
// Slack instances the caller may let an Instant agent ride.
func apiTeamSlackInstantApps(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	uid, admin := actorID(c), isAdminCtx(c)
	out := []sharedSlackApp{}
	if globalChannels != nil {
		for _, ch := range globalChannels.Channels() {
			sc, ok := ch.(*agentslack.Channel)
			if !ok {
				continue
			}
			key := globalChannels.InstanceKeyOf(ch)
			if !instantSharedAllowed(key, uid, admin) || !sc.IsConfigured() {
				continue
			}
			_, name, teamName := sc.BotIdentity()
			out = append(out, sharedSlackApp{Key: key, BotName: name, TeamName: teamName,
				Online: sc.IsConfigured(), Shared: key == instantOwnerInstance})
		}
	}
	slices.SortFunc(out, func(a, b sharedSlackApp) int { return strings.Compare(a.Key, b.Key) })
	c.JSON(http.StatusOK, map[string]any{"apps": out})
}

// AgentSlackInstantStatus is GET /api/team/agents/{id}/slack/instant. The
// avatar token is never part of it.
type AgentSlackInstantStatus struct {
	Enabled       bool     `json:"enabled"`
	SharedChannel string   `json:"shared_channel,omitempty"`
	BoundChannels []string `json:"bound_channels"`
	PrefixEnabled bool     `json:"prefix_enabled"`
	Username      string   `json:"username"`
	AvatarReady   bool     `json:"avatar_ready"`
	SharedOnline  bool     `json:"shared_online"`
	// CustomizeScope is "ok", "missing" or "unknown" (bot offline / Slack
	// unreachable) for chat:write.customize.
	CustomizeScope string   `json:"customize_scope"`
	Warnings       []string `json:"warnings"`
}

const customizeScope = "chat:write.customize"

// instantScopesOf reads the shared bot's scopes; a stub in tests.
var instantScopesOf = func(ch *agentslack.Channel) ([]string, error) { return ch.BotTokenScopes() }

func instantStatusOf(p entity.AgentPersona) (AgentSlackInstantStatus, error) {
	_, cfg, found, err := instantRow(globalDB, p.ID)
	if err != nil {
		return AgentSlackInstantStatus{}, err
	}
	st := AgentSlackInstantStatus{
		Enabled: found, BoundChannels: []string{}, Username: instantUsername(p),
		CustomizeScope: "unknown", Warnings: []string{},
	}
	if !found {
		return st, nil
	}
	st.SharedChannel, st.PrefixEnabled = cfg.SharedChannel, cfg.PrefixEnabled
	st.BoundChannels = append(st.BoundChannels, cfg.BoundChannels...)
	st.AvatarReady = cfg.AvatarToken != "" && instantPublicURL() != ""
	if !st.AvatarReady {
		st.Warnings = append(st.Warnings, "wick has no public URL, so replies show a robot emoji instead of the agent's avatar")
	}
	ch := sharedSlackInstance(cfg.SharedChannel)
	if ch == nil || !ch.IsConfigured() {
		st.Warnings = append(st.Warnings, "the shared Slack app is not running — the agent cannot answer in Slack")
		return st, nil
	}
	st.SharedOnline = true
	if scopes, err := instantScopesOf(ch); err == nil {
		if slices.Contains(scopes, customizeScope) {
			st.CustomizeScope = "ok"
		} else {
			st.CustomizeScope = "missing"
			st.Warnings = append(st.Warnings, "the shared Slack app lacks the chat:write.customize scope — replies are sent as the bot, not as the agent; add the scope and reinstall the app")
		}
	}
	if len(cfg.BoundChannels) == 0 && !cfg.PrefixEnabled {
		st.Warnings = append(st.Warnings, "no channel is bound and prefix routing is off — nothing reaches this agent")
	}
	return st, nil
}

func instantPublicURL() string {
	if globalConfigs == nil {
		return ""
	}
	return strings.TrimRight(globalConfigs.AppURL(), "/")
}

// instantUsername is the name an agent's replies carry: its project's
// name, else its handle.
func instantUsername(p entity.AgentPersona) string {
	if p.ProjectID != "" && globalMgr != nil {
		if proj, ok := globalMgr.Registry().Project(p.ProjectID); ok && proj.Meta.Name != "" {
			return proj.Meta.Name
		}
	}
	return p.Handle
}

// apiTeamAgentSlackInstantGet handles GET /api/team/agents/{id}/slack/instant.
func apiTeamAgentSlackInstantGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	st, err := instantStatusOf(p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

type agentSlackInstantReq struct {
	SharedChannel *string   `json:"shared_channel"`
	BoundChannels *[]string `json:"bound_channels"`
	PrefixEnabled *bool     `json:"prefix_enabled"`
}

// apiTeamAgentSlackInstantPut handles PUT /api/team/agents/{id}/slack/instant:
// turns Instant mode on or changes it. Omitted fields keep their value.
// Only the owner reaches it (loadOwnTeamAgent answers 404 to anyone else).
func apiTeamAgentSlackInstantPut(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	if p.Kind != "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "a remote agent cannot answer in Slack as an Instant agent"})
		return
	}
	var req agentSlackInstantReq
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if _, found, err := agentSlackRowOf(p.ID); err != nil || found {
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusConflict, map[string]string{"error": "this agent has its own Slack app (Custom mode) — disconnect it before switching to Instant"})
		return
	}
	_, cfg, _, err := instantRow(globalDB, p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.SharedChannel != nil {
		cfg.SharedChannel = strings.TrimSpace(*req.SharedChannel)
	}
	if req.PrefixEnabled != nil {
		cfg.PrefixEnabled = *req.PrefixEnabled
	}
	if req.BoundChannels != nil {
		seen := map[string]bool{}
		cfg.BoundChannels = []string{}
		for _, raw := range *req.BoundChannels {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			id, ok := normalizeSlackChannel(raw)
			if !ok {
				c.JSON(http.StatusBadRequest, map[string]string{"error": "not a Slack channel id or link: " + strings.TrimSpace(raw)})
				return
			}
			if !seen[id] {
				seen[id] = true
				cfg.BoundChannels = append(cfg.BoundChannels, id)
			}
		}
	}
	if cfg.BoundChannels == nil {
		cfg.BoundChannels = []string{}
	}
	if cfg.SharedChannel == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "pick the shared Slack app to use"})
		return
	}
	if !instantSharedAllowed(cfg.SharedChannel, p.OwnerUserID, isAdminCtx(c)) || sharedSlackInstance(cfg.SharedChannel) == nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "that Slack app is not available to you"})
		return
	}
	if err := checkInstantBindings(p.ID, cfg.BoundChannels); err != nil {
		status := http.StatusInternalServerError
		var bound errChannelBound
		if errors.As(err, &bound) {
			c.JSON(http.StatusConflict, map[string]string{"error": err.Error(), "channel": bound.channel, "agent": bound.owner})
			return
		}
		c.JSON(status, map[string]string{"error": err.Error()})
		return
	}
	if cfg.AvatarToken == "" {
		if cfg.AvatarToken, err = newAvatarToken(); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": "avatar token: " + err.Error()})
			return
		}
	}
	if err := saveInstantRow(globalDB, p, cfg); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	instantCache.invalidate()
	st, _ := instantStatusOf(p)
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentSlackInstantDelete handles DELETE
// /api/team/agents/{id}/slack/instant: the agent leaves the shared app.
func apiTeamAgentSlackInstantDelete(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	removeAgentSlackInstant(p.ID)
	c.JSON(http.StatusOK, map[string]string{"status": "disabled"})
}

// apiTeamAgentSlackInstantRotate handles POST
// /api/team/agents/{id}/slack/instant/rotate-avatar: a new avatar token,
// so the old public URL stops working. The token is not returned.
func apiTeamAgentSlackInstantRotate(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	_, cfg, found, err := instantRow(globalDB, p.ID)
	if err != nil || !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "the agent is not in Instant mode"})
		return
	}
	if cfg.AvatarToken, err = newAvatarToken(); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "avatar token: " + err.Error()})
		return
	}
	if err := saveInstantRow(globalDB, p, cfg); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	instantCache.invalidate()
	st, _ := instantStatusOf(p)
	c.JSON(http.StatusOK, st)
}

// agentSlackRowOf reports whether agentID has a Custom-mode connection.
func agentSlackRowOf(agentID string) (entity.AgentChannel, bool, error) {
	var row entity.AgentChannel
	err := globalDB.Where("type = ? AND name = ?", "slack-agent", agentID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

// ── Router: the channel's view of Instant agents ─────────────────────

// instantSnapshot is every enabled Instant agent, grouped by the instance
// key it rides, plus the avatars behind their tokens.
type instantSnapshot struct {
	byInstance map[string][]agentslack.InstantAgent
	avatars    map[string]instantAvatar // token → avatar
	at         time.Time
}

type instantAvatar struct {
	shape, color, etag string
}

// instantCacheTTL bounds how stale a rename or an avatar change can be in
// Slack; a save through this API invalidates at once.
const instantCacheTTL = 30 * time.Second

type instantCacheT struct {
	mu   sync.Mutex
	snap *instantSnapshot
	png  sync.Map // etag → []byte
}

var instantCache = &instantCacheT{}

func (c *instantCacheT) invalidate() {
	c.mu.Lock()
	c.snap = nil
	c.mu.Unlock()
}

func (c *instantCacheT) get() *instantSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && time.Since(c.snap.at) < instantCacheTTL {
		return c.snap
	}
	c.snap = buildInstantSnapshot()
	return c.snap
}

func buildInstantSnapshot() *instantSnapshot {
	snap := &instantSnapshot{byInstance: map[string][]agentslack.InstantAgent{}, avatars: map[string]instantAvatar{}, at: time.Now()}
	if globalDB == nil || globalTeam == nil {
		return snap
	}
	rows, err := listInstantRows(globalDB)
	if err != nil {
		log.Warn().Err(err).Msg("team: list instant agents")
		return snap
	}
	base := instantPublicURL()
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		var cfg instantConfig
		if json.Unmarshal([]byte(r.Config), &cfg) != nil || cfg.SharedChannel == "" {
			continue
		}
		p, err := globalTeam.Get(context.Background(), r.Name)
		if err != nil || p.Disabled {
			continue
		}
		av := team.DecodeAvatar(p.Avatar)
		sum := sha256.Sum256([]byte(av.Kind + "|" + av.Shape + "|" + av.Color))
		etag := hex.EncodeToString(sum[:])[:16]
		if cfg.AvatarToken != "" {
			snap.avatars[cfg.AvatarToken] = instantAvatar{shape: av.Shape, color: av.Color, etag: etag}
		}
		snap.byInstance[cfg.SharedChannel] = append(snap.byInstance[cfg.SharedChannel], agentslack.InstantAgent{
			Persona: agentslack.Persona{
				AgentID: p.ID, ProjectID: p.ProjectID, Username: instantUsername(p),
				IconURL: agentslack.AvatarURL(base, cfg.AvatarToken, etag[:8]),
			},
			Handle: p.Handle, Channels: cfg.BoundChannels, PrefixEnabled: cfg.PrefixEnabled,
		})
	}
	return snap
}

// instantRouter implements agentslack.PersonaRouter over instantCache.
type instantRouter struct{}

func (instantRouter) Agents(ch *agentslack.Channel) []agentslack.InstantAgent {
	if globalChannels == nil || ch == nil {
		return nil
	}
	key := globalChannels.InstanceKeyOf(ch)
	if !strings.HasPrefix(key, instantSharedPrefix) {
		return nil
	}
	return instantCache.get().byInstance[key]
}

func (instantRouter) SessionAgent(sessionID string) string {
	if globalMgr == nil || sessionID == "" {
		return ""
	}
	if s, ok := globalMgr.Registry().Session(sessionID); ok {
		return s.Meta.AgentID
	}
	return ""
}

func (instantRouter) AvatarPNG(token string) ([]byte, string, bool) {
	av, ok := instantCache.get().avatars[token]
	if !ok {
		return nil, "", false
	}
	if b, ok := instantCache.png.Load(av.etag); ok {
		return b.([]byte), av.etag, true
	}
	b := agentslack.RenderAvatarPNG(av.shape, av.color)
	instantCache.png.Store(av.etag, b)
	return b, av.etag, true
}

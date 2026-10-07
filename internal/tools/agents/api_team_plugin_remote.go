package agents

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/pluginremote"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	serviceplugin "github.com/yogasw/wick/internal/services/plugin"
	wickentity "github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// isPluginRemote reports whether p is a remote agent served by a service
// plugin's remote_source.
func isPluginRemote(p entity.AgentPersona) bool { return p.Kind == pluginremote.Kind }

func pluginRemoteStore() *pluginremote.Store {
	if globalDB == nil {
		return nil
	}
	return pluginremote.NewStore(globalDB)
}

// pluginTransport reaches the running service plugin key.
func pluginTransport(key string) (http.RoundTripper, error) {
	h := serviceplugin.Default()
	if h == nil {
		return nil, serviceplugin.ErrNotRunning
	}
	s, ok := h.Get(key)
	if !ok {
		return nil, serviceplugin.ErrNotRunning
	}
	return s.Sup.Transport()
}

// pluginRemoteSpawner is RemoteSpawnerFor's answer for a plugin remote agent.
func pluginRemoteSpawner(p entity.AgentPersona) provider.Spawner {
	st := pluginRemoteStore()
	if st == nil {
		return remoteFailSpawner{msg: "Storage is not ready."}
	}
	cfg, found, err := st.Load(p.ID)
	if err != nil || !found {
		return remoteFailSpawner{msg: "This plugin remote agent has no settings; add it again."}
	}
	values, err := pluginremote.OpenValues(pluginRemoteCodec(), cfg.Values)
	if err != nil {
		log.Warn().Str("agent", p.ID).Msg("team: plugin remote config could not be decrypted")
		return remoteFailSpawner{msg: "Could not read this plugin remote agent's config; save it again."}
	}
	src := pluginremote.NewSource(cfg.PluginKey, pluginTransport)
	src.AgentID, src.Config = p.ID, values
	return remote.Spawner{Source: src}
}

// pluginRemoteCodecOverride stands in for the configs service in tests.
var pluginRemoteCodecOverride pluginremote.Codec

// pluginRemoteCodec encrypts secret per-agent config values — the configs
// codec connector secrets use.
func pluginRemoteCodec() pluginremote.Codec {
	if pluginRemoteCodecOverride != nil {
		return pluginRemoteCodecOverride
	}
	if globalConfigs == nil {
		return nil
	}
	return globalConfigs
}

// pluginRemoteDecl is the RemoteConfigs service plugin key declares.
func pluginRemoteDecl(key string) []wickentity.Config {
	h := serviceplugin.Default()
	if h == nil {
		return nil
	}
	s, ok := h.Get(key)
	if !ok {
		return nil
	}
	return s.Manifest.RemoteConfigs
}

// pluginRemoteFields lists decl with stored values, secrets masked: a
// secret's value never leaves the server, only HasValue tells one is set.
func pluginRemoteFields(decl []wickentity.Config, stored map[string]string) []serviceplugin.ConfigField {
	out := make([]serviceplugin.ConfigField, 0, len(decl))
	for _, d := range decl {
		v, has := stored[d.Key], stored[d.Key] != ""
		if d.IsSecret {
			v = ""
			if has {
				v = serviceplugin.SecretMask
			}
		}
		out = append(out, serviceplugin.ConfigField{Key: d.Key, Value: v, Type: d.Type, Options: d.Options,
			Description: d.Description, IsSecret: d.IsSecret, HasValue: has, Required: d.Required})
	}
	return out
}

// mergePluginRemoteValues applies in over old: keys outside decl are
// refused, an empty or masked secret keeps the stored one, and a required
// field left empty is an error naming it. The result holds plaintext for
// the keys in, sealed values for the secrets kept.
func mergePluginRemoteValues(decl []wickentity.Config, old, in map[string]string) (map[string]string, error) {
	known := map[string]bool{}
	for _, d := range decl {
		known[d.Key] = true
	}
	for k := range in {
		if !known[k] {
			return nil, fmt.Errorf("unknown config %q", k)
		}
	}
	out := map[string]string{}
	for _, d := range decl {
		v, sent := in[d.Key]
		v = strings.TrimSpace(v)
		if !sent || (d.IsSecret && (v == "" || v == serviceplugin.SecretMask)) {
			v = old[d.Key]
		}
		if v == "" && d.Required {
			return nil, fmt.Errorf("config %q is required", d.Key)
		}
		if v != "" {
			out[d.Key] = v
		}
	}
	return out, nil
}

// sealPluginRemoteValues encrypts the secrets of merged that are not
// sealed yet (a kept one already is).
func sealPluginRemoteValues(decl []wickentity.Config, merged map[string]string) (map[string]string, error) {
	return pluginremote.SealValues(pluginRemoteCodec(), decl, merged)
}

// pluginSourceItem is one "Plugin" choice of the Remote agent wizard.
type pluginSourceItem struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
	State       string `json:"state"`
	// RemoteConfigs are the per-agent fields the plugin declares (no values).
	RemoteConfigs []serviceplugin.ConfigField `json:"remote_configs"`
}

// apiTeamPluginSources handles GET /api/team/plugin-sources: the service
// plugins that declare remote_source.
func apiTeamPluginSources(c *tool.Ctx) {
	out := []pluginSourceItem{}
	if h := serviceplugin.Default(); h != nil {
		for _, v := range h.RemoteSources() {
			out = append(out, pluginSourceItem{Key: v.Key, Name: v.Name, Description: v.Description, Version: v.Version, State: v.Status.State,
				RemoteConfigs: pluginRemoteFields(pluginRemoteDecl(v.Key), nil)})
		}
	}
	c.JSON(http.StatusOK, out)
}

type pluginCreateReq struct {
	PluginKey string       `json:"plugin_key"`
	Name      string       `json:"name"`
	Handle    string       `json:"handle"`
	Tagline   *string      `json:"tagline"`
	Avatar    *team.Avatar `json:"avatar"`
	// Config answers the plugin's RemoteConfigs (plaintext; secrets are
	// encrypted before they are stored).
	Config map[string]string `json:"config"`
}

// apiTeamPluginRemoteCreate handles POST /api/team/plugin-remote.
func apiTeamPluginRemoteCreate(c *tool.Ctx) {
	st := pluginRemoteStore()
	if st == nil || globalTeam == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "storage is not ready"})
		return
	}
	var req pluginCreateReq
	if !decodeRemoteReq(c, &req) {
		return
	}
	var src *serviceplugin.ServiceView
	if h := serviceplugin.Default(); h != nil {
		for _, v := range h.RemoteSources() {
			if v.Key == req.PluginKey {
				v := v
				src = &v
			}
		}
	}
	if src == nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "no service plugin with remote_source named " + req.PluginKey})
		return
	}
	tagline, ok := validTagline(c, req.Tagline, "")
	if !ok {
		return
	}
	decl := pluginRemoteDecl(src.Key)
	values, err := mergePluginRemoteValues(decl, nil, req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if values, err = sealPluginRemoteValues(decl, values); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not encrypt the agent config"})
		return
	}
	owner := actorID(c)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = firstNonBlank(src.Name, src.Key)
	}
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
	} else if handle, err = freeHandle(c.Context(), owner, handleFromName(name)); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	pid, err := createAgentProjectFor(c.Context(), owner, name, "🧩", "Plugin remote agent.", "", pluginremote.ProviderKey, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	av := team.DefaultAvatarFor(handle)
	if req.Avatar != nil {
		av = *req.Avatar
	}
	p := &entity.AgentPersona{
		OwnerUserID: owner, Handle: handle, ProjectID: pid, Kind: pluginremote.Kind, Tagline: tagline,
		MentionFrom:       remoteMentionDefault(""),
		AllowedConnectors: "[]", AllowedNativeTools: "[]",
		Features: team.EncodeFeatures(team.Features{}),
		Avatar:   team.EncodeAvatar(av),
	}
	if err := globalTeam.Create(c.Context(), p); err != nil {
		discardTeamAgentProject(c, pid)
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	if err := st.Save(pluginremote.Config{AgentID: p.ID, OwnerUserID: owner, PluginKey: src.Key, Usage: pluginremote.UsageByMention, Values: values}); err != nil {
		_ = globalTeam.Delete(c.Context(), p.ID)
		discardTeamAgentProject(c, pid)
		log.Ctx(c.Context()).Warn().Err(err).Msg("team: save plugin remote settings")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	announceAgentCreated(c, *p, name, "plugin-remote")
	users := teamProjectUsersFor(c.Context(), []entity.AgentPersona{*p})
	c.JSON(http.StatusOK, teamAgentToItem(*p, users, teamLiveNow(), ownerReach(c)))
}

// pluginRemoteInfo is what GET/PATCH /api/team/agents/{id}/plugin-remote
// answer: the plugin and the agent's config, secrets masked.
type pluginRemoteInfo struct {
	PluginKey string                      `json:"plugin_key"`
	Configs   []serviceplugin.ConfigField `json:"configs"`
}

// loadOwnPluginRemote is the caller's plugin remote agent {id} and its row.
func loadOwnPluginRemote(c *tool.Ctx) (pluginremote.Config, bool) {
	st := pluginRemoteStore()
	if st == nil || globalTeam == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "storage is not ready"})
		return pluginremote.Config{}, false
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return pluginremote.Config{}, false
	}
	cfg, found, err := st.Load(p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return cfg, false
	}
	if !isPluginRemote(p) || !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "not a plugin remote agent"})
		return cfg, false
	}
	return cfg, true
}

// apiTeamPluginRemoteGet handles GET /api/team/agents/{id}/plugin-remote.
func apiTeamPluginRemoteGet(c *tool.Ctx) {
	cfg, ok := loadOwnPluginRemote(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, pluginRemoteInfo{PluginKey: cfg.PluginKey, Configs: pluginRemoteFields(pluginRemoteDecl(cfg.PluginKey), cfg.Values)})
}

// apiTeamPluginRemoteUpdate handles PATCH /api/team/agents/{id}/plugin-remote
// {config}: sent keys replace the stored ones; an empty or masked secret
// keeps it.
func apiTeamPluginRemoteUpdate(c *tool.Ctx) {
	cfg, ok := loadOwnPluginRemote(c)
	if !ok {
		return
	}
	var req struct {
		Config map[string]string `json:"config"`
	}
	if !decodeRemoteReq(c, &req) {
		return
	}
	// Without the plugin's declaration a save would drop every stored value.
	h := serviceplugin.Default()
	if h == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "plugin " + cfg.PluginKey + " is not installed"})
		return
	}
	if _, ok := h.Get(cfg.PluginKey); !ok {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "plugin " + cfg.PluginKey + " is not installed"})
		return
	}
	decl := pluginRemoteDecl(cfg.PluginKey)
	values, err := mergePluginRemoteValues(decl, cfg.Values, req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if values, err = sealPluginRemoteValues(decl, values); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not encrypt the agent config"})
		return
	}
	cfg.Values = values
	if err := pluginRemoteStore().Update(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, pluginRemoteInfo{PluginKey: cfg.PluginKey, Configs: pluginRemoteFields(decl, cfg.Values)})
}

// pluginRemoteSessionFieldsOverride stands in for asking the running plugin
// in tests.
var pluginRemoteSessionFieldsOverride func(cfg pluginremote.Config) ([]wickplugin.SessionField, error)

// pluginRemoteSessionFields asks agent cfg's plugin which values a new
// session takes, defaults resolved from the agent's config.
func pluginRemoteSessionFields(c *tool.Ctx, agentID string, cfg pluginremote.Config) ([]wickplugin.SessionField, error) {
	if pluginRemoteSessionFieldsOverride != nil {
		return pluginRemoteSessionFieldsOverride(cfg)
	}
	values, err := pluginremote.OpenValues(pluginRemoteCodec(), cfg.Values)
	if err != nil {
		return nil, errors.New("could not read this agent's config; save it again")
	}
	src := pluginremote.NewSource(cfg.PluginKey, pluginTransport)
	src.AgentID, src.Config = agentID, values
	return src.SessionFields(c.Context())
}

// pluginSessionOptions is what GET/PUT
// /api/team/agents/{id}/plugin-remote/session-options answer: the fields
// the plugin declares for a new session, the session's values, and whether
// they are locked because the remote session already exists.
type pluginSessionOptions struct {
	Fields []wickplugin.SessionField `json:"fields"`
	Values map[string]string         `json:"values"`
	Locked bool                      `json:"locked"`
}

// pluginSessionDir is the session dir of ?session_id= when it is a chat of
// agent agentID; "" with ok=true when no session was named (a draft chat).
func pluginSessionDir(c *tool.Ctx, agentID string) (dir string, ok bool) {
	sid := c.Query("session_id")
	if sid == "" {
		return "", true
	}
	if globalMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "storage is not ready"})
		return "", false
	}
	s, found := globalMgr.Registry().Session(sid)
	if !found || s.Meta.AgentID != agentID {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return "", false
	}
	return globalLayout.SessionDir(sid), true
}

// loadPluginSessionOptions resolves the agent, its fields and the session
// dir shared by GET and PUT.
func loadPluginSessionOptions(c *tool.Ctx) (dir string, out pluginSessionOptions, ok bool) {
	cfg, ok := loadOwnPluginRemote(c)
	if !ok {
		return "", out, false
	}
	if dir, ok = pluginSessionDir(c, cfg.AgentID); !ok {
		return "", out, false
	}
	fields, err := pluginRemoteSessionFields(c, cfg.AgentID, cfg)
	if err != nil {
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return "", out, false
	}
	out = pluginSessionOptions{Fields: fields, Values: map[string]string{}}
	if out.Fields == nil {
		out.Fields = []wickplugin.SessionField{}
	}
	if dir != "" {
		vals, locked := pluginremote.SessionOptions(dir)
		out.Locked = locked
		for k, v := range vals {
			out.Values[k] = v
		}
	}
	return dir, out, true
}

// apiTeamPluginSessionOptionsGet handles GET
// /api/team/agents/{id}/plugin-remote/session-options[?session_id=].
func apiTeamPluginSessionOptionsGet(c *tool.Ctx) {
	if _, out, ok := loadPluginSessionOptions(c); ok {
		c.JSON(http.StatusOK, out)
	}
}

// apiTeamPluginSessionOptionsPut handles PUT
// /api/team/agents/{id}/plugin-remote/session-options?session_id= {options}:
// the values a new chat's first turn creates the remote session with. An
// empty value means the field's default; once the remote session exists
// the values are locked (409).
func apiTeamPluginSessionOptionsPut(c *tool.Ctx) {
	dir, out, ok := loadPluginSessionOptions(c)
	if !ok {
		return
	}
	if dir == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	var req struct {
		Options map[string]string `json:"options"`
	}
	if !decodeRemoteReq(c, &req) {
		return
	}
	known := map[string]bool{}
	for _, f := range out.Fields {
		known[f.Key] = true
		if f.Required && strings.TrimSpace(req.Options[f.Key]) == "" && f.Default == "" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": f.Label + " is required"})
			return
		}
	}
	for k := range req.Options {
		if !known[k] {
			c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown session field %q", k)})
			return
		}
	}
	switch err := pluginremote.SaveSessionOptions(dir, req.Options); {
	case errors.Is(err, pluginremote.ErrSessionStarted):
		c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out.Values, out.Locked = pluginremote.SessionOptions(dir)
	if out.Values == nil {
		out.Values = map[string]string{}
	}
	c.JSON(http.StatusOK, out)
}

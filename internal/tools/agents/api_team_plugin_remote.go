package agents

import (
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/remote/pluginremote"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	serviceplugin "github.com/yogasw/wick/internal/services/plugin"
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
	return remote.Spawner{Source: pluginremote.NewSource(cfg.PluginKey, pluginTransport)}
}

// pluginSourceItem is one "Plugin" choice of the Remote agent wizard.
type pluginSourceItem struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
	State       string `json:"state"`
}

// apiTeamPluginSources handles GET /api/team/plugin-sources: the service
// plugins that declare remote_source.
func apiTeamPluginSources(c *tool.Ctx) {
	out := []pluginSourceItem{}
	if h := serviceplugin.Default(); h != nil {
		for _, v := range h.RemoteSources() {
			out = append(out, pluginSourceItem{Key: v.Key, Name: v.Name, Description: v.Description, Version: v.Version, State: v.Status.State})
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
	owner := actorID(c)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = firstNonBlank(src.Name, src.Key)
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
	if err := st.Save(pluginremote.Config{AgentID: p.ID, OwnerUserID: owner, PluginKey: src.Key, Usage: pluginremote.UsageByMention}); err != nil {
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

package manager

import (
	"net/http"
	"net/url"
	"sort"
	"time"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/plugins/source"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Origins shown in Admin → Plugins → Installed. "local" is the fallback for
// a plugin on disk with no recorded origin — never guessed.
const (
	originOfficial = connplugin.OriginOfficial
	originSource   = connplugin.OriginSource
	originURLZip   = connplugin.OriginURLZip
	originUpload   = connplugin.OriginUpload
	originLocal    = "local"
)

// installedPlugin is one row of the Installed tab. Every URL in it is
// token-free: source links are the GitHub repo page or the index URL with
// userinfo + query stripped, and private sources never get an asset link.
type installedPlugin struct {
	Key              string     `json:"key"`
	Name             string     `json:"name"`
	Kind             string     `json:"kind"`
	Version          string     `json:"version"`
	Enabled          bool       `json:"enabled"`
	DetailPath       string     `json:"detail_path"`
	Origin           string     `json:"origin"` // official | source | url-zip | upload | local
	SourceID         string     `json:"source_id,omitempty"`
	SourceName       string     `json:"source_name,omitempty"`
	SourceURL        string     `json:"source_url,omitempty"`
	LastCheckAt      *time.Time `json:"last_check_at,omitempty"`
	LatestVersion    string     `json:"latest_version,omitempty"`
	UpdateAvailable  bool       `json:"update_available"`
	DownloadURL      string     `json:"download_url,omitempty"`
	LastHealthAt     *time.Time `json:"last_health_at,omitempty"`
	LastHealthOK     bool       `json:"last_health_ok"`
	LastHealthDetail string     `json:"last_health_detail,omitempty"`
}

// officialSource describes the built-in wick catalog as a read-only source.
type officialSource struct {
	URL         string     `json:"url"`
	Plugins     int        `json:"plugins"`
	LastCheckAt *time.Time `json:"last_check_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type installedListResponse struct {
	Plugins  []installedPlugin `json:"plugins"`
	Official officialSource    `json:"official"`
	IsAdmin  bool              `json:"is_admin"`
}

var detailPathPrefix = map[string]string{
	wickplugin.KindConnector: "/connectors/",
	wickplugin.KindTool:      "/tools/",
	wickplugin.KindJob:       "/jobs/",
	wickplugin.KindService:   "/services/",
}

// apiInstalled lists every installed plugin across kinds with its origin,
// update state and links.
func (h *PluginsHandler) apiInstalled(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	resp := installedListResponse{Plugins: []installedPlugin{}, IsAdmin: user != nil && user.IsAdmin()}

	catalog := map[string]connplugin.Available{}
	if h.registry != nil {
		avail, err := h.registry.List(ctx)
		resp.Official.URL = safeURL(h.registry.URL())
		resp.Official.Plugins = len(avail)
		if err != nil {
			resp.Official.Error = err.Error()
		}
		if t := h.registry.FetchedAt(); !t.IsZero() {
			resp.Official.LastCheckAt = &t
		}
		for _, a := range avail {
			catalog[a.Key] = a
		}
	}

	states := map[string]entity.PluginState{}
	if rows, err := h.store.All(); err == nil {
		for _, s := range rows {
			states[s.Key] = s
		}
	}
	sources := map[string]entity.PluginSource{}
	if h.sources != nil && h.sources.Sources != nil {
		if list, err := h.sources.Sources.List(); err == nil {
			for _, s := range list {
				sources[s.ID] = s
			}
		}
	}

	host := source.HostOSArch()
	for _, kind := range wickplugin.Kinds {
		found, _ := connplugin.ScanKind(connplugin.KindDir(kind), kind)
		for _, f := range found {
			p := installedPlugin{
				Key:        f.Key,
				Name:       f.Manifest.Module.Meta.Name,
				Kind:       kind,
				Version:    f.Manifest.Version,
				Enabled:    true,
				DetailPath: detailPathPrefix[kind] + f.Key,
			}
			st, hasState := states[f.Key]
			if hasState {
				p.Enabled = st.Enabled
				p.LastHealthAt, p.LastHealthOK, p.LastHealthDetail = st.LastHealthAt, st.LastHealthOK, st.LastHealthDetail
			}
			p.Origin = resolveOrigin(st)
			switch p.Origin {
			case originSource, originURLZip:
				p.SourceID = st.SourceID
				if s, ok := sources[st.SourceID]; ok {
					fillFromSource(&p, s, st, host)
				}
			case originOfficial:
				if a, ok := catalog[f.Key]; ok {
					p.LatestVersion = a.Version
					p.UpdateAvailable = connplugin.VersionNewer(a.Version, p.Version)
					p.DownloadURL = safeURL(a.AssetFor(host))
				}
				p.SourceName = "Official wick"
				p.SourceURL = resp.Official.URL
				p.LastCheckAt = resp.Official.LastCheckAt
			}
			resp.Plugins = append(resp.Plugins, p)
		}
	}
	sort.Slice(resp.Plugins, func(i, j int) bool { return resp.Plugins[i].Key < resp.Plugins[j].Key })
	writeJSON(w, http.StatusOK, resp)
}

// resolveOrigin maps the recorded state to a display origin. A linked
// source always wins (Check claims installed plugins by source_id even when
// they were installed another way); otherwise only a recorded origin counts.
func resolveOrigin(st entity.PluginState) string {
	if st.SourceID != "" {
		if st.Origin == originURLZip {
			return originURLZip
		}
		return originSource
	}
	switch st.Origin {
	case originOfficial, originUpload:
		return st.Origin
	}
	return originLocal
}

// fillFromSource adds source name/link, update state and a download link.
func fillFromSource(p *installedPlugin, s entity.PluginSource, st entity.PluginState, host string) {
	p.SourceName = s.Name
	p.LastCheckAt = s.LastCheckAt
	if s.Type == source.TypeURL && source.IsZipURL(s.URL) {
		p.Origin = originURLZip
	}
	if s.Type == source.TypeGitHub {
		p.SourceURL = "https://github.com/" + url.PathEscape(s.Owner) + "/" + url.PathEscape(s.Repo)
	} else {
		p.SourceURL = safeURL(s.URL)
	}
	if st.AvailableVersion != "" && connplugin.VersionNewer(st.AvailableVersion, p.Version) {
		p.LatestVersion, p.UpdateAvailable = st.AvailableVersion, true
	}
	if s.Private {
		// Private assets need the PAT; link the release page instead.
		if s.Type == source.TypeGitHub {
			p.DownloadURL = p.SourceURL + "/releases"
		}
		return
	}
	for _, e := range source.Entries(&s) {
		if e.Key != p.Key {
			continue
		}
		if p.LatestVersion == "" {
			p.LatestVersion = e.Version
		}
		if a, ok := e.Assets[host]; ok {
			p.DownloadURL = safeURL(a.URL)
		}
	}
}

// safeURL drops userinfo, query and fragment so no credential embedded in a
// stored URL ever reaches the browser. Non-http(s) URLs return "".
func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

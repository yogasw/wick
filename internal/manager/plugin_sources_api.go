package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/plugins/source"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// PluginSourcesHandler serves Admin → Plugins: sources (url / GitHub),
// the Available list they offer, zip upload, the six-step Test, Check now,
// and the per-plugin source status the detail pages use for the "Update
// available" badge. Reading is open to any logged-in user; every action is
// admin-only. A source's PAT is write-only — responses only say has_pat.
type PluginSourcesHandler struct {
	Sources *source.Manager
	// Health reports runtime health of an installed key (step 6 of Test).
	Health source.HealthFunc
}

// RegisterRoutes wires /manager/api/plugin-sources and the upload endpoint.
func (h *PluginSourcesHandler) RegisterRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler { return authMidd.RequireAuth(next) }
	admin := func(next http.HandlerFunc) http.Handler { return authMidd.RequireAdmin(next) }
	mux.Handle("GET /manager/api/plugin-sources", auth(h.apiList))
	mux.Handle("POST /manager/api/plugin-sources", admin(h.apiCreate))
	mux.Handle("POST /manager/api/plugin-sources/test", admin(h.apiTestInput))
	mux.Handle("POST /manager/api/plugin-sources/{id}", admin(h.apiEdit))
	mux.Handle("DELETE /manager/api/plugin-sources/{id}", admin(h.apiDelete))
	mux.Handle("POST /manager/api/plugin-sources/{id}/test", admin(h.apiTest))
	mux.Handle("POST /manager/api/plugin-sources/{id}/check", admin(h.apiCheck))
	mux.Handle("POST /manager/api/plugin-sources/{id}/install", admin(h.apiInstall))
	mux.Handle("GET /manager/api/plugin-available", auth(h.apiAvailable))
	mux.Handle("GET /manager/api/plugin-audit", admin(h.apiAudit))
	mux.Handle("POST /manager/api/plugins/upload", admin(h.apiUpload))
	mux.Handle("GET /manager/api/plugins/{key}/source", auth(h.apiPluginSource))
}

// sourceView is the API shape of a source; the PAT never leaves the server.
type sourceView struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	URL             string     `json:"url,omitempty"`
	Repo            string     `json:"repo,omitempty"`
	Private         bool       `json:"private"`
	HasPAT          bool       `json:"has_pat"`
	PubKey          string     `json:"pub_key,omitempty"`
	KeyFilter       string     `json:"key_filter,omitempty"`
	AllowPrerelease bool       `json:"allow_prerelease"`
	AutoUpdate      bool       `json:"auto_update"`
	PollMinutes     int        `json:"poll_minutes"`
	Enabled         bool       `json:"enabled"`
	LastCheckAt     *time.Time `json:"last_check_at,omitempty"`
	LastStatus      string     `json:"last_status,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	Plugins         int        `json:"plugins"`
}

func viewSource(s entity.PluginSource) sourceView {
	v := sourceView{ID: s.ID, Name: s.Name, Type: s.Type, URL: s.URL, Private: s.Private, HasPAT: s.PAT != "",
		PubKey: s.PubKey, KeyFilter: s.KeyFilter, AllowPrerelease: s.AllowPrerelease, AutoUpdate: s.AutoUpdate,
		PollMinutes: s.PollMinutes, Enabled: s.Enabled, LastCheckAt: s.LastCheckAt, LastStatus: s.LastStatus,
		LastError: s.LastError, Plugins: len(source.Entries(&s))}
	if s.Type == source.TypeGitHub {
		v.Repo = s.Owner + "/" + s.Repo
	}
	return v
}

func actor(r *http.Request) string {
	if u := login.GetUser(r.Context()); u != nil {
		return u.Email
	}
	return ""
}

func isAdmin(r *http.Request) bool {
	u := login.GetUser(r.Context())
	return u != nil && u.IsAdmin()
}

func jsonErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (h *PluginSourcesHandler) apiList(w http.ResponseWriter, r *http.Request) {
	list, err := h.Sources.List()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]sourceView, 0, len(list))
	for _, s := range list {
		out = append(out, viewSource(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out, "is_admin": isAdmin(r)})
}

func (h *PluginSourcesHandler) save(w http.ResponseWriter, r *http.Request, id string) {
	var in source.SourceInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	s, err := h.Sources.Save(id, in, actor(r))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, viewSource(*s))
}

// apiCreate runs the same check as Test before saving; a failed check answers
// 422 with the steps and stores nothing, so the form can be fixed and resent.
func (h *PluginSourcesHandler) apiCreate(w http.ResponseWriter, r *http.Request) {
	var in source.SourceInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	s, steps, err := h.Sources.Add(r.Context(), in, actor(r), h.Health)
	var verr *source.ValidationError
	switch {
	case errors.As(err, &verr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "steps": steps})
		return
	case err != nil:
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, viewSource(*s))
}

// apiTestInput checks an unsaved source from the Add form. Nothing is stored;
// ?id= tests an edit so a stored PAT is used when the field is left empty.
func (h *PluginSourcesHandler) apiTestInput(w http.ResponseWriter, r *http.Request) {
	var in source.SourceInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	steps, err := h.Sources.TestInput(r.Context(), r.URL.Query().Get("id"), in, h.Health)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": steps})
}
func (h *PluginSourcesHandler) apiEdit(w http.ResponseWriter, r *http.Request) {
	h.save(w, r, r.PathValue("id"))
}

func (h *PluginSourcesHandler) apiDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.Sources.Delete(r.PathValue("id"), actor(r)); err != nil {
		jsonErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *PluginSourcesHandler) apiTest(w http.ResponseWriter, r *http.Request) {
	s, err := h.Sources.Get(r.PathValue("id"))
	if err != nil {
		jsonErr(w, http.StatusNotFound, err)
		return
	}
	steps := h.Sources.Client.Test(r.Context(), s, h.Health)
	writeJSON(w, http.StatusOK, map[string]any{"steps": steps})
}

func (h *PluginSourcesHandler) apiCheck(w http.ResponseWriter, r *http.Request) {
	res, err := h.Sources.Check(r.Context(), r.PathValue("id"))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *PluginSourcesHandler) apiInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		jsonErr(w, http.StatusBadRequest, fmt.Errorf("key required"))
		return
	}
	in, err := h.Sources.InstallFromSource(r.Context(), r.PathValue("id"), req.Key, actor(r), nil)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": in.Key, "kind": in.Kind, "version": in.Version})
}

// availableView is one plugin offered by a source, with its install status.
type availableView struct {
	SourceID         string   `json:"source_id"`
	SourceName       string   `json:"source_name"`
	Key              string   `json:"key"`
	Kind             string   `json:"kind"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Version          string   `json:"version"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	ArchOK           bool     `json:"arch_ok"`
	OSArch           []string `json:"os_arch"`
}

func (h *PluginSourcesHandler) apiAvailable(w http.ResponseWriter, r *http.Request) {
	list, _ := h.Sources.List()
	installed := installedVersions()
	out := []availableView{}
	for _, s := range list {
		for _, e := range source.Entries(&s) {
			v := availableView{SourceID: s.ID, SourceName: s.Name, Key: e.Key, Kind: wickplugin.NormalizeKind(e.Kind),
				Name: e.Name, Description: e.Description, Version: e.Version, InstalledVersion: installed[e.Key], OSArch: []string{}}
			for oa := range e.Assets {
				v.OSArch = append(v.OSArch, oa)
				v.ArchOK = v.ArchOK || oa == source.HostOSArch()
			}
			out = append(out, v)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": out, "is_admin": isAdmin(r)})
}

func (h *PluginSourcesHandler) apiAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Sources.Audit(100)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// apiUpload installs a multipart "file" zip (100 MB cap) after the same
// verification as any other install.
func (h *PluginSourcesHandler) apiUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20+1<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Errorf("multipart field \"file\" required: %w", err))
		return
	}
	defer f.Close()
	if !strings.HasSuffix(strings.ToLower(hdr.Filename), ".zip") {
		jsonErr(w, http.StatusBadRequest, fmt.Errorf("upload a .zip"))
		return
	}
	tmp, err := os.MkdirTemp("", "wick-plugin-up-*")
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, "upload.zip")
	out, err := os.Create(dst)
	if err == nil {
		_, err = io.Copy(out, f)
		out.Close()
	}
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	in, err := h.Sources.Upload(r.Context(), dst, actor(r))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": in.Key, "kind": in.Kind, "version": in.Version})
}

// pluginSourceView feeds the detail-page badge + kebab "Update to vX".
type pluginSourceView struct {
	Key              string `json:"key"`
	Kind             string `json:"kind"`
	SourceID         string `json:"source_id,omitempty"`
	SourceName       string `json:"source_name,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	AvailableVersion string `json:"available_version,omitempty"`
	IsAdmin          bool   `json:"is_admin"`
}

func (h *PluginSourcesHandler) apiPluginSource(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var st entity.PluginState
	if err := h.Sources.DB.Where("key = ?", key).First(&st).Error; err != nil {
		jsonErr(w, http.StatusNotFound, fmt.Errorf("plugin %q not found", key))
		return
	}
	v := pluginSourceView{Key: key, Kind: st.Kind, SourceID: st.SourceID, InstalledVersion: st.InstalledVersion,
		AvailableVersion: st.AvailableVersion, IsAdmin: isAdmin(r)}
	if st.SourceID != "" {
		if s, err := h.Sources.Get(st.SourceID); err == nil {
			v.SourceName = s.Name
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// updateFromSource is the source branch of POST /plugins/{key}/update: a
// plugin installed from a source updates from that source (JSON or SSE).
func (h *PluginSourcesHandler) updateFromSource(w http.ResponseWriter, r *http.Request, key string) bool {
	if h == nil || h.Sources == nil {
		return false
	}
	var st entity.PluginState
	if err := h.Sources.DB.Where("key = ?", key).First(&st).Error; err != nil || st.SourceID == "" {
		return false
	}
	run := func(ctx context.Context, pf connplugin.ProgressFunc) (source.Installed, error) {
		return h.Sources.Update(ctx, key, actor(r), pf)
	}
	if !wantsSSE(r) {
		in, err := run(r.Context(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": key, "version": in.Version})
		return true
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		_ = rc.Flush()
	}
	in, err := run(r.Context(), func(p connplugin.Progress) { send(p) })
	if err != nil {
		send(map[string]any{"phase": "error", "error": err.Error()})
		return true
	}
	send(map[string]any{"ok": true, "updated": key, "version": in.Version})
	return true
}

// installedVersions maps key → installed version across every kind folder.
func installedVersions() map[string]string {
	out := map[string]string{}
	for _, k := range wickplugin.Kinds {
		found, _ := connplugin.ScanKind(connplugin.KindDir(k), k)
		for _, f := range found {
			out[f.Key] = f.Manifest.Version
		}
	}
	return out
}

// InstalledHealth is the default step-6 check: the installed binary still
// verifies against its manifest, plus whatever runtime probe the caller adds
// (service plugins: supervisor state).
func InstalledHealth(probe func(kind, key string) (bool, string)) source.HealthFunc {
	return func(key string) (bool, bool, string) {
		for _, k := range wickplugin.Kinds {
			found, _ := connplugin.ScanKind(connplugin.KindDir(k), k)
			for _, f := range found {
				if f.Key != key {
					continue
				}
				if err := wickplugin.VerifyManifest(f.Manifest, f.BinaryPath); err != nil {
					return true, false, err.Error()
				}
				if probe != nil {
					ok, detail := probe(k, key)
					return true, ok, "v" + f.Manifest.Version + " " + detail
				}
				return true, true, "v" + f.Manifest.Version + " verified"
			}
		}
		return false, false, ""
	}
}

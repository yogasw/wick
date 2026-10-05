package source

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// DefaultPollMinutes is how often a source is re-checked when unset.
const DefaultPollMinutes = 30

// Manager owns plugin sources: CRUD, checks (with ETag), installs and
// updates from a source, uploads, polling and the audit trail.
type Manager struct {
	DB      *gorm.DB
	Client  *Client
	Encrypt func(string) (string, error)
	Install InstallOptions
	// OnInstalled runs after a plugin landed on disk (reconcile the reloader,
	// restart a service plugin). nil is fine.
	OnInstalled func(ctx context.Context, kind, key string)
	now         func() time.Time
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// List returns every source, newest first.
func (m *Manager) List() ([]entity.PluginSource, error) {
	var out []entity.PluginSource
	err := m.DB.Order("created_at desc").Find(&out).Error
	return out, err
}

// Get loads one source.
func (m *Manager) Get(id string) (*entity.PluginSource, error) {
	var s entity.PluginSource
	if err := m.DB.Where("id = ?", id).First(&s).Error; err != nil {
		return nil, fmt.Errorf("source %q not found", id)
	}
	return &s, nil
}

// SourceInput is the editable part of a source. PAT is plaintext on the way
// in ("" on edit keeps the stored one) and encrypted before it is stored.
type SourceInput struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	URL             string `json:"url"`
	Repo            string `json:"repo"` // owner/repo
	Private         bool   `json:"private"`
	PAT             string `json:"pat"`
	ClearPAT        bool   `json:"clear_pat"`
	PubKey          string `json:"pub_key"`
	KeyFilter       string `json:"key_filter"`
	AllowPrerelease bool   `json:"allow_prerelease"`
	AutoUpdate      bool   `json:"auto_update"`
	PollMinutes     int    `json:"poll_minutes"`
	Enabled         *bool  `json:"enabled"`
}

// Save creates (id=="") or updates a source.
func (m *Manager) Save(id string, in SourceInput, actor string) (*entity.PluginSource, error) {
	s, err := m.build(id, in)
	if err != nil {
		return nil, err
	}
	return m.persist(s, id, actor)
}

// TestInput runs the source health check on in without storing anything, so
// the Add form can be checked before it is saved. id is the source being
// edited ("" for a new one) so a stored PAT is still used when left empty.
func (m *Manager) TestInput(ctx context.Context, id string, in SourceInput, health HealthFunc) ([]Step, error) {
	s, err := m.build(id, in)
	if err != nil {
		return nil, err
	}
	return m.Client.Test(ctx, s, health), nil
}

// ValidationError is an Add rejected by the health check; Steps say where.
type ValidationError struct{ Steps []Step }

func (e *ValidationError) Error() string {
	for _, st := range e.Steps {
		if st.Status == "fail" {
			return fmt.Sprintf("%s: %s", st.Name, st.Message)
		}
	}
	return "source check failed"
}

// Add runs the same check as TestInput and saves only when it passes, so a
// failed Add never leaves a half-made source row behind. "After install" is
// not a gate: it reports on plugins already installed, not on the source.
func (m *Manager) Add(ctx context.Context, in SourceInput, actor string, health HealthFunc) (*entity.PluginSource, []Step, error) {
	s, err := m.build("", in)
	if err != nil {
		return nil, nil, err
	}
	steps := m.Client.Test(ctx, s, health)
	for _, st := range steps {
		if st.Status == "fail" && st.Name != "After install" {
			return nil, steps, &ValidationError{Steps: steps}
		}
	}
	saved, err := m.persist(s, "", actor)
	return saved, steps, err
}

// build applies in onto a new or stored source row without saving it.
func (m *Manager) build(id string, in SourceInput) (*entity.PluginSource, error) {
	s := &entity.PluginSource{Enabled: true}
	if id != "" {
		cur, err := m.Get(id)
		if err != nil {
			return nil, err
		}
		s = cur
	} else {
		s.ID = newID()
	}
	s.Name, s.Type = strings.TrimSpace(in.Name), in.Type
	s.Private, s.PubKey, s.KeyFilter = in.Private, strings.TrimSpace(in.PubKey), strings.TrimSpace(in.KeyFilter)
	s.AllowPrerelease, s.AutoUpdate = in.AllowPrerelease, in.AutoUpdate
	s.PollMinutes = in.PollMinutes
	if s.PollMinutes <= 0 {
		s.PollMinutes = DefaultPollMinutes
	}
	if in.Enabled != nil {
		s.Enabled = *in.Enabled
	}
	switch in.Type {
	case TypeURL:
		s.URL, s.Owner, s.Repo = strings.TrimSpace(in.URL), "", ""
		if s.URL == "" {
			return nil, errors.New("url is required")
		}
	case TypeGitHub:
		owner, repo, ok := strings.Cut(strings.Trim(strings.TrimSpace(in.Repo), "/"), "/")
		if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
			return nil, errors.New("repo must be owner/repo")
		}
		s.Owner, s.Repo, s.URL = owner, repo, ""
	default:
		return nil, fmt.Errorf("type must be %q or %q", TypeURL, TypeGitHub)
	}
	if in.ClearPAT || s.Type != TypeGitHub {
		s.PAT = ""
	}
	if in.PAT != "" && s.Type == TypeGitHub {
		tok := in.PAT
		if m.Encrypt != nil {
			enc, err := m.Encrypt(in.PAT)
			if err != nil {
				return nil, fmt.Errorf("encrypt PAT: %w", err)
			}
			tok = enc
		}
		s.PAT = tok
	}
	if s.Name == "" {
		s.Name = s.Owner + "/" + s.Repo
		if s.Type == TypeURL {
			s.Name = s.URL
		}
	}
	s.ETag = "" // config changed: next check is a full fetch
	return s, nil
}

func (m *Manager) persist(s *entity.PluginSource, id, actor string) (*entity.PluginSource, error) {
	if err := m.DB.Save(s).Error; err != nil {
		return nil, err
	}
	action := "source.update"
	if id == "" {
		action = "source.add"
	}
	m.audit(entity.PluginAudit{Actor: actor, Action: action, SourceID: s.ID, Detail: s.Name})
	return s, nil
}

// Delete removes a source. Installed plugins stay; they just lose updates.
func (m *Manager) Delete(id, actor string) error {
	if err := m.DB.Where("id = ?", id).Delete(&entity.PluginSource{}).Error; err != nil {
		return err
	}
	m.DB.Model(&entity.PluginState{}).Where("source_id = ?", id).
		Updates(map[string]any{"source_id": "", "available_version": ""})
	m.audit(entity.PluginAudit{Actor: actor, Action: "source.remove", SourceID: id})
	return nil
}

// Entries returns the source's cached index (no network).
func Entries(s *entity.PluginSource) []Entry {
	if s.IndexJSON == "" {
		return nil
	}
	var es []Entry
	_ = json.Unmarshal([]byte(s.IndexJSON), &es)
	return es
}

// installedVersions scans every kind folder: key → (kind, version).
func (m *Manager) installedVersions() map[string][2]string {
	out := map[string][2]string{}
	for _, k := range wickplugin.Kinds {
		found, _ := connplugin.ScanKind(m.Install.root(k), k)
		for _, f := range found {
			out[f.Key] = [2]string{k, f.Manifest.Version}
		}
	}
	return out
}

// CheckResult is one source check: the plugins it offers and which installed
// ones now have a newer version.
type CheckResult struct {
	Entries     []Entry  `json:"entries"`
	Updates     []string `json:"updates"`
	NotModified bool     `json:"not_modified"`
	AutoUpdated []string `json:"auto_updated,omitempty"`
}

// Check fetches the source (ETag-aware), caches the index, and flags
// AvailableVersion on installed plugins it provides. With AutoUpdate on, the
// newer versions are installed right away.
func (m *Manager) Check(ctx context.Context, id string) (CheckResult, error) {
	s, err := m.Get(id)
	if err != nil {
		return CheckResult{}, err
	}
	now := m.clock()
	res, ferr := m.Client.Fetch(ctx, s, s.ETag)
	s.LastCheckAt = &now
	if ferr != nil {
		s.LastStatus, s.LastError = "error", ferr.Error()
		m.DB.Save(s)
		return CheckResult{}, ferr
	}
	if !res.NotModified {
		raw, _ := json.Marshal(res.Entries)
		s.IndexJSON, s.ETag = string(raw), res.ETag
	}
	s.LastStatus, s.LastError = "ok", ""
	m.DB.Save(s)

	out := CheckResult{Entries: Entries(s), NotModified: res.NotModified}
	installed := m.installedVersions()
	for _, e := range out.Entries {
		cur, ok := installed[e.Key]
		if !ok {
			continue
		}
		avail := ""
		if newer(e.Version, cur[1]) {
			avail = e.Version
			out.Updates = append(out.Updates, e.Key)
		}
		m.DB.Model(&entity.PluginState{}).Where("key = ? AND (source_id = '' OR source_id IS NULL OR source_id = ?)", e.Key, s.ID).
			Updates(map[string]any{"source_id": s.ID, "available_version": avail})
		if avail != "" && s.AutoUpdate {
			if _, err := m.InstallFromSource(ctx, s.ID, e.Key, "auto-update", nil); err == nil {
				out.AutoUpdated = append(out.AutoUpdated, e.Key)
			} else {
				log.Warn().Err(err).Str("plugin", e.Key).Msg("plugin auto-update failed")
			}
		}
	}
	return out, nil
}

// InstallFromSource installs (or updates) key from source id, records its
// state and audit row, then runs OnInstalled.
func (m *Manager) InstallFromSource(ctx context.Context, id, key, actor string, pf connplugin.ProgressFunc) (Installed, error) {
	s, err := m.Get(id)
	if err != nil {
		return Installed{}, err
	}
	var entry *Entry
	for _, e := range Entries(s) {
		if e.Key == key {
			e := e
			entry = &e
		}
	}
	if entry == nil {
		return Installed{}, fmt.Errorf("%q is not offered by source %s (run Check now)", key, s.Name)
	}
	from := m.installedVersions()[key][1]
	opts := m.Install
	opts.Progress = pf
	in, err := m.Client.InstallEntry(ctx, s, *entry, opts)
	if err != nil {
		m.audit(entity.PluginAudit{Actor: actor, Action: "install.fail", SourceID: id, Key: key, FromVersion: from, ToVersion: entry.Version, Detail: err.Error()})
		return Installed{}, err
	}
	m.recordInstall(ctx, in, id, from, actor)
	return in, nil
}

// Update reinstalls key from the source it came from.
func (m *Manager) Update(ctx context.Context, key, actor string, pf connplugin.ProgressFunc) (Installed, error) {
	var st entity.PluginState
	if err := m.DB.Where("key = ?", key).First(&st).Error; err != nil || st.SourceID == "" {
		return Installed{}, fmt.Errorf("%q was not installed from a source", key)
	}
	return m.InstallFromSource(ctx, st.SourceID, key, actor, pf)
}

// Upload installs an uploaded zip (admin). No source, so no updates.
func (m *Manager) Upload(ctx context.Context, zipPath, actor string) (Installed, error) {
	in, err := InstallZip(zipPath, m.Install)
	if err != nil {
		m.audit(entity.PluginAudit{Actor: actor, Action: "upload.fail", Detail: err.Error()})
		return Installed{}, err
	}
	from := ""
	m.recordInstall(ctx, in, "", from, actor)
	return in, nil
}

func (m *Manager) recordInstall(ctx context.Context, in Installed, sourceID, from, actor string) {
	_ = connplugin.NewStateStore(m.DB).Record(in.Key, in.Kind, in.Version)
	m.DB.Model(&entity.PluginState{}).Where("key = ?", in.Key).
		Updates(map[string]any{"source_id": sourceID, "available_version": "", "origin": m.originOf(sourceID)})
	action := "install"
	if from != "" {
		action = "update"
	}
	if sourceID == "" {
		action = "upload"
	}
	m.audit(entity.PluginAudit{Actor: actor, Action: action, SourceID: sourceID, Key: in.Key, FromVersion: from, ToVersion: in.Version, ZipSHA256: in.ZipSHA256})
	if m.OnInstalled != nil {
		m.OnInstalled(ctx, in.Kind, in.Key)
	}
}

// originOf maps the source an install came from to PluginState.Origin:
// no source = upload, a bare .zip link source = url-zip, anything else =
// source.
func (m *Manager) originOf(sourceID string) string {
	if sourceID == "" {
		return connplugin.OriginUpload
	}
	if s, err := m.Get(sourceID); err == nil && s.Type == TypeURL && isZipURL(s.URL) {
		return connplugin.OriginURLZip
	}
	return connplugin.OriginSource
}

// Audit returns the newest audit rows.
func (m *Manager) Audit(limit int) ([]entity.PluginAudit, error) {
	var out []entity.PluginAudit
	err := m.DB.Order("id desc").Limit(limit).Find(&out).Error
	return out, err
}

func (m *Manager) audit(a entity.PluginAudit) {
	a.At = m.clock()
	if err := m.DB.Create(&a).Error; err != nil {
		log.Warn().Err(err).Str("action", a.Action).Msg("plugin audit write failed")
	}
	log.Info().Str("actor", a.Actor).Str("action", a.Action).Str("source", a.SourceID).Str("plugin", a.Key).
		Str("from", a.FromVersion).Str("to", a.ToVersion).Str("zip_sha256", a.ZipSHA256).Msg("plugin audit")
}

// Due reports whether s should be polled now.
func Due(s entity.PluginSource, now time.Time) bool {
	if !s.Enabled || isZipURL(s.URL) {
		return false
	}
	if s.LastCheckAt == nil {
		return true
	}
	every := s.PollMinutes
	if every <= 0 {
		every = DefaultPollMinutes
	}
	return now.Sub(*s.LastCheckAt) >= time.Duration(every)*time.Minute
}

// Run polls due sources every tick until ctx ends.
func (m *Manager) Run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		srcs, _ := m.List()
		for _, s := range srcs {
			if Due(s, m.clock()) {
				if _, err := m.Check(ctx, s.ID); err != nil {
					log.Debug().Err(err).Str("source", s.Name).Msg("plugin source check failed")
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

package managedbin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/safeexec"
)

// Manager owns every managed type's directory. One process-wide Default.
type Manager struct {
	// Root returns <wick data dir>/providers/bin. Set by the provider
	// package (it knows the app name); tests point it at a temp dir.
	Root func() string
	// KeepVersions is how many non-current versions retention keeps.
	KeepVersions func() int
	// Wrap puts the sandboxed `--version` run in the same memory scope an
	// agent spawn gets. Returns the wrapped argv and a release func. nil =
	// run unwrapped (tests, guard off).
	Wrap func(bin string, args []string) (string, []string, func())
	// Host is the machine asset selection keys on. Swapped in tests.
	Host func() Host
	// InUse counts running processes per version under a versions dir.
	InUse func(versionsDir string) map[string]int
	// Enabled gates the background release check per type. nil = all.
	Enabled func(typ string) bool

	client *client
	prep   hostPrep

	mu      sync.Mutex
	jobs    map[string]*JobInfo
	typeMus map[string]*sync.Mutex

	// Release cache (latestcache.go), guarded by mu; cacheMu serialises
	// writes of the cache file.
	cacheMu     sync.Mutex
	cacheLoaded bool
	snaps       map[string]LatestSnapshot
	inflight    map[string]chan struct{}
	forcedAt    map[string]time.Time
}

// New returns a Manager with production defaults (Root must be set).
func New() *Manager {
	return &Manager{
		KeepVersions: func() int { return 2 },
		Host:         DetectHost,
		InUse:        scanInUse,
		client:       newClient(),
		prep:         defaultHostPrep,
		jobs:         map[string]*JobInfo{},
		typeMus:      map[string]*sync.Mutex{},
		snaps:        map[string]LatestSnapshot{},
		inflight:     map[string]chan struct{}{},
		forcedAt:     map[string]time.Time{},
	}
}

// Default is the process-wide manager.
var Default = New()

func (m *Manager) root() string {
	if m.Root == nil {
		return ""
	}
	return m.Root()
}

func (m *Manager) typeLock(typ string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.typeMus[typ]
	if !ok {
		l = &sync.Mutex{}
		m.typeMus[typ] = l
	}
	return l
}

// CurrentPath is the binary spawn should run for typ: the `current`
// version's file, when one is installed and still on disk.
func (m *Manager) CurrentPath(typ string) (path, version string, ok bool) {
	src, found := Lookup(typ)
	if !found || m.root() == "" {
		return "", "", false
	}
	ver := m.readCurrent(typ)
	if ver == "" || validVersion(ver) != nil {
		return "", "", false
	}
	p := m.binPath(typ, ver, src.Binary())
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", "", false
	}
	return p, ver, true
}

// ── Status ────────────────────────────────────────────────────────────

// InstalledView is one installed version plus live facts.
type InstalledView struct {
	VersionInfo
	Current   bool `json:"current"`
	InUse     int  `json:"in_use"`
	Removable bool `json:"removable"`
}

// Status is everything the Binary section renders for one type.
type Status struct {
	Type            string          `json:"type"`
	Binary          string          `json:"binary"`
	Repo            string          `json:"repo"`
	Host            Host            `json:"host"`
	HostLabel       string          `json:"host_label"`
	Current         string          `json:"current"`
	CurrentPath     string          `json:"current_path"`
	Installed       []InstalledView `json:"installed"`
	Latest          *Release        `json:"latest,omitempty"`
	Releases        []Release       `json:"releases,omitempty"`
	LatestCheckedAt time.Time       `json:"latest_checked_at,omitempty"`
	LatestErr       string          `json:"latest_err,omitempty"`
	UpdateAvailable bool            `json:"update_available"`
	Job             *JobInfo        `json:"job,omitempty"`
	LastJob         *JobInfo        `json:"last_job,omitempty"`
}

// Status reports typ without touching the network: the release facts are
// whatever the background cache last learned.
func (m *Manager) Status(typ string) (Status, error) {
	src, ok := Lookup(typ)
	if !ok {
		return Status{}, fmt.Errorf("%s is not a managed binary type", typ)
	}
	h := m.Host()
	out := Status{Type: typ, Binary: src.Binary(), Repo: src.Repo(), Host: h, HostLabel: h.Label()}
	st := m.loadState(typ)
	out.LastJob = st.LastJob
	cur := m.readCurrent(typ)
	if p, v, ok := m.CurrentPath(typ); ok {
		out.Current, out.CurrentPath = v, p
	} else {
		cur = ""
	}
	inUse := m.InUse(m.versionsDir(typ))
	for _, vi := range st.Versions {
		iv := InstalledView{VersionInfo: vi, Current: vi.Version == cur, InUse: inUse[vi.Version]}
		iv.Removable = !iv.Current && iv.InUse == 0
		out.Installed = append(out.Installed, iv)
	}
	sort.Slice(out.Installed, func(i, j int) bool {
		return out.Installed[i].InstalledAt.After(out.Installed[j].InstalledAt)
	})
	if s, ok := m.LatestSnapshot(typ); ok {
		out.LatestCheckedAt, out.LatestErr, out.Releases = s.FetchedAt, s.Err, s.Releases
		if s.Latest != nil {
			r := *s.Latest
			out.Latest = &r
			out.UpdateAvailable = cur != "" && r.Version != "" && r.Version != cur
		}
	}
	m.mu.Lock()
	if j := m.jobs[typ]; j != nil {
		c := *j
		out.Job = &c
	}
	m.mu.Unlock()
	return out, nil
}

// Releases lists recent releases from the cache (for "pick a specific
// version"). No network; the background loop fills it.
func (m *Manager) Releases(typ string) ([]Release, error) {
	if _, ok := Lookup(typ); !ok {
		return nil, fmt.Errorf("%s is not a managed binary type", typ)
	}
	s, _ := m.LatestSnapshot(typ)
	return s.Releases, snapErr(s)
}

// ── Jobs ──────────────────────────────────────────────────────────────

// Job phases, in order.
const (
	PhaseResolve  = "resolve"
	PhaseDownload = "download"
	PhaseVerify   = "verify"
	PhaseProbe    = "probe"
	PhaseSave     = "save"
	PhaseSwitch   = "switching"
	PhaseDone     = "done"
	PhaseError    = "error"
)

// JobInfo is the progress of one download, polled by the UI. Activate
// says whether the job also switches `current` when it succeeds.
type JobInfo struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Tag      string `json:"tag,omitempty"`
	Version  string `json:"version,omitempty"`
	Activate bool   `json:"activate"`
	Phase    string `json:"phase"`
	Done     int64  `json:"done"`
	Total    int64  `json:"total"`
	// BytesPerSec is the average download speed so far.
	BytesPerSec int64     `json:"bytes_per_sec,omitempty"`
	Message     string    `json:"message,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
}

// Running reports whether the job is still in flight.
func (j *JobInfo) Running() bool { return j != nil && j.Phase != PhaseDone && j.Phase != PhaseError }

// InflightCount is the number of running jobs, for upgrade.Register:
// a reload waits for an install instead of cutting a download in half.
func (m *Manager) InflightCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		if j.Running() {
			n++
		}
	}
	return n
}

// ErrJobRunning is returned when typ already has an install in flight.
var ErrJobRunning = errors.New("an install is already running for this type")

// StartInstall launches a background job for tag ("" = latest) and
// returns immediately; the HTTP handler never blocks on the download.
// activate=false is download-only: the version is fetched, verified and
// stored but `current` stays — except on a first install, where there is
// nothing to switch away from. On ErrJobRunning the returned job is the
// one already in flight.
func (m *Manager) StartInstall(typ, tag string, activate bool) (*JobInfo, error) {
	if _, ok := Lookup(typ); !ok {
		return nil, fmt.Errorf("%s is not a managed binary type", typ)
	}
	if m.root() == "" {
		return nil, errors.New("wick data dir unknown")
	}
	m.mu.Lock()
	if j := m.jobs[typ]; j.Running() {
		c := *j
		m.mu.Unlock()
		return &c, ErrJobRunning
	}
	j := &JobInfo{ID: fmt.Sprintf("%s-%d", typ, time.Now().UnixNano()), Type: typ, Tag: tag, Activate: activate, Phase: PhaseResolve, StartedAt: time.Now()}
	m.jobs[typ] = j
	// Copied before the job starts: install mutates j under m.mu.
	c := *j
	m.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		err := m.install(ctx, typ, tag, j)
		m.finish(typ, j, err)
	}()
	return &c, nil
}

// Install downloads and activates synchronously (tests, CLI). Same flow
// as the job.
func (m *Manager) Install(ctx context.Context, typ, tag string) (*JobInfo, error) {
	return m.run(ctx, typ, tag, true)
}

// Download is the synchronous download-only job (tests, CLI).
func (m *Manager) Download(ctx context.Context, typ, tag string) (*JobInfo, error) {
	return m.run(ctx, typ, tag, false)
}

func (m *Manager) run(ctx context.Context, typ, tag string, activate bool) (*JobInfo, error) {
	j := &JobInfo{Type: typ, Tag: tag, Activate: activate, Phase: PhaseResolve, StartedAt: time.Now()}
	err := m.install(ctx, typ, tag, j)
	m.finish(typ, j, err)
	return j, err
}

func (m *Manager) setJob(j *JobInfo, f func(*JobInfo)) {
	m.mu.Lock()
	f(j)
	m.mu.Unlock()
}

func (m *Manager) finish(typ string, j *JobInfo, err error) {
	m.setJob(j, func(j *JobInfo) {
		j.FinishedAt = time.Now()
		if err != nil {
			j.Phase, j.Error = PhaseError, err.Error()
		} else {
			j.Phase = PhaseDone
		}
	})
	l := m.typeLock(typ)
	l.Lock()
	st := m.loadState(typ)
	m.mu.Lock()
	c := *j
	m.mu.Unlock()
	st.LastJob = &c
	if serr := m.saveState(typ, st); serr != nil {
		log.Warn().Err(serr).Str("type", typ).Msg("managedbin: save last job failed")
	}
	l.Unlock()
	if err != nil {
		log.Warn().Err(err).Str("type", typ).Str("tag", j.Tag).Msg("managedbin: install failed")
	} else {
		log.Info().Str("type", typ).Str("version", j.Version).Str("msg", j.Message).Msg("managedbin: install ok")
	}
}

// install is the fixed, security-relevant order. Do not reorder:
//  1. download to versions/<ver>.partial (https to GitHub only, size cap)
//  2. sha256 == GitHub digest (+ source cross-check); mismatch → delete,
//     never executed
//  3. only then `--version` in a sandbox
//  4. parsed version == tag, else delete; current untouched
//  5. rename to versions/<ver>, then — only when the job activates, or
//     nothing is current yet — move current
func (m *Manager) install(ctx context.Context, typ, tag string, j *JobInfo) error {
	src, _ := Lookup(typ)
	l := m.typeLock(typ)
	l.Lock()
	defer l.Unlock()

	var rel Release
	var err error
	if tag == "" {
		rel, err = m.client.latest(ctx, src.Repo())
	} else {
		rel, err = m.client.byTag(ctx, src.Repo(), tag)
	}
	if err != nil {
		return fmt.Errorf("release lookup: %w", err)
	}
	ver := rel.Version
	if err := validVersion(ver); err != nil {
		return err
	}
	m.setJob(j, func(j *JobInfo) { j.Tag, j.Version = rel.Tag, ver })

	_, _, hasCurrent := m.CurrentPath(typ)
	activate := j.Activate || !hasCurrent
	st := m.loadState(typ)
	if _, have := st.Versions[ver]; have {
		// Already on disk: no download. Re-activating still re-verifies.
		if !activate {
			m.setJob(j, func(j *JobInfo) { j.Message = "already downloaded" })
			return nil
		}
		if m.readCurrent(typ) == ver {
			if _, _, ok := m.CurrentPath(typ); ok {
				m.setJob(j, func(j *JobInfo) { j.Message = "already installed and active" })
				return nil
			}
		}
		if err := m.activateLocked(typ, ver); err != nil {
			return err
		}
		m.setJob(j, func(j *JobInfo) { j.Message = "already installed — switched without downloading" })
		return nil
	}

	h := m.Host()
	asset, err := src.PickAsset(h, rel.Assets)
	if err != nil {
		return fmt.Errorf("%s (%s): %w", rel.Tag, h.Label(), err)
	}
	if asset.SHA256 == "" {
		return fmt.Errorf("%s has no sha256 digest in the GitHub API; refusing to install", asset.Name)
	}
	if asset.Size > MaxDownloadBytes {
		return fmt.Errorf("%s is %d bytes, over the %d byte limit", asset.Name, asset.Size, int64(MaxDownloadBytes))
	}

	// 1. download
	partial := filepath.Join(m.versionsDir(typ), ver+".partial")
	_ = os.RemoveAll(partial)
	if err := os.MkdirAll(partial, 0o700); err != nil {
		return err
	}
	cleanup := func() { _ = os.RemoveAll(partial) }
	m.setJob(j, func(j *JobInfo) { j.Phase, j.Total = PhaseDownload, asset.Size })
	dl := filepath.Join(partial, "download")
	dlStart := time.Now()
	got, err := m.client.download(ctx, asset.URL, dl, MaxDownloadBytes, func(done, total int64) {
		m.setJob(j, func(j *JobInfo) {
			j.Done = done
			if total > 0 {
				j.Total = total
			}
			if el := time.Since(dlStart).Seconds(); el > 0.2 {
				j.BytesPerSec = int64(float64(done) / el)
			}
		})
	})
	if err != nil {
		cleanup()
		return fmt.Errorf("download %s: %w", asset.Name, err)
	}

	// 2. verify — before anything could execute it
	m.setJob(j, func(j *JobInfo) { j.Phase = PhaseVerify })
	if !strings.EqualFold(got, asset.SHA256) {
		cleanup()
		return fmt.Errorf("sha256 mismatch for %s: got %s, GitHub says %s — deleted, not executed", asset.Name, got, asset.SHA256)
	}
	if err := src.CrossCheck(ctx, m.client, rel, asset, got); err != nil {
		cleanup()
		return fmt.Errorf("cross-check %s: %w — deleted, not executed", asset.Name, err)
	}
	bin := filepath.Join(partial, src.Binary())
	if err := src.Unpack(dl, bin); err != nil {
		cleanup()
		return fmt.Errorf("unpack %s: %w", asset.Name, err)
	}
	_ = os.Remove(dl)
	if err := os.Chmod(bin, 0o755); err != nil {
		cleanup()
		return err
	}
	if err := m.prep.prepareBinary(ctx, h, bin); err != nil {
		keepForInspection(partial, "prepare: "+err.Error())
		return fmt.Errorf("%s (%s): %w — kept in %s", src.Binary(), h.Label(), err, partial)
	}
	binSum, err := fileSHA256(bin)
	if err != nil {
		cleanup()
		return err
	}

	// 3 + 4. sandboxed --version, must equal the tag
	m.setJob(j, func(j *JobInfo) { j.Phase = PhaseProbe })
	// A failed probe keeps .partial (+ probe.log) for inspection; it is
	// never activated and the next install of this version replaces it.
	out, parsed, err := m.runVersion(ctx, typ, bin)
	if err != nil {
		err = m.prep.explainExecError(h, bin, err)
		keepForInspection(partial, "--version: "+err.Error()+"\n"+out)
		return fmt.Errorf("%s --version: %w — kept in %s", src.Binary(), err, partial)
	}
	if !MatchesTag(rel.Tag, parsed) {
		keepForInspection(partial, "--version: "+out)
		return fmt.Errorf("%s --version reported %q, expected %s — not installed, kept in %s", src.Binary(), parsed, rel.Tag, partial)
	}

	// 5. publish, then (maybe) switch
	m.setJob(j, func(j *JobInfo) { j.Phase = PhaseSave })
	final := filepath.Join(m.versionsDir(typ), ver)
	_ = os.RemoveAll(final)
	if err := os.Rename(partial, final); err != nil {
		cleanup()
		return err
	}
	st = m.loadState(typ)
	st.Versions[ver] = VersionInfo{
		Version: ver, Tag: rel.Tag, Asset: asset.Name, SHA256: binSum, AssetSHA256: got,
		Size: asset.Size, InstalledAt: time.Now().UTC(), Host: h, VersionOutput: out,
	}
	if err := m.saveState(typ, st); err != nil {
		return err
	}
	if !activate {
		m.setJob(j, func(j *JobInfo) { j.Message = "downloaded " + out })
		m.pruneLocked(typ)
		return nil
	}
	m.setJob(j, func(j *JobInfo) { j.Phase = PhaseSwitch })
	if err := writeAtomic(m.currentPath(typ), []byte(ver+"\n")); err != nil {
		return err
	}
	m.setJob(j, func(j *JobInfo) { j.Message = "installed " + out })
	m.pruneLocked(typ)
	return nil
}

// keepForInspection records why a .partial was left behind.
func keepForInspection(partial, msg string) {
	_ = os.WriteFile(filepath.Join(partial, "probe.log"), []byte(time.Now().UTC().Format(time.RFC3339)+" "+msg+"\n"), 0o600)
}

// scopeBusEnv is what the memory-scope wrapper (systemd-run --user --scope)
// needs to reach the user manager. Without it the wrapped --version dies
// with "Failed to connect to bus: No medium found" before the binary runs.
// Both are locations, not credentials, so they are safe to hand on.
func scopeBusEnv() []string {
	var out []string
	for _, k := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// ErrVersionOutput is wrapped when a binary's output cannot be parsed.
var ErrVersionOutput = errors.New("unrecognised --version output")

// runVersion runs the contract argv with a 15s timeout, an empty env (no
// DATABASE_URL, tokens or real HOME — HOME/TMPDIR/XDG point at a scratch
// dir), cwd = that scratch dir, inside the agent memory scope when Wrap
// is set.
func (m *Manager) runVersion(ctx context.Context, typ, bin string) (raw, parsed string, err error) {
	c, ok := ContractFor(typ)
	if !ok {
		return "", "", fmt.Errorf("no version contract for %s", typ)
	}
	scratchRoot := filepath.Join(m.root(), ".tmp")
	if err := os.MkdirAll(scratchRoot, 0o700); err != nil {
		return "", "", err
	}
	scratch, err := os.MkdirTemp(scratchRoot, typ+"-verify-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(scratch)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	execBin, argv, release := bin, c.Args, func() {}
	if m.Wrap != nil {
		execBin, argv, release = m.Wrap(bin, c.Args)
	}
	defer release()
	cmd := safeexec.CommandContext(cctx, execBin, argv...)
	cmd.Dir = scratch
	cmd.Env = []string{
		"HOME=" + scratch, "TMPDIR=" + scratch,
		"XDG_CONFIG_HOME=" + filepath.Join(scratch, "config"),
		"XDG_DATA_HOME=" + filepath.Join(scratch, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(scratch, "cache"),
		"XDG_STATE_HOME=" + filepath.Join(scratch, "state"),
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"NO_COLOR=1", "TERM=dumb",
		// A freshly downloaded CLI must not replace itself (opencode's
		// cli/upgrade.ts honours this; harmless for the others).
		"OPENCODE_DISABLE_AUTOUPDATE=true",
	}
	if m.Wrap != nil {
		cmd.Env = append(cmd.Env, scopeBusEnv()...)
	}
	b, err := cmd.CombinedOutput()
	raw = strings.TrimSpace(string(b))
	if first, _, _ := strings.Cut(raw, "\n"); first != "" {
		raw = strings.TrimSpace(first)
	}
	if cctx.Err() != nil {
		return raw, "", fmt.Errorf("timed out after 15s")
	}
	if err != nil {
		return raw, "", fmt.Errorf("%w: %s", err, raw)
	}
	v, ok := c.Parse(string(b))
	if !ok {
		return raw, "", fmt.Errorf("%w: %q", ErrVersionOutput, raw)
	}
	return raw, v, nil
}

// VerifyCurrent re-runs the sandboxed `--version` against the active
// binary (the "Re-check" button) and reports the raw line.
func (m *Manager) VerifyCurrent(ctx context.Context, typ string) (raw string, err error) {
	p, ver, ok := m.CurrentPath(typ)
	if !ok {
		return "", errors.New("no managed version installed")
	}
	raw, parsed, err := m.runVersion(ctx, typ, p)
	if err != nil {
		return raw, err
	}
	if parsed != ver {
		return raw, fmt.Errorf("binary reports %s but %s is active", parsed, ver)
	}
	return raw, nil
}

// ── Activate / remove / retention ─────────────────────────────────────

// ErrTampered means an installed file no longer matches its recorded sha256.
var ErrTampered = errors.New("installed binary changed on disk since install")

// Activate makes an installed version current (rollback). No download;
// the file's sha256 is recomputed and must match state.json.
func (m *Manager) Activate(typ, ver string) error {
	l := m.typeLock(typ)
	l.Lock()
	defer l.Unlock()
	return m.activateLocked(typ, ver)
}

func (m *Manager) activateLocked(typ, ver string) error {
	src, ok := Lookup(typ)
	if !ok {
		return fmt.Errorf("%s is not a managed binary type", typ)
	}
	if err := validVersion(ver); err != nil {
		return err
	}
	st := m.loadState(typ)
	vi, ok := st.Versions[ver]
	if !ok {
		return fmt.Errorf("%s %s is not installed", typ, ver)
	}
	sum, err := fileSHA256(m.binPath(typ, ver, src.Binary()))
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, vi.SHA256) {
		return fmt.Errorf("%w: %s %s sha256 %s, recorded %s", ErrTampered, typ, ver, sum, vi.SHA256)
	}
	if err := writeAtomic(m.currentPath(typ), []byte(ver+"\n")); err != nil {
		return err
	}
	m.pruneLocked(typ)
	return nil
}

// Remove deletes an installed version. Refused for current or a version
// a running process still uses.
func (m *Manager) Remove(typ, ver string) error {
	l := m.typeLock(typ)
	l.Lock()
	defer l.Unlock()
	if err := validVersion(ver); err != nil {
		return err
	}
	if m.readCurrent(typ) == ver {
		return fmt.Errorf("%s %s is the active version", typ, ver)
	}
	if n := m.InUse(m.versionsDir(typ))[ver]; n > 0 {
		return fmt.Errorf("%s %s is still used by %d running process(es)", typ, ver, n)
	}
	return m.removeLocked(typ, ver)
}

func (m *Manager) removeLocked(typ, ver string) error {
	if err := os.RemoveAll(filepath.Join(m.versionsDir(typ), ver)); err != nil {
		return err
	}
	st := m.loadState(typ)
	delete(st.Versions, ver)
	return m.saveState(typ, st)
}

// Prune applies retention now (e.g. after a process on an old version ended).
func (m *Manager) Prune(typ string) {
	l := m.typeLock(typ)
	l.Lock()
	defer l.Unlock()
	m.pruneLocked(typ)
}

// pruneLocked keeps current + the KeepVersions most recently installed
// others; older ones go only when no process runs them (a later Prune
// catches them once their process ends).
func (m *Manager) pruneLocked(typ string) {
	keep := 2
	if m.KeepVersions != nil {
		keep = m.KeepVersions()
	}
	if keep < 0 {
		keep = 0
	}
	cur := m.readCurrent(typ)
	st := m.loadState(typ)
	var others []VersionInfo
	for _, vi := range st.Versions {
		if vi.Version != cur {
			others = append(others, vi)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i].InstalledAt.After(others[j].InstalledAt) })
	if len(others) <= keep {
		return
	}
	inUse := m.InUse(m.versionsDir(typ))
	for _, vi := range others[keep:] {
		if inUse[vi.Version] > 0 {
			continue
		}
		if err := m.removeLocked(typ, vi.Version); err != nil {
			log.Warn().Err(err).Str("type", typ).Str("version", vi.Version).Msg("managedbin: prune failed")
		}
	}
}

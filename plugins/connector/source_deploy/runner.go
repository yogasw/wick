// Runner: runs the deploy-from-source script (scripts/deploy-from-source.sh,
// .ps1 on Windows) on this machine in the background: build a branch, a fork or a local
// checkout and install it the way the script already does — zero-downtime reload where
// the daemon supports it — without a release, a terminal or an agent working it out.
//
// The script stays the one place that knows how to build and install. This plugin only
// fetches it, starts it detached from wick (it replaces the very process that started
// it), keeps its log and exit code on disk, and reports how it ended. Everything is a
// file under the plugin's data dir, so the plugin process wick spawns after the swap reads
// the same state the old one wrote.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultScriptURL is the script used when a profile names none: the one in wick's own
// repo, .sh or .ps1 by the OS this runs on.
const DefaultScriptURL = "https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source"

// keepRuns is how many finished runs stay on disk; older logs are deleted.
const keepRuns = 20

// Run states.
const (
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCanceled  = "canceled"
)

// Profile is a saved way to deploy: which script, with which arguments. Saved once (by an
// agent or the UI) so an update afterwards is one call with no parameters.
type Profile struct {
	Name      string    `json:"name"`
	Script    string    `json:"script,omitempty"` // https URL or a local path; empty = DefaultScriptURL
	Args      []string  `json:"args,omitempty"`
	Default   bool      `json:"default,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Run is one execution of a profile.
type Run struct {
	ID         string     `json:"id"`
	Profile    string     `json:"profile"`
	Script     string     `json:"script"`
	ScriptSHA  string     `json:"script_sha256,omitempty"`
	Args       []string   `json:"args,omitempty"`
	StartedBy  string     `json:"started_by,omitempty"`
	SessionID  string     `json:"session_id,omitempty"`
	PID        int        `json:"pid"`
	State      string     `json:"state"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Status is a run plus what its log says so far.
type Status struct {
	Run
	Stage string `json:"stage,omitempty"` // the last "== …" line the script printed
	Tail  string `json:"tail,omitempty"`
}

// Service owns the profiles and runs under one directory.
type Service struct {
	dir string
	mu  sync.Mutex
	// Fetch downloads a script URL. Swappable for tests.
	Fetch func(ctx context.Context, url string) ([]byte, error)
}

// New returns a Service storing its files under dir.
func New(dir string) *Service {
	return &Service{dir: dir, Fetch: httpFetch}
}

func (s *Service) runsDir() string      { return filepath.Join(s.dir, "runs") }
func (s *Service) profilesPath() string { return filepath.Join(s.dir, "profiles.json") }
func (s *Service) runPath(id string) string {
	return filepath.Join(s.runsDir(), id+".json")
}
func (s *Service) logPath(id string) string  { return filepath.Join(s.runsDir(), id+".log") }
func (s *Service) exitPath(id string) string { return filepath.Join(s.runsDir(), id+".exit") }

// ---------------------------------------------------------------- profiles

// Profiles lists the saved profiles, the default first.
func (s *Service) Profiles() ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadProfiles()
}

func (s *Service) loadProfiles() ([]Profile, error) {
	b, err := os.ReadFile(s.profilesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []Profile
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.profilesPath(), err)
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Default != ps[j].Default {
			return ps[i].Default
		}
		return ps[i].Name < ps[j].Name
	})
	return ps, nil
}

func (s *Service) storeProfiles(ps []Profile) error {
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.profilesPath(), b)
}

// SaveProfile creates or replaces the profile with p.Name. The first profile saved, or one
// saved with Default, becomes the default.
func (s *Service) SaveProfile(p Profile) (Profile, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return Profile{}, errors.New("name is required")
	}
	p.Script = strings.TrimSpace(p.Script)
	if p.Script != "" && !isURL(p.Script) {
		abs, err := expandPath(p.Script)
		if err != nil {
			return Profile{}, err
		}
		if _, err := os.Stat(abs); err != nil {
			return Profile{}, fmt.Errorf("script %s: %w", abs, err)
		}
		p.Script = abs
	}
	if p.Script != "" && isURL(p.Script) && !strings.HasPrefix(p.Script, "https://") {
		return Profile{}, errors.New("script URL must be https")
	}
	p.UpdatedAt = time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.loadProfiles()
	if err != nil {
		return Profile{}, err
	}
	out := ps[:0]
	for _, x := range ps {
		if x.Name != p.Name {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		p.Default = true
	}
	if p.Default {
		for i := range out {
			out[i].Default = false
		}
	}
	out = append(out, p)
	if err := s.storeProfiles(out); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// DeleteProfile removes a profile. Deleting the default makes the next one the default.
func (s *Service) DeleteProfile(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.loadProfiles()
	if err != nil {
		return err
	}
	out, found, wasDefault := ps[:0], false, false
	for _, x := range ps {
		if x.Name == name {
			found, wasDefault = true, x.Default
			continue
		}
		out = append(out, x)
	}
	if !found {
		return fmt.Errorf("no profile %q", name)
	}
	if wasDefault && len(out) > 0 {
		out[0].Default = true
	}
	return s.storeProfiles(out)
}

// resolveProfile picks name, or the default when name is empty. With no profile saved at
// all an empty name still works: the default script with no arguments.
func (s *Service) resolveProfile(name string) (Profile, error) {
	ps, err := s.loadProfiles()
	if err != nil {
		return Profile{}, err
	}
	name = strings.TrimSpace(name)
	for _, p := range ps {
		if (name == "" && p.Default) || (name != "" && p.Name == name) {
			return p, nil
		}
	}
	if name == "" {
		return Profile{Name: "default"}, nil
	}
	return Profile{}, fmt.Errorf("no profile %q (list_profiles shows the saved ones)", name)
}

// ---------------------------------------------------------------- runs

// StartOptions overrides a profile for one run.
type StartOptions struct {
	Profile   string
	ExtraArgs []string // appended after the profile's args
	StartedBy string
	SessionID string
}

// Start launches the profile's script detached and returns at once. Only one run at a
// time: two builds at once is what freezes a small machine, and two swaps race.
func (s *Service) Start(ctx context.Context, o StartOptions) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.activeLocked(); ok {
		return Run{}, fmt.Errorf("run %s is still %s (started %s) — wait for it or cancel it first",
			r.ID, r.State, r.StartedAt.Format(time.RFC3339))
	}
	p, err := s.resolveProfile(o.Profile)
	if err != nil {
		return Run{}, err
	}
	if err := os.MkdirAll(s.runsDir(), 0o700); err != nil {
		return Run{}, err
	}
	id := newRunID()
	script, err := s.materializeScript(ctx, p.Script, id)
	if err != nil {
		return Run{}, err
	}
	sum, err := fileSHA256(script)
	if err != nil {
		return Run{}, err
	}
	args := append(append([]string{}, p.Args...), o.ExtraArgs...)
	r := Run{
		ID:        id,
		Profile:   p.Name,
		Script:    scriptLabel(p.Script),
		ScriptSHA: sum,
		Args:      args,
		StartedBy: o.StartedBy,
		SessionID: o.SessionID,
		State:     StateRunning,
		StartedAt: time.Now().UTC(),
	}
	logf, err := os.OpenFile(s.logPath(id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Run{}, err
	}
	defer logf.Close()
	fmt.Fprintf(logf, "== wick source deploy %s: profile %s, script %s (sha256 %s)\n   args: %s\n",
		id, p.Name, r.Script, sum[:12], strings.Join(args, " "))
	pid, err := launch(script, args, s.exitPath(id), id, logf)
	if err != nil {
		return Run{}, fmt.Errorf("start script: %w", err)
	}
	r.PID = pid
	if err := s.saveRun(r); err != nil {
		return Run{}, err
	}
	s.pruneLocked()
	return r, nil
}

// materializeScript returns a local path to run: a URL is downloaded next to the run's
// log (so the bytes that ran are the ones whose hash is recorded), a path is used as is.
func (s *Service) materializeScript(ctx context.Context, src, id string) (string, error) {
	if src == "" {
		src = DefaultScriptURL + scriptExt()
	}
	if !isURL(src) {
		return src, nil
	}
	b, err := s.Fetch(ctx, src)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", src, err)
	}
	ext := filepath.Ext(strings.SplitN(src, "?", 2)[0])
	if ext == "" {
		ext = scriptExt()
	}
	path := filepath.Join(s.runsDir(), id+".script"+ext)
	if err := os.WriteFile(path, b, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// Cancel stops a running run (its whole process tree).
func (s *Service) Cancel(id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.pickLocked(id)
	if err != nil {
		return Run{}, err
	}
	if r.State != StateRunning {
		return r, fmt.Errorf("run %s already %s", r.ID, r.State)
	}
	if err := killTree(r.PID); err != nil && processAlive(r.PID) {
		return r, fmt.Errorf("stop pid %d: %w", r.PID, err)
	}
	now := time.Now().UTC()
	r.State, r.FinishedAt = StateCanceled, &now
	return r, s.saveRun(r)
}

// Get returns one run with its stage and the last lines of its log. id empty = the newest.
func (s *Service) Get(id string, lines int) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.pickLocked(id)
	if err != nil {
		return Status{}, err
	}
	r = s.refreshLocked(r)
	return s.status(r, lines), nil
}

// Runs lists runs, newest first.
func (s *Service) Runs(limit int) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.listLocked()
	if err != nil {
		return nil, err
	}
	for i := range rs {
		rs[i] = s.refreshLocked(rs[i])
	}
	if limit > 0 && len(rs) > limit {
		rs = rs[:limit]
	}
	return rs, nil
}

// refreshLocked moves a running run to its final state once the script wrote its exit
// code, or once its process is gone without one (killed, machine rebooted).
func (s *Service) refreshLocked(r Run) Run {
	if r.State != StateRunning {
		return r
	}
	if b, err := os.ReadFile(s.exitPath(r.ID)); err == nil {
		code, perr := strconv.Atoi(strings.TrimSpace(string(b)))
		if perr != nil {
			code = -1
		}
		now := time.Now().UTC()
		if fi, err := os.Stat(s.exitPath(r.ID)); err == nil {
			now = fi.ModTime().UTC()
		}
		r.ExitCode, r.FinishedAt = &code, &now
		r.State = StateSucceeded
		if code != 0 {
			r.State = StateFailed
		}
		_ = s.saveRun(r)
		return r
	}
	if !processAlive(r.PID) {
		now := time.Now().UTC()
		r.State, r.FinishedAt = StateFailed, &now
		r.Error = "the script process ended without an exit code (killed, or the machine restarted)"
		_ = s.saveRun(r)
	}
	return r
}

func (s *Service) activeLocked() (Run, bool) {
	rs, _ := s.listLocked()
	for _, r := range rs {
		if r = s.refreshLocked(r); r.State == StateRunning {
			return r, true
		}
	}
	return Run{}, false
}

func (s *Service) pickLocked(id string) (Run, error) {
	id = strings.TrimSpace(id)
	if id != "" {
		return s.loadRun(id)
	}
	rs, err := s.listLocked()
	if err != nil {
		return Run{}, err
	}
	if len(rs) == 0 {
		return Run{}, errors.New("no deploy has run yet")
	}
	return rs[0], nil
}

func (s *Service) loadRun(id string) (Run, error) {
	if strings.ContainsAny(id, `/\.`) {
		return Run{}, fmt.Errorf("bad run id %q", id)
	}
	b, err := os.ReadFile(s.runPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Run{}, fmt.Errorf("no run %q", id)
		}
		return Run{}, err
	}
	var r Run
	return r, json.Unmarshal(b, &r)
}

func (s *Service) saveRun(r Run) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.runPath(r.ID), b)
}

func (s *Service) listLocked() ([]Run, error) {
	ents, err := os.ReadDir(s.runsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rs []Run
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if r, err := s.loadRun(strings.TrimSuffix(name, ".json")); err == nil {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].StartedAt.After(rs[j].StartedAt) })
	return rs, nil
}

// pruneLocked deletes the files of finished runs past keepRuns.
func (s *Service) pruneLocked() {
	rs, _ := s.listLocked()
	if len(rs) <= keepRuns {
		return
	}
	for _, r := range rs[keepRuns:] {
		if r.State == StateRunning {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(s.runsDir(), r.ID+".*"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
}

func (s *Service) status(r Run, lines int) Status {
	st := Status{Run: r}
	tail, stage := readTail(s.logPath(r.ID), lines)
	st.Tail, st.Stage = tail, stage
	return st
}

// readTail returns the last n lines of the log and the last "== " stage line in it. Only
// the end of the file is read: a long build log stays cheap to poll.
func readTail(path string, n int) (tail, stage string) {
	if n <= 0 {
		n = 20
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	const window = 64 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > window {
		_, _ = f.Seek(fi.Size()-window, io.SeekStart)
	}
	b, _ := io.ReadAll(f)
	var all []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		all = append(all, line)
		if strings.HasPrefix(line, "== ") {
			stage = strings.TrimPrefix(line, "== ")
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return strings.Join(all, "\n"), stage
}

// ---------------------------------------------------------------- helpers

func scriptExt() string {
	if runtime.GOOS == "windows" {
		return ".ps1"
	}
	return ".sh"
}

func scriptLabel(src string) string {
	if src == "" {
		return DefaultScriptURL + scriptExt()
	}
	return src
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

func newRunID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeFile replaces path atomically, so a reader never sees half a JSON file.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func httpFetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// childEnv is wick's environment minus what belongs to wick itself: systemd's socket
// handover variables would make the script's own `<app> reload` think it was handed
// wick's listener.
func childEnv(exitFile, runID string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "LISTEN_FDS", "LISTEN_PID", "LISTEN_FDNAMES", "NOTIFY_SOCKET":
			continue
		}
		env = append(env, kv)
	}
	exe, _ := os.Executable()
	return append(env,
		"WICK_DEPLOY_EXIT="+exitFile,
		"WICK_DEPLOY_RUN_ID="+runID,
		"WICK_DEPLOY_NOTIFY_EXE="+exe,
		"DEBIAN_FRONTEND=noninteractive",
		"GIT_TERMINAL_PROMPT=0",
	)
}

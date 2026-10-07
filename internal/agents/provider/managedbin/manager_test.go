package managedbin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeSource is a managed type whose "binary" is a shell script. The
// script touches a marker when executed, which is how the tests prove a
// file that failed verification was never run.
type fakeSource struct{}

func (fakeSource) Binary() string { return "fakecli" }
func (fakeSource) Repo() string   { return "acme/fakecli" }
func (fakeSource) PickAsset(h Host, as []Asset) (Asset, error) {
	for _, a := range as {
		if a.Name == "fakecli-"+h.OS+"-"+h.Arch {
			return a, nil
		}
	}
	return Asset{}, errors.New("no asset")
}
func (fakeSource) Unpack(d, dest string) error                                       { return os.Rename(d, dest) }
func (fakeSource) CrossCheck(context.Context, Fetcher, Release, Asset, string) error { return nil }
func (fakeSource) Contract() VersionContract {
	return VersionContract{Args: []string{"--version"}, Parse: FirstSemver}
}

func init() { Register("fake", fakeSource{}) }

type fakeGitHub struct {
	srv       *httptest.Server
	downloads atomic.Int32
	api       atomic.Int32      // every /repos/… request
	latestTag string            // served by /releases/latest
	block     chan struct{}     // non-nil: /releases/latest waits on it
	scripts   map[string]string // tag -> script body
	digests   map[string]string // tag -> override digest ("" = correct)
}

func script(marker, printed string) string {
	return fmt.Sprintf("#!/bin/sh\ntouch %q\nenv > %q.env\necho %q\n", marker, marker, printed)
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{scripts: map[string]string{}, digests: map[string]string{}}
	mux := http.NewServeMux()
	rel := func(tag string) map[string]any {
		body := f.scripts[tag]
		sum := sha256.Sum256([]byte(body))
		d := "sha256:" + hex.EncodeToString(sum[:])
		if o := f.digests[tag]; o != "" {
			d = o
		}
		return map[string]any{"tag_name": tag, "assets": []map[string]any{{
			"name": "fakecli-linux-x64", "size": len(body), "digest": d,
			"browser_download_url": f.srv.URL + "/dl/" + tag,
		}}}
	}
	mux.HandleFunc("/repos/acme/fakecli/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		tag := strings.TrimPrefix(r.URL.Path, "/repos/acme/fakecli/releases/tags/")
		if _, ok := f.scripts[tag]; !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rel(tag))
	})
	mux.HandleFunc("/repos/acme/fakecli/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if f.block != nil {
			<-f.block
		}
		if f.latestTag == "" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rel(f.latestTag))
	})
	mux.HandleFunc("/repos/acme/fakecli/releases", func(w http.ResponseWriter, r *http.Request) {
		var out []map[string]any
		for tag := range f.scripts {
			out = append(out, rel(tag))
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		f.downloads.Add(1)
		_, _ = w.Write([]byte(f.scripts[strings.TrimPrefix(r.URL.Path, "/dl/")]))
	})
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			f.api.Add(1)
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newTestManager(t *testing.T, gh *fakeGitHub) (*Manager, string) {
	root := t.TempDir()
	m := New()
	m.Root = func() string { return root }
	m.Host = func() Host { return Host{OS: "linux", Arch: "x64", AVX2: true} }
	m.InUse = func(string) map[string]int { return map[string]int{} }
	m.client.apiBase = gh.srv.URL
	m.client.allowed = func(u *url.URL) bool {
		return u.Scheme == "https" && u.Host == strings.TrimPrefix(gh.srv.URL, "https://")
	}
	m.client.http.Transport = gh.srv.Client().Transport
	return m, root
}

func TestInstallHappyPathAndSandbox(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://secret")
	gh := newFakeGitHub(t)
	marker := filepath.Join(t.TempDir(), "ran")
	gh.scripts["v1.2.3"] = script(marker, "fakecli 1.2.3")
	m, root := newTestManager(t, gh)

	j, err := m.Install(context.Background(), "fake", "v1.2.3")
	if err != nil || j.Phase != PhaseDone {
		t.Fatalf("install: %v %+v", err, j)
	}
	p, v, ok := m.CurrentPath("fake")
	if !ok || v != "1.2.3" || p != filepath.Join(root, "fake", "versions", "1.2.3", "fakecli") {
		t.Fatalf("current %q %q %v", p, v, ok)
	}
	st, _ := m.Status("fake")
	if len(st.Installed) != 1 || st.Installed[0].VersionOutput != "fakecli 1.2.3" || st.Installed[0].SHA256 == "" {
		t.Fatalf("status %+v", st.Installed)
	}
	env, _ := os.ReadFile(marker + ".env")
	if strings.Contains(string(env), "DATABASE_URL") {
		t.Fatal("--version ran with DATABASE_URL in env")
	}
	if !strings.Contains(string(env), "HOME="+filepath.Join(root, ".tmp")) {
		t.Fatalf("HOME not sandboxed: %s", env)
	}
	if _, err := os.Stat(filepath.Join(root, "fake", "versions", "1.2.3.partial")); !os.IsNotExist(err) {
		t.Fatal(".partial left behind")
	}
}

func TestShaMismatchIsNeverExecuted(t *testing.T) {
	gh := newFakeGitHub(t)
	marker := filepath.Join(t.TempDir(), "ran")
	gh.scripts["v1.2.3"] = script(marker, "fakecli 1.2.3")
	gh.digests["v1.2.3"] = "sha256:" + strings.Repeat("0", 64)
	m, root := newTestManager(t, gh)

	_, err := m.Install(context.Background(), "fake", "v1.2.3")
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want sha mismatch, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("binary was executed despite failing sha256")
	}
	if _, _, ok := m.CurrentPath("fake"); ok {
		t.Fatal("current set after failed install")
	}
	ents, _ := os.ReadDir(filepath.Join(root, "fake", "versions"))
	if len(ents) != 0 {
		t.Fatalf("leftover files: %v", ents)
	}
}

func TestVersionMismatchRejected(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = script(filepath.Join(t.TempDir(), "ran"), "fakecli 9.9.9")
	m, root := newTestManager(t, gh)
	_, err := m.Install(context.Background(), "fake", "v1.2.3")
	if err == nil || !strings.Contains(err.Error(), "expected v1.2.3") {
		t.Fatalf("got %v", err)
	}
	if _, _, ok := m.CurrentPath("fake"); ok {
		t.Fatal("current moved")
	}
	// a failed probe is kept for inspection, with the reason
	log, err := os.ReadFile(filepath.Join(root, "fake", "versions", "1.2.3.partial", "probe.log"))
	if err != nil || !strings.Contains(string(log), "fakecli 9.9.9") {
		t.Fatalf("probe.log %q %v", log, err)
	}
	if st, _ := m.Status("fake"); len(st.Installed) != 0 {
		t.Fatalf("kept .partial listed as installed: %+v", st.Installed)
	}
}

func TestTermuxGlibcWithoutRunnerKeepsPartial(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.scripts["v1.2.3"] = string(fakeELF("/lib/ld-linux-aarch64.so.1"))
	m, root := newTestManager(t, gh)
	m.Host = func() Host { return Host{OS: "linux", Arch: "x64", AVX2: true, Termux: true} }
	m.prep.resolve = func(string) (string, error) { return "", os.ErrNotExist }
	_, err := m.Install(context.Background(), "fake", "v1.2.3")
	if !errors.Is(err, ErrMissingInterpreter) || !strings.Contains(err.Error(), "glibc-runner") {
		t.Fatalf("got %v", err)
	}
	if _, _, ok := m.CurrentPath("fake"); ok {
		t.Fatal("current moved")
	}
	partial := filepath.Join(root, "fake", "versions", "1.2.3.partial")
	if _, err := os.Stat(filepath.Join(partial, "fakecli")); err != nil {
		t.Fatalf("binary not kept: %v", err)
	}
	if log, _ := os.ReadFile(filepath.Join(partial, "probe.log")); !strings.Contains(string(log), "ld-linux-aarch64") {
		t.Fatalf("probe.log %q", log)
	}
}

func TestUpdateNoopRollbackAndTamper(t *testing.T) {
	gh := newFakeGitHub(t)
	dir := t.TempDir()
	gh.scripts["v1.2.3"] = script(filepath.Join(dir, "a"), "fakecli 1.2.3")
	gh.scripts["v1.2.4"] = script(filepath.Join(dir, "b"), "fakecli 1.2.4")
	m, root := newTestManager(t, gh)
	ctx := context.Background()
	if _, err := m.Install(ctx, "fake", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ctx, "fake", "v1.2.4"); err != nil {
		t.Fatal(err)
	}
	if gh.downloads.Load() != 2 {
		t.Fatalf("downloads %d", gh.downloads.Load())
	}
	// same version again = no-op
	j, err := m.Install(ctx, "fake", "v1.2.4")
	if err != nil || !strings.Contains(j.Message, "already installed") || gh.downloads.Load() != 2 {
		t.Fatalf("noop: %v %+v dl=%d", err, j, gh.downloads.Load())
	}
	// rollback without download
	if err := m.Activate("fake", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, v, _ := m.CurrentPath("fake"); v != "1.2.3" || gh.downloads.Load() != 2 {
		t.Fatalf("rollback v=%s dl=%d", v, gh.downloads.Load())
	}
	// install of an already-present non-current version switches without download
	if _, err := m.Install(ctx, "fake", "v1.2.4"); err != nil || gh.downloads.Load() != 2 {
		t.Fatalf("reinstall: %v", err)
	}
	// tampering is refused
	bin := filepath.Join(root, "fake", "versions", "1.2.3", "fakecli")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho evil 1.2.3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("fake", "1.2.3"); !errors.Is(err, ErrTampered) {
		t.Fatalf("want ErrTampered, got %v", err)
	}
	if _, v, _ := m.CurrentPath("fake"); v != "1.2.4" {
		t.Fatalf("current changed on refused activate: %s", v)
	}
}

func TestRetentionAndRemoveGuards(t *testing.T) {
	gh := newFakeGitHub(t)
	dir := t.TempDir()
	for i, tag := range []string{"v1.0.0", "v1.0.1", "v1.0.2", "v1.0.3"} {
		gh.scripts[tag] = script(filepath.Join(dir, fmt.Sprint(i)), "fakecli "+strings.TrimPrefix(tag, "v"))
	}
	m, _ := newTestManager(t, gh)
	m.KeepVersions = func() int { return 1 }
	inUse := map[string]int{"1.0.0": 1}
	m.InUse = func(string) map[string]int { return inUse }
	ctx := context.Background()
	for _, tag := range []string{"v1.0.0", "v1.0.1", "v1.0.2", "v1.0.3"} {
		if _, err := m.Install(ctx, "fake", tag); err != nil {
			t.Fatal(err)
		}
	}
	have := map[string]bool{}
	st, _ := m.Status("fake")
	for _, iv := range st.Installed {
		have[iv.Version] = true
	}
	// current 1.0.3 + 1 kept (1.0.2) + 1.0.0 protected because in use
	if !have["1.0.3"] || !have["1.0.2"] || !have["1.0.0"] || have["1.0.1"] {
		t.Fatalf("retention kept %v", have)
	}
	if err := m.Remove("fake", "1.0.3"); err == nil {
		t.Fatal("removed current")
	}
	if err := m.Remove("fake", "1.0.0"); err == nil {
		t.Fatal("removed in-use version")
	}
	inUse = map[string]int{}
	m.Prune("fake")
	st, _ = m.Status("fake")
	if len(st.Installed) != 2 {
		t.Fatalf("after process ended, want 2 left, got %d", len(st.Installed))
	}
}

func TestDisallowedHostRefused(t *testing.T) {
	c := newClient()
	if _, err := c.get(context.Background(), "https://evil.example.com/x"); err == nil {
		t.Fatal("non-GitHub host accepted")
	}
	if _, err := c.get(context.Background(), "http://github.com/x"); err == nil {
		t.Fatal("plain http accepted")
	}
}

func TestHostAndVersionHelpers(t *testing.T) {
	if got := (Host{OS: "linux", Arch: "x64", AVX2: true}).Label(); got != "linux-x64 · glibc · AVX2" {
		t.Fatal(got)
	}
	if got := (Host{OS: "linux", Arch: "arm64", Musl: true}).Label(); got != "linux-arm64 · musl" {
		t.Fatal(got)
	}
	if !cpuinfoHasAVX2([]byte("flags : fpu sse avx avx2 bmi2")) || cpuinfoHasAVX2([]byte("flags : fpu avx avx512f")) {
		t.Fatal("avx2 detection")
	}
	if v, ok := FirstSemver("2.1.284 (Claude Code)\n"); !ok || v != "2.1.284" {
		t.Fatal(v)
	}
	if !MatchesTag("v18.4.3", "18.4.3") || MatchesTag("v18.4.3", "18.4.2") {
		t.Fatal("MatchesTag")
	}
}

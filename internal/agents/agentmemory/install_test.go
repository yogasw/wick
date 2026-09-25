package agentmemory

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Install tests (PLAN §12.4).
//
// Nothing here reaches GitHub: the release, its assets and their checksums
// are served by an httptest server, and the archives are built in-test. A
// downloader that can only be tested by downloading is one nobody runs the
// tests for — and this one decides what binary ends up executable on the host.

// ── asset selection ──────────────────────────────────────────────────

// TestAssetNamePerHost pins the names to what the project actually publishes
// (read off ai-memory v2.4.0's release, 2026-09-25). A wrong name here
// installs a binary that cannot run, and the symptom arrives much later as a
// daemon that never starts.
func TestAssetNamePerHost(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want, kind   string
	}{
		{"linux", "amd64", "ai-memory-linux-x86_64.tar.gz", "tar.gz"},
		{"linux", "arm64", "ai-memory-linux-aarch64.tar.gz", "tar.gz"},
		{"darwin", "amd64", "ai-memory-macos-x86_64.tar.gz", "tar.gz"},
		{"darwin", "arm64", "ai-memory-macos-aarch64.tar.gz", "tar.gz"},
		{"windows", "amd64", "ai-memory-windows-x86_64.zip", "zip"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			name, kind, err := assetName("ai-memory", tc.goos, tc.goarch)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if name != tc.want || kind != tc.kind {
				t.Fatalf("got %q/%q, want %q/%q", name, kind, tc.want, tc.kind)
			}
		})
	}
}

// TestAssetNameRefusesAHostWithNoBuild: an unsupported pair is an error, not
// the nearest-looking asset.
func TestAssetNameRefusesAHostWithNoBuild(t *testing.T) {
	for _, tc := range []struct{ goos, goarch string }{
		{"windows", "arm64"},
		{"linux", "386"},
		{"plan9", "amd64"},
	} {
		if name, _, err := assetName("ai-memory", tc.goos, tc.goarch); err == nil {
			t.Fatalf("%s/%s resolved to %q — it must refuse instead", tc.goos, tc.goarch, name)
		}
	}
}

// ── a release to install from ────────────────────────────────────────

// fakeBinary is what the archives carry. Its bytes are checked after the
// install: a binary that arrives truncated or is some other file from the
// archive is exactly the failure the extraction has to avoid.
var fakeBinary = []byte("#!/bin/sh\necho ai-memory 2.4.0\n")

func tarGzWith(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func zipWith(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// releaseServer serves a GitHub-shaped release plus its assets. sums maps an
// asset name to the checksum body served for it; an asset with no entry
// publishes no checksum, which is its own test case.
func releaseServer(t *testing.T, tag string, assets map[string][]byte, sums map[string]string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		rel := ghRelease{TagName: tag}
		for name, blob := range assets {
			rel.Assets = append(rel.Assets, ghAsset{
				Name: name, Size: int64(len(blob)),
				URL: srv.URL + "/download/" + url.PathEscape(name),
			})
			if sum, ok := sums[name]; ok {
				rel.Assets = append(rel.Assets, ghAsset{
					Name: name + ".sha256", Size: int64(len(sum)),
					URL: srv.URL + "/download/" + url.PathEscape(name+".sha256"),
				})
			}
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/download/"))
		if err != nil {
			http.Error(w, "bad name", http.StatusBadRequest)
			return
		}
		if blob, ok := assets[name]; ok {
			_, _ = w.Write(blob)
			return
		}
		if sum, ok := sums[strings.TrimSuffix(name, ".sha256")]; ok && strings.HasSuffix(name, ".sha256") {
			_, _ = w.Write([]byte(sum))
			return
		}
		http.NotFound(w, r)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// checksumLine is the published format: "<hex>  <filename>".
func checksumLine(blob []byte, name string) string {
	return fmt.Sprintf("%s  %s\n", sha256Hex(blob), name)
}

// TestInstallLandsAndIsResolvable is the whole point of the feature: after an
// install, the binary is in wick's own directory, it is executable, and every
// exec path finds it WITHOUT the user touching PATH.
func TestInstallLandsAndIsResolvable(t *testing.T) {
	archive := tarGzWith(t, map[string][]byte{
		"./ai-memory": fakeBinary,
		"./LICENSE":   []byte("MIT"),
		"./docs/x.md": []byte("# docs"),
	})
	name := "ai-memory-linux-x86_64.tar.gz"
	srv := releaseServer(t, "v2.4.0",
		map[string][]byte{name: archive},
		map[string]string{name: checksumLine(archive, name)},
	)

	dir := t.TempDir()
	out, err := releaseInstall{
		repo: "akitaonrails/ai-memory", bin: "ai-memory", dest: dir,
		goos: "linux", goarch: "amd64", api: srv.URL, client: srv.Client(),
	}.run(context.Background())
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	path := filepath.Join(dir, "ai-memory")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if !bytes.Equal(got, fakeBinary) {
		t.Fatalf("installed bytes differ from the archive's")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed %v — a binary nobody can execute", fi.Mode().Perm())
	}
	// Only the binary: the archive's docs and licence are not wick's to own.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the install left %v — only the binary belongs to wick", names)
	}
	// The log has to name what happened; it is what the panel shows.
	for _, want := range []string{"downloading", "verified sha256", "installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("install log has no %q line:\n%s", want, out)
		}
	}

	// And the resolver prefers it, with PATH answering nothing at all.
	withBinDir(t, dir)
	withNoPath(t)
	resolved, err := ResolveBackendBin("ai-memory")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved != path {
		t.Fatalf("resolved %q, want wick's own copy %q", resolved, path)
	}
}

// TestInstallRefusesAChecksumMismatch: a tampered or truncated download must
// leave NOTHING behind. A half-installed binary would be reported as
// installed by the next status read.
func TestInstallRefusesAChecksumMismatch(t *testing.T) {
	archive := tarGzWith(t, map[string][]byte{"./ai-memory": fakeBinary})
	name := "ai-memory-linux-x86_64.tar.gz"
	srv := releaseServer(t, "v2.4.0",
		map[string][]byte{name: archive},
		// A checksum for different bytes — what a tampered asset looks like.
		map[string]string{name: checksumLine([]byte("something else"), name)},
	)

	dir := t.TempDir()
	out, err := releaseInstall{
		repo: "akitaonrails/ai-memory", bin: "ai-memory", dest: dir,
		goos: "linux", goarch: "amd64", api: srv.URL, client: srv.Client(),
	}.run(context.Background())
	if err == nil {
		t.Fatal("a mismatched checksum was accepted")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") || !strings.Contains(err.Error(), "nothing was installed") {
		t.Fatalf("error does not say what happened: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused install still wrote %d file(s)", len(entries))
	}
	if strings.Contains(out, "installed ") {
		t.Fatalf("the log claims an install happened:\n%s", out)
	}
}

// TestInstallSaysWhenThereIsNoChecksum: every real asset publishes one, so
// its absence is an anomaly the operator should see rather than a silent skip.
func TestInstallWarnsWithoutAChecksum(t *testing.T) {
	archive := tarGzWith(t, map[string][]byte{"./ai-memory": fakeBinary})
	name := "ai-memory-linux-x86_64.tar.gz"
	srv := releaseServer(t, "v2.4.0", map[string][]byte{name: archive}, nil)

	dir := t.TempDir()
	out, err := releaseInstall{
		repo: "akitaonrails/ai-memory", bin: "ai-memory", dest: dir,
		goos: "linux", goarch: "amd64", api: srv.URL, client: srv.Client(),
	}.run(context.Background())
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "without verification") {
		t.Fatalf("an unverified install must say so:\n%s", out)
	}
}

// TestInstallFromZip covers the windows asset, which is the one that is not a
// tarball — and whose binary carries a .exe the archive may or may not use.
func TestInstallFromZip(t *testing.T) {
	archive := zipWith(t, map[string][]byte{"ai-memory.exe": fakeBinary, "README.md": []byte("x")})
	name := "ai-memory-windows-x86_64.zip"
	srv := releaseServer(t, "v2.4.0",
		map[string][]byte{name: archive},
		map[string]string{name: checksumLine(archive, name)},
	)

	dir := t.TempDir()
	if _, err := (releaseInstall{
		repo: "akitaonrails/ai-memory", bin: "ai-memory", dest: dir,
		goos: "windows", goarch: "amd64", api: srv.URL, client: srv.Client(),
	}).run(context.Background()); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ai-memory.exe")); err != nil {
		t.Fatalf("the windows binary is not where the resolver looks: %v", err)
	}
}

// TestInstallReportsAMissingAsset names what the release DOES publish, so a
// host with no build gets evidence rather than "not found".
func TestInstallReportsAMissingAsset(t *testing.T) {
	other := "ai-memory-linux-aarch64.tar.gz"
	srv := releaseServer(t, "v2.4.0", map[string][]byte{other: []byte("irrelevant")}, nil)
	_, err := releaseInstall{
		repo: "akitaonrails/ai-memory", bin: "ai-memory", dest: t.TempDir(),
		goos: "linux", goarch: "amd64", api: srv.URL, client: srv.Client(),
	}.run(context.Background())
	if err == nil {
		t.Fatal("a release without this host's asset was accepted")
	}
	if !strings.Contains(err.Error(), other) {
		t.Fatalf("the error does not say what the release has: %v", err)
	}
}

// TestInstallRefusesAnArchiveWithoutTheBinary: the archive is the release's,
// and an archive that does not carry the executable is not something to
// half-install.
func TestInstallRefusesAnArchiveWithoutTheBinary(t *testing.T) {
	archive := tarGzWith(t, map[string][]byte{"./LICENSE": []byte("MIT")})
	name := "ai-memory-linux-x86_64.tar.gz"
	srv := releaseServer(t, "v2.4.0",
		map[string][]byte{name: archive},
		map[string]string{name: checksumLine(archive, name)},
	)
	dir := t.TempDir()
	_, err := releaseInstall{
		repo: "akitaonrails/ai-memory", bin: "ai-memory", dest: dir,
		goos: "linux", goarch: "amd64", api: srv.URL, client: srv.Client(),
	}.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "contains no") {
		t.Fatalf("error: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("nothing should have been written, found %d file(s)", len(entries))
	}
}

// TestInstallNeedsSomewhereToPutIt: an unwired bin dir refuses rather than
// guessing a directory to own.
func TestInstallNeedsSomewhereToPutIt(t *testing.T) {
	_, err := releaseInstall{repo: "o/r", bin: "ai-memory", goos: "linux", goarch: "amd64"}.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no install directory") {
		t.Fatalf("error: %v", err)
	}
}

// ── resolution ───────────────────────────────────────────────────────

func withBinDir(t *testing.T, dir string) {
	t.Helper()
	prev := BinDir()
	t.Cleanup(func() { SetBinDir(prev) })
	SetBinDir(dir)
}

// withNoPath makes the PATH half answer nothing, which is the state this
// whole feature exists for: the binary is installed and PATH knows nothing
// about it.
func withNoPath(t *testing.T) {
	t.Helper()
	prev := resolveOnPath
	t.Cleanup(func() { resolveOnPath = prev })
	resolveOnPath = func(string) (string, error) { return "", errors.New(`executable file not found in $PATH`) }
}

// TestResolvePrefersWicksOwnCopy: with the binary in both places, wick's copy
// wins — that is what makes the installed version the one that actually runs.
func TestResolvePrefersWicksOwnCopy(t *testing.T) {
	dir := t.TempDir()
	own := filepath.Join(dir, "ai-memory")
	if err := os.WriteFile(own, fakeBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	withBinDir(t, dir)
	prev := resolveOnPath
	t.Cleanup(func() { resolveOnPath = prev })
	resolveOnPath = func(string) (string, error) { return "/usr/local/bin/ai-memory", nil }

	got, err := ResolveBackendBin("ai-memory")
	if err != nil {
		t.Fatal(err)
	}
	if got != own {
		t.Fatalf("resolved %q, want wick's own %q", got, own)
	}
}

// TestResolveFallsBackToPath: a host that already had the binary installed
// keeps working exactly as before.
func TestResolveFallsBackToPath(t *testing.T) {
	withBinDir(t, t.TempDir()) // empty: wick installed nothing
	prev := resolveOnPath
	t.Cleanup(func() { resolveOnPath = prev })
	resolveOnPath = func(string) (string, error) { return "/usr/local/bin/ai-memory", nil }

	got, err := ResolveBackendBin("ai-memory")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/local/bin/ai-memory" {
		t.Fatalf("resolved %q, want the PATH copy", got)
	}
}

// TestResolveIgnoresANonExecutableCopy: a file that is there but cannot be
// run must not shadow a working one on PATH.
func TestResolveIgnoresANonExecutableCopy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ai-memory"), []byte("half a download"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBinDir(t, dir)
	prev := resolveOnPath
	t.Cleanup(func() { resolveOnPath = prev })
	resolveOnPath = func(string) (string, error) { return "/usr/local/bin/ai-memory", nil }

	got, err := ResolveBackendBin("ai-memory")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/local/bin/ai-memory" {
		t.Fatalf("resolved %q — a non-executable file must not shadow PATH", got)
	}
}

// ── the manager's side ───────────────────────────────────────────────

// TestManagerInstallIsNoLongerAStub: the "not implemented yet — put it on
// PATH manually" answer is gone for a backend that publishes releases, and
// Installed() flips once the binary lands.
func TestManagerInstallIsNoLongerAStub(t *testing.T) {
	archive := tarGzWith(t, map[string][]byte{"./ai-memory": fakeBinary})
	name, _, err := assetName("ai-memory", hostGOOS(), hostGOARCH())
	if err != nil {
		t.Skipf("no published asset for this host: %v", err)
	}
	srv := releaseServer(t, "v2.4.0",
		map[string][]byte{name: archive},
		map[string]string{name: checksumLine(archive, name)},
	)

	dir := t.TempDir()
	withBinDir(t, dir)
	withNoPath(t)
	prevAPI := githubAPIBase
	t.Cleanup(func() { githubAPIBase = prevAPI })
	githubAPIBase = srv.URL

	m := newManager(Descriptor{
		ID: "install-mem", DisplayName: "ai-memory", BinName: "ai-memory",
		PrefPort: 41900, HealthPath: "/healthz",
		InstallKind: InstallGitHubRelease, ReleaseRepo: "akitaonrails/ai-memory",
	})
	if m.Installed() {
		t.Fatal("nothing is installed yet")
	}
	out, err := m.Install(context.Background())
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if strings.Contains(out, "not implemented") {
		t.Fatalf("the stub answer survived:\n%s", out)
	}
	if !m.Installed() {
		t.Fatal("Installed() is still false after a successful install")
	}
	if got, want := m.BinPath(), filepath.Join(dir, "ai-memory"); got != want {
		t.Fatalf("BinPath %q, want %q", got, want)
	}
}

// TestManagerInstallRefusesABackendItCannotInstall: "wick does not install
// this one" is a fact about the backend, told apart from a failure.
func TestManagerInstallRefusesABackendItCannotInstall(t *testing.T) {
	m := newManager(Descriptor{
		ID: "manual-mem", DisplayName: "manual-mem", BinName: "manual-mem",
		PrefPort: 42000, InstallKind: InstallManual,
	})
	_, err := m.Install(context.Background())
	if !errors.Is(err, ErrInstallNotSupported) {
		t.Fatalf("err %v, want ErrInstallNotSupported", err)
	}
}

// TestInstallHandlerStatuses: the panel branches on these, and 501 ("wick
// does not install this") must not be how a failed download is reported.
func TestInstallHandlerStatuses(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true})

	t.Run("a backend wick cannot install answers 501", func(t *testing.T) {
		be := &Backend{Desc: Descriptor{ID: "manual2", DisplayName: "manual2", BinName: "manual2", InstallKind: InstallManual}}
		be.Mgr = newManager(be.Desc)
		w, c := post(nil)
		installHandler(be, c)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status %d, want 501", w.Code)
		}
	})

	t.Run("a failed install answers 502 with the log", func(t *testing.T) {
		// A release endpoint that is down: the install is supported, it
		// just did not work.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		prevAPI := githubAPIBase
		t.Cleanup(func() { githubAPIBase = prevAPI })
		githubAPIBase = srv.URL
		withBinDir(t, t.TempDir())

		be := &Backend{Desc: Descriptor{
			ID: "gh2", DisplayName: "gh2", BinName: "ai-memory", PrefPort: 42100,
			InstallKind: InstallGitHubRelease, ReleaseRepo: "o/r",
		}}
		be.Mgr = newManager(be.Desc)
		w, c := post(nil)
		installHandler(be, c)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("status %d, want 502", w.Code)
		}
		body := decodeBody(t, w)
		if msg, _ := body["error"].(string); msg == "" {
			t.Error("no error message for the panel")
		}
		if _, ok := body["output"]; !ok {
			t.Error("the log is what makes a failure diagnosable — it must travel with the error")
		}
	})
}

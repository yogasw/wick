package source

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// buildZip returns a plugin zip (binary + plugin.json) and its sha256.
func buildZip(t *testing.T, key, kind, ver string) ([]byte, string) {
	t.Helper()
	bin := []byte("binary " + key + " " + ver)
	sum := sha256.Sum256(bin)
	m := wickplugin.Manifest{
		SchemaVersion: 1, Kind: kind, Version: ver, ProtoVersion: wickplugin.ProtoVersion,
		Entry: key, OSArch: []string{HostOSArch()}, SHA256: hex.EncodeToString(sum[:]),
		Module: connector.Module{Meta: connector.Meta{Key: key, Name: key}},
	}
	mj, _ := json.Marshal(m)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		data []byte
	}{{key, bin}, {"plugin.json", mj}} { // fixed order: the zip hash must be stable
		w, _ := zw.Create(f.name)
		_, _ = w.Write(f.data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zs := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(zs[:])
}

func zipName(key, ver string) string {
	return fmt.Sprintf("%s-%s-%s.zip", key, ver, strings.ReplaceAll(HostOSArch(), "/", "-"))
}

type rel struct {
	key, kind, ver string
	pre            bool
	badSHA         bool
	sig            string
}

// fakeGitHub serves the Releases API (api) and browser downloads (dl) from two
// hosts, so tests can prove the PAT only ever reaches the API host.
type fakeGitHub struct {
	api, dl     *httptest.Server
	rels        []rel
	pat         string
	private     bool
	mu          sync.Mutex
	assetCalls  int
	dlAuthSeen  bool
	releaseHits int
}

func newFakeGitHub(t *testing.T, private bool, pat string, rels ...rel) *fakeGitHub {
	f := &fakeGitHub{rels: rels, pat: pat, private: private}
	files := map[string][]byte{} // asset name -> bytes
	type asset struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
		DL   string `json:"browser_download_url"`
	}
	f.dl = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if r.Header.Get("Authorization") != "" {
			f.dlAuthSeen = true
		}
		f.mu.Unlock()
		if f.private {
			http.Error(w, "not found", 404) // browser_download_url never works for private
			return
		}
		b, ok := files[filepath.Base(r.URL.Path)+"@"+filepath.Base(filepath.Dir(r.URL.Path))]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(f.dl.Close)
	byID := map[int64][]byte{}
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authed := r.Header.Get("Authorization") == "Bearer "+f.pat && f.pat != ""
		switch {
		case r.URL.Path == "/user":
			if !authed {
				http.Error(w, "bad credentials", 401)
				return
			}
			_, _ = w.Write([]byte(`{"login":"bot"}`))
		case r.URL.Path == "/repos/acme/plugins":
			if f.private && !authed {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"full_name":"acme/plugins"}`))
		case r.URL.Path == "/repos/acme/plugins/releases":
			f.mu.Lock()
			f.releaseHits++
			f.mu.Unlock()
			if f.private && !authed {
				http.NotFound(w, r)
				return
			}
			if r.Header.Get("If-None-Match") == `"rel-1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			var out []map[string]any
			var id int64
			for _, rl := range f.rels {
				tag := rl.key + "/v" + rl.ver
				zb, zs := buildZip(t, rl.key, rl.kind, rl.ver)
				if rl.badSHA {
					zs = strings.Repeat("0", 64)
				}
				zn := zipName(rl.key, rl.ver)
				idx, _ := json.Marshal([]map[string]any{{
					"key": rl.key, "kind": rl.kind, "version": rl.ver, "proto_version": 1,
					"assets": map[string]any{HostOSArch(): map[string]string{"url": zn, "zip_sha256": zs, "signature": rl.sig}},
				}})
				var assets []asset
				for name, data := range map[string][]byte{zn: zb, "plugins.json": idx} {
					id++
					files[name+"@"+strings.ReplaceAll(tag, "/", "_")] = data
					byID[id] = data
					assets = append(assets, asset{ID: id, Name: name,
						URL: fmt.Sprintf("%s/repos/acme/plugins/releases/assets/%d", f.api.URL, id),
						DL:  fmt.Sprintf("%s/download/%s/%s", f.dl.URL, strings.ReplaceAll(tag, "/", "_"), name)})
				}
				out = append(out, map[string]any{"tag_name": tag, "prerelease": rl.pre, "assets": assets})
			}
			out = append(out, map[string]any{"tag_name": "v9.9.9", "assets": []asset{}}) // core tag: ignored
			w.Header().Set("ETag", `"rel-1"`)
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(r.URL.Path, "/repos/acme/plugins/releases/assets/"):
			f.mu.Lock()
			f.assetCalls++
			f.mu.Unlock()
			if r.Header.Get("Accept") != "application/octet-stream" || !authed {
				http.Error(w, "need octet-stream + bearer", 415)
				return
			}
			var id int64
			fmt.Sscanf(filepath.Base(r.URL.Path), "%d", &id)
			_, _ = w.Write(byID[id])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.api.Close)
	return f
}

func (f *fakeGitHub) client() *Client {
	return &Client{HTTP: http.DefaultClient, GitHubAPI: f.api.URL}
}

func ghSource(private bool, pat string) *entity.PluginSource {
	return &entity.PluginSource{ID: "s1", Type: TypeGitHub, Owner: "acme", Repo: "plugins", Private: private, PAT: pat, Enabled: true}
}

func tmpRoot(t *testing.T) InstallOptions {
	dir := t.TempDir()
	return InstallOptions{Root: func(kind string) string { return filepath.Join(dir, wickplugin.KindFolder(kind)) }}
}

func TestGitHubPublicMultiPluginHighestVersion(t *testing.T) {
	f := newFakeGitHub(t, false, "",
		rel{key: "alpha", kind: "tool", ver: "1.0.0"}, rel{key: "alpha", kind: "tool", ver: "1.2.0"},
		rel{key: "alpha", kind: "tool", ver: "2.0.0", pre: true}, rel{key: "beta", kind: "job", ver: "0.1.0"})
	res, err := f.client().Fetch(context.Background(), ghSource(false, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Entries[0].Key != "alpha" || res.Entries[0].Version != "1.2.0" || res.Entries[1].Key != "beta" {
		t.Fatalf("want alpha 1.2.0 (prerelease ignored) + beta, got %+v", res.Entries)
	}
	if u := res.Entries[0].Assets[HostOSArch()].URL; !strings.HasPrefix(u, f.dl.URL+"/download/alpha_v1.2.0/") {
		t.Fatalf("relative url not resolved against the release: %s", u)
	}
	opts := tmpRoot(t)
	in, err := f.client().InstallEntry(context.Background(), ghSource(false, ""), res.Entries[1], opts)
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != "job" || in.Version != "0.1.0" {
		t.Fatalf("installed %+v", in)
	}
	if _, err := os.Stat(filepath.Join(opts.Root("job"), "beta", "plugin.json")); err != nil {
		t.Fatalf("job not installed into its kind folder: %v", err)
	}
}

func TestGitHubPrereleaseWhenAllowed(t *testing.T) {
	f := newFakeGitHub(t, false, "", rel{key: "alpha", kind: "tool", ver: "1.2.0"}, rel{key: "alpha", kind: "tool", ver: "2.0.0", pre: true})
	src := ghSource(false, "")
	src.AllowPrerelease = true
	res, err := f.client().Fetch(context.Background(), src, "")
	if err != nil || res.Entries[0].Version != "2.0.0" {
		t.Fatalf("want 2.0.0 with allow_prerelease, got %+v %v", res.Entries, err)
	}
}

func TestGitHubETagNotModified(t *testing.T) {
	f := newFakeGitHub(t, false, "", rel{key: "alpha", kind: "tool", ver: "1.0.0"})
	res, err := f.client().Fetch(context.Background(), ghSource(false, ""), "")
	if err != nil || res.ETag != `"rel-1"` {
		t.Fatalf("first fetch: %v etag=%q", err, res.ETag)
	}
	res2, err := f.client().Fetch(context.Background(), ghSource(false, ""), res.ETag)
	if err != nil || !res2.NotModified || res2.Entries != nil {
		t.Fatalf("want 304 not-modified, got %+v %v", res2, err)
	}
}

func TestGitHubPrivateDownloadsThroughAssetAPI(t *testing.T) {
	f := newFakeGitHub(t, true, "ghp_test", rel{key: "alpha", kind: "service", ver: "1.0.0"})
	src := ghSource(true, "ghp_test")
	c := f.client()
	res, err := c.Fetch(context.Background(), src, "")
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("private fetch: %+v %v", res, err)
	}
	opts := tmpRoot(t)
	if _, err := c.InstallEntry(context.Background(), src, res.Entries[0], opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.Root("service"), "alpha", "alpha")); err != nil {
		t.Fatalf("service binary missing: %v", err)
	}
	if f.assetCalls != 2 { // plugins.json + zip
		t.Fatalf("asset API calls = %d, want 2", f.assetCalls)
	}
	if f.dlAuthSeen {
		t.Fatal("PAT leaked to a non-API host")
	}
	// Without the PAT the private repo is invisible.
	if _, err := c.Fetch(context.Background(), ghSource(true, ""), ""); err == nil {
		t.Fatal("private fetch without PAT should fail")
	}
}

func TestZipSHA256MismatchRejected(t *testing.T) {
	f := newFakeGitHub(t, false, "", rel{key: "alpha", kind: "tool", ver: "1.0.0", badSHA: true})
	res, _ := f.client().Fetch(context.Background(), ghSource(false, ""), "")
	opts := tmpRoot(t)
	_, err := f.client().InstallEntry(context.Background(), ghSource(false, ""), res.Entries[0], opts)
	if err == nil || !strings.Contains(err.Error(), "zip_sha256 mismatch") {
		t.Fatalf("want zip_sha256 mismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.Root("tool"), "alpha")); !os.IsNotExist(err) {
		t.Fatal("rejected plugin must not land on disk")
	}
}

func TestSignaturePinnedKey(t *testing.T) {
	t.Setenv("WICK_PLUGIN_PUBKEY", "")
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, zs := buildZip(t, "alpha", "tool", "1.0.0")
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(zs)))
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	bad := base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, []byte(zs)))
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	for name, tc := range map[string]struct {
		sig  string
		want string
	}{"good": {good, ""}, "bad": {bad, "signature verification failed"}, "unsigned": {"", "signature required"}} {
		t.Run(name, func(t *testing.T) {
			f := newFakeGitHub(t, false, "", rel{key: "alpha", kind: "tool", ver: "1.0.0", sig: tc.sig})
			src := ghSource(false, "")
			src.PubKey = pubB64
			res, _ := f.client().Fetch(context.Background(), src, "")
			_, err := f.client().InstallEntry(context.Background(), src, res.Entries[0], tmpRoot(t))
			if tc.want == "" && err != nil {
				t.Fatalf("good signature rejected: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseIndexRelativeAndV1(t *testing.T) {
	raw := []byte(`[
	 {"key":"alpha","kind":"tool","version":"1.0.0","assets":{"linux/amd64":{"url":"alpha-1.0.0-linux-amd64.zip","zip_sha256":"ab"}}},
	 {"name":"legacy","version":"0.2.0","assets":{"linux/amd64":"https://cdn.example.com/legacy.zip"}}]`)
	es, err := ParseIndex(raw, "https://host.example/rel/v1/plugins.json")
	if err != nil {
		t.Fatal(err)
	}
	if es[0].Assets["linux/amd64"].URL != "https://host.example/rel/v1/alpha-1.0.0-linux-amd64.zip" || es[0].Assets["linux/amd64"].ZipSHA256 != "ab" {
		t.Fatalf("relative url: %+v", es[0].Assets)
	}
	if es[1].Key != "legacy" || es[1].Assets["linux/amd64"].URL != "https://cdn.example.com/legacy.zip" {
		t.Fatalf("v1 compat: %+v", es[1])
	}
	if err := ValidateIndex(append(es, es[0])); err == nil {
		t.Fatal("duplicate key should fail validation")
	}
}

func statuses(steps []Step) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.Status[:1])
	}
	return b.String()
}

func TestHealthCheckSixSteps(t *testing.T) {
	t.Run("all ok", func(t *testing.T) {
		f := newFakeGitHub(t, true, "ghp_test", rel{key: "alpha", kind: "tool", ver: "1.0.0"})
		steps := f.client().Test(context.Background(), ghSource(true, "ghp_test"), func(string) (bool, bool, string) { return true, true, "v1.0.0 healthy" })
		if len(steps) != 6 || statuses(steps) != "oooooo" {
			t.Fatalf("want 6 ok, got %s %+v", statuses(steps), steps)
		}
	})
	t.Run("bad zip hash", func(t *testing.T) {
		f := newFakeGitHub(t, false, "", rel{key: "alpha", kind: "tool", ver: "1.0.0", badSHA: true})
		steps := f.client().Test(context.Background(), ghSource(false, ""), nil)
		if statuses(steps) != "ooooff"[:5]+"s" || !strings.Contains(steps[4].Message, "zip_sha256") {
			t.Fatalf("want step 5 fail, step 6 skip: %s %+v", statuses(steps), steps)
		}
	})
	t.Run("private without PAT", func(t *testing.T) {
		f := newFakeGitHub(t, true, "ghp_test", rel{key: "alpha", kind: "tool", ver: "1.0.0"})
		steps := f.client().Test(context.Background(), ghSource(true, ""), nil)
		if statuses(steps) != "ofssss" || !strings.Contains(steps[1].Message, "needs a PAT") {
			t.Fatalf("want auth fail: %s %+v", statuses(steps), steps)
		}
	})
	t.Run("PAT without repo access", func(t *testing.T) {
		f := newFakeGitHub(t, true, "ghp_test", rel{key: "alpha", kind: "tool", ver: "1.0.0"})
		f.pat = "ghp_other"
		steps := f.client().Test(context.Background(), ghSource(true, "ghp_test"), nil)
		if steps[1].Status != "fail" || !strings.Contains(steps[1].Message, "PAT rejected") {
			t.Fatalf("want PAT rejected: %+v", steps[1])
		}
	})
	t.Run("plain http rejected", func(t *testing.T) {
		steps := (&Client{}).Test(context.Background(), &entity.PluginSource{Type: TypeURL, URL: "http://example.com/plugins.json"}, nil)
		if steps[0].Status != "fail" || !strings.Contains(steps[0].Message, "not HTTPS") {
			t.Fatalf("want https fail: %+v", steps[0])
		}
	})
}

func newDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	postgres.Migrate(db)
	return db
}

// TestManagerURLSourceCheckAndUpdate covers a plugins.json URL source:
// install, version bump detected by Check, Update via the source, and the
// OnInstalled hook (which restarts service plugins in the server).
func TestManagerURLSourceCheckAndUpdate(t *testing.T) {
	ver := "1.0.0"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			zb, _ := buildZip(t, "echo", "service", strings.Split(filepath.Base(r.URL.Path), "-")[1])
			_, _ = w.Write(zb)
			return
		}
		_, zs := buildZip(t, "echo", "service", ver)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"key": "echo", "kind": "service", "version": ver,
			"assets": map[string]any{HostOSArch(): map[string]string{"url": "files/" + zipName("echo", ver), "zip_sha256": zs}}}})
	}))
	defer srv.Close()

	var hooks []string
	m := &Manager{DB: newDB(t), Client: &Client{HTTP: http.DefaultClient}, Install: tmpRoot(t),
		Encrypt: func(p string) (string, error) { return "wick_cenc_" + p, nil },
		OnInstalled: func(_ context.Context, kind, key string) { hooks = append(hooks, kind+":"+key) }}
	s, err := m.Save("", SourceInput{Type: TypeURL, URL: srv.URL + "/plugins.json"}, "admin@x")
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoUpdate || s.PollMinutes != 30 {
		t.Fatalf("defaults: auto_update=%v poll=%d", s.AutoUpdate, s.PollMinutes)
	}
	if _, err := m.Check(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallFromSource(context.Background(), s.ID, "echo", "admin@x", nil); err != nil {
		t.Fatal(err)
	}
	ver = "1.1.0"
	res, err := m.Check(context.Background(), s.ID)
	if err != nil || len(res.Updates) != 1 {
		t.Fatalf("update not detected: %+v %v", res, err)
	}
	var st entity.PluginState
	m.DB.Where("key = ?", "echo").First(&st)
	if st.AvailableVersion != "1.1.0" || st.SourceID != s.ID || st.Kind != "service" {
		t.Fatalf("state after check: %+v", st)
	}
	in, err := m.Update(context.Background(), "echo", "admin@x", nil)
	if err != nil || in.Version != "1.1.0" {
		t.Fatalf("update: %+v %v", in, err)
	}
	m.DB.Where("key = ?", "echo").First(&st)
	if st.AvailableVersion != "" || st.InstalledVersion != "1.1.0" {
		t.Fatalf("state after update: %+v", st)
	}
	if strings.Join(hooks, ",") != "service:echo,service:echo" {
		t.Fatalf("OnInstalled hooks: %v", hooks)
	}
	audit, _ := m.Audit(10)
	if len(audit) < 3 || audit[0].Action != "update" || audit[0].FromVersion != "1.0.0" || audit[0].ToVersion != "1.1.0" || audit[0].ZipSHA256 == "" {
		t.Fatalf("audit: %+v", audit)
	}
}

func TestManagerEncryptsPATAndDefaults(t *testing.T) {
	m := &Manager{DB: newDB(t), Client: &Client{}, Encrypt: func(p string) (string, error) { return "wick_cenc_x" + p, nil }}
	s, err := m.Save("", SourceInput{Type: TypeGitHub, Repo: "acme/plugins", Private: true, PAT: "ghp_secret"}, "a")
	if err != nil {
		t.Fatal(err)
	}
	if s.PAT != "wick_cenc_xghp_secret" || s.Name != "acme/plugins" {
		t.Fatalf("pat/name: %q %q", s.PAT, s.Name)
	}
	s2, _ := m.Save(s.ID, SourceInput{Type: TypeGitHub, Repo: "acme/plugins", Private: true}, "a")
	if s2.PAT != s.PAT {
		t.Fatal("empty PAT on edit must keep the stored one")
	}
	if _, err := m.Save("", SourceInput{Type: TypeGitHub, Repo: "nope"}, "a"); err == nil {
		t.Fatal("bad repo accepted")
	}
}

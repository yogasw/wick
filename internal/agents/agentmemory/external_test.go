package agentmemory

// Tests for reaching the store from outside wick.
//
// What is being protected is the whole point of the coverage: every
// observation the feature captures came out of a client session, so a gate
// that opens by accident is not a bug with a workaround. The cases below are
// therefore written as "what does an outsider get", not "does the function
// return true".

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/yogasw/wick/pkg/tool"
	"testing"
)

const extID = "ext-mem"

// ── harness ──────────────────────────────────────────────────────────

// fakeDaemon stands in for the memory backend on loopback. It answers the
// health path, and records what actually reached it — which is how "refused"
// is told apart from "refused after forwarding it anyway".
type fakeDaemon struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits int
	path string
	auth string
}

func (d *fakeDaemon) seen() (hits int, path, auth string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hits, d.path, d.auth
}

// startFakeDaemon points m at a live loopback server, the way a real start
// would: the bound port is recorded and the cached health result dropped.
func startFakeDaemon(t *testing.T, m *Manager) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		d.mu.Lock()
		d.hits++
		d.path = r.URL.Path
		d.auth = r.Header.Get("Authorization")
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(d.srv.Close)

	u, err := url.Parse(d.srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("test server port: %v", err)
	}
	m.port.Store(int32(port))
	m.invalidateHealth()
	return d
}

// withExternalBackend registers the backend these tests drive and wires the
// real route registration over fs, so the getters under test are the ones
// production wires — not ones the test set by hand.
func withExternalBackend(t *testing.T, fs *fakeStore) (*Backend, map[string]tool.HandlerFunc) {
	t.Helper()
	Register(Descriptor{ID: extID, DisplayName: "ext-mem", BinName: "ext-mem-no-such-binary",
		PrefPort: 41800, HealthPath: "/healthz", Data: &fakeData{}})
	rr := &recordingRouter{routes: map[string]tool.HandlerFunc{}}
	prev := store
	t.Cleanup(func() { store = prev })
	RegisterRoutes(rr, fs)
	be, ok := Get(extID)
	if !ok {
		t.Fatal("the test backend did not register")
	}
	// Each test starts from a clean count, so an assertion about rejections
	// describes its own requests (the registry — and the manager on it — is
	// process-wide).
	be.Mgr.ext = externalStats{}
	return be, rr.routes
}

// callExternal drives the mounted root route as an off-machine caller.
func callExternal(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = "203.0.113.7:51544"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// ── the gate ─────────────────────────────────────────────────────────

// TestExternalDefaultsToRefused is the requirement that matters most: a host
// that did not ask for external access gets nothing, and the daemon is never
// reached on the way to finding that out.
func TestExternalDefaultsToRefused(t *testing.T) {
	fs := &fakeStore{enabled: true, admin: true}
	be, _ := withExternalBackend(t, fs)
	daemon := startFakeDaemon(t, be.Mgr)

	w := callExternal(ExternalProxy(extID), http.MethodGet, ExternalMountPath(extID)+"/api/v1/projects", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — external access is off by default", w.Code)
	}
	if hits, _, _ := daemon.seen(); hits != 0 {
		t.Fatalf("the daemon was reached %d times by a request that should never have left wick", hits)
	}

	// The master switch off is a 404, not a 403: a host not running the
	// feature must look like one, the same rule the panel endpoints follow.
	fs.enabled = false
	w = callExternal(ExternalProxy(extID), http.MethodGet, ExternalMountPath(extID)+"/api/v1/projects", "tok")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 with the master switch off", w.Code)
	}
}

// TestExternalTokenDecidesEveryRequest walks the four answers a caller can
// get once the switch is on, and checks the daemon only ever saw the one that
// was allowed.
func TestExternalTokenDecidesEveryRequest(t *testing.T) {
	const good = "agmem_the-real-one"
	fs := &fakeStore{enabled: true, admin: true, external: true,
		set: Settings{Tuning: Tuning{AuthToken: "daemons-own-token"}}}
	be, _ := withExternalBackend(t, fs)
	daemon := startFakeDaemon(t, be.Mgr)
	h := ExternalProxy(extID)
	p := ExternalMountPath(extID) + "/api/v1/projects"

	// On, but nothing minted yet: refused rather than open.
	if w := callExternal(h, http.MethodGet, p, good); w.Code != http.StatusForbidden {
		t.Fatalf("no token stored: status %d, want 403", w.Code)
	}

	fs.extToken = good

	if w := callExternal(h, http.MethodGet, p, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer sent: status %d, want 401", w.Code)
	}
	if w := callExternal(h, http.MethodGet, p, "agmem_not-it"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d, want 401", w.Code)
	}
	// A prefix of the real token is a wrong token. This is the case a
	// HasPrefix or a truncating compare would wave through.
	if w := callExternal(h, http.MethodGet, p, good[:len(good)-3]); w.Code != http.StatusUnauthorized {
		t.Fatalf("truncated token: status %d, want 401", w.Code)
	}
	if hits, _, _ := daemon.seen(); hits != 0 {
		t.Fatalf("a refused request still reached the daemon (%d hits)", hits)
	}

	w := callExternal(h, http.MethodGet, p, good)
	if w.Code != http.StatusOK {
		t.Fatalf("right token: status %d, body %s", w.Code, w.Body.String())
	}
	hits, path, auth := daemon.seen()
	if hits != 1 {
		t.Fatalf("the daemon saw %d requests, want 1", hits)
	}
	if path != "/api/v1/projects" {
		t.Fatalf("forwarded path %q — the mount prefix must be stripped", path)
	}
	// The caller's credential is wick's to check and the daemon's never to
	// see; the daemon gets its OWN token instead.
	if auth != "Bearer daemons-own-token" {
		t.Fatalf("upstream Authorization %q — the caller's token must not be forwarded", auth)
	}
	if strings.Contains(auth, good) {
		t.Fatalf("the caller's token leaked upstream: %q", auth)
	}
}

// TestRevocationBitesImmediately: the token is read per request, so revoking
// it stops the next call — no restart, no cache to wait out.
func TestRevocationBitesImmediately(t *testing.T) {
	const good = "agmem_live-token"
	fs := &fakeStore{enabled: true, admin: true, external: true, extToken: good}
	be, _ := withExternalBackend(t, fs)
	startFakeDaemon(t, be.Mgr)
	h := ExternalProxy(extID)
	p := ExternalMountPath(extID) + "/api/v1/projects"

	if w := callExternal(h, http.MethodGet, p, good); w.Code != http.StatusOK {
		t.Fatalf("before revocation: status %d", w.Code)
	}
	fs.extToken = ""
	if w := callExternal(h, http.MethodGet, p, good); w.Code != http.StatusForbidden {
		t.Fatalf("after revocation: status %d, want 403 on the very next request", w.Code)
	}
	// Same for the switch: flipping it off stops the token that still works.
	fs.extToken, fs.external = good, false
	if w := callExternal(h, http.MethodGet, p, good); w.Code != http.StatusForbidden {
		t.Fatalf("after switching off: status %d, want 403", w.Code)
	}
}

// TestOnlyDataPathsAreExposed: the backend's own web UI is deliberately kept
// off the wick root (PLAN §13.2), and this route must not be the accident
// that reverses that.
func TestOnlyDataPathsAreExposed(t *testing.T) {
	const good = "agmem_ok"
	fs := &fakeStore{enabled: true, admin: true, external: true, extToken: good}
	be, _ := withExternalBackend(t, fs)
	daemon := startFakeDaemon(t, be.Mgr)
	h := ExternalProxy(extID)

	for _, path := range []string{"/web", "/web/index.html", "/", "/hook"} {
		w := callExternal(h, http.MethodGet, ExternalMountPath(extID)+path, good)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 — it is not on the exposed list", path, w.Code)
		}
	}
	if hits, _, _ := daemon.seen(); hits != 0 {
		t.Fatalf("an unexposed path still reached the daemon (%d hits)", hits)
	}
	for _, path := range []string{"/api/v1/projects", "/mcp"} {
		if w := callExternal(h, http.MethodPost, ExternalMountPath(extID)+path, good); w.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200 — it is on the exposed list", path, w.Code)
		}
	}
}

// TestRejectionsAreVisible: a refusal nobody can see makes "why can't my
// script reach it?" unanswerable, which is why airouter publishes its
// rejected requests. There is no live stream here, so they are counted and
// kept — with the REASON, which is the part a status code cannot carry.
func TestRejectionsAreVisible(t *testing.T) {
	fs := &fakeStore{enabled: true, admin: true, external: true}
	be, routes := withExternalBackend(t, fs)
	startFakeDaemon(t, be.Mgr)
	h := ExternalProxy(extID)
	p := ExternalMountPath(extID) + "/api/v1/projects"

	callExternal(h, http.MethodGet, p, "whatever") // no token stored
	fs.extToken = "agmem_ok"
	callExternal(h, http.MethodGet, p, "")          // no bearer
	callExternal(h, http.MethodGet, p, "agmem_bad") // wrong token
	callExternal(h, http.MethodGet, p, "agmem_ok")  // allowed

	allowed, rejected, recent := be.Mgr.ExternalSnapshot()
	if allowed != 1 || rejected != 3 {
		t.Fatalf("allowed=%d rejected=%d, want 1 and 3", allowed, rejected)
	}
	if len(recent) != 3 {
		t.Fatalf("recent has %d entries, want 3", len(recent))
	}
	// Newest first — the panel shows the last refusal at the top.
	wantReasons := []string{reasonBadToken, reasonNoCredential, reasonNoToken}
	for i, want := range wantReasons {
		if recent[i].Reason != want {
			t.Errorf("recent[%d].reason = %q, want %q", i, recent[i].Reason, want)
		}
	}
	if recent[0].ClientIP != "203.0.113.7" {
		t.Errorf("client_ip %q — a refusal has to say who was refused", recent[0].ClientIP)
	}
	if recent[0].Path != "/api/v1/projects" || recent[0].Status != http.StatusUnauthorized {
		t.Errorf("recent[0] = %+v", recent[0])
	}

	// …and the panel can read all of it back.
	w, c := get(nil)
	routes["GET "+"/agentmemory/"+extID+"/external"](c)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /external: status %d", w.Code)
	}
	body := decodeBody(t, w)
	ext, ok := body["external"].(map[string]any)
	if !ok {
		t.Fatalf("no external block in %v", body)
	}
	if ext["rejected_total"].(float64) != 3 {
		t.Errorf("rejected_total = %v, want 3", ext["rejected_total"])
	}
	if rows, ok := ext["recent"].([]any); !ok || len(rows) != 3 {
		t.Errorf("recent came back as %v", ext["recent"])
	}
}

// TestTokenCompareIsConstantTime guards the comparison itself. It is a
// source-level check on purpose: a `==` on two strings behaves identically to
// a constant-time compare in every functional test that could be written, so
// the only way to catch the swap is to look at the code. A leaked token is
// the thing at stake, and byte-at-a-time string equality leaks its length and
// its prefix through timing.
func TestTokenCompareIsConstantTime(t *testing.T) {
	src, err := os.ReadFile("external.go")
	if err != nil {
		t.Fatalf("read external.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "subtle.ConstantTimeCompare([]byte(got), []byte(want))") {
		t.Error("the bearer comparison is no longer subtle.ConstantTimeCompare — a plain == leaks the token through timing")
	}
	for _, bad := range []string{"got == want", "want == got", "strings.HasPrefix(got, want)"} {
		if strings.Contains(body, bad) {
			t.Errorf("external.go compares the token with %q", bad)
		}
	}
}

// ── the managing endpoints ───────────────────────────────────────────

// TestExternalControls covers the three writes: the switch refuses to open a
// tokenless route, minting shows the token exactly once, and revoking clears
// both halves.
func TestExternalControls(t *testing.T) {
	fs := &fakeStore{enabled: true, admin: true}
	_, routes := withExternalBackend(t, fs)
	p := "/agentmemory/" + extID

	// Turning it on with no token is refused: an enabled route with no
	// token answers 403 to everything, which looks like a broken feature.
	w, c := post(url.Values{"enabled": {"true"}})
	routes["POST "+p+"/external"](c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("enable with no token: status %d, want 400", w.Code)
	}
	if fs.external {
		t.Fatal("the switch was flipped on despite the refusal")
	}

	// Mint. The plaintext token appears exactly here and nowhere else.
	w, c = post(nil)
	routes["POST "+p+"/external/token"](c)
	if w.Code != http.StatusOK {
		t.Fatalf("mint: status %d (%s)", w.Code, w.Body.String())
	}
	minted, _ := decodeBody(t, w)["token"].(string)
	if !strings.HasPrefix(minted, externalTokenPrefix) || len(minted) < 40 {
		t.Fatalf("minted token looks wrong: %q", minted)
	}
	if fs.extToken != minted {
		t.Fatalf("stored %q, minted %q", fs.extToken, minted)
	}

	// Reading the state back never echoes it — only that one exists.
	w, c = get(nil)
	routes["GET "+p+"/external"](c)
	ext := decodeBody(t, w)["external"].(map[string]any)
	if ext["has_token"] != true {
		t.Error("has_token is false right after minting")
	}
	if strings.Contains(w.Body.String(), minted) {
		t.Fatal("the stored token was echoed back to the page")
	}

	// Now the switch takes.
	_, c = post(url.Values{"enabled": {"true"}})
	routes["POST "+p+"/external"](c)
	if !fs.external {
		t.Fatal("the switch did not turn on with a token stored")
	}

	// Minting again rotates: a new value, and the old one is gone.
	w, c = post(nil)
	routes["POST "+p+"/external/token"](c)
	second, _ := decodeBody(t, w)["token"].(string)
	if second == minted || fs.extToken != second {
		t.Fatalf("rotation did not replace the token (%q → %q, stored %q)", minted, second, fs.extToken)
	}

	// Revoke clears the token AND closes the switch — neither leftover
	// state is one an operator would want.
	w, c = post(nil)
	routes["POST "+p+"/external/revoke"](c)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: status %d", w.Code)
	}
	if fs.extToken != "" || fs.external {
		t.Fatalf("after revoke: token %q, enabled %v", fs.extToken, fs.external)
	}
	ext = decodeBody(t, w)["external"].(map[string]any)
	if ext["has_token"] != false || ext["enabled"] != false {
		t.Fatalf("the state still claims %v", ext)
	}
}

// TestExternalStateNamesTheURL: nobody should have to guess the address, so
// the state carries the one the operator is demonstrably reaching wick on.
func TestExternalStateNamesTheURL(t *testing.T) {
	_, routes := withExternalBackend(t, &fakeStore{enabled: true, admin: true})
	w, c := get(nil)
	routes["GET /agentmemory/"+extID+"/external"](c)
	ext := decodeBody(t, w)["external"].(map[string]any)
	if url, _ := ext["url"].(string); !strings.HasSuffix(url, ExternalMountPath(extID)) {
		t.Fatalf("url %q does not end at the mount", url)
	}
	paths, _ := ext["paths"].([]any)
	if len(paths) != len(externalUpstreamPrefixes) {
		t.Fatalf("paths %v — the panel has to be able to say what is reachable", ext["paths"])
	}
}

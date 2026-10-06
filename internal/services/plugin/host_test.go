package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/service/a2aservice"
)

type nopConn struct{}

func (nopConn) Configure(context.Context, map[string]string) error { return nil }
func (nopConn) Health(context.Context) error                       { return nil }

// fakeProcs serves handler on each spawned socket; crash() ends the newest.
type fakeProcs struct {
	mu      sync.Mutex
	handler http.Handler
	spawns  []time.Time
	envs    [][]string
	crashes []chan struct{}
	// delay slows each spawn down (a boot that takes a while).
	delay time.Duration
}

func (f *fakeProcs) spawn(_, socket string, env []string, stderr io.Writer) (func(), wickplugin.ToolConn, <-chan struct{}, error) {
	f.mu.Lock()
	delay := f.delay
	f.mu.Unlock()
	time.Sleep(delay)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, nil, nil, err
	}
	srv := &http.Server{Handler: f.handler}
	go func() { _ = srv.Serve(ln) }()
	exited := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { _ = srv.Close(); close(exited) }) }
	crash := make(chan struct{})
	go func() { <-crash; end() }()
	fmt.Fprintln(stderr, "plugin booted")
	f.mu.Lock()
	f.spawns = append(f.spawns, time.Now())
	f.envs = append(f.envs, env)
	f.crashes = append(f.crashes, crash)
	f.mu.Unlock()
	return end, nopConn{}, exited, nil
}

func (f *fakeProcs) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.spawns) }

func (f *fakeProcs) crash() {
	f.mu.Lock()
	c := f.crashes[len(f.crashes)-1]
	f.mu.Unlock()
	close(c)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTestHost(t *testing.T, h http.Handler, sm wickplugin.ServiceModule) (*Host, *fakeProcs, *httptest.Server) {
	t.Helper()
	dir, err := os.MkdirTemp("", "svc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeProcs{handler: h}
	host := NewHost(NewTokens(""), dir)
	host.spawn = f.spawn
	host.BaseURL = func() string { return "http://wick.test" }
	host.SessionUser = func(r *http.Request) *User {
		if r.Header.Get("Cookie") == "session=ok" {
			return &User{ID: "u1", Email: "u1@example.test", Name: "U One"}
		}
		return nil
	}
	s := host.Add(sm.Meta.Key, "1.0.0", sm, "bin")
	s.Sup.Start()
	t.Cleanup(host.StopAll)
	waitFor(t, "running", func() bool { return s.Sup.Status().State == StateRunning })
	mux := http.NewServeMux()
	mux.Handle("/x/", host)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return host, f, srv
}

func TestSupervisorRestartsWithBackoff(t *testing.T) {
	oldMin, oldMax := BackoffMin, BackoffMax
	BackoffMin, BackoffMax = 40*time.Millisecond, 160*time.Millisecond
	t.Cleanup(func() { BackoffMin, BackoffMax = oldMin, oldMax })
	host, f, _ := newTestHost(t, http.NotFoundHandler(), wickplugin.ServiceModule{Meta: wickplugin.ToolMeta{Key: "crashy"}})
	s, _ := host.Get("crashy")
	for i := 1; i <= 3; i++ {
		f.crash()
		waitFor(t, "restart", func() bool { return f.count() == i+1 && s.Sup.Status().State == StateRunning })
	}
	if st := s.Sup.Status(); st.Restarts != 3 || st.LastError == "" {
		t.Fatalf("status = %+v", st)
	}
	gaps := []time.Duration{}
	for i := 1; i < len(f.spawns); i++ {
		gaps = append(gaps, f.spawns[i].Sub(f.spawns[i-1]))
	}
	// 40ms, 80ms, 160ms: each wait at least the backoff step.
	for i, min := range []time.Duration{40, 80, 160} {
		if gaps[i] < min*time.Millisecond {
			t.Errorf("gap %d = %s, want >= %dms", i, gaps[i], min)
		}
	}
	logs := strings.Join(s.Sup.Logs.Lines(), "\n")
	if !strings.Contains(logs, "plugin booted") || !strings.Contains(logs, "restarting in") {
		t.Errorf("logs missing plugin stderr / restart lines:\n%s", logs)
	}
	s.Sup.Stop()
	if s.Sup.Status().State != StateStopped {
		t.Fatal("stop did not stop")
	}
	n := f.count()
	time.Sleep(250 * time.Millisecond)
	if f.count() != n {
		t.Fatal("stopped service was restarted")
	}
}

func TestRingLogKeepsLast(t *testing.T) {
	l := NewRingLog(3)
	fmt.Fprint(l, "a\nb\nc\nd\npart")
	fmt.Fprint(l, "ial\n")
	if got := strings.Join(l.Lines(), ","); got != "c,d,partial" {
		t.Fatalf("lines = %s", got)
	}
}

func authModule() (http.Handler, wickplugin.ServiceModule) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s user=%s base=%s auth=%s", r.URL.Path, r.Header.Get(wickplugin.HeaderUserID),
			r.Header.Get(wickplugin.HeaderBase), r.Header.Get("Authorization"))
	})
	return mux, wickplugin.ServiceModule{Meta: wickplugin.ToolMeta{Key: "authy"}, Routes: []wickplugin.ServiceRoute{
		{Prefix: "/pub", Auth: wickplugin.AuthPublic}, {Prefix: "/api", Auth: wickplugin.AuthToken}, {Prefix: "/me", Auth: wickplugin.AuthSession},
	}}
}

func get(t *testing.T, url string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRouteAuth(t *testing.T) {
	h, sm := authModule()
	host, _, srv := newTestHost(t, h, sm)
	u := srv.URL + "/x/authy"

	if code, body := get(t, u+"/pub/a", map[string]string{"X-Wick-User-Id": "forged"}); code != 200 || body != "path=/pub/a user= base=/x/authy auth=" {
		t.Fatalf("public: %d %q", code, body)
	}
	if code, _ := get(t, u+"/api/x", nil); code != 401 {
		t.Fatalf("token route without token: %d", code)
	}
	if code, _ := get(t, u+"/api/x", map[string]string{"Authorization": "Bearer wick_svc_wrong"}); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	tok, plain, err := host.Tokens.Generate("authy", "ci")
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(t, u+"/api/x", map[string]string{"Authorization": "Bearer " + plain}); code != 200 || !strings.HasSuffix(body, "auth=") {
		t.Fatalf("good token: %d %q (the plugin must not see the bearer)", code, body)
	}
	_, rotated, _ := host.Tokens.Rotate("authy", tok.ID)
	if code, _ := get(t, u+"/api/x", map[string]string{"Authorization": "Bearer " + plain}); code != 401 {
		t.Fatalf("rotated-away token: %d", code)
	}
	if code, _ := get(t, u+"/api/x", map[string]string{"Authorization": "Bearer " + rotated}); code != 200 {
		t.Fatalf("rotated token: %d", code)
	}
	_ = host.Tokens.Revoke("authy", tok.ID)
	if code, _ := get(t, u+"/api/x", map[string]string{"Authorization": "Bearer " + rotated}); code != 401 {
		t.Fatalf("revoked token: %d", code)
	}
	if _, _, ok := host.Tokens.LookupCallback(rotated); ok {
		t.Fatal("access token must not work as a callback token")
	}
	if code, _ := get(t, u+"/me", nil); code != 401 {
		t.Fatalf("session route without session: %d", code)
	}
	if code, body := get(t, u+"/me", map[string]string{"Cookie": "session=ok"}); code != 200 || !strings.Contains(body, "user=u1") {
		t.Fatalf("session route: %d %q", code, body)
	}
	if code, _ := get(t, u+"/other", nil); code != 404 {
		t.Fatalf("undeclared route: %d", code)
	}
	if code, _ := get(t, u+wickplugin.RemotePathDescribe, nil); code != 404 {
		t.Fatalf("internal remote path reachable from outside: %d", code)
	}
}

func TestSSEFlushedPerEvent(t *testing.T) {
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "data: second\n\n")
	})
	_, _, srv := newTestHost(t, mux, wickplugin.ServiceModule{Meta: wickplugin.ToolMeta{Key: "sse"},
		Routes: []wickplugin.ServiceRoute{{Prefix: "/", Auth: wickplugin.AuthPublic}}})
	resp, err := http.Get(srv.URL + "/x/sse/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "data: first\n" {
			t.Fatalf("first line = %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first SSE event not delivered before the stream ended")
	}
	close(release)
}

func TestCallbackTokenScope(t *testing.T) {
	sm := wickplugin.ServiceModule{Meta: wickplugin.ToolMeta{Key: "caller"}, CallbackScopes: []string{"notify"}}
	host, f, srv := newTestHost(t, http.NotFoundHandler(), sm)
	host.HandleCallback("POST /api/notify", "notify", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := CallerFrom(r.Context())
		fmt.Fprint(w, "notified by "+c.Key)
	}))
	host.HandleCallback("POST /api/execute", "connectors:execute", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	var tok, base string
	for _, e := range f.envs[0] {
		if v, ok := strings.CutPrefix(e, wickplugin.EnvPluginToken+"="); ok {
			tok = v
		}
		if v, ok := strings.CutPrefix(e, wickplugin.EnvBaseURL+"="); ok {
			base = v
		}
	}
	if tok == "" || base != "http://wick.test" {
		t.Fatalf("process env missing callback token/base url")
	}
	post := func(path, bearer string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/x/-"+path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get(t, srv.URL+"/x/-/api/whoami", map[string]string{"Authorization": "Bearer " + tok}); code != 200 || !strings.Contains(body, `"plugin":"caller"`) {
		t.Fatalf("whoami: %d %s", code, body)
	}
	if code, body := post("/api/notify", tok); code != 200 || body != "notified by caller" {
		t.Fatalf("in-scope call: %d %s", code, body)
	}
	if code, _ := post("/api/execute", tok); code != 403 {
		t.Fatalf("out-of-scope call: %d", code)
	}
	if code, _ := post("/api/notify", "wick_plg_forged"); code != 401 {
		t.Fatalf("forged token: %d", code)
	}
	// Revoke: the live token dies now, and a restart gets none.
	_ = host.Tokens.SetCallbackRevoked("caller", true)
	if code, _ := post("/api/notify", tok); code != 401 {
		t.Fatalf("revoked token: %d", code)
	}
	s, _ := host.Get("caller")
	s.Sup.Restart()
	waitFor(t, "respawn", func() bool { return f.count() == 2 && s.Sup.Status().State == StateRunning })
	for _, e := range f.envs[1] {
		if strings.HasPrefix(e, wickplugin.EnvPluginToken+"=") {
			t.Fatal("revoked plugin still got a callback token")
		}
	}
	// Re-allow + restart: a new token, the old one stays dead.
	_ = host.Tokens.SetCallbackRevoked("caller", false)
	s.Sup.Restart()
	waitFor(t, "respawn", func() bool { return f.count() == 3 && s.Sup.Status().State == StateRunning })
	if code, _ := post("/api/notify", tok); code != 401 {
		t.Fatalf("old token after re-allow: %d", code)
	}
}

// TestA2AClientThroughHost drives an a2aservice repeater mounted behind the
// host's /x/{key} proxy with the a2a-go client: card, message/send and
// message/stream.
func TestA2AClientThroughHost(t *testing.T) {
	mux := http.NewServeMux()
	a2aservice.Mount(mux, a2aservice.Card{Name: "Echo bot", Description: "echo"}, func(_ context.Context, contextID, text string, chunks chan<- string) error {
		chunks <- "echo: "
		chunks <- text
		return nil
	})
	_, _, srv := newTestHost(t, mux, wickplugin.ServiceModule{Meta: wickplugin.ToolMeta{Key: "repeater"},
		Routes: []wickplugin.ServiceRoute{{Prefix: "/", Auth: wickplugin.AuthPublic}}})
	ctx := context.Background()
	card, err := (&agentcard.Resolver{Client: srv.Client(), CardParser: agentcard.DefaultCardParser}).Resolve(ctx, srv.URL+"/x/repeater", agentcard.WithPath("/.well-known/agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "Echo bot" || card.SupportedInterfaces[0].URL != srv.URL+"/x/repeater/" {
		t.Fatalf("card = %s %s", card.Name, card.SupportedInterfaces[0].URL)
	}
	c, err := a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SendMessage(ctx, &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := res.(*a2a.Task)
	if !ok || task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("send result = %T %+v", res, res)
	}
	var reply string
	for _, art := range task.Artifacts {
		for _, p := range art.Parts {
			reply += p.Text()
		}
	}
	if reply != "echo: hi" {
		t.Fatalf("reply = %q", reply)
	}
	var kinds []string
	for ev, err := range c.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("yo"))}) {
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, fmt.Sprintf("%T", ev))
	}
	if len(kinds) < 4 {
		t.Fatalf("stream events = %v", kinds)
	}
	b, _ := json.Marshal(kinds)
	t.Logf("message/stream events via host: %s", b)
}

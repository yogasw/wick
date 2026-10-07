package terminal

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	provider "github.com/yogasw/wick/internal/agents/provider"

	"github.com/yogasw/wick/pkg/safeexec"
)

// The test binary doubles as a fake gotty (FAKE_GOTTY=1): it parses the
// flags wick passes, demands Basic auth + the auth token in the ws init
// message like the real one, echoes ws frames, and starts a long-running
// child in its own session — the shape that outlives a naive kill.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_GOTTY") == "1" {
		fakeGotty(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func fakeGotty(argv []string) {
	fs := flag.NewFlagSet("gotty", flag.ExitOnError)
	port := fs.Int("port", 0, "")
	path := fs.String("path", "/", "")
	cfg := fs.String("config", "", "")
	fs.String("address", "", "")
	fs.Bool("once", false, "")
	fs.Bool("permit-write", false, "")
	fs.Int("timeout", 0, "")
	fs.Int("close-timeout", 0, "")
	fs.String("title-format", "", "")
	_ = fs.Parse(argv)
	cred := new(string)
	if b, err := os.ReadFile(*cfg); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "credential = "); ok {
				*cred, _ = strconv.Unquote(v)
			}
		}
	}
	child := safeexec.Command("sleep", "300")
	setSession(child)
	_ = child.Start()
	fmt.Fprintf(os.Stderr, "child=%d\n", child.Process.Pid)
	_ = os.WriteFile(os.Getenv("FAKE_GOTTY_CHILD"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
	http.Handle(*path, fakeGottyHandler(*path, *cred))
	_ = http.ListenAndServe("127.0.0.1:"+strconv.Itoa(*port), nil)
}

// fakeGottyHandler is the part of gotty the proxy talks to.
func fakeGottyHandler(base, cred string) http.Handler {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "http://"+r.Host }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want || r.Header.Get("Cookie") != "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="GoTTY"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch strings.TrimPrefix(r.URL.Path, base) {
		case "":
			io.WriteString(w, "<html>gotty</html>")
		case "auth_token.js":
			fmt.Fprintf(w, "var gotty_auth_token = '%s';", cred)
		case "ws":
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			_, msg, err := c.ReadMessage()
			var init struct{ AuthToken string }
			if err != nil || json.Unmarshal(msg, &init) != nil || init.AuthToken != cred {
				return
			}
			for {
				mt, m, err := c.ReadMessage()
				if err != nil {
					return
				}
				_ = c.WriteMessage(mt, append([]byte("echo:"), m...))
			}
		default:
			http.NotFound(w, r)
		}
	})
}

// sessionOn wires a Session to an httptest upstream without a process.
func sessionOn(t *testing.T, base, cred string) (*Session, *Manager) {
	t.Helper()
	up := httptest.NewServer(fakeGottyHandler(base, cred))
	t.Cleanup(up.Close)
	_, portS, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	port, _ := strconv.Atoi(portS)
	m := NewManager()
	s := &Session{ID: "sid", BasePath: base, port: port, credential: cred, done: make(chan struct{}), m: m, Started: time.Now()}
	m.sessions[s.ID] = s
	return s, m
}

func TestProxyHTTPKeepsCredentialServerSide(t *testing.T) {
	s, _ := sessionOn(t, "/t/sid/", "u:secret")
	wick := httptest.NewServer(s)
	defer wick.Close()

	req, _ := http.NewRequest("GET", wick.URL+"/t/sid/", nil)
	req.Header.Set("Cookie", "wick_session=abc") // must not reach gotty
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("index: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	resp, _ = http.Get(wick.URL + "/t/sid/auth_token.js")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "secret") || string(b) != authTokenJS {
		t.Fatalf("auth_token.js leaked the credential: %q", b)
	}
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Fatal("WWW-Authenticate must not reach the browser")
	}
	resp, _ = http.Get(wick.URL + "/other/")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("path outside the session: %d", resp.StatusCode)
	}
	resp, _ = http.Post(wick.URL+"/t/sid/", "text/plain", nil)
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("POST: %d", resp.StatusCode)
	}
}

func TestProxyWebsocketEchoAndCloseEndsSession(t *testing.T) {
	s, m := sessionOn(t, "/t/sid/", "u:secret")
	wick := httptest.NewServer(s)
	defer wick.Close()

	h := http.Header{"Origin": {wick.URL}}
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(wick.URL, "http")+"/t/sid/ws", h)
	if err != nil {
		t.Fatal(err)
	}
	// The browser's init carries the blank token it was given.
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"Arguments":"","AuthToken":""}`))
	_ = c.WriteMessage(websocket.TextMessage, []byte("1ls"))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil || string(msg) != "echo:1ls" {
		t.Fatalf("echo = %q, %v", msg, err)
	}

	// A second attach is refused (gotty --once).
	if _, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(wick.URL, "http")+"/t/sid/ws", h); err == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("second attach: %v", err)
	}

	c.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("closing the websocket did not end the session")
	}
	if m.Get("sid") != nil {
		t.Fatal("ended session is still registered")
	}
}

func TestProxyWebsocketRejectsCrossOrigin(t *testing.T) {
	s, _ := sessionOn(t, "/t/sid/", "u:p")
	wick := httptest.NewServer(s)
	defer wick.Close()
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(wick.URL, "http")+"/t/sid/ws", http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin ws must be refused: %v", err)
	}
}

func TestRestoreUpgrade(t *testing.T) {
	h := http.Header{"Connection": {"keep-alive"}}
	restoreUpgrade(h)
	if h.Get("Connection") != "Upgrade" {
		t.Fatalf("Connection = %q", h.Get("Connection"))
	}
	h = http.Header{"Connection": {"keep-alive, Upgrade"}}
	restoreUpgrade(h)
	if h.Get("Connection") != "keep-alive, Upgrade" {
		t.Fatalf("existing token rewritten: %q", h.Get("Connection"))
	}
}

func TestWithAuthToken(t *testing.T) {
	s := &Session{credential: "u:p"}
	got := string(s.withAuthToken([]byte(`{"Arguments":"?x","AuthToken":""}`)))
	if !strings.Contains(got, `"AuthToken":"u:p"`) || !strings.Contains(got, `"Arguments":"?x"`) {
		t.Fatalf("init = %s", got)
	}
	if string(s.withAuthToken([]byte("1abc"))) != "1abc" {
		t.Fatal("non-init frames must pass through")
	}
}

var _ = provider.TypeOMP

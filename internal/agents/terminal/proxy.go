package terminal

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// proxy.go is wick's reverse proxy in front of one gotty: plain HTTP for
// the page and its assets, a websocket pump for the terminal itself. The
// caller (the HTTP layer) has already checked the wick login and the
// provider admin guard; this only talks to 127.0.0.1.
//
// The gotty credential stays server-side: the proxy adds Basic auth to
// every upstream request, serves auth_token.js with the token blanked,
// and writes the real token into the websocket's init message on the way
// up. Browser cookies and Authorization never reach gotty.

// authTokenJS is what the browser gets instead of gotty's auth_token.js.
const authTokenJS = "var gotty_auth_token = '';"

func (s *Session) auth(h http.Header) {
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.credential)))
}

// ServeHTTP proxies one request to this session's gotty.
func (s *Session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.done:
		http.Error(w, "terminal closed", http.StatusGone)
		return
	default:
	}
	if !strings.HasPrefix(r.URL.Path, s.BasePath) && r.URL.Path+"/" != s.BasePath {
		http.NotFound(w, r)
		return
	}
	s.touch()
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.serveWS(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.httpProxy().ServeHTTP(w, r)
}

func (s *Session) httpProxy() *httputil.ReverseProxy {
	target, _ := url.Parse(s.upstream("http"))
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Accept-Encoding") // keep bodies plain for the auth_token.js rewrite
			s.auth(pr.Out.Header)
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("WWW-Authenticate")
			if strings.HasSuffix(resp.Request.URL.Path, "/auth_token.js") {
				resp.Body.Close()
				resp.Body = io.NopCloser(strings.NewReader(authTokenJS))
				resp.ContentLength = int64(len(authTokenJS))
				resp.Header.Set("Content-Length", strconv.Itoa(len(authTokenJS)))
			}
			resp.Header.Set("Cache-Control", "no-store")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "terminal unavailable", http.StatusBadGateway)
		},
	}
}

var upgrader = websocket.Upgrader{
	// Same-origin only: the terminal page is served by wick itself.
	CheckOrigin: sameOrigin,
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// restoreUpgrade puts back the "upgrade" Connection token some reverse
// proxies (nginx with a hardcoded `proxy_set_header Connection`) strip,
// without which gorilla rejects the handshake (same repair as the login
// TTY and internal/tty).
func restoreUpgrade(h http.Header) {
	for _, v := range h.Values("Connection") {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), "upgrade") {
				return
			}
		}
	}
	h.Set("Connection", "Upgrade")
}

// withAuthToken rewrites gotty's websocket init message
// ({"Arguments":…,"AuthToken":…}) to carry the real credential.
func (s *Session) withAuthToken(msg []byte) []byte {
	var init map[string]any
	if json.Unmarshal(bytes.TrimSpace(msg), &init) != nil {
		return msg
	}
	if _, ok := init["AuthToken"]; !ok {
		return msg
	}
	init["AuthToken"] = s.credential
	out, err := json.Marshal(init)
	if err != nil {
		return msg
	}
	return out
}

func (s *Session) serveWS(w http.ResponseWriter, r *http.Request) {
	if !s.wsTaken.CompareAndSwap(false, true) {
		http.Error(w, "terminal already attached", http.StatusConflict)
		return
	}
	restoreUpgrade(r.Header)

	target := s.upstream("ws") + r.URL.Path
	header := http.Header{"Origin": {s.upstream("http")}}
	s.auth(header)
	if p := websocket.Subprotocols(r); len(p) > 0 {
		header["Sec-WebSocket-Protocol"] = p
	}
	backend, resp, err := websocket.DefaultDialer.Dial(target, header)
	if err != nil {
		http.Error(w, "terminal unavailable", http.StatusBadGateway)
		s.Close("websocket dial failed")
		return
	}
	defer backend.Close()
	var respHeader http.Header
	if resp != nil {
		if p := resp.Header.Get("Sec-WebSocket-Protocol"); p != "" {
			respHeader = http.Header{"Sec-WebSocket-Protocol": {p}}
		}
	}
	client, err := upgrader.Upgrade(w, r, respHeader)
	if err != nil {
		s.Close("websocket upgrade failed")
		return
	}
	defer client.Close()
	log.Info().Str("component", "terminal").Str("event", "attach").Str("session", s.ID).Str("user", s.User).Msg("terminal: websocket attached")

	// The command starts once gotty has the init message; snapshot its
	// tree shortly after so a gotty crash cannot orphan it unseen.
	go func() {
		select {
		case <-time.After(time.Second):
			s.remember()
		case <-s.done:
		}
	}()

	up := make(chan struct{})
	go func() {
		defer close(up)
		first := true
		for {
			mt, msg, err := client.ReadMessage()
			if err != nil {
				return
			}
			if first && mt == websocket.TextMessage {
				msg, first = s.withAuthToken(msg), false
			}
			s.touch()
			if backend.WriteMessage(mt, msg) != nil {
				return
			}
		}
	}()
	down := make(chan struct{})
	go func() {
		defer close(down)
		for {
			mt, msg, err := backend.ReadMessage()
			if err != nil {
				return
			}
			s.touch()
			if client.WriteMessage(mt, msg) != nil {
				return
			}
		}
	}()
	select {
	case <-up:
	case <-down:
	case <-s.done:
	}
	s.Close("websocket closed")
}

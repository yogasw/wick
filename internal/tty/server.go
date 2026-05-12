package tty

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	Shell     string       // shell to run, default "bash"
	GottyBin  string       // path to gotty binary, default "gotty"
	GottyPort int          // gotty listen port, default 8081
	GottyAddr string       // gotty listen addr, default "127.0.0.1"
	Prefix    string       // URL prefix the handler is mounted at, e.g. "/tools/webtty/tty"
	Logger    *slog.Logger // optional; defaults to slog.Default()
}

type Server struct {
	cfg      Config
	mu       sync.Mutex
	cmd      *exec.Cmd
	proxy    *httputil.ReverseProxy
	upgrader websocket.Upgrader
	log      *slog.Logger
}

func New(cfg Config) *Server {
	if cfg.Shell == "" {
		cfg.Shell = "bash"
	}
	if cfg.GottyBin == "" {
		cfg.GottyBin = "gotty"
	}
	if cfg.GottyPort == 0 {
		cfg.GottyPort = 8081
	}
	if cfg.GottyAddr == "" {
		cfg.GottyAddr = "127.0.0.1"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	backendURL, _ := url.Parse(fmt.Sprintf("http://%s:%d", cfg.GottyAddr, cfg.GottyPort))
	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	proxy.ModifyResponse = func(r *http.Response) error {
		r.Header.Del("X-Frame-Options")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		cfg.Logger.Error("proxy error", "path", r.URL.Path, "error", err)
		http.Error(w, "backend unavailable", http.StatusBadGateway)
	}

	return &Server{
		cfg:   cfg,
		log:   cfg.Logger,
		proxy: proxy,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil {
		s.log.Info("tty: gotty already running")
		return nil
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.GottyAddr, s.cfg.GottyPort)
	s.log.Info("tty: starting gotty", "addr", addr, "shell", s.cfg.Shell)

	args := []string{
		"--permit-write",
		"--port", fmt.Sprintf("%d", s.cfg.GottyPort),
		"--address", s.cfg.GottyAddr,
		"--reconnect",
		"--reconnect-time", "3",
	}
	if s.cfg.Prefix != "" {
		// gotty --path tells it to embed the correct WebSocket URL in its JS
		args = append(args, "--path", s.cfg.Prefix)
	}
	args = append(args, s.cfg.Shell)

	cmd := exec.Command(s.cfg.GottyBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		s.log.Error("tty: failed to start gotty", "error", err)
		return fmt.Errorf("start gotty: %w", err)
	}

	s.cmd = cmd
	s.log.Info("tty: gotty spawned", "pid", cmd.Process.Pid, "addr", addr)
	return nil
}

func (s *Server) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil {
		return
	}
	pid := s.cmd.Process.Pid
	s.log.Info("tty: stopping gotty", "pid", pid)
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	s.cmd = nil
	s.log.Info("tty: gotty stopped", "pid", pid)
}

func (s *Server) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

// Shutdown stops gotty gracefully.
func (s *Server) Shutdown(_ context.Context) error {
	s.stop()
	return nil
}

// Handler returns an http.Handler for all terminal routes.
// Mount it at cfg.Prefix+"/" in your router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", s.handleStart)
	mux.HandleFunc("/stop", s.handleStop)
	mux.HandleFunc("/kill", s.handleKill)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/", s.handleProxy)

	if s.cfg.Prefix != "" {
		return http.StripPrefix(s.cfg.Prefix, mux)
	}
	return mux
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.log.Info("tty: /start requested", "remote", r.RemoteAddr)

	if err := s.start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	checkURL := fmt.Sprintf("http://%s:%d/", s.cfg.GottyAddr, s.cfg.GottyPort)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			s.log.Error("tty: gotty did not become ready in time")
			http.Error(w, "gotty startup timeout", http.StatusGatewayTimeout)
			return
		case <-time.After(120 * time.Millisecond):
			resp, err := http.Get(checkURL)
			if err == nil {
				resp.Body.Close()
				s.log.Info("tty: gotty ready")
				fmt.Fprint(w, "ok")
				return
			}
		}
	}
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.log.Info("tty: /stop requested", "remote", r.RemoteAddr)
	s.stop()
	fmt.Fprint(w, "ok")
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	s.log.Info("tty: /kill requested", "remote", r.RemoteAddr)
	s.stop()
	fmt.Fprint(w, "ok")
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.isRunning() {
		fmt.Fprint(w, "running")
	} else {
		fmt.Fprint(w, "stopped")
	}
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !s.isRunning() {
		http.Error(w, "terminal not running", http.StatusServiceUnavailable)
		return
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.proxyWebSocket(w, r)
		return
	}
	s.proxy.ServeHTTP(w, r)
}

func (s *Server) proxyWebSocket(w http.ResponseWriter, r *http.Request) {
	addr := fmt.Sprintf("%s:%d", s.cfg.GottyAddr, s.cfg.GottyPort)
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	targetURL := "ws://" + addr + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	reqProtos := websocket.Subprotocols(r)
	header := http.Header{"Origin": {fmt.Sprintf("http://%s", addr)}}
	if len(reqProtos) > 0 {
		header["Sec-WebSocket-Protocol"] = reqProtos
	}

	s.log.Debug("tty: dialing ws backend", "target", targetURL, "protocols", reqProtos)

	backend, resp, err := websocket.DefaultDialer.Dial(targetURL, header)
	if err != nil {
		s.log.Error("tty: ws dial failed", "target", targetURL, "error", err)
		http.Error(w, "ws backend unavailable", http.StatusBadGateway)
		return
	}
	defer backend.Close()

	var respHeader http.Header
	if resp != nil {
		if p := resp.Header.Get("Sec-WebSocket-Protocol"); p != "" {
			respHeader = http.Header{"Sec-WebSocket-Protocol": {p}}
		}
	}

	client, err := s.upgrader.Upgrade(w, r, respHeader)
	if err != nil {
		s.log.Error("tty: ws upgrade failed", "error", err)
		return
	}
	defer client.Close()

	s.log.Info("tty: ws session started", "remote", r.RemoteAddr)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			mt, msg, err := client.ReadMessage()
			if err != nil {
				s.log.Debug("tty: client disconnected", "remote", r.RemoteAddr, "error", err)
				return
			}
			if err := backend.WriteMessage(mt, msg); err != nil {
				s.log.Debug("tty: backend write error", "error", err)
				return
			}
		}
	}()
	go func() {
		for {
			mt, msg, err := backend.ReadMessage()
			if err != nil {
				s.log.Debug("tty: backend disconnected", "error", err)
				return
			}
			if err := client.WriteMessage(mt, msg); err != nil {
				s.log.Debug("tty: client write error", "remote", r.RemoteAddr, "error", err)
				return
			}
		}
	}()

	<-done
	s.log.Info("tty: ws session ended", "remote", r.RemoteAddr)
}

package tty

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Config struct {
	Shell     string // shell to run, default "bash"
	GottyBin  string // path to gotty binary, default "gotty"
	GottyPort int    // gotty listen port, default 8081
	GottyAddr string // gotty listen addr, default "127.0.0.1"
	Prefix    string // URL prefix the handler is mounted at, e.g. "/tools/webtty/tty"
}

type Server struct {
	cfg      Config
	mu       sync.Mutex
	cmd      *exec.Cmd
	proxy    *httputil.ReverseProxy
	upgrader websocket.Upgrader
	log      zerolog.Logger
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

	logger := log.With().Str("component", "tty").Logger()

	backendURL, _ := url.Parse(fmt.Sprintf("http://%s:%d", cfg.GottyAddr, cfg.GottyPort))
	logger.Info().
		Str("gotty_bin", cfg.GottyBin).
		Str("gotty_addr", cfg.GottyAddr).
		Int("gotty_port", cfg.GottyPort).
		Str("shell", cfg.Shell).
		Str("prefix", cfg.Prefix).
		Str("backend_url", backendURL.String()).
		Msg("tty: server configured")

	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	proxy.ModifyResponse = func(r *http.Response) error {
		r.Header.Del("X-Frame-Options")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error().Err(err).Str("path", r.URL.Path).Msg("tty: proxy error")
		http.Error(w, "backend unavailable", http.StatusBadGateway)
	}

	return &Server{
		cfg:   cfg,
		log:   logger,
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
		s.log.Info().Int("pid", s.cmd.Process.Pid).Msg("tty: gotty already running")
		return nil
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.GottyAddr, s.cfg.GottyPort)

	args := []string{
		"--permit-write",
		"--port", fmt.Sprintf("%d", s.cfg.GottyPort),
		"--address", s.cfg.GottyAddr,
		"--reconnect",
		"--reconnect-time", "3",
		s.cfg.Shell,
	}

	s.log.Info().
		Str("bin", s.cfg.GottyBin).
		Str("addr", addr).
		Str("shell", s.cfg.Shell).
		Strs("args", args).
		Msg("tty: spawning gotty")

	cmd := exec.Command(s.cfg.GottyBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		s.log.Error().Err(err).Str("bin", s.cfg.GottyBin).Msg("tty: failed to spawn gotty — is gotty in PATH?")
		return fmt.Errorf("start gotty: %w", err)
	}

	s.cmd = cmd
	s.log.Info().
		Int("pid", cmd.Process.Pid).
		Str("addr", addr).
		Msg("tty: gotty spawned")
	return nil
}

func (s *Server) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil {
		return
	}
	pid := s.cmd.Process.Pid
	s.log.Info().Int("pid", pid).Msg("tty: killing gotty")
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
	s.cmd = nil
	s.log.Info().Int("pid", pid).Msg("tty: gotty stopped")
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

	s.log.Info().Str("prefix", s.cfg.Prefix).Msg("tty: handler mounted")

	if s.cfg.Prefix != "" {
		return http.StripPrefix(s.cfg.Prefix, mux)
	}
	return mux
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Str("remote", r.RemoteAddr).Msg("tty: /start requested")

	if err := s.start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	checkURL := fmt.Sprintf("http://%s:%d/", s.cfg.GottyAddr, s.cfg.GottyPort)
	s.log.Info().Str("check_url", checkURL).Msg("tty: polling gotty readiness")

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	attempts := 0
	for {
		select {
		case <-ctx.Done():
			s.log.Error().Int("attempts", attempts).Msg("tty: gotty did not become ready in time")
			http.Error(w, "gotty startup timeout", http.StatusGatewayTimeout)
			return
		case <-time.After(120 * time.Millisecond):
			attempts++
			resp, err := http.Get(checkURL)
			if err == nil {
				resp.Body.Close()
				s.log.Info().Int("attempts", attempts).Msg("tty: gotty ready")
				fmt.Fprint(w, "ok")
				return
			}
			s.log.Debug().Err(err).Int("attempt", attempts).Msg("tty: gotty not ready yet")
		}
	}
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Str("remote", r.RemoteAddr).Msg("tty: /stop requested")
	s.stop()
	fmt.Fprint(w, "ok")
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Str("remote", r.RemoteAddr).Msg("tty: /kill requested")
	s.stop()
	fmt.Fprint(w, "ok")
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	running := s.isRunning()
	s.log.Debug().Bool("running", running).Msg("tty: /status")
	if running {
		fmt.Fprint(w, "running")
	} else {
		fmt.Fprint(w, "stopped")
	}
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	isWS := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	s.log.Debug().
		Str("method", r.Method).
		Str("path", r.URL.Path).
		Str("remote", r.RemoteAddr).
		Bool("websocket", isWS).
		Bool("gotty_running", s.isRunning()).
		Msg("tty: proxy request")

	if !s.isRunning() {
		s.log.Warn().Str("path", r.URL.Path).Msg("tty: request arrived but gotty not running — call /start first")
		http.Error(w, "terminal not running", http.StatusServiceUnavailable)
		return
	}
	if isWS {
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

	s.log.Info().
		Str("target", targetURL).
		Str("remote", r.RemoteAddr).
		Strs("protocols", reqProtos).
		Msg("tty: dialing ws backend")

	backend, resp, err := websocket.DefaultDialer.Dial(targetURL, header)
	if err != nil {
		s.log.Error().Err(err).Str("target", targetURL).Msg("tty: ws dial failed")
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
		s.log.Error().Err(err).Msg("tty: ws upgrade failed")
		return
	}
	defer client.Close()

	s.log.Info().Str("remote", r.RemoteAddr).Msg("tty: ws session started")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			mt, msg, err := client.ReadMessage()
			if err != nil {
				s.log.Debug().Err(err).Str("remote", r.RemoteAddr).Msg("tty: client disconnected")
				return
			}
			if err := backend.WriteMessage(mt, msg); err != nil {
				s.log.Debug().Err(err).Msg("tty: backend write error")
				return
			}
		}
	}()
	go func() {
		for {
			mt, msg, err := backend.ReadMessage()
			if err != nil {
				s.log.Debug().Err(err).Msg("tty: backend disconnected")
				return
			}
			if err := client.WriteMessage(mt, msg); err != nil {
				s.log.Debug().Err(err).Str("remote", r.RemoteAddr).Msg("tty: client write error")
				return
			}
		}
	}()

	<-done
	s.log.Info().Str("remote", r.RemoteAddr).Msg("tty: ws session ended")
}

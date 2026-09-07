package agents

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
	"github.com/yogasw/wick/pkg/tool"
)

// loginTTY owns the per-instance reconnect (login) TTY sessions for
// the providers SPA.
var loginTTY = logintty.NewManager()

// LoginTTYSessionDTO is the running-session view for the SPA.
type LoginTTYSessionDTO struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	RemainingS int    `json:"remaining_s"`
	CapS       int    `json:"cap_s"`
}

// LoginTTYStatusResponse is GET /api/providers/{type}/{name}/logintty:
// whether reconnect is available for this type, who is currently
// connected per the credential files, and the live session if any.
type LoginTTYStatusResponse struct {
	Supported   bool                `json:"supported"`
	Account     logintty.Account    `json:"account"`
	Session     *LoginTTYSessionDTO `json:"session,omitempty"`
	DefaultTTLS int                 `json:"default_ttl_s"`
	ExtendS     int                 `json:"extend_s"`
	MaxTTLS     int                 `json:"max_ttl_s"`
}

func loginTTYSessionDTO(s *logintty.Session) *LoginTTYSessionDTO {
	if s == nil {
		return nil
	}
	fr := s.TTLFrame()
	return &LoginTTYSessionDTO{
		ID:         s.ID,
		State:      s.State(),
		RemainingS: fr.RemainingS,
		CapS:       fr.CapS,
	}
}

// findLoginInstance resolves the {type}/{name} path pair to an
// instance, writing the 404 itself on miss.
func findLoginInstance(c *tool.Ctx) (provider.Instance, bool) {
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	ins, err := provider.Find(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return provider.Instance{}, false
	}
	return ins, true
}

// apiProviderLoginTTYStatus reports connect status for one instance:
// account from the credential files (honouring the instance's env
// overrides like CLAUDE_CONFIG_DIR) plus the live login session.
func apiProviderLoginTTYStatus(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	_, supported := logintty.LoginCommand(ins.Type, nil)
	c.JSON(http.StatusOK, LoginTTYStatusResponse{
		Supported:   supported,
		Account:     logintty.ReadAccount(ins.Type, ins.Env),
		Session:     loginTTYSessionDTO(loginTTY.Get(ins.Type, ins.Name)),
		DefaultTTLS: int(logintty.DefaultTTL.Seconds()),
		ExtendS:     int(logintty.ExtendStep.Seconds()),
		MaxTTLS:     int(logintty.MaxTTL.Seconds()),
	})
}

// apiProviderLoginTTYUsage reports current rate-limit utilization for
// the connected account (claude's 5h/7d windows). Best-effort: types
// without a usage API return supported=false, a failed fetch returns
// the error string so the SPA can show "usage unavailable".
func apiProviderLoginTTYUsage(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	windows, err := logintty.ReadUsage(ins.Type, ins.Env)
	if err != nil {
		if errors.Is(err, logintty.ErrUsageUnsupported) {
			c.JSON(http.StatusOK, map[string]any{"supported": false, "windows": []logintty.UsageWindow{}})
			return
		}
		c.JSON(http.StatusOK, map[string]any{"supported": true, "windows": []logintty.UsageWindow{}, "error": err.Error()})
		return
	}
	if windows == nil {
		windows = []logintty.UsageWindow{}
	}
	c.JSON(http.StatusOK, map[string]any{"supported": true, "windows": windows})
}

// apiProviderLoginTTYStart launches (or attaches to) the login TTY for
// one instance.
func apiProviderLoginTTYStart(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	bin, found := provider.ResolveBinary(ins)
	if !found {
		c.JSON(http.StatusConflict, map[string]string{"error": "binary not found: " + bin})
		return
	}
	s, err := loginTTY.Start(ins, bin)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"session": loginTTYSessionDTO(s)})
}

// apiProviderLoginTTYExtend pushes the running session's kill deadline
// out one step.
func apiProviderLoginTTYExtend(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	fr, ok := loginTTY.Extend(ins.Type, ins.Name)
	if !ok {
		c.JSON(http.StatusConflict, map[string]any{"error": "no running session or cap reached", "remaining_s": fr.RemainingS, "cap_s": fr.CapS})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"remaining_s": fr.RemainingS, "cap_s": fr.CapS})
}

// apiProviderLoginTTYKill terminates the running session.
func apiProviderLoginTTYKill(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	if !loginTTY.Kill(ins.Type, ins.Name) {
		c.JSON(http.StatusConflict, map[string]string{"error": "no running session"})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "killed"})
}

var loginTTYUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Same-origin SPA; the agents auth middleware already gated this
	// request before the upgrade.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// loginTTYClientMsg is what the browser terminal sends: keystrokes
// (base64) or a resize.
type loginTTYClientMsg struct {
	T    string `json:"t"` // in | resize
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// apiProviderLoginTTYWS streams session frames to the browser terminal
// and feeds keystrokes back into the PTY.
func apiProviderLoginTTYWS(c *tool.Ctx) {
	if notReady(c) || !requireAdmin(c) {
		return
	}
	ins, ok := findLoginInstance(c)
	if !ok {
		return
	}
	s := loginTTY.Get(ins.Type, ins.Name)
	if s == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no login session"})
		return
	}

	conn, err := loginTTYUpgrader.Upgrade(c.W, c.R, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	l := log.With().
		Str("component", "logintty-ws").
		Str("type", string(ins.Type)).
		Str("name", ins.Name).
		Str("session", s.ID).
		Logger()

	frames := make(chan logintty.Frame, 256)
	snapshot := s.Attach(frames)
	defer s.Detach(frames)

	writeFrame := func(fr logintty.Frame) error {
		b, err := json.Marshal(fr)
		if err != nil {
			return err
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, b)
	}
	for _, fr := range snapshot {
		if err := writeFrame(fr); err != nil {
			return
		}
	}

	// Reader: keystrokes + resizes from the browser. Closes done on any
	// read error (tab closed).
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var in loginTTYClientMsg
			if json.Unmarshal(msg, &in) != nil {
				continue
			}
			switch in.T {
			case "in":
				data, err := base64.StdEncoding.DecodeString(in.Data)
				if err != nil || len(data) == 0 {
					continue
				}
				if err := s.Input(data); err != nil {
					l.Debug().Err(err).Msg("logintty-ws: input write failed")
				}
			case "resize":
				if err := s.Resize(in.Cols, in.Rows); err != nil {
					l.Debug().Err(err).Msg("logintty-ws: resize failed")
				}
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-readerDone:
			return
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		case fr := <-frames:
			if err := writeFrame(fr); err != nil {
				return
			}
		}
	}
}

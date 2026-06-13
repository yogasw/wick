package agentctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/rs/zerolog/log"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// Server listens on the agentctl unix socket and dispatches commands
// to the agent pool. Start it in a goroutine after the pool is ready.
type Server struct {
	pool   *agentpool.Pool
	layout agentconfig.Layout
	// onRefresh handles OpRefreshSession — reload the session into the
	// daemon's registry + re-broadcast its meta. nil = the op is a
	// no-op (returns OK so the stdio caller doesn't treat it as fatal).
	onRefresh func(sessionID string)
}

func NewServer(pool *agentpool.Pool, layout agentconfig.Layout) *Server {
	return &Server{pool: pool, layout: layout}
}

// WithRefresh wires the OpRefreshSession handler. Pass a closure that
// reloads the session into the in-memory registry and broadcasts its
// meta over SSE.
func (s *Server) WithRefresh(fn func(sessionID string)) *Server {
	s.onRefresh = fn
	return s
}

// Listen binds the socket and serves until ctx is cancelled.
func (s *Server) Listen(ctx context.Context) error {
	path := SocketPath()
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("agentctl: listen %s: %w", path, err)
	}
	log.Info().Str("socket", path).Msg("agentctl: listening")

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			log.Warn().Err(err).Msg("agentctl: accept error")
			continue
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	var cmd Cmd
	if err := json.NewDecoder(conn).Decode(&cmd); err != nil {
		_ = json.NewEncoder(conn).Encode(Reply{Error: "decode: " + err.Error()})
		return
	}
	var rep Reply
	switch cmd.Op {
	case OpSwitchProvider:
		rep = s.switchProvider(cmd)
	case OpKill:
		rep = s.kill(cmd)
	case OpRefreshSession:
		rep = s.refreshSession(cmd)
	default:
		rep = Reply{Error: "unknown op: " + cmd.Op}
	}
	_ = json.NewEncoder(conn).Encode(rep)
}

func (s *Server) resolveSession(sessionID, agentName string) (string, string, error) {
	if sessionID == "" {
		active := s.pool.ActiveSnapshot()
		if len(active) == 0 {
			return "", "", fmt.Errorf("no active agent session")
		}
		if len(active) > 1 {
			ids := make([]string, 0, len(active))
			for _, e := range active {
				ids = append(ids, e.SessionID+"/"+e.AgentName)
			}
			return "", "", fmt.Errorf("multiple active sessions %v — specify session_id", ids)
		}
		sessionID = active[0].SessionID
		if agentName == "" {
			agentName = active[0].AgentName
		}
	}
	if agentName == "" {
		agentName = "main"
	}
	return sessionID, agentName, nil
}

func (s *Server) switchProvider(cmd Cmd) Reply {
	provType := cmd.ProviderType
	provName := cmd.ProviderName
	if provName == "" {
		provName = provType
	}

	// Validate provider.
	ins, err := provider.Find(provider.Type(provType), provName)
	if err != nil {
		return Reply{Error: fmt.Sprintf("provider %q/%q not found", provType, provName)}
	}
	if ins.Disabled {
		return Reply{Error: fmt.Sprintf("provider %q/%q is disabled", provType, provName)}
	}

	sessionID, agentName, err := s.resolveSession(cmd.SessionID, cmd.AgentName)
	if err != nil {
		return Reply{Error: err.Error()}
	}

	// Update agents.json first (sync).
	sess, err := session.Load(s.layout, sessionID)
	if err != nil {
		return Reply{Error: "load session: " + err.Error()}
	}
	updated := false
	for i, a := range sess.Agents {
		if a.Name == agentName {
			newKey := provType + "/" + provName
			if a.CLISessionID != "" {
				if sess.Agents[i].ProviderSessions == nil {
					sess.Agents[i].ProviderSessions = map[string]string{}
				}
				sess.Agents[i].ProviderSessions[a.Provider] = a.CLISessionID
			}
			sess.Agents[i].Provider = newKey
			sess.Agents[i].CLISessionID = sess.Agents[i].ProviderSessions[newKey]
			updated = true
			break
		}
	}
	if !updated {
		return Reply{Error: fmt.Sprintf("agent %q not found in session %q", agentName, sessionID)}
	}
	if err := session.SaveAgents(s.layout, sessionID, sess.Agents); err != nil {
		return Reply{Error: "save agents: " + err.Error()}
	}

	// Kill async — file already updated.
	go func() {
		if err := s.pool.Kill(sessionID, agentName); err != nil {
			log.Warn().Str("session", sessionID).Err(err).Msg("agentctl: async kill failed")
		}
	}()

	return Reply{
		OK:           true,
		SessionID:    sessionID,
		AgentName:    agentName,
		ProviderType: provType,
		ProviderName: provName,
	}
}

func (s *Server) refreshSession(cmd Cmd) Reply {
	if cmd.SessionID == "" {
		return Reply{Error: "session_id required for refresh_session"}
	}
	if s.onRefresh != nil {
		s.onRefresh(cmd.SessionID)
	}
	return Reply{OK: true, SessionID: cmd.SessionID}
}

func (s *Server) kill(cmd Cmd) Reply {
	sessionID, agentName, err := s.resolveSession(cmd.SessionID, cmd.AgentName)
	if err != nil {
		return Reply{Error: err.Error()}
	}
	if err := s.pool.Kill(sessionID, agentName); err != nil {
		return Reply{Error: "kill: " + err.Error()}
	}
	return Reply{OK: true, SessionID: sessionID, AgentName: agentName}
}

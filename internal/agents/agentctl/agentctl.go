// Package agentctl provides a unix-socket control channel between the
// wick MCP stdio process and the wick HTTP daemon. The daemon owns the
// agent pool; stdio processes send commands (switch_provider, kill) and
// receive a JSON response.
//
// Socket path: ~/.<app>/agents/agentctl.sock  (same dir as gate.sock)
package agentctl

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/yogasw/wick/internal/appname"
)

// SocketPath returns the platform path for the agentctl unix socket.
func SocketPath() string {
	app := appname.Resolve()
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "."+app, "agents", "agentctl.sock")
}

// Cmd is the command sent from stdio → daemon.
type Cmd struct {
	Op           string `json:"op"`                      // "switch_provider" | "kill"
	SessionID    string `json:"session_id,omitempty"`    // empty = auto-resolve from active pool
	AgentName    string `json:"agent_name,omitempty"`    // empty = "main"
	ProviderType string `json:"provider_type,omitempty"` // for switch_provider
	ProviderName string `json:"provider_name,omitempty"` // for switch_provider
}

// Reply is the daemon's response.
type Reply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Fields echoed on success.
	SessionID    string `json:"session_id,omitempty"`
	AgentName    string `json:"agent_name,omitempty"`
	ProviderType string `json:"provider_type,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
}

const dialTimeout = 3 * time.Second

// Send connects to the daemon socket, sends cmd, and returns the reply.
// Used by the stdio MCP handler.
func Send(cmd Cmd) (Reply, error) {
	conn, err := net.DialTimeout("unix", SocketPath(), dialTimeout)
	if err != nil {
		return Reply{}, fmt.Errorf("agentctl: dial daemon socket: %w — is wick daemon running?", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return Reply{}, fmt.Errorf("agentctl: send cmd: %w", err)
	}
	var rep Reply
	if err := json.NewDecoder(conn).Decode(&rep); err != nil {
		return Reply{}, fmt.Errorf("agentctl: decode reply: %w", err)
	}
	return rep, nil
}

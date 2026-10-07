package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// extramcp.go is the per-instance "extra MCP servers" setting for omp and
// opencode: servers the operator wants next to wick's own, in the common
// mcpServers shape (Claude/omp style):
//
//	{"mcpServers": {"github": {"type": "http", "url": "https://…",
//	    "headers": {"Authorization": "Bearer ${GITHUB_TOKEN}"}},
//	  "fs": {"command": "npx", "args": ["-y", "@mcp/fs", "/srv"], "env": {"X": "${X}"}}}}
//
// (the outer "mcpServers" key is optional). Secrets are never written as
// plaintext: a header/env value whose key looks like a credential must be
// an env reference (${VAR}) resolved from the instance Env at spawn. The
// name "wick" is reserved — wick's own per-session entry is never
// overridden.

// ExtraMCPServer is one extra server, provider-neutral.
type ExtraMCPServer struct {
	Type    string            `json:"type,omitempty"` // http | sse | stdio (default: stdio when command set, else http)
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

var (
	mcpNameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	envRefRe    = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	secretKeyRe = regexp.MustCompile(`(?i)auth|token|secret|key|pass|cookie|credential`)
)

// ReservedMCPName is wick's own server; extras may not use it.
const ReservedMCPName = "wick"

// ParseExtraMCP validates the raw setting. Empty = none.
func ParseExtraMCP(raw string) (map[string]ExtraMCPServer, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		return nil, fmt.Errorf("extra MCP servers: not a JSON object: %w", err)
	}
	if wrapped, ok := outer["mcpServers"]; ok && len(outer) == 1 {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(wrapped, &inner); err != nil {
			return nil, fmt.Errorf("extra MCP servers: mcpServers must be an object: %w", err)
		}
		outer = inner
	}
	out := make(map[string]ExtraMCPServer, len(outer))
	for name, rawSrv := range outer {
		if !mcpNameRe.MatchString(name) {
			return nil, fmt.Errorf("extra MCP servers: invalid server name %q", name)
		}
		if strings.EqualFold(name, ReservedMCPName) {
			return nil, fmt.Errorf("extra MCP servers: %q is reserved for wick's own server", name)
		}
		var s ExtraMCPServer
		dec := json.NewDecoder(strings.NewReader(string(rawSrv)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("extra MCP servers: %s: %w", name, err)
		}
		if s.Type == "" {
			if s.Command != "" {
				s.Type = "stdio"
			} else {
				s.Type = "http"
			}
		}
		switch s.Type {
		case "http", "sse":
			if !strings.HasPrefix(s.URL, "https://") && !strings.HasPrefix(s.URL, "http://") {
				return nil, fmt.Errorf("extra MCP servers: %s: %s needs an http(s) url", name, s.Type)
			}
			if s.Command != "" {
				return nil, fmt.Errorf("extra MCP servers: %s: both url and command set", name)
			}
		case "stdio":
			if s.Command == "" {
				return nil, fmt.Errorf("extra MCP servers: %s: stdio needs a command", name)
			}
			if s.URL != "" {
				return nil, fmt.Errorf("extra MCP servers: %s: both url and command set", name)
			}
		default:
			return nil, fmt.Errorf("extra MCP servers: %s: unknown type %q", name, s.Type)
		}
		for k, v := range s.Headers {
			if err := checkSecretRef(name, "header", k, v); err != nil {
				return nil, err
			}
		}
		for k, v := range s.Env {
			if err := checkSecretRef(name, "env", k, v); err != nil {
				return nil, err
			}
		}
		out[name] = s
	}
	return out, nil
}

// checkSecretRef refuses a plaintext value for a credential-looking key:
// it would otherwise be written into the provider's config file.
func checkSecretRef(server, what, key, val string) error {
	if !secretKeyRe.MatchString(key) || val == "" || envRefRe.MatchString(val) {
		return nil
	}
	return fmt.Errorf("extra MCP servers: %s: %s %q looks like a secret — reference it as ${VAR} and put VAR in the instance Env", server, what, key)
}

// ExtraMCPNames lists the configured names, sorted.
func ExtraMCPNames(m map[string]ExtraMCPServer) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// OMPEntry is the omp mcp.json shape (docs/mcp-config.md): stdio/http/sse,
// ${VAR} expanded by omp at discovery.
func (s ExtraMCPServer) OMPEntry() map[string]any {
	e := map[string]any{"type": s.Type}
	if s.Type == "stdio" {
		e["command"] = s.Command
		if len(s.Args) > 0 {
			e["args"] = s.Args
		}
		if len(s.Env) > 0 {
			e["env"] = s.Env
		}
		return e
	}
	e["url"] = s.URL
	if len(s.Headers) > 0 {
		e["headers"] = s.Headers
	}
	return e
}

// OpencodeEntry is the opencode `mcp` shape (docs mcp-servers.mdx): remote
// {url, headers} or local {command: [cmd, args…], environment}, with ${VAR}
// rewritten to opencode's {env:VAR}.
func (s ExtraMCPServer) OpencodeEntry() map[string]any {
	if s.Type == "stdio" {
		e := map[string]any{"type": "local", "enabled": true, "command": append([]string{toOpencodeRef(s.Command)}, mapStrings(s.Args, toOpencodeRef)...)}
		if len(s.Env) > 0 {
			e["environment"] = mapValues(s.Env, toOpencodeRef)
		}
		return e
	}
	e := map[string]any{"type": "remote", "enabled": true, "url": toOpencodeRef(s.URL)}
	if len(s.Headers) > 0 {
		e["headers"] = mapValues(s.Headers, toOpencodeRef)
	}
	return e
}

func toOpencodeRef(v string) string { return envRefRe.ReplaceAllString(v, "{env:$1}") }

func mapStrings(in []string, f func(string) string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func mapValues(in map[string]string, f func(string) string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = f(v)
	}
	return out
}

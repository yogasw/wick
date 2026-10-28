package agents

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/mcp"
)

// wickSessionRow names the wick session an agent process belongs to when
// the pool cannot — a process spawned by the PREVIOUS daemon during a
// reload, or one the pool has already released. Read from the MCP bearer
// wick handed the process at spawn, which every provider with MCP wiring
// carries for the whole life of the process.
//
// DISPLAY ONLY. The token's signature is never checked here: anything on
// this machine can put a string that looks like a wick token into its own
// argv or environment, so nothing below may be used to grant, deny or
// attribute anything. It labels a row on an admin page, and that is all.
type wickSessionRow struct {
	// SessionID is the token's sid claim, as written ("<parent>--sub-<id>"
	// for a sub-agent). Empty when the token carried no session.
	SessionID string `json:"session_id,omitempty"`
	// Title is the session label (meta.json "label"), from the session
	// itself or, for a sub-agent without one, its root session.
	Title string `json:"title,omitempty"`
	// ProjectID/Project name the project the session (or its root) lives
	// in. ProjectID set with Project empty = the project could not be read.
	ProjectID string `json:"project_id,omitempty"`
	Project   string `json:"project,omitempty"`
	// Agent is the Team agent the session belongs to ("@handle" plus its
	// name), so two claude rows tell apart which agent each one is. Empty
	// for a session of no Team agent.
	Agent string `json:"agent,omitempty"`
	// Detected is true when a well-formed wick_sub_ token was found. A row
	// whose title or project is empty despite Detected is a wick session
	// the page could not resolve, which the page flags rather than hiding.
	Detected bool `json:"detected"`
}

// wickTokenClaims is everything allowed out of the token reader. The token
// itself never leaves readWickTokenClaims: no field here can hold it.
type wickTokenClaims struct {
	UserID    string
	SessionID string
	Expires   int64
}

// wickTokenEnvVar is the variable codex, omp and opencode read the wick
// bearer from (mcpTokenEnvVar in each provider's mcp_config.go). The three
// constants agree; if one ever diverges this reader must learn it too.
const wickTokenEnvVar = "WICK_MCP_TOKEN"

// procRead reads /proc/<pid>/<file>. A var so tests can feed a fake /proc.
var procRead = func(pid int, file string) ([]byte, error) {
	return os.ReadFile("/proc/" + strconv.Itoa(pid) + "/" + file)
}

// wickClaimsCacheTTL bounds how long a pid's answer is reused. Short: a
// pid can be recycled, and the page polls every few seconds anyway.
const wickClaimsCacheTTL = 30 * time.Second

type wickClaimsCacheEntry struct {
	claims wickTokenClaims
	ok     bool
	at     time.Time
}

var (
	wickClaimsMu    sync.Mutex
	wickClaimsCache = map[int]wickClaimsCacheEntry{}
)

// cachedWickTokenClaims is readWickTokenClaims behind a short per-pid
// cache, so a polling page does not re-read the same cmdline every tick.
// Only claims are cached, never the token.
func cachedWickTokenClaims(pid int) (wickTokenClaims, bool) {
	now := time.Now()
	wickClaimsMu.Lock()
	for p, e := range wickClaimsCache {
		if now.Sub(e.at) > wickClaimsCacheTTL {
			delete(wickClaimsCache, p)
		}
	}
	if e, ok := wickClaimsCache[pid]; ok {
		wickClaimsMu.Unlock()
		return e.claims, e.ok
	}
	wickClaimsMu.Unlock()

	c, ok := readWickTokenClaims(pid)
	wickClaimsMu.Lock()
	wickClaimsCache[pid] = wickClaimsCacheEntry{claims: c, ok: ok, at: now}
	wickClaimsMu.Unlock()
	return c, ok
}

// readWickTokenClaims finds the wick bearer a process was spawned with and
// returns its claims. claude carries it in its --mcp-config argument;
// codex, omp and opencode in WICK_MCP_TOKEN. cmdline first because it is
// world-readable and cheap; environ only when that found nothing, and it
// is readable only for processes of this same user.
//
// Every failure — process gone, permission denied, no token, malformed
// token — is a plain false: the row then stays "outside wick".
func readWickTokenClaims(pid int) (wickTokenClaims, bool) {
	if b, err := procRead(pid, "cmdline"); err == nil {
		if c, ok := wickClaimsFromCmdline(splitNUL(b)); ok {
			return c, true
		}
	}
	if b, err := procRead(pid, "environ"); err == nil {
		if c, ok := wickClaimsFromEnviron(splitNUL(b)); ok {
			return c, true
		}
	}
	return wickTokenClaims{}, false
}

func splitNUL(b []byte) []string {
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\x00")
}

// wickClaimsFromCmdline reads the bearer out of claude's inline
// --mcp-config JSON (mcpServers.wick.headers.Authorization). A path
// instead of inline JSON is ignored: following it would mean opening
// whatever file a stranger's argv names.
func wickClaimsFromCmdline(argv []string) (wickTokenClaims, bool) {
	for i, a := range argv {
		var cfg string
		switch {
		case a == "--mcp-config" && i+1 < len(argv):
			cfg = argv[i+1]
		case strings.HasPrefix(a, "--mcp-config="):
			cfg = strings.TrimPrefix(a, "--mcp-config=")
		default:
			continue
		}
		cfg = strings.TrimSpace(cfg)
		if !strings.HasPrefix(cfg, "{") {
			continue
		}
		var parsed struct {
			MCPServers map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal([]byte(cfg), &parsed) != nil {
			continue
		}
		srv, ok := parsed.MCPServers["wick"]
		if !ok {
			continue
		}
		auth := srv.Headers["Authorization"]
		if c, ok := wickClaimsFromBearer(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))); ok {
			return c, true
		}
	}
	return wickTokenClaims{}, false
}

// wickClaimsFromEnviron reads WICK_MCP_TOKEN from a process environment.
func wickClaimsFromEnviron(env []string) (wickTokenClaims, bool) {
	prefix := wickTokenEnvVar + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return wickClaimsFromBearer(strings.TrimSpace(strings.TrimPrefix(kv, prefix)))
		}
	}
	return wickTokenClaims{}, false
}

// wickClaimsFromBearer decodes the payload of a signed wick_sub_ token
// WITHOUT verifying it — display only, see wickSessionRow. The opaque
// form (wick_sub_<hex>, no dots) names no session and is not detected;
// neither is any other bearer (the per-boot internal token, a PAT).
func wickClaimsFromBearer(tok string) (wickTokenClaims, bool) {
	body, ok := strings.CutPrefix(tok, mcp.ScopedTokenPrefix)
	if !ok {
		return wickTokenClaims{}, false
	}
	parts := strings.Split(body, ".")
	if len(parts) != 3 || parts[1] == "" {
		return wickTokenClaims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return wickTokenClaims{}, false
	}
	var p struct {
		UID string `json:"uid"`
		SID string `json:"sid"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return wickTokenClaims{}, false
	}
	uid := p.UID
	if uid == "" {
		uid = p.Sub
	}
	if uid == "" && p.SID == "" {
		return wickTokenClaims{}, false
	}
	return wickTokenClaims{UserID: uid, SessionID: p.SID, Expires: p.Exp}, true
}

// rootSessionID strips a sub-agent suffix: "<parent>--sub-<id>" → parent.
func rootSessionID(sid string) string {
	if i := strings.Index(sid, "--sub-"); i > 0 {
		return sid[:i]
	}
	return sid
}

// detectWickSession labels a process the pool does not know. ok=false when
// no wick token was found — the row stays a stranger.
//
// Metadata goes through session.Load / project.Load, which validate the id
// before touching disk: the sid comes from an unverified token, so it must
// never be joined into a path unchecked.
func detectWickSession(pid int) (wickSessionRow, bool) {
	c, ok := cachedWickTokenClaims(pid)
	if !ok {
		return wickSessionRow{}, false
	}
	if c.SessionID == "" {
		return wickSessionRow{Detected: true}, true
	}
	return describeWickSession(c.SessionID), true
}

// describeWickSession labels sessionID: its title, project and Team agent.
// Also used for rows the pool owns, which know their session but not what
// it is; two "claude" rows otherwise read the same.
func describeWickSession(sessionID string) wickSessionRow {
	row := wickSessionRow{SessionID: sessionID, Detected: true}
	ids := []string{sessionID}
	if root := rootSessionID(sessionID); root != sessionID {
		ids = append(ids, root)
	}
	for _, id := range ids {
		sess, err := session.Load(globalLayout, id)
		if err != nil {
			continue
		}
		if row.Title == "" {
			row.Title = strings.TrimSpace(sess.Meta.Label)
		}
		if row.ProjectID == "" {
			row.ProjectID = sess.Meta.ProjectID
		}
	}
	if row.ProjectID != "" {
		if p, err := project.Load(globalLayout, row.ProjectID); err == nil {
			row.Project = strings.TrimSpace(p.Meta.Name)
		}
	}
	for _, id := range ids {
		if a := globalTeam.AgentFor(context.Background(), id); a != nil {
			row.Agent = "@" + a.Handle
			break
		}
	}
	return row
}

package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/clitoken"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/login"
)

// WickCLIToken mints (or lists, or revokes) the short-lived bearer a shell
// uses to talk back into THIS session.
//
// It takes no session id, and that is the security design rather than an
// omission. Session ids are not secret — they are in the URL of every web
// session — so a tool that accepted one would let anybody with a shell on
// this host mint a credential for somebody else's conversation. The only
// session this can mint for is the one the call arrives from, which the
// per-spawn X-Wick-Session-Id header establishes and the caller cannot
// forge: it is set by the process that spawned the agent.
func WickCLIToken(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	const tool = "wick_cli_token"

	sessionID := ResolveCallSession(r.Header.Get("X-Wick-Session-Id"), "")
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "this tool only works inside an agent session (no session on the call)", tool)
		return
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), tool)
		return
	}
	user := login.GetUser(r.Context())
	if !canManageSession(user, sess.Meta.UserID) {
		rsp.ToolError(w, req.ID, "session not found: "+sessionID, tool)
		return
	}

	action := strings.ToLower(strings.TrimSpace(str(args["action"])))
	if action == "" {
		action = "issue"
	}
	switch action {
	case "issue":
		ttl := clitoken.DefaultTTL
		if raw := strings.TrimSpace(str(args["ttl"])); raw != "" {
			d, perr := time.ParseDuration(raw)
			if perr != nil {
				rsp.ToolError(w, req.ID, fmt.Sprintf("ttl %q is not a duration (try 30m, 90m, 2h)", raw), tool)
				return
			}
			ttl = d
		}
		userID := sess.Meta.UserID
		if user != nil {
			userID = user.ID
		}
		g, ierr := clitoken.Default.Issue(sessionID, userID, str(args["note"]), ttl)
		if ierr != nil {
			rsp.ToolError(w, req.ID, ierr.Error(), tool)
			return
		}
		base := clitoken.BaseURL()
		rsp.ToolJSON(w, req.ID, map[string]any{
			"token":    g.Token,
			"base_url": base,
			// Spelled out rather than left to be derived. The address is
			// the one thing a script cannot guess — this app answers on a
			// public name behind a proxy, and the loopback port that looks
			// obvious returns 403 from the host gate, which is a confusing
			// way to find out you had the wrong door.
			"endpoints": map[string]string{
				"send":   base + "/api/cli/send",
				"whoami": base + "/api/cli/whoami",
			},
			"session_id": g.SessionID,
			"expires_at": g.ExpiresAt.Format(time.RFC3339),
			"expires_in": fmt.Sprintf("%.0fm", time.Until(g.ExpiresAt).Minutes()),
			// The two shapes a script actually needs, spelled out: nobody
			// should have to derive a curl from a field list.
			"usage_cli": fmt.Sprintf(
				"WICK_CLI_TOKEN=%s WICK_BASE_URL=%s support-tools agent send --text \"build finished\"",
				g.Token, base),
			"usage_curl": fmt.Sprintf(
				"curl -sS -X POST %s/api/cli/send -H 'Authorization: Bearer %s' -H 'Content-Type: application/json' -d '{\"text\":\"build finished\"}'",
				base, g.Token),
			"check_first": fmt.Sprintf(
				"WICK_CLI_TOKEN=%s WICK_BASE_URL=%s support-tools agent whoami", g.Token, base),
			"note": "Bound to this session only, expires as shown, and dies with the daemon. " +
				"Anything sent lands as a normal user turn — the session wakes on it.",
			// The one failure mode worth naming at mint time, because it is
			// the likeliest: deploys are exactly when builds run, so a job
			// often finishes while wick is swapping binaries.
			"if_wick_restarts": "`agent send` retries an unreachable or still-booting daemon for 90s " +
				"(--retry to change, 0 to fail fast), so a report that lands during a handover still arrives. " +
				"A full restart takes ~80s here. If the daemon is down longer than that the command exits 4 — " +
				"write the result to a file and let the next turn read it, rather than losing it.",
			"exit_codes": map[string]string{
				"0": "delivered",
				"2": "usage: no token, or nothing to send",
				"3": "token expired or revoked, or this session is gone — mint a new one",
				"4": "wick unreachable even after retrying",
				"5": "wick answered and refused",
			},
		})
	case "list":
		out := []map[string]any{}
		for _, g := range clitoken.Default.ListFor(sessionID) {
			out = append(out, map[string]any{
				"token":      g.Token, // already masked by ListFor
				"note":       g.Note,
				"expires_at": g.ExpiresAt.Format(time.RFC3339),
			})
		}
		rsp.ToolJSON(w, req.ID, map[string]any{"tokens": out, "session_id": sessionID})
	case "revoke":
		tok := strings.TrimSpace(str(args["token"]))
		if tok == "" {
			// No token named: drop every one this session holds, which is
			// what somebody reaching for "revoke" in a hurry means.
			rsp.ToolJSON(w, req.ID, map[string]any{
				"revoked":    clitoken.Default.RevokeSession(sessionID),
				"session_id": sessionID,
			})
			return
		}
		// Only this session's own tokens, so a leaked token id cannot be
		// used to revoke somebody else's.
		if g, ok := clitoken.Default.Resolve(tok); !ok || g.SessionID != sessionID {
			rsp.ToolError(w, req.ID, "no such live token in this session", tool)
			return
		}
		rsp.ToolJSON(w, req.ID, map[string]any{"revoked": clitoken.Default.Revoke(tok)})
	default:
		rsp.ToolError(w, req.ID, "action must be issue, list or revoke", tool)
	}
}

// str reads a string argument without panicking on a wrong type.
func str(v any) string {
	s, _ := v.(string)
	return s
}

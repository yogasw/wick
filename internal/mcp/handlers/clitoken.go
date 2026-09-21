package handlers

import (
	"context"
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
// call itself establishes and the caller cannot forge: it comes from the
// credential the spawn was given (see internal/mcp/auth.go).
func WickCLIToken(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	const tool = "wick_cli_token"

	sessionID := SessionOf(r)
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

	// One action: issue. There is no list and no revoke because there is
	// nothing to list or revoke — a token is a signed statement, not a row
	// somewhere, and it is short enough (2h ceiling) that waiting it out is
	// the answer. To cancel every outstanding one at once, rotate the app's
	// session secret.
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
	g, ierr := clitoken.Issue(sessionID, userID, str(args["note"]), ttl)
	if ierr != nil {
		rsp.ToolError(w, req.ID, ierr.Error(), tool)
		return
	}

	// Prove the address before handing it over. A token with an address
	// that does not answer is worse than no token: the script carries it
	// all the way to the end of the build and only then discovers it has
	// nowhere to report.
	base := clitoken.BaseURL()
	verifyCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	reachable, verr := clitoken.PickReachable(verifyCtx, g.Token, clitoken.Candidates())
	cancel()
	verified := verr == nil
	if verified {
		base = reachable
	}
	out := map[string]any{
		"token":    g.Token,
		"base_url": base,
		// Spelled out rather than left to be derived: the address is the
		// one thing a script cannot guess.
		"endpoints": map[string]string{
			"send":   base + "/api/cli/send",
			"whoami": base + "/api/cli/whoami",
		},
		"session_id": g.SessionID,
		"expires_at": g.ExpiresAt.Format(time.RFC3339),
		"expires_in": fmt.Sprintf("%.0fm", time.Until(g.ExpiresAt).Minutes()),
		"usage_cli": fmt.Sprintf(
			"WICK_CLI_TOKEN=%s WICK_BASE_URL=%s support-tools agent send --text \"build finished\"",
			g.Token, base),
		"usage_curl": fmt.Sprintf(
			"curl -sS -X POST %s/api/cli/send -H 'Authorization: Bearer %s' -H 'Content-Type: application/json' -d '{\"text\":\"build finished\"}'",
			base, g.Token),
		"check_first": fmt.Sprintf(
			"WICK_CLI_TOKEN=%s WICK_BASE_URL=%s support-tools agent whoami", g.Token, base),
		"note": "Bound to this session only, reachable from this machine only, and it expires. " +
			"Anything sent lands as a normal user turn — the session wakes on it. " +
			"It survives a wick restart (it is signed, not remembered), and it cannot be revoked: " +
			"let it expire, or rotate the app's session secret to void every outstanding token.",
		"if_wick_restarts": "`agent send` retries an unreachable or still-booting daemon for 90s " +
			"(--retry to change, 0 to fail fast), so a report that lands during a handover still arrives. " +
			"A full restart takes ~80s here. If the daemon is down longer than that the command exits 4 — " +
			"write the result to a file and let the next turn read it, rather than losing it.",
		"exit_codes": map[string]string{
			"0": "delivered",
			"2": "usage: no token, or nothing to send",
			"3": "token expired, or this session is gone — mint a new one",
			"4": "wick unreachable even after retrying",
			"5": "wick answered and refused",
		},
		"verified": verified,
	}
	if !verified {
		out["verify_error"] = verr.Error()
		out["warning"] = "None of the addresses answered this token, so the one above is a guess. " +
			"Check it with `support-tools agent whoami` before handing it to a job — " +
			"a script that cannot reach wick has nowhere to report its result."
		out["tried"] = clitoken.Candidates()
	}
	rsp.ToolJSON(w, req.ID, out)
}

// str reads a string argument without panicking on a wrong type.
func str(v any) string {
	s, _ := v.(string)
	return s
}

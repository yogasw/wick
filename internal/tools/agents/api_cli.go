package agents

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/clitoken"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── the CLI channel ──────────────────────────────────────────────────────

   A shell talking into the session that minted its token. Same shape as
   any other channel — a message arrives, the agent wakes — except the
   transport is a build script.

   Every endpoint here derives the session from the TOKEN. None of them
   takes a session id, and that is the security property: there is no id to
   substitute, so a token cannot be pointed at somebody else's session even
   if the id is public (it is: it is in the URL of the web UI).
*/

// cliAPIPrefix is the tool-internal root these handlers are mounted at.
// The public mount is /api/cli/… via TicketRESTShim.
const cliAPIPrefix = ticketAPIPrefix + "/cli"

// cliSessionKey carries the resolved grant from the middleware to the
// handler, so a handler can never read a session id off the request.
type cliSessionKey struct{}

// isCLIAPIPath reports whether a path belongs to the CLI channel.
func isCLIAPIPath(p string) bool {
	rest, ok := strings.CutPrefix(p, cliAPIPrefix)
	if !ok {
		return false
	}
	switch rest {
	case "/send", "/whoami":
		return true
	}
	return false
}

// CLIAPIAuthMW resolves a wick_cli_ bearer into its grant.
//
// Separate from the ticket middleware on purpose: these tokens are not
// Personal Access Tokens and must not reach the ticket surface, and a PAT
// must not reach this one. Each validator owns its own prefix and its own
// paths, so neither can widen the other by accident.
func CLIAPIAuthMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isCLIAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// This machine only. The channel exists for work running HERE — a
		// build, a deploy, a migration — so a request from anywhere else is
		// not a use case, it is a leaked token being tried from off-box.
		// Refusing before the token is even read keeps the credential's
		// blast radius at the machine that minted it.
		//
		// A proxied request is "elsewhere" too: nginx forwards from
		// loopback, so the socket looks local while the caller is not. The
		// forwarding headers are what give that away, and their presence
		// alone is enough to refuse — this endpoint has no reason to be
		// behind a proxy at all.
		if why := notLocalReason(r); why != "" {
			writeCLIForbidden(w, why)
			return
		}
		tok := bearerToken(r)
		if tok == "" {
			writeTicketAuthError(w, "this endpoint needs a wick_cli_ token — mint one with the wick_cli_token MCP tool")
			return
		}
		grant, ok := clitoken.Default.Resolve(tok)
		if !ok {
			writeTicketAuthError(w, "token is unknown or expired — mint a fresh one (they last 30 minutes by default)")
			return
		}
		ctx := context.WithValue(r.Context(), cliSessionKey{}, grant)
		// The grant names a real person, and everything the script does is
		// attributed to them rather than to a synthetic principal.
		if ticketAPIUsers != nil && grant.UserID != "" {
			if u, err := ticketAPIUsers.GetUserByID(r.Context(), grant.UserID); err == nil && u != nil {
				ctx = login.WithUser(ctx, u, ticketAPIUsers.GetUserFilterTagIDs(r.Context(), grant.UserID))
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// notLocalReason explains why a request is not from this machine, or "".
func notLocalReason(r *http.Request) string {
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if strings.TrimSpace(r.Header.Get(h)) != "" {
			return "the CLI channel is reachable from this machine only, and this request came through a proxy (" + h + ")"
		}
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil || !ip.IsLoopback() {
		return "the CLI channel is reachable from this machine only (your address: " + host + ")"
	}
	return ""
}

// writeCLIForbidden answers a non-local caller in JSON, since everything
// on this surface is a machine.
func writeCLIForbidden(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":` + jsonQuote(msg) + `}`))
}

// cliGrant pulls the resolved grant out of the request context.
func cliGrant(c *tool.Ctx) (clitoken.Grant, bool) {
	g, ok := c.Context().Value(cliSessionKey{}).(clitoken.Grant)
	return g, ok
}

// apiCLIWhoami answers what a token is good for: which session, whose, and
// how long it has left. The first thing a script should call, and the
// cheapest way to tell "wrong token" from "wick is down".
func apiCLIWhoami(c *tool.Ctx) {
	g, ok := cliGrant(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "no token"})
		return
	}
	out := map[string]any{
		"session_id": g.SessionID,
		"expires_at": g.ExpiresAt.Format(time.RFC3339),
		"expires_in": int(time.Until(g.ExpiresAt).Round(time.Second).Seconds()),
		"note":       g.Note,
	}
	if sess, found := globalMgr.Registry().Session(g.SessionID); found {
		out["session_label"] = sess.Meta.Label
		out["session_status"] = string(sess.Meta.Status)
	} else {
		// A token for a session that no longer exists is worth saying out
		// loud: the script is about to shout into a room that is gone.
		out["session_missing"] = true
	}
	c.JSON(http.StatusOK, out)
}

// apiCLISend delivers a message into the token's session as a user turn —
// the same path the web composer takes, so the agent cannot tell the
// difference between a person typing and a build script reporting.
func apiCLISend(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	g, ok := cliGrant(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "no token"})
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	sess, found := globalMgr.Registry().Session(g.SessionID)
	if !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session no longer exists"})
		return
	}
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	if agentName == "" {
		c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "no agent in session"})
		return
	}

	// Detached from the request context: the pool spawns a subprocess, and
	// inheriting an HTTP context would kill the agent the moment this
	// response returns.
	bg := log.Ctx(c.Context()).WithContext(context.Background())
	if err := globalPool.Send(bg, g.SessionID, agentName, "cli", "user", req.Text); err != nil {
		log.Ctx(c.Context()).Error().Msgf("cli send %s: %s", g.SessionID, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"status":     "queued",
		"session_id": g.SessionID,
	})
}

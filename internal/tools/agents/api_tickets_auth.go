package agents

import (
	"context"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// TokenAuthenticator resolves a Personal Access Token to its owning user.
// Satisfied by accesstoken.Service — declared here so this package does not
// import it directly.
type TokenAuthenticator interface {
	Authenticate(ctx context.Context, plain string) (userID string, err error)
}

// UserLookup loads a user by id, so a resolved token can be turned into the
// same *entity.User the cookie path puts in the context.
type UserLookup interface {
	GetUserByID(ctx context.Context, id string) (*entity.User, error)
	// GetUserFilterTagIDs returns the user's filter tag IDs — the same set
	// the login cookie carries. Without them a token request cannot pass
	// tag-share checks, so a project the user reaches in the browser via a
	// tag grant would answer 404 to their own token.
	GetUserFilterTagIDs(ctx context.Context, userID string) []string
}

var (
	ticketAPITokens TokenAuthenticator
	ticketAPIUsers  UserLookup
)

// SetTicketAPIAuth wires the token authenticator used by the ticket REST
// surface. Called once at boot; leaving it unset simply means the API stays
// cookie-only, which is the pre-integration behaviour.
func SetTicketAPIAuth(tokens TokenAuthenticator, users UserLookup) {
	ticketAPITokens, ticketAPIUsers = tokens, users
}

// ticketAPIPrefix is where the agents tool is mounted. Paths are matched
// against it rather than guessed, so a rename of the tool cannot silently
// open (or close) the token surface.
const ticketAPIPrefix = "/tools/agents/api"

// TicketRESTBase is the public, machine-facing mount of the ticket REST
// API. The tool-internal routes stay under /tools/agents/api where the
// browser SPA fetches them; this short root path is what integrations are
// told to use, so the documented base URL does not leak the internal tool
// layout.
const TicketRESTBase = "/api"

// TicketRESTShim serves the ticket REST API at TicketRESTBase by rewriting
// the path onto the tool-internal prefix and handing the request to the
// same gated chain mounted at /tools/ — bearer auth, tool access, and
// per-project checks all apply unchanged.
//
// A path outside the token allowlist answers JSON 404 instead of falling
// through to cookie auth: everything under /api is a machine caller, and a
// machine must never be answered with a login redirect.
func TicketRESTShim(gated http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rewritten := ticketAPIPrefix + strings.TrimPrefix(r.URL.Path, TicketRESTBase)
		if !isTicketAPIPath(rewritten) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unknown endpoint"}`))
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = rewritten
		gated.ServeHTTP(w, r2)
	})
}

// isTicketAPIPath reports whether a URL is one of the ticket endpoints a
// Personal Access Token may reach.
//
// Deliberately a small allowlist, not a prefix match on /api: the rest of
// the agents API (sessions, providers, admin surfaces) must stay
// cookie-only, and a broad match here would quietly make all of it
// token-authable.
func isTicketAPIPath(p string) bool {
	rest, ok := strings.CutPrefix(p, ticketAPIPrefix)
	if !ok {
		return false
	}
	switch {
	case rest == "/tickets" || strings.HasPrefix(rest, "/tickets/"):
		return true
	case rest == "/notes" || strings.HasPrefix(rest, "/notes/"):
		return true
	case rest == "/ticket-events":
		return true
	}
	// Project-scoped board + config: /projects/{id}/tickets…
	if after, found := strings.CutPrefix(rest, "/projects/"); found {
		_, sub, hasSub := strings.Cut(after, "/")
		if !hasSub {
			return false
		}
		return sub == "tickets" || strings.HasPrefix(sub, "tickets/") ||
			sub == "ticket-config"
	}
	return false
}

// bearerToken pulls the token out of an Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	// Case-insensitive scheme: curl users type "bearer" as often as "Bearer".
	if len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ticketAPIAuthMW lets a Personal Access Token stand in for the session
// cookie on the ticket REST endpoints.
//
// Why a dedicated middleware rather than teaching login.Session about
// bearers: the cookie is the browser's credential for the WHOLE app, and
// making every page token-authable would widen the attack surface for a
// feature only the ticket API needs. It is mounted on the /api shim only
// (see TicketRESTShim) — a bearer works nowhere else; /tools/ stays
// cookie-only.
//
// A cookie session already in context wins — a browser request carrying a
// stale Authorization header should not be downgraded to whatever that
// token maps to.
// It must run BEFORE RequireToolAccess: that middleware rejects a request
// with no user in context, so a bearer-only caller has to be resolved into a
// user first or it never reaches the handler.
func TicketAPIAuthMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the ticket endpoints are token-authable. Everything else
		// under /tools/ stays cookie-only, so this cannot widen the app's
		// auth surface by accident.
		if !isTicketAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if login.GetUser(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}
		tok := bearerToken(r)
		if tok == "" || ticketAPITokens == nil || ticketAPIUsers == nil {
			next.ServeHTTP(w, r) // unauthenticated; the handler's own checks apply
			return
		}
		uid, err := ticketAPITokens.Authenticate(r.Context(), tok)
		if err != nil {
			writeTicketAuthError(w, "invalid or revoked token")
			return
		}
		user, err := ticketAPIUsers.GetUserByID(r.Context(), uid)
		if err != nil || user == nil {
			writeTicketAuthError(w, "token owner no longer exists")
			return
		}
		if !user.Approved {
			writeTicketAuthError(w, "token owner is not approved")
			return
		}
		// Resolve the user's filter tags fresh, mirroring what the login
		// cookie stores. Stamping nil here is NOT "full tag set" — every
		// tag check intersects the context set, so nil means "no tags" and
		// silently 404s any project the user only reaches via a tag grant.
		tags := ticketAPIUsers.GetUserFilterTagIDs(r.Context(), uid)
		next.ServeHTTP(w, r.WithContext(login.WithUser(r.Context(), user, tags)))
	})
}

// writeTicketAuthError answers a failed bearer with JSON, since every caller
// on this path is a machine.
func writeTicketAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="wick tickets"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":` + jsonQuote(msg) + `}`))
}

// jsonQuote is a minimal string quoter for the two literals above — pulling
// in encoding/json for a fixed message would be heavier than it is worth.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// requireTicketAPI refuses a token-authenticated request when the project
// has not opted into the REST surface.
//
// A cookie request is the browser UI and is always allowed: the toggle
// governs machine access, not whether the board works. Callers answer a
// refusal with an explicit 403 — projectAccessMW has already established
// the caller may see this project, so naming the real reason ("the API is
// off") leaks nothing and saves the integrator a debugging session.
func requireTicketAPI(c *tool.Ctx, projectID string) bool {
	if bearerToken(c.R) == "" {
		return true // cookie session — the UI, always allowed
	}
	cfg, ok := ticketConfigFor(projectID)
	if !ok {
		log.Ctx(c.Context()).Warn().Str("project", projectID).
			Msg("ticket api: 404 — project meta unreadable")
		return false
	}
	if !cfg.Integrations.APIEnabled {
		log.Ctx(c.Context()).Warn().Str("project", projectID).
			Msg("ticket api: 404 — REST API toggle is off for this project")
		return false
	}
	return true
}

// callerActor describes who is making this request, for the webhook
// envelope. A bearer request reports as "api" rather than "user" even though
// a PAT belongs to a person: a receiver echoing changes back needs to tell
// its own writes apart from a human moving a card in the UI.
func callerActor(c *tool.Ctx) ticket.Actor {
	kind := ticket.ActorUser
	if bearerToken(c.R) != "" {
		kind = ticket.ActorAPI
	}
	u := login.GetUser(c.Context())
	if u == nil {
		return ticket.Actor{Type: kind}
	}
	return ticket.Actor{Type: kind, ID: u.ID, Name: displayName(u)}
}

// displayName picks the friendliest label available for a user.
func displayName(u *entity.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// ticketConfigFor reads a project's ticket config, disk first: meta.json is
// the source of truth (the project id is its folder name), so a toggle an
// admin just flipped is honoured even if the registry's cached copy is
// stale. The registry stays as a fallback for a momentarily unreadable
// file. Only ids already known to the registry reach this (projectAccessMW
// / ticket lookup run first), so the id cannot be attacker-shaped.
func ticketConfigFor(projectID string) (project.TicketConfig, bool) {
	if p, err := project.Load(globalLayout, projectID); err == nil {
		return p.Meta.Ticket, true
	}
	if globalMgr == nil {
		return project.TicketConfig{}, false
	}
	p, ok := globalMgr.Registry().Project(projectID)
	if !ok {
		return project.TicketConfig{}, false
	}
	return p.Meta.Ticket, true
}

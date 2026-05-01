package pat

import (
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pat/view"
)

// appConfig is the subset of configs.Service the handler needs to
// build the MCP endpoint URL. Kept as an interface to avoid pulling
// the full configs package into this handler.
type appConfig interface {
	AppURL() string
}

// Handler exposes the /profile/tokens and /profile/mcp routes — the
// user-facing surface for issuing access tokens and reading the MCP
// install instructions. Tokens are general-purpose bearers (any wick
// HTTP endpoint accepts them); the MCP page is a thin documentation
// layer pointing at the same tokens.
type Handler struct {
	svc *Service
	cfg appConfig
}

func NewHandler(svc *Service, cfg appConfig) *Handler {
	return &Handler{svc: svc, cfg: cfg}
}

// Register wires the routes. All require an authenticated user; no
// admin role gate (every approved user manages their own tokens).
func (h *Handler) Register(mux *http.ServeMux, midd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return midd.RequireAuth(next)
	}
	mux.Handle("GET /profile/tokens", auth(h.tokensPage))
	mux.Handle("POST /profile/tokens", auth(h.create))
	mux.Handle("POST /profile/tokens/{id}/revoke", auth(h.revoke))
	mux.Handle("GET /profile/mcp", auth(h.mcpPage))
}

// ── /profile/tokens ──────────────────────────────────────────────────

func (h *Handler) tokensPage(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	tokens, err := h.svc.ListActive(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to load tokens", http.StatusInternalServerError)
		return
	}
	view.TokensPage(view.TokensPageData{
		User:   user,
		Tokens: tokens,
	}).Render(r.Context(), w)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))

	res, err := h.svc.Issue(r.Context(), user.ID, name)
	if err != nil {
		h.renderTokensWithError(w, r, user, err.Error())
		return
	}
	tokens, listErr := h.svc.ListActive(r.Context(), user.ID)
	if listErr != nil {
		http.Error(w, "failed to reload tokens", http.StatusInternalServerError)
		return
	}
	// Render directly with the plaintext in the banner — POST → GET
	// would lose the secret since we only ever return it once.
	view.TokensPage(view.TokensPageData{
		User:         user,
		Tokens:       tokens,
		JustIssued:   res.Token,
		JustIssuedID: res.Row.ID,
	}).Render(r.Context(), w)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	id := r.PathValue("id")

	if err := h.svc.Revoke(r.Context(), id, user.ID); err != nil {
		h.renderTokensWithError(w, r, user, err.Error())
		return
	}
	http.Redirect(w, r, "/profile/tokens", http.StatusSeeOther)
}

func (h *Handler) renderTokensWithError(w http.ResponseWriter, r *http.Request, user *entity.User, msg string) {
	tokens, _ := h.svc.ListActive(r.Context(), user.ID)
	view.TokensPage(view.TokensPageData{
		User:   user,
		Tokens: tokens,
		ErrMsg: msg,
	}).Render(r.Context(), w)
}

// ── /profile/mcp ─────────────────────────────────────────────────────

func (h *Handler) mcpPage(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	tokens, err := h.svc.ListActive(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to load tokens", http.StatusInternalServerError)
		return
	}
	view.MCPPage(view.MCPPageData{
		User:        user,
		EndpointURL: h.endpointURL(r),
		HasTokens:   len(tokens) > 0,
	}).Render(r.Context(), w)
}

// endpointURL returns the base URL the MCP client should use, with
// "/mcp" appended. Falls back to the request host when AppURL is unset
// (typical on first boot before the admin saves it).
func (h *Handler) endpointURL(r *http.Request) string {
	base := strings.TrimRight(h.cfg.AppURL(), "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/mcp"
}

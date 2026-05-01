package admin

import (
	"net/http"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
)

// accessTokensAdminPage lists every active Personal Access Token
// across all users. PATs are general-purpose bearers — MCP is just
// one caller — so the surface lives at /admin/access-tokens. Owner
// name/email are joined per distinct user_id seen in the result.
func (h *Handler) accessTokensAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)

	tokens, err := h.tokens.ListAllActive(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	owners := map[string]struct{ Name, Email string }{}
	for _, t := range tokens {
		if _, seen := owners[t.UserID]; seen {
			continue
		}
		u, err := h.repo.GetUser(ctx, t.UserID)
		if err != nil || u == nil {
			owners[t.UserID] = struct{ Name, Email string }{}
			continue
		}
		owners[t.UserID] = struct{ Name, Email string }{Name: u.Name, Email: u.Email}
	}

	rows := make([]view.AccessTokenRow, len(tokens))
	usersWithPAT := map[string]struct{}{}
	neverUsed := 0
	for i, t := range tokens {
		o := owners[t.UserID]
		rows[i] = view.AccessTokenRow{Token: t, OwnerName: o.Name, OwnerEmail: o.Email}
		usersWithPAT[t.UserID] = struct{}{}
		if t.LastUsedAt == nil {
			neverUsed++
		}
	}

	stats := view.AccessTokensStats{
		ActiveTokens: len(tokens),
		UsersWithPAT: len(usersWithPAT),
		NeverUsed:    neverUsed,
	}
	view.AccessTokensPage(rows, stats, user).Render(ctx, w)
}

// revokeTokenAdmin stamps RevokedAt on any token, bypassing the
// owner-equality check on the user-facing /profile/tokens revoke.
func (h *Handler) revokeTokenAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.tokens.RevokeAny(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/access-tokens", http.StatusFound)
}

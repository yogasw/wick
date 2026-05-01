package admin

import (
	"net/http"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
)

// connectionsAdminPage lists every active OAuth grant across all
// users — one row per (user, client) pair with at least one
// non-revoked, non-expired token. Owner name/email are joined per
// distinct user_id seen in the result set.
func (h *Handler) connectionsAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)

	grants, err := h.oauth.ListAllGrants(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	owners := map[string]struct{ Name, Email string }{}
	clientIDs := map[string]struct{}{}
	for _, g := range grants {
		clientIDs[g.ClientID] = struct{}{}
		if _, seen := owners[g.UserID]; seen {
			continue
		}
		u, err := h.repo.GetUser(ctx, g.UserID)
		if err != nil || u == nil {
			owners[g.UserID] = struct{ Name, Email string }{}
			continue
		}
		owners[g.UserID] = struct{ Name, Email string }{Name: u.Name, Email: u.Email}
	}

	rows := make([]view.ConnectionRow, len(grants))
	for i, g := range grants {
		o := owners[g.UserID]
		rows[i] = view.ConnectionRow{
			UserID:     g.UserID,
			OwnerName:  o.Name,
			OwnerEmail: o.Email,
			ClientID:   g.ClientID,
			ClientName: g.ClientName,
			GrantedAt:  g.GrantedAt,
			LastUsedAt: g.LastUsedAt,
			TokenCount: g.TokenCount,
		}
	}

	stats := view.ConnectionsAdminStats{
		ActiveGrants:  len(grants),
		UniqueClients: len(clientIDs),
		UniqueUsers:   len(owners),
	}
	view.ConnectionsAdminPage(rows, stats, user).Render(ctx, w)
}

// disconnectGrantAdmin revokes every active token the targeted user
// holds for the targeted OAuth client. Same effect as the user's own
// /profile/connections disconnect, but no owner-equality check.
func (h *Handler) disconnectGrantAdmin(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userID")
	clientID := r.PathValue("clientID")
	if err := h.oauth.RevokeGrant(r.Context(), userID, clientID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/connections", http.StatusFound)
}

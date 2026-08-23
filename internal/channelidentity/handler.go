package channelidentity

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// handler.go serves the "Channel connections" panel on the account page: the
// chat accounts a user is reachable on, and a pause switch per connection.
//
// Every route is scoped to the CALLER. A user sees and pauses only their own
// connections — a connection names someone's Slack account, so exposing another
// user's rows would leak where they can be contacted.

// Handler wires the account-page endpoints.
type Handler struct {
	store *Store
}

// NewHandler builds the HTTP surface over store.
func NewHandler(store *Store) *Handler { return &Handler{store: store} }

// Register mounts the routes behind the auth middleware.
func (h *Handler) Register(mux *http.ServeMux, midd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler { return midd.RequireAuth(next) }
	mux.Handle("GET /api/channel-connections", auth(h.list))
	mux.Handle("POST /api/channel-connections/{id}/pause", auth(h.setPaused(true)))
	mux.Handle("POST /api/channel-connections/{id}/resume", auth(h.setPaused(false)))
	// Merge is admin-only: it deletes an account, so it can never be something
	// a user does to themselves.
	mux.Handle("POST /admin/users/{id}/merge-into", midd.RequireAuth(midd.RequireAdmin(http.HandlerFunc(h.merge))))
}

// merge folds a channel-only account into a real one.
//
// Needed because not every channel reports an email. Slack does, so its senders
// match an existing account automatically; Telegram does not, so a Telegram
// sender necessarily starts as a SEPARATE account. Joining the two is a human
// judgement — the only other signal is a display name, and merging two people
// who share one is worse than leaving them apart.
func (h *Handler) merge(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")
	targetID := strings.TrimSpace(r.FormValue("target_user_id"))
	if sourceID == "" || targetID == "" {
		http.Error(w, "both source and target are required", http.StatusBadRequest)
		return
	}
	res, err := h.store.Merge(r.Context(), sourceID, targetID)
	if err != nil {
		switch {
		case errors.Is(err, ErrSameUser),
			errors.Is(err, ErrMergeIntoUnapproved),
			errors.Is(err, ErrMergeAdminSource):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	log.Warn().
		Str("source_user", sourceID).
		Str("target_user", targetID).
		Int("moved", res.MovedConnections).
		Int("skipped", res.SkippedConnections).
		Msg("admin: merged channel account into another user")
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

// connectionJSON is the read model the account page renders.
type connectionJSON struct {
	ID          string     `json:"id"`
	ChannelType string     `json:"channelType"`
	InstanceKey string     `json:"instanceKey"`
	DisplayName string     `json:"displayName,omitempty"`
	Email       string     `json:"email,omitempty"`
	Paused      bool       `json:"paused"`
	LastSeenAt  *time.Time `json:"lastSeenAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rows, err := h.store.ListForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]connectionJSON, 0, len(rows))
	for _, c := range rows {
		out = append(out, toJSON(c))
	}
	writeJSON(w, out)
}

// setPaused returns the pause/resume handler. The store scopes the update by
// user id, so a guessed connection id belonging to someone else is a no-op
// rather than a cross-account write.
func (h *Handler) setPaused(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := login.GetUser(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "missing connection id", http.StatusBadRequest)
			return
		}
		if err := h.store.SetPaused(r.Context(), user.ID, id, paused); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]bool{"paused": paused})
	}
}

func toJSON(c entity.UserChannelIdentity) connectionJSON {
	return connectionJSON{
		ID:          c.ID,
		ChannelType: c.ChannelType,
		InstanceKey: c.InstanceKey,
		DisplayName: c.DisplayName,
		Email:       c.EmailAtLink,
		Paused:      c.Paused(),
		LastSeenAt:  c.LastSeenAt,
		CreatedAt:   c.CreatedAt,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

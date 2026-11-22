package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
)

// Connector TYPE sharing (Share dialog). The rules live in
// connectors/type_share.go; this file is the HTTP surface:
//
//   - the owner (creator of a custom connector) shares by email and may
//     hide their connector again;
//   - admins additionally link team tags and open a type to everyone.
//
// System connectors (wick's own management plane) are never shareable.

// typeVisibleTo applies the type-sharing gate for one request.
func (h *Handler) typeVisibleTo(r *http.Request, user *entity.User, key string) bool {
	isAdmin := user != nil && user.IsAdmin()
	return h.connectors.TypeVisibleTo(r.Context(), key, userID(user), isAdmin)
}

// canShareType reports whether user may open the Share dialog for mod:
// admins, and the creator of a custom connector. Never for system ones.
func (h *Handler) canShareType(ctx context.Context, mod connector.Module, user *entity.User) bool {
	if user == nil || hasDefaultTag(mod.Meta.DefaultTags, tags.System.Name) {
		return false
	}
	return user.IsAdmin() || h.connectors.TypeOwner(ctx, mod.Meta.Key) == user.ID
}

type sharePerson struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type shareTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type sharingJSON struct {
	Visibility string        `json:"visibility"`
	Owner      *sharePerson  `json:"owner,omitempty"`
	People     []sharePerson `json:"people"`
	Tags       []shareTag    `json:"tags"`
	// AvailableTags is the admin's tag picker; empty for non-admins.
	AvailableTags []shareTag `json:"available_tags"`
	IsAdmin       bool       `json:"is_admin"`
}

type shareRequest struct {
	Action string `json:"action"`
	Email  string `json:"email"`
	UserID string `json:"user_id"`
	TagID  string `json:"tag_id"`
}

// shareGate resolves the connector and the caller's rights over its sharing.
// ok=false means a response was already written.
func (h *Handler) shareGate(w http.ResponseWriter, r *http.Request) (key string, user *entity.User, isAdmin, ok bool) {
	ctx := r.Context()
	user = login.GetUser(ctx)
	key = r.PathValue("key")
	mod, found := h.connectors.Module(key)
	if !found || user == nil || !h.typeVisibleTo(r, user, key) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return "", nil, false, false
	}
	if hasDefaultTag(mod.Meta.DefaultTags, tags.System.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "system connectors stay admin-only and cannot be shared"})
		return "", nil, false, false
	}
	isAdmin = user.IsAdmin()
	if !isAdmin && h.connectors.TypeOwner(ctx, key) != user.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the connector's owner or an admin can change who sees it"})
		return "", nil, false, false
	}
	return key, user, isAdmin, true
}

// apiConnectorSharing serves GET /manager/api/connectors/{key}/sharing.
func (h *Handler) apiConnectorSharing(w http.ResponseWriter, r *http.Request) {
	key, _, isAdmin, ok := h.shareGate(w, r)
	if !ok {
		return
	}
	h.writeSharing(w, r, key, isAdmin)
}

func (h *Handler) writeSharing(w http.ResponseWriter, r *http.Request, key string, isAdmin bool) {
	ctx := r.Context()
	sh, err := h.connectors.GetTypeSharing(ctx, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := sharingJSON{
		Visibility:    visibilityLabel(!sh.Restricted),
		People:        []sharePerson{},
		Tags:          []shareTag{},
		AvailableTags: []shareTag{},
		IsAdmin:       isAdmin,
	}
	ids := append([]string{}, sh.UserIDs...)
	if sh.Owner != "" {
		ids = append(ids, sh.Owner)
	}
	byID := map[string]*entity.User{}
	if h.users != nil && len(ids) > 0 {
		if us, err := h.users.GetUsersByIDs(ctx, ids); err == nil {
			for _, u := range us {
				byID[u.ID] = u
			}
		}
	}
	person := func(id string) sharePerson {
		if u := byID[id]; u != nil {
			return sharePerson{ID: id, Name: u.Name, Email: u.Email}
		}
		return sharePerson{ID: id}
	}
	if sh.Owner != "" {
		p := person(sh.Owner)
		out.Owner = &p
	}
	for _, id := range sh.UserIDs {
		if id == sh.Owner {
			continue
		}
		out.People = append(out.People, person(id))
	}
	for _, t := range sh.Tags {
		out.Tags = append(out.Tags, shareTag{ID: t.ID, Name: t.Name})
	}
	if isAdmin {
		if all, err := h.connectors.ShareableTags(ctx); err == nil {
			for _, t := range all {
				out.AvailableTags = append(out.AvailableTags, shareTag{ID: t.ID, Name: t.Name})
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// apiSetConnectorSharing serves POST /manager/api/connectors/{key}/sharing
// with {"action": "...", ...}. Actions:
//
//	share_email  {email}   owner/admin — share with one approved user
//	unshare_user {user_id} owner/admin — remove a person
//	restrict               owner/admin — hide from everyone not shared with
//	everyone               admin       — open to every user
//	link_tag     {tag_id}  admin       — share with every holder of a tag
//	unlink_tag   {tag_id}  admin
//
// Responds with the updated sharing state (same shape as GET).
func (h *Handler) apiSetConnectorSharing(w http.ResponseWriter, r *http.Request) {
	key, user, isAdmin, ok := h.shareGate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req shareRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	adminOnly := func() bool {
		if !isAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only an admin can do this"})
		}
		return isAdmin
	}
	var err error
	switch req.Action {
	case "share_email":
		email := strings.TrimSpace(strings.ToLower(req.Email))
		if email == "" || h.users == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		target, lerr := h.users.GetUserByEmail(ctx, email)
		if lerr != nil || target == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no wick user with email " + email})
			return
		}
		if !target.Approved {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": email + " is not approved on wick yet"})
			return
		}
		err = h.connectors.ShareTypeWithUser(ctx, key, target.ID)
	case "unshare_user":
		if req.UserID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
			return
		}
		err = h.connectors.UnshareTypeUser(ctx, key, req.UserID)
	case "restrict":
		owner := h.connectors.TypeOwner(ctx, key)
		if owner == "" && !isAdmin {
			owner = user.ID
		}
		err = h.connectors.RestrictType(ctx, key, owner)
	case "everyone":
		if !adminOnly() {
			return
		}
		err = h.connectors.OpenType(ctx, key)
	case "link_tag":
		if !adminOnly() {
			return
		}
		err = h.connectors.LinkTypeTag(ctx, key, req.TagID)
	case "unlink_tag":
		if !adminOnly() {
			return
		}
		err = h.connectors.UnlinkTypeTag(ctx, key, req.TagID)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
		return
	}
	if errors.Is(err, connectors.ErrTypeOpen) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.writeSharing(w, r, key, isAdmin)
}
